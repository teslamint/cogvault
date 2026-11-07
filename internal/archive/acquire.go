package archive

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"encoding/xml"
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
	"syscall"
	"time"
	"unicode/utf8"
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
	// ErrStore is a downloaded file that could not be stored (disk, registry,
	// permissions): the download itself worked, so it is not a network fault.
	ErrStore = errors.New("the downloaded file could not be stored")
	// ErrHostNotAllowed is a request to a host the Acquirer's AllowHost refuses,
	// on the first URL or on a redirect. Nothing is sent to that host.
	ErrHostNotAllowed = errors.New("host is not on the allowed list")
	// ErrURLNotAllowed is a URL refused by the Acquirer's URL policy before
	// any request to that URL is sent.
	ErrURLNotAllowed = errors.New("URL is outside the allowed policy")
	// ErrNotPDF is a body that does not start with the PDF header, refused by an
	// Acquirer with PDFOnly before anything is stored.
	ErrNotPDF = errors.New("the response is not a PDF")
	// ErrMediaNotAllowed is a body or declared format refused by an explicit
	// AllowedMediaTypes policy. The response is never stored.
	ErrMediaNotAllowed = errors.New("the response does not match the allowed media types")
	// ErrRateLimited is HTTP 429 when StopOnRateLimit is enabled. Unlike the
	// generic RemoteStatusError, it ends a collection job without a retry.
	ErrRateLimited = errors.New("rate limited")
)

// scrubbedError is a transport or parser error reduced to a fixed message. The
// text of such an error can repeat a URL in any form (absolute, relative, or
// scheme-relative, as in a Location Go cannot parse), and a redirect URL can
// carry a signed token, so no part of the original text is kept. It still
// unwraps to its cause, so errors.Is and errors.As (a DNS error, a timeout, a
// net.Error) keep classifying it.
type scrubbedError struct{ err error }

func (e *scrubbedError) Error() string {
	var dns *net.DNSError
	var ne net.Error
	switch {
	case errors.As(e.err, &dns):
		return "the request failed: DNS lookup failed"
	case errors.Is(e.err, context.DeadlineExceeded) || (errors.As(e.err, &ne) && ne.Timeout()):
		return "the request failed: timed out"
	case strings.Contains(e.err.Error(), "Location header"):
		return "the request failed: a redirect Location could not be parsed"
	}
	return "the request failed: could not connect or the connection broke"
}
func (e *scrubbedError) Unwrap() error { return e.err }

// storeFaultCode names a storage failure by a fixed code. The text of a
// storage error can carry host paths, SQLite statements, or permission
// details, so job status, which a remote client can read, gets only the code;
// an operator finds the cause by checking the archive host (disk space,
// permissions, the registry), not in the job record.
func storeFaultCode(err error) string {
	switch {
	case errors.Is(err, syscall.ENOSPC):
		return "no_space"
	case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM):
		return "permission_denied"
	case errors.Is(err, syscall.EROFS):
		return "read_only"
	case errors.Is(err, ErrConflict):
		return "key_conflict"
	}
	if t := strings.ToLower(err.Error()); strings.Contains(t, "locked") || strings.Contains(t, "busy") {
		return "registry_busy"
	}
	return "storage_error"
}

// RemoteStatusError is a refused HTTP status. It matches ErrRemote, so
// callers that only test errors.Is(err, ErrRemote) are unaffected; the
// collector reads Status to decide whether a later try can succeed.
type RemoteStatusError struct{ Status int }

func (e *RemoteStatusError) Error() string {
	return fmt.Sprintf("%v: HTTP %d", ErrRemote, e.Status)
}

