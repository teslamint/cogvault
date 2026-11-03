package archive

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
)

// Acquisition fetches a public http(s) URL as a new original. It is the
// conservative subset of ADR 0027 stage E that needs no open policy:
//
//   - only http and https, no userinfo, no cookies, no proxy, no
//     credentials of any kind;
//   - every address DNS returns for every hop (the first request and each
//     redirect) must be public; one non-public address refuses the hop, and
//     the connection goes only to an address that was checked, so a second
//     DNS answer cannot steer it elsewhere (DNS rebinding);
//   - at most acquireMaxRedirects redirects, a whole-request timeout, and a
//     byte limit: a larger body is refused, never truncated;
//   - `Accept-Encoding: identity` and no transparent decompression, so the
//     stored bytes are the bytes the server sent;
//   - the capture's completeness is always unknown: a successful download
//     does not show the document is complete.
//
// It does not read robots.txt or terms of use, render pages, run scripts,
// or load linked resources.

const (
	acquireMaxRedirects = 5
	// AcquireTimeout bounds one acquisition, redirects included.
	AcquireTimeout = 60 * time.Second
	// AcquireSupplier marks captures made by Acquire.
	AcquireSupplier  = "cogvault archive acquire"
	acquireUserAgent = "cogvault-archive/1 (operator-requested single download)"
)

var (
	// ErrBlockedAddress is a host that resolves to a non-public address.
	ErrBlockedAddress = errors.New("destination is not a public address")
	// ErrRemote is a response cogvault refuses to store (status, encoding).
	ErrRemote = errors.New("remote refused or unusable response")
	// ErrAccessRestricted is a 401, 403, 407, or 451 response. Cogvault
	// never retries with credentials; a person must act.
	ErrAccessRestricted = errors.New("access restricted")
)

// blockedPrefixes are non-public ranges netip's predicates do not cover.
var blockedPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8",         // "this network"
		"100.64.0.0/10",     // carrier-grade NAT (also some cloud metadata)
		"192.0.0.0/24",      // IETF protocol assignments
		"192.0.2.0/24",      // documentation
		"198.18.0.0/15",     // benchmarking
		"198.51.100.0/24",   // documentation
		"203.0.113.0/24",    // documentation
		"240.0.0.0/4",       // reserved, broadcast
		"64:ff9b::/96",      // NAT64: may map to private IPv4
		"64:ff9b:1::/48",    // local-use NAT64
		"2001:db8::/32",     // documentation
		"2002::/16",         // 6to4: may map to private IPv4
		"100::/64",          // discard
		"fd00:ec2::254/128", // cloud metadata (also inside fc00::/7)
		"2001::/32",         // Teredo: may map to private IPv4
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// PublicAddr reports whether a is a public unicast address cogvault may
// connect to.
func PublicAddr(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsValid() || a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsInterfaceLocalMulticast() || a.IsMulticast() || a.IsUnspecified() || !a.IsGlobalUnicast() {
		return false
	}
	for _, p := range blockedPrefixes {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

// Acquirer downloads public URLs. The zero value uses the system resolver
// and dialer; tests replace Resolve and Dial, never the address check.
type Acquirer struct {
	Resolve  func(ctx context.Context, host string) ([]netip.Addr, error)
	Dial     func(ctx context.Context, network, addr string) (net.Conn, error)
	Timeout  time.Duration
	MaxBytes int64
	Now      func() time.Time
}

// Hop is one request in an acquisition: its URL, the checked address the
// connection used, and the response status.
type Hop struct {
	URL     string `json:"url"`
	Address string `json:"address"`
	Status  int    `json:"status"`
}

// AcquisitionRecord is the response provenance kept with the capture.
type AcquisitionRecord struct {
	RequestedURL string            `json:"requested_url"`
	FinalURL     string            `json:"final_url"`
	Hops         []Hop             `json:"hops"`
	Headers      map[string]string `json:"headers"`
	FetchedAt    string            `json:"fetched_at"`
	Size         int64             `json:"size"`
}

// recordedHeaders are the response headers kept as provenance.
var recordedHeaders = []string{"Content-Type", "Content-Length", "Content-Encoding", "Content-Disposition", "ETag", "Last-Modified", "Date"}

func (q Acquirer) resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	if q.Resolve != nil {
		return q.Resolve(ctx, host)
	}
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

func (q Acquirer) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	if q.Dial != nil {
		return q.Dial(ctx, network, addr)
	}
	return (&net.Dialer{Timeout: 15 * time.Second}).DialContext(ctx, network, addr)
}

// checkedDial resolves host once, refuses it if any address is not public,
// and connects only to a checked address. It records the address used.
func (q Acquirer) checkedDial(record func(string)) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		var addrs []netip.Addr
		if ip, err := netip.ParseAddr(host); err == nil {
			addrs = []netip.Addr{ip}
		} else if addrs, err = q.resolve(ctx, host); err != nil {
			return nil, fmt.Errorf("resolve %s: %w", host, err)
		}
		if len(addrs) == 0 {
			return nil, fmt.Errorf("resolve %s: no addresses", host)
		}
		for _, a := range addrs {
			if !PublicAddr(a) {
				return nil, fmt.Errorf("%w: %s resolves to %s", ErrBlockedAddress, host, a)
			}
		}
		target := net.JoinHostPort(addrs[0].Unmap().String(), port)
		record(target)
		return q.dial(ctx, network, target)
	}
}

