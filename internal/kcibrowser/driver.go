// Package kcibrowser drives a real browser through the Chrome DevTools
// Protocol to fetch the official full text of one KCI article by clicking the
// portal's own "KCI 원문 내려받기" control. It implements archive.BrowserDriver
// (the contract and its outcome codes are in internal/archive/browser.go).
//
// Safety properties, all enforced in this package:
//
//   - Fixed destinations. The browser may reach only the allowed hosts
//     (default: www.kci.go.kr). Their addresses are resolved here, every
//     answer must be public, and the browser is pinned to the checked address
//     with --host-resolver-rules; every other name fails to resolve. So a
//     second DNS answer cannot steer the browser (rebinding), and nothing
//     else on the network is reachable.
//   - Every request is paused and allowed only if its host is allowed;
//     anything else is blocked and recorded, which is how a click that leads
//     off the portal is recognised.
//   - A fresh profile per fetch: no cookies, no stored login, no extensions.
//   - It never logs in and never accepts a dialog. A login, consent step,
//     challenge, or missing control ends as a reason code, not a workaround.
//   - It clicks one thing: the official control, found by its visible label.
package kcibrowser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"

	"github.com/teslamint/cogvault/internal/archive"
)

// Options configures a Driver. The zero value is the production setting.
type Options struct {
	// ChromePath is the browser binary; empty lets chromedp find one.
	ChromePath string
	// AllowedHosts are the only hosts the browser may reach. Default:
	// archive.BrowserHost.
	AllowedHosts []string
	// NoSandbox disables Chrome's sandbox, which a non-root container without
	// user namespaces needs. It weakens the browser: use it only there.
	NoSandbox bool
	// Timeout bounds one fetch; DownloadWait bounds the wait for a file after
	// the click. Defaults 150s and 60s.
	Timeout      time.Duration
	DownloadWait time.Duration
	// MaxBytes bounds the file. Default archive.MaxObjectBytes.
	MaxBytes int64
	// Resolve answers DNS for the allowed hosts. Default: the system resolver.
	Resolve func(ctx context.Context, host string) ([]netip.Addr, error)
}

// Driver is an archive.BrowserDriver backed by headless Chrome.
type Driver struct {
	o Options
	// Test-only switches, set by this package's tests: they let a local
	// loopback server stand in for the portal. No Options field or command
	// flag reaches them.
	allowPrivate bool
	allowHTTP    bool
}

// New returns a Driver with defaults filled in.
func New(o Options) *Driver {
	if len(o.AllowedHosts) == 0 {
		o.AllowedHosts = []string{archive.BrowserHost}
	}
	if o.Timeout <= 0 {
		o.Timeout = 150 * time.Second
	}
	if o.DownloadWait <= 0 {
		o.DownloadWait = 60 * time.Second
	}
	if o.MaxBytes <= 0 || o.MaxBytes > archive.MaxObjectBytes {
		o.MaxBytes = archive.MaxObjectBytes
	}
	return &Driver{o: o}
}

// controlXPath finds the official download control by its visible label.
var controlXPath = fmt.Sprintf(`//a[.//span[normalize-space(.)=%q]]`, archive.KCIControlLabel)

// session is the state the event handlers share with Fetch.
type session struct {
	mu       sync.Mutex
	blocked  map[string]bool
	dialogs  []string
	begin    *browser.EventDownloadWillBegin
	progress *browser.EventDownloadProgress
	wake     chan struct{}
}

func (s *session) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// allowedHost reports whether the browser may request raw.
func (d *Driver) allowedHost(raw string) (host string, ok bool) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && !(d.allowHTTP && u.Scheme == "http")) {
		return "", false
	}
	h := strings.ToLower(u.Hostname())
	for _, a := range d.o.AllowedHosts {
		if h == strings.ToLower(a) {
			return h, true
		}
	}
	return h, false
}