func (e *RemoteStatusError) Is(target error) bool { return target == ErrRemote }

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
	// AllowHost, when set, is asked for the lowercase host of the first URL and
	// of every redirect before any request to it; false refuses with
	// ErrHostNotAllowed. It narrows the public-address rule, never widens it.
	AllowHost func(host string) bool
	// AllowURL, when set, checks the full first URL and each redirect before
	// any request to it. It can require https and a default port in addition
	// to the host rule; false refuses with ErrURLNotAllowed.
	AllowURL func(u *url.URL) bool
	// PDFOnly refuses a body that does not start with "%PDF-" (ErrNotPDF).
	PDFOnly bool
	// AllowedMediaTypes, when non-nil, restricts bodies to an explicit subset
	// of application/pdf, text/html, and application/xhtml+xml. Both the
	// declared media and body must match (a PDF header also admits an absent
	// or application/octet-stream declaration). Encoded responses are refused.
	// Empty or unsupported policies are invalid. Nil retains legacy behavior,
	// including PDFOnly; setting both applies both restrictions.
	AllowedMediaTypes []string
	// MaxRedirects narrows the default of five redirects to 0..5. Zero sends
	// only the first request; nil retains the default. Invalid values fail
	// before any request. One attempt sends at most 1 + MaxRedirects requests.
	MaxRedirects *int
	// StopOnRateLimit makes HTTP 429 require human action instead of allowing
	// the collection queue's legacy automatic retry.
	StopOnRateLimit bool
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
	// SignedRedirect is true when the download went through the one signed
	// redirect a source policy allows. The recorded URLs then carry no token
	// (sourcepolicy.go).
	SignedRedirect bool `json:"signed_redirect,omitempty"`
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
	redirectLimit := acquireMaxRedirects
	if q.MaxRedirects != nil {
		redirectLimit = *q.MaxRedirects
		if redirectLimit < 0 || redirectLimit > acquireMaxRedirects {
			return nil, rec, fmt.Errorf("%w: redirect limit must be between 0 and 5", ErrInvalid)
		}
	}
	if err := q.validateMediaPolicy(); err != nil {
		return nil, rec, err
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, rec, fmt.Errorf("%w: unparsable URL", ErrInvalid)
	}
	pm, err := admitFirstURL(u)
	if err != nil {
		return nil, rec, err
	}
	if q.AllowHost != nil && !q.AllowHost(strings.ToLower(u.Hostname())) {
		return nil, rec, fmt.Errorf("%w: the first URL", ErrHostNotAllowed)
	}
	if q.AllowURL != nil && !q.AllowURL(u) {
		return nil, rec, fmt.Errorf("%w: the first URL", ErrURLNotAllowed)
	}
	rec.RequestedURL = u.String()
	if pm != nil {
		rec.RequestedURL = pm.first
	}
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
			rec.Hops = append(rec.Hops, Hop{URL: pm.redact(via[len(via)-1].URL), Address: lastAddr(), Status: req.Response.StatusCode})
			if len(via) > redirectLimit {
				return fmt.Errorf("%w: more than %d redirects", ErrRemote, redirectLimit)
			}
			// The one redirect that may carry a query is the signed redirect the
			// first URL's policy names. It is allowed only as the FIRST redirect
			// (len(via) == 1) and only once (SignedRedirect not yet set); every
			// other hop takes the generic checks, which refuse any query, so a
			// token can be neither repeated nor passed on a further hop.
			if q.AllowHost != nil && !q.AllowHost(strings.ToLower(req.URL.Hostname())) {
				return fmt.Errorf("%w: a redirect", ErrHostNotAllowed)
			}
			if q.AllowURL != nil && !q.AllowURL(req.URL) {
				return fmt.Errorf("%w: a redirect", ErrURLNotAllowed)
			}
			if len(via) == 1 && !rec.SignedRedirect && pm.matchRedirect(req.URL) {
				rec.SignedRedirect = true
			} else if err := checkAcquireURL(req.URL); err != nil {
				return err
			}
			req.Header.Del("Authorization")
			req.Header.Del("Cookie")
			// Go sets Referer to the previous URL; never pass a signed URL on.
			req.Header.Del("Referer")
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, rec, fmt.Errorf("%w: the request could not be built", ErrInvalid)
	}
	req.Header.Set("User-Agent", acquireUserAgent)
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := client.Do(req)
	if err != nil {
		// *url.Error repeats the request URL, which for a refused redirect is
		// the URL that was refused, and the cause can repeat the Location it
		// could not parse. Keep the cause and scrub every URL from its text.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		if !errors.Is(err, ErrBlockedAddress) && !errors.Is(err, ErrInvalid) && !errors.Is(err, ErrRemote) && !errors.Is(err, ErrAccessRestricted) && !errors.Is(err, ErrTooLarge) && !errors.Is(err, ErrHostNotAllowed) && !errors.Is(err, ErrURLNotAllowed) {
			err = &scrubbedError{err}
		}
		return nil, rec, err
	}
	defer resp.Body.Close()
	rec.FinalURL = pm.redact(resp.Request.URL)
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
	case http.StatusTooManyRequests:
		if q.StopOnRateLimit {
			return nil, rec, ErrRateLimited
		}
		return nil, rec, &RemoteStatusError{Status: resp.StatusCode}
	default:
		return nil, rec, &RemoteStatusError{Status: resp.StatusCode}
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
	if q.PDFOnly && !bytes.HasPrefix(body, []byte("%PDF-")) {
		return nil, rec, ErrNotPDF
	}
	if q.AllowedMediaTypes != nil {
		if err := q.checkMediaBody(body, resp.Header); err != nil {
			return nil, rec, err
		}
	}
	rec.Size = int64(len(body))
	return body, rec, nil
}