// checkAcquireURL admits only absolute http(s) URLs with no userinfo, no
// query, and no fragment. A query can carry a signed URL or an API token,
// and cogvault cannot tell a credential from an ordinary parameter, so this
// conservative subset refuses every query (including a bare "?") until a
// policy says which are safe. Refused URLs are never echoed: errors name
// only the reason.
func checkAcquireURL(u *url.URL) error {
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Hostname() == "" {
		return fmt.Errorf("%w: not an absolute http(s) URL", ErrInvalid)
	}
	if u.User != nil {
		return fmt.Errorf("%w: URLs with credentials are refused", ErrInvalid)
	}
	if u.RawQuery != "" || u.ForceQuery {
		return fmt.Errorf("%w: URLs with a query are refused; a query may carry a credential", ErrInvalid)
	}
	if u.Fragment != "" || u.RawFragment != "" || strings.Contains(u.String(), "#") {
		return fmt.Errorf("%w: URLs with a fragment are refused", ErrInvalid)
	}
	return nil
}

// redactURL keeps scheme, host, and path only, for attempt records.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "(unparsable URL)"
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}).String()
}

// fetch downloads raw. It returns the body and the provenance, or an error
// that wraps ErrBlockedAddress, ErrAccessRestricted, ErrRemote, ErrTooLarge,
// or ErrInvalid where one applies.
func (q Acquirer) fetch(ctx context.Context, raw string) ([]byte, AcquisitionRecord, error) {
	now := time.Now
	if q.Now != nil {
		now = q.Now
	}
	rec := AcquisitionRecord{Headers: map[string]string{}, FetchedAt: now().UTC().Format(time.RFC3339Nano)}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, rec, fmt.Errorf("%w: unparsable URL", ErrInvalid)
	}
	if err := checkAcquireURL(u); err != nil {
		return nil, rec, err
	}
	rec.RequestedURL = u.String()
	timeout, limit := q.Timeout, q.MaxBytes
	if timeout <= 0 {
		timeout = AcquireTimeout
	}
	if limit <= 0 || limit > MaxObjectBytes {
		limit = MaxObjectBytes
	}
	var mu sync.Mutex
	var addrs []string
	transport := &http.Transport{
		Proxy:                  nil,
		DialContext:            q.checkedDial(func(a string) { mu.Lock(); addrs = append(addrs, a); mu.Unlock() }),
		DisableCompression:     true,
		DisableKeepAlives:      true,
		TLSHandshakeTimeout:    15 * time.Second,
		ResponseHeaderTimeout:  30 * time.Second,
		MaxResponseHeaderBytes: 1 << 20,
	}
	defer transport.CloseIdleConnections()
	lastAddr := func() string {
		mu.Lock()
		defer mu.Unlock()
		if len(addrs) == 0 {
			return ""
		}
		return addrs[len(addrs)-1]
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			rec.Hops = append(rec.Hops, Hop{URL: via[len(via)-1].URL.String(), Address: lastAddr(), Status: req.Response.StatusCode})
			if len(via) > acquireMaxRedirects {
				return fmt.Errorf("%w: more than %d redirects", ErrRemote, acquireMaxRedirects)
			}
			if err := checkAcquireURL(req.URL); err != nil {
				return err
			}
			req.Header.Del("Authorization")
			req.Header.Del("Cookie")
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, rec, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	req.Header.Set("User-Agent", acquireUserAgent)
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := client.Do(req)
	if err != nil {
		// *url.Error repeats the request URL, which for a refused redirect
		// is the URL that was refused; keep only the cause.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, rec, err
	}
	defer resp.Body.Close()
	rec.FinalURL = resp.Request.URL.String()
	rec.Hops = append(rec.Hops, Hop{URL: rec.FinalURL, Address: lastAddr(), Status: resp.StatusCode})
	for _, h := range recordedHeaders {
		if v := resp.Header.Get(h); v != "" {
			rec.Headers[h] = v
		}
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusProxyAuthRequired, http.StatusUnavailableForLegalReasons:
		return nil, rec, fmt.Errorf("%w: HTTP %d", ErrAccessRestricted, resp.StatusCode)
	default:
		return nil, rec, fmt.Errorf("%w: HTTP %d", ErrRemote, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, rec, fmt.Errorf("%w: reading the body: %v", ErrRemote, err)
	}
	if int64(len(body)) > limit {
		return nil, rec, fmt.Errorf("%w: the response is larger than %d bytes", ErrTooLarge, limit)
	}
	if resp.ContentLength >= 0 && resp.ContentLength != int64(len(body)) {
		return nil, rec, fmt.Errorf("%w: got %d bytes, Content-Length says %d", ErrRemote, len(body), resp.ContentLength)
	}
	rec.Size = int64(len(body))
	return body, rec, nil
}

// extForMedia maps a declared media type to a file extension. Unknown
// types get ".bin", which no ingest digests.
func extForMedia(mediaType string) string {
	switch mediaType {
	case "application/pdf":
		return ".pdf"
	case "text/html", "application/xhtml+xml":
		return ".html"
	case "text/markdown", "text/x-markdown":
		return ".md"
	case "text/plain":
		return ".txt"
	}
	return ".bin"
}

// acquiredName builds a base file name from the final URL path and the
// declared media type. A body sent with a content coding (gzip and the
// like) is stored as sent, so its name gets ".bin": its format is not the
// declared type until decoded, and nothing digests or extracts it as such.
func acquiredName(final *url.URL, mediaType, contentEncoding string) string {
	stem := strings.TrimSuffix(path.Base(final.Path), path.Ext(final.Path))
	var b strings.Builder
	for _, r := range stem {
		if b.Len() >= 80 {
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		}
	}
	name := b.String()
	if name == "" {
		name = "download"
	}
	if enc := strings.ToLower(strings.TrimSpace(contentEncoding)); enc != "" && enc != "identity" {
		return name + ".bin"
	}
	return name + extForMedia(mediaType)
}

// Acquire downloads a public URL and stores it as a new source capture with
// completeness unknown and its response provenance. Each run is its own
// acquisition: the same bytes later share the object but get a new capture,
// and different bytes are a separate edition. Nothing is replaced.
func (a *Archive) Acquire(ctx context.Context, q Acquirer, rawURL string) (Result, AcquisitionRecord, error) {
	body, rec, err := q.fetch(ctx, rawURL)
	if err != nil {
		return Result{}, rec, err
	}
	final, _ := url.Parse(rec.FinalURL)
	mediaType := ""
	if mt, _, err := mime.ParseMediaType(rec.Headers["Content-Type"]); err == nil {
		mediaType = strings.ToLower(mt)
	}
	stored := mediaType
	if stored == "" {
		stored = http.DetectContentType(body)
	}
	res, err := a.captureSource(ctx, SourceRequest{
		Content: bytes.NewReader(body), OriginalName: acquiredName(final, mediaType, rec.Headers["Content-Encoding"]),
		URL: rec.FinalURL, Supplier: AcquireSupplier, MediaType: stored,
	}, rec.FetchedAt)
	if err != nil {
		return Result{}, rec, err
	}
	body2, err := json.Marshal(rec)
	if err != nil {
		return res, rec, err
	}
	unlock, err := a.lock(ctx, false)
	if err != nil {
		return res, rec, err
	}
	defer unlock()
	if _, err := a.db.ExecContext(ctx, `INSERT OR IGNORE INTO acquisitions (capture_id, record) VALUES (?, ?)`, res.CaptureID, string(body2)); err != nil {
		return res, rec, fmt.Errorf("capture %s stored but its response record was not: %w", res.CaptureID, err)
	}
	return res, rec, nil
}

// Reacquire downloads the URL of an existing capture again as a recovery
// attempt. A success adds a new capture linked by an attempt (the old
// capture is never replaced); a failure is recorded as an attempt too:
// human_action_required for restricted access, failed otherwise.
func (a *Archive) Reacquire(ctx context.Context, q Acquirer, captureID, cause string) (Result, AcquisitionRecord, error) {
	old, err := a.Status(ctx, captureID)
	if err != nil {
		return Result{}, AcquisitionRecord{}, err
	}
	if old.URL == "" {
		return Result{}, AcquisitionRecord{}, fmt.Errorf("%w: %s has no URL to acquire again", ErrInvalid, captureID)
	}
	if !oneOf(cause, attemptCauses) {
		return Result{}, AcquisitionRecord{}, fmt.Errorf("%w: cause %q", ErrInvalid, cause)
	}
	tried := "GET " + redactURL(old.URL)
	res, rec, err := a.Acquire(ctx, q, old.URL)
	if err != nil {
		result, next := "failed", "check the error and try again or ask for the file"
		if errors.Is(err, ErrAccessRestricted) {
			result, next = "human_action_required", "obtain an authorized copy; cogvault never uses credentials"
		}
		if res.CaptureID != "" {
			return res, rec, err
		}
		if _, aerr := a.RecordAttempt(ctx, Attempt{CaptureID: captureID, Cause: cause, Tried: tried, Result: result, RemainingGap: truncateText(err.Error(), 500), NextAction: next}); aerr != nil {
			return res, rec, errors.Join(err, aerr)
		}
		return res, rec, err
	}
	if _, err := a.RecordAttempt(ctx, Attempt{CaptureID: captureID, Cause: cause, Tried: tried, Result: "acquired", ResultCaptureID: res.CaptureID, NextAction: "judge completeness; the new capture is unknown"}); err != nil {
		return res, rec, fmt.Errorf("acquired %s but did not link it to %s: %w", res.CaptureID, captureID, err)
	}
	return res, rec, nil
}

func truncateText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func (a *Archive) acquisition(ctx context.Context, captureID string) (*AcquisitionRecord, error) {
	var body string
	err := a.db.QueryRowContext(ctx, `SELECT record FROM acquisitions WHERE capture_id = ?`, captureID).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rec AcquisitionRecord
	if err := json.Unmarshal([]byte(body), &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}