// resolverRules resolves every allowed host, refuses a non-public answer, and
// returns the Chrome --host-resolver-rules that pin each host to a checked
// address and make every other name unresolvable. A refusal returns ok=false.
func (d *Driver) resolverRules(ctx context.Context) (rules string, ok bool, err error) {
	resolve := d.o.Resolve
	if resolve == nil {
		resolve = func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		}
	}
	var parts []string
	hosts := append([]string(nil), d.o.AllowedHosts...)
	sort.Strings(hosts)
	for _, h := range hosts {
		var addrs []netip.Addr
		if ip, perr := netip.ParseAddr(h); perr == nil {
			addrs = []netip.Addr{ip}
		} else if addrs, err = resolve(ctx, h); err != nil {
			return "", false, fmt.Errorf("resolve: %w", err)
		}
		if len(addrs) == 0 {
			return "", false, errors.New("resolve: no addresses")
		}
		for _, a := range addrs {
			if !d.allowPrivate && !archive.PublicAddr(a) {
				return "", false, nil
			}
		}
		if _, perr := netip.ParseAddr(h); perr == nil {
			// A literal is not resolved, but the wildcard rule would still
			// refuse it, so exclude it; its address was checked above.
			parts = append(parts, "EXCLUDE "+h)
			continue
		}
		pick := addrs[0].Unmap()
		for _, a := range addrs {
			if a.Unmap().Is4() {
				pick = a.Unmap()
				break
			}
		}
		target := pick.String()
		if pick.Is6() {
			target = "[" + target + "]"
		}
		parts = append(parts, "MAP "+h+" "+target)
	}
	parts = append(parts, "MAP * ~NOTFOUND")
	return strings.Join(parts, ", "), true, nil
}

type evidence struct {
	Executor         string   `json:"executor"`
	Control          string   `json:"control"`
	DetailURL        string   `json:"detail_url"`
	DownloadHost     string   `json:"download_host,omitempty"`
	SuggestedName    string   `json:"suggested_filename,omitempty"`
	Size             int64    `json:"size,omitempty"`
	BlockedHosts     []string `json:"blocked_hosts,omitempty"`
	DialogsDismissed int      `json:"dialogs_dismissed,omitempty"`
}