func (q Acquirer) validateMediaPolicy() error {
	if q.AllowedMediaTypes == nil {
		return nil
	}
	if len(q.AllowedMediaTypes) == 0 {
		return fmt.Errorf("%w: the allowed media types must not be empty", ErrInvalid)
	}
	for _, media := range q.AllowedMediaTypes {
		switch media {
		case "application/pdf", "text/html", "application/xhtml+xml":
		default:
			return fmt.Errorf("%w: unsupported allowed media type", ErrInvalid)
		}
	}
	return nil
}

// checkMediaBody is an opt-in format gate, not a completeness validator. A
// PDF header or recognizable HTML cannot establish that the original is whole.
// Nothing here renders HTML, runs scripts, or fetches any linked resource.
func (q Acquirer) checkMediaBody(body []byte, headers http.Header) error {
	if enc := strings.ToLower(strings.TrimSpace(strings.Join(headers.Values("Content-Encoding"), ","))); enc != "" && enc != "identity" {
		return ErrMediaNotAllowed
	}
	if len(headers.Values("Content-Type")) > 1 {
		return ErrMediaNotAllowed
	}
	media, params := "", map[string]string{}
	if declared := headers.Get("Content-Type"); declared != "" {
		var err error
		media, params, err = mime.ParseMediaType(declared)
		if err != nil {
			return ErrMediaNotAllowed
		}
	}
	if bytes.HasPrefix(body, []byte("%PDF-")) {
		if oneOf("application/pdf", q.AllowedMediaTypes) && (media == "application/pdf" || media == "application/octet-stream" || media == "") {
			return nil
		}
		return ErrMediaNotAllowed
	}
	if !oneOf(media, q.AllowedMediaTypes) || (media != "text/html" && media != "application/xhtml+xml") {
		return ErrMediaNotAllowed
	}
	charset := strings.ToLower(params["charset"])
	if charset != "" && charset != "utf-8" && charset != "us-ascii" {
		return ErrMediaNotAllowed
	}
	if !utf8.Valid(body) {
		return ErrMediaNotAllowed
	}
	for _, b := range body {
		if (b < 0x20 && b != '\t' && b != '\n' && b != '\r' && b != '\f') || b == 0x7f || (charset == "us-ascii" && b >= 0x80) {
			return ErrMediaNotAllowed
		}
	}
	sniff := bytes.TrimSpace(bytes.TrimPrefix(body, []byte("\xef\xbb\xbf")))
	// XHTML commonly begins with an XML declaration, which DetectContentType
	// labels text/xml. Validate that declaration, then sniff the HTML after it.
	if media == "application/xhtml+xml" && bytes.HasPrefix(sniff, []byte("<?xml")) {
		dec := xml.NewDecoder(bytes.NewReader(sniff))
		if _, err := dec.Token(); err != nil {
			return ErrMediaNotAllowed
		}
		sniff = bytes.TrimSpace(sniff[dec.InputOffset():])
	}
	if http.DetectContentType(sniff) != "text/html; charset=utf-8" {
		return ErrMediaNotAllowed
	}
	if obviousHTMLAccessPage(body) {
		return fmt.Errorf("%w: the HTML response appears to require access or a challenge", ErrAccessRestricted)
	}
	return nil
}