// Fetch opens the article detail page and clicks the official control. It
// returns a code (no error) for every outcome the portal decides, and an error
// only for a browser or network fault.
func (d *Driver) Fetch(parent context.Context, detailURL string) (archive.BrowserFetch, error) {
	if _, ok := d.allowedHost(detailURL); !ok {
		return archive.BrowserFetch{Code: archive.ReasonUnexpectedPage}, nil
	}
	ctx, cancel := context.WithTimeout(parent, d.o.Timeout)
	defer cancel()

	rules, ok, err := d.resolverRules(ctx)
	if err != nil {
		return archive.BrowserFetch{}, err
	}
	if !ok {
		return archive.BrowserFetch{Code: archive.ReasonBlockedAddress}, nil
	}

	work, err := os.MkdirTemp("", "cogvault-kci-*")
	if err != nil {
		return archive.BrowserFetch{}, err
	}
	defer os.RemoveAll(work)
	profile, dl := filepath.Join(work, "profile"), filepath.Join(work, "dl")
	if err := os.Mkdir(dl, 0o700); err != nil {
		return archive.BrowserFetch{}, err
	}

	opts := append([]chromedp.ExecAllocatorOption(nil), chromedp.DefaultExecAllocatorOptions[:]...)
	opts = append(opts,
		chromedp.Headless,
		chromedp.UserDataDir(profile),
		chromedp.Flag("host-resolver-rules", rules),
		chromedp.Flag("no-proxy-server", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-sync", true),
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("mute-audio", true),
	)
	if d.o.ChromePath != "" {
		opts = append(opts, chromedp.ExecPath(d.o.ChromePath))
	}
	if d.o.NoSandbox {
		opts = append(opts, chromedp.NoSandbox, chromedp.Flag("disable-dev-shm-usage", true))
	}
	actx, cancelAlloc := chromedp.NewExecAllocator(ctx, opts...)
	defer cancelAlloc()
	cctx, cancelCtx := chromedp.NewContext(actx)
	defer cancelCtx()

	s := &session{blocked: map[string]bool{}, wake: make(chan struct{}, 1)}
	if err := chromedp.Listen(cctx, func(ev any) {
		switch e := ev.(type) {
		case *fetch.EventRequestPaused:
			// Handlers must not run commands inline, so each decision runs in its own goroutine.
			go d.guard(cctx, s, e)
		case *page.EventJavascriptDialogOpening:
			s.mu.Lock()
			s.dialogs = append(s.dialogs, e.Message)
			s.mu.Unlock()
			go func() {
				// Never accept: accepting could consent to something.
				_, _ = chromedp.Call(cctx, page.HandleJavaScriptDialog, page.HandleJavaScriptDialogParams{Accept: false})
				s.signal()
			}()
		}
	}); err != nil {
		return archive.BrowserFetch{}, fmt.Errorf("browser start: %w", err)
	}
	if err := chromedp.ListenBrowser(cctx, func(ev any) {
		switch e := ev.(type) {
		case *browser.EventDownloadWillBegin:
			s.mu.Lock()
			s.begin = e
			s.mu.Unlock()
			s.signal()
		case *browser.EventDownloadProgress:
			s.mu.Lock()
			s.progress = e
			s.mu.Unlock()
			s.signal()
		}
	}); err != nil {
		return archive.BrowserFetch{}, fmt.Errorf("browser start: %w", err)
	}

	yes := true
	if _, err := chromedp.Call(cctx, page.Enable, page.EnableParams{}); err != nil {
		return archive.BrowserFetch{}, fmt.Errorf("browser setup: %w", err)
	}
	if _, err := chromedp.CallBrowser(cctx, browser.SetDownloadBehavior, browser.SetDownloadBehaviorParams{
		Behavior: browser.SetDownloadBehaviorBehaviorAllowAndName, DownloadPath: dl, EventsEnabled: &yes,
	}); err != nil {
		return archive.BrowserFetch{}, fmt.Errorf("browser setup: %w", err)
	}
	if _, err := chromedp.Call(cctx, fetch.Enable, fetch.EnableParams{}); err != nil {
		return archive.BrowserFetch{}, fmt.Errorf("browser setup: %w", err)
	}

	ev := evidence{Executor: "browser", Control: archive.KCIControlLabel, DetailURL: detailURL}
	finish := func(f archive.BrowserFetch) (archive.BrowserFetch, error) {
		s.mu.Lock()
		for h := range s.blocked {
			ev.BlockedHosts = append(ev.BlockedHosts, h)
		}
		ev.DialogsDismissed = len(s.dialogs)
		s.mu.Unlock()
		sort.Strings(ev.BlockedHosts)
		b, _ := json.Marshal(ev)
		f.Evidence = string(b)
		return f, nil
	}

	if err := chromedp.Do(cctx, chromedp.Navigate(detailURL)); err != nil {
		if s.hasBlocked() {
			return finish(archive.BrowserFetch{Code: archive.ReasonExternalSite})
		}
		return archive.BrowserFetch{}, fmt.Errorf("open page: %w", err)
	}
	if code := d.pageCode(cctx, s, detailURL); code != "" {
		return finish(archive.BrowserFetch{Code: code})
	}

	nodes, err := chromedp.Run(cctx, chromedp.Nodes(controlXPath, chromedp.AtLeast(0)))
	if err != nil {
		return archive.BrowserFetch{}, fmt.Errorf("find control: %w", err)
	}
	if len(nodes) == 0 {
		return finish(archive.BrowserFetch{Code: archive.ReasonFulltextNotPublic})
	}
	if err := chromedp.Do(cctx, chromedp.Click(controlXPath)); err != nil {
		return archive.BrowserFetch{}, fmt.Errorf("click control: %w", err)
	}

	return d.awaitDownload(cctx, s, dl, detailURL, &ev, finish)
}

// pageCode returns a code when the loaded page is not the article detail page
// the contract expects, and "" when it is. A detail page defines the portal's
// own fncDown handler whether or not the article offers full text.
func (d *Driver) pageCode(ctx context.Context, s *session, detailURL string) string {
	var info struct {
		Path        string `json:"path"`
		HasHandler  bool   `json:"hasHandler"`
		HasPassword bool   `json:"hasPassword"`
	}
	raw, err := chromedp.Run(ctx, chromedp.Evaluate[string](`JSON.stringify({path: location.pathname, hasHandler: typeof fncDown === 'function', hasPassword: !!document.querySelector('input[type=password]')})`))
	if err != nil || json.Unmarshal([]byte(raw), &info) != nil {
		return archive.ReasonUnexpectedPage
	}
	want, _ := url.Parse(detailURL)
	switch {
	case info.HasPassword || strings.Contains(strings.ToLower(info.Path), "login"):
		return archive.ReasonLoginRequired
	case want == nil || info.Path != want.Path || !info.HasHandler:
		return archive.ReasonUnexpectedPage
	}
	return ""
}

func (s *session) hasBlocked() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.blocked) > 0
}

// guard allows a request only to an allowed host and blocks, and records, the
// rest. Recording the host (never an address) is what lets Fetch tell that a
// click led off the portal.
func (d *Driver) guard(ctx context.Context, s *session, e *fetch.EventRequestPaused) {
	host, ok := d.allowedHost(e.Request.URL)
	if ok {
		_, _ = chromedp.Call(ctx, fetch.ContinueRequest, fetch.ContinueRequestParams{RequestID: e.RequestID})
		return
	}
	s.mu.Lock()
	if host == "" {
		host = "(unparsable)"
	}
	s.blocked[host] = true
	s.mu.Unlock()
	_, _ = chromedp.Call(ctx, fetch.FailRequest, fetch.FailRequestParams{RequestID: e.RequestID, ErrorReason: network.ErrorReasonBlockedByClient})
	s.signal()
}