// obviousHTMLAccessPage only recognizes common, explicit access-page signals.
// It is deliberately conservative: neither a match nor its absence judges
// completeness, and some legitimate pages with login forms can be held back.
func obviousHTMLAccessPage(body []byte) bool {
	dec := xml.NewDecoder(bytes.NewReader(stripRawText(body)))
	dec.Strict, dec.AutoClose, dec.Entity = false, xml.HTMLAutoClose, xml.HTMLEntity
	var heading strings.Builder
	inHeading := false
	for {
		tok, err := dec.Token()
		if err != nil {
			return inHeading && accessHeading(heading.String())
		}
		switch token := tok.(type) {
		case xml.StartElement:
			name := strings.ToLower(token.Name.Local)
			if name == "title" || name == "h1" {
				inHeading = true
				heading.Reset()
			}
			for _, attr := range token.Attr {
				key, value := strings.ToLower(attr.Name.Local), strings.ToLower(attr.Value)
				if name == "input" && key == "type" && value == "password" {
					return true
				}
				if key == "id" || key == "class" {
					for _, field := range strings.Fields(value) {
						if field == "challenge-form" || field == "g-recaptcha" || field == "h-captcha" {
							return true
						}
					}
				}
			}
		case xml.CharData:
			if inHeading {
				heading.Write(token)
			}
		case xml.EndElement:
			name := strings.ToLower(token.Name.Local)
			if inHeading && (name == "title" || name == "h1") {
				if accessHeading(heading.String()) {
					return true
				}
				inHeading = false
			}
		}
	}
}

func accessHeading(s string) bool {
	s = strings.Join(strings.Fields(strings.ToLower(s)), " ")
	s = strings.Trim(s, " .!?:")
	for _, phrase := range []string{"access denied", "access restricted", "forbidden", "unauthorized", "authentication required", "login required", "sign in", "log in", "login", "captcha", "just a moment", "verify you are human", "verify that you are human", "checking your browser", "security check", "attention required"} {
		if s == phrase || strings.HasPrefix(s, phrase+" - ") || strings.HasPrefix(s, phrase+" | ") {
			return true
		}
	}
	return false
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
	return a.acquire(ctx, q, rawURL, SourceRequest{Supplier: AcquireSupplier})
}

// acquire is Acquire with the capture's key, supplier, and title taken
// from base; the bytes, name, URL, and media type come from the download.
func (a *Archive) acquire(ctx context.Context, q Acquirer, rawURL string, base SourceRequest) (Result, AcquisitionRecord, error) {
	body, rec, err := q.fetch(ctx, rawURL)
	if err != nil {
		return Result{}, rec, err
	}
	final, _ := url.Parse(rec.FinalURL)
	mediaType := ""
	if mt, _, err := mime.ParseMediaType(rec.Headers["Content-Type"]); err == nil {
		mediaType = strings.ToLower(mt)
	}
	// The explicit policy admits an actual PDF served without a specific
	// declaration. Keep the response headers intact as provenance, but name
	// and label the stored object by the format the policy checked.
	if q.AllowedMediaTypes != nil && bytes.HasPrefix(body, []byte("%PDF-")) {
		mediaType = "application/pdf"
	}
	stored := mediaType
	if stored == "" {
		stored = http.DetectContentType(body)
	}
	download, err := json.Marshal(rec)
	if err != nil {
		return Result{}, rec, err
	}
	// The response record is written in the capture's transaction, so a
	// capture never exists without it.
	name := acquiredName(final, mediaType, rec.Headers["Content-Encoding"])
	captureURL := rec.FinalURL
	if rec.SignedRedirect {
		// The final URL is the signed redirect with its token removed; the stable
		// identity of the source is the URL that was asked for, and its policy
		// names the file.
		captureURL = rec.RequestedURL
		if ru, perr := url.Parse(rec.RequestedURL); perr == nil {
			if n := matchSource(ru).storedName(); n != "" && strings.HasSuffix(name, ".pdf") {
				name = n
			}
		}
	}
	if base.bind != nil {
		base.bind.rec = rec
	}
	res, err := a.captureSource(ctx, SourceRequest{
		Key: base.Key, Content: bytes.NewReader(body), OriginalName: name,
		URL: captureURL, Supplier: base.Supplier, Title: base.Title, MediaType: stored,
		bind: base.bind,
	}, rec.FetchedAt, string(download))
	if err != nil {
		if errors.Is(err, ErrLeaseLost) || errors.Is(err, ErrJobNotFound) {
			return Result{}, rec, err
		}
		return Result{}, rec, fmt.Errorf("%w: %w", ErrStore, err)
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