// awaitDownload waits for the file the click should start, enforces the size
// bound, and classifies the cases where none arrives.
func (d *Driver) awaitDownload(ctx context.Context, s *session, dl, detailURL string, ev *evidence, finish func(archive.BrowserFetch) (archive.BrowserFetch, error)) (archive.BrowserFetch, error) {
	timer := time.NewTimer(d.o.DownloadWait)
	defer timer.Stop()
	canceled := false
	for {
		s.mu.Lock()
		begin, prog, blocked := s.begin, s.progress, len(s.blocked) > 0
		s.mu.Unlock()
		if begin != nil {
			if h, _, ok := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(begin.URL, "https://"), "http://"), "/"); ok {
				ev.DownloadHost = strings.ToLower(strings.Split(h, ":")[0])
			}
			ev.SuggestedName = path.Base(begin.SuggestedFilename)
		}
		if prog != nil {
			if !canceled && (int64(prog.ReceivedBytes) > d.o.MaxBytes || int64(prog.TotalBytes) > d.o.MaxBytes) {
				canceled = true
				_, _ = chromedp.CallBrowser(ctx, browser.CancelDownload, browser.CancelDownloadParams{GUID: prog.GUID})
			}
			switch prog.State {
			case browser.DownloadProgressStateCompleted:
				return d.readDownload(dl, prog.GUID, ev, finish)
			case browser.DownloadProgressStateCanceled:
				if canceled {
					return finish(archive.BrowserFetch{Code: archive.ReasonTooLarge})
				}
				return finish(archive.BrowserFetch{Code: archive.ReasonDownloadNotStarted})
			}
		}
		if begin == nil && blocked {
			// The click led to a request outside the allowed hosts and no file started.
			return finish(archive.BrowserFetch{Code: archive.ReasonExternalSite})
		}
		select {
		case <-s.wake:
		case <-time.After(150 * time.Millisecond):
		case <-timer.C:
			return finish(archive.BrowserFetch{Code: d.noDownloadCode(ctx, s, detailURL)})
		case <-ctx.Done():
			return archive.BrowserFetch{}, ctx.Err()
		}
	}
}

// readDownload reads the finished file. allowAndName saves it under its GUID.
func (d *Driver) readDownload(dl, guid string, ev *evidence, finish func(archive.BrowserFetch) (archive.BrowserFetch, error)) (archive.BrowserFetch, error) {
	p := filepath.Join(dl, filepath.Base(guid))
	info, err := os.Stat(p)
	if err != nil {
		return finish(archive.BrowserFetch{Code: archive.ReasonDownloadNotStarted})
	}
	if info.Size() > d.o.MaxBytes {
		return finish(archive.BrowserFetch{Code: archive.ReasonTooLarge})
	}
	body, err := os.ReadFile(p)
	if err != nil {
		return archive.BrowserFetch{}, fmt.Errorf("read download: %w", err)
	}
	ev.Size = int64(len(body))
	return finish(archive.BrowserFetch{Body: body})
}

// noDownloadCode says why the click produced no file, from what the page
// shows. It never acts on the page: it only reads.
func (d *Driver) noDownloadCode(ctx context.Context, s *session, detailURL string) string {
	s.mu.Lock()
	dialogs := strings.ToLower(strings.Join(s.dialogs, " "))
	s.mu.Unlock()
	switch {
	case strings.Contains(dialogs, "로그인") || strings.Contains(dialogs, "login"):
		return archive.ReasonLoginRequired
	case strings.Contains(dialogs, "동의") || strings.Contains(dialogs, "약관") || strings.Contains(dialogs, "agree") || strings.Contains(dialogs, "consent"):
		return archive.ReasonConsentRequired
	}
	var info struct {
		Path        string `json:"path"`
		HasPassword bool   `json:"hasPassword"`
		Text        string `json:"text"`
	}
	raw, err := chromedp.Run(ctx, chromedp.Evaluate[string](`JSON.stringify({path: location.pathname, hasPassword: !!document.querySelector('input[type=password]'), text: (document.body ? document.body.innerText : '').slice(0, 4000)})`))
	if err == nil && json.Unmarshal([]byte(raw), &info) == nil {
		text := strings.ToLower(info.Text)
		switch {
		case info.HasPassword || strings.Contains(strings.ToLower(info.Path), "login"):
			return archive.ReasonLoginRequired
		case strings.Contains(text, "captcha") || strings.Contains(text, "자동입력") || strings.Contains(text, "보안문자"):
			return archive.ReasonChallenge
		case strings.Contains(text, "이용약관") || strings.Contains(text, "동의"):
			return archive.ReasonConsentRequired
		}
	}
	return archive.ReasonDownloadNotStarted
}
