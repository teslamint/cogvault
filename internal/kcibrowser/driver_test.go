package kcibrowser

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/teslamint/cogvault/internal/archive"
)

func TestAllowedHostFollowsTheAllowlistAndScheme(t *testing.T) {
	d := New(Options{})
	for raw, want := range map[string]bool{
		"https://www.kci.go.kr/kciportal/x":   true,
		"https://WWW.KCI.GO.KR/x":             true,
		"https://www.kci.go.kr:443/x":         true,
		"http://www.kci.go.kr/x":              false, // plain http only in tests
		"https://www.kci.go.kr.evil.test/x":   false,
		"https://evil.test/x":                 false,
		"https://www.kci.go.kr@evil.test/x":   false, // userinfo: the host is evil.test
		"https://localhost/x":                 false,
		"data:text/html,hi":                   false,
		"ftp://www.kci.go.kr/x":               false,
		"https://10.0.0.5/x":                  false,
		"https://www.kci.go.kr%2eevil.test/x": false,
	} {
		if _, got := d.allowedHost(raw); got != want {
			t.Errorf("allowedHost(%q) = %v, want %v", raw, got, want)
		}
	}
	d.allowHTTP = true
	if _, ok := d.allowedHost("http://www.kci.go.kr/x"); !ok {
		t.Error("the test switch did not admit plain http")
	}
}

// The browser is pinned to the address this process checked, every other name
// is unresolvable, and a host with any non-public answer is refused before a
// browser starts.
func TestResolverRulesPinCheckedAddressesAndRefuseNonPublic(t *testing.T) {
	answer := func(addrs ...string) func(context.Context, string) ([]netip.Addr, error) {
		return func(context.Context, string) ([]netip.Addr, error) {
			var out []netip.Addr
			for _, a := range addrs {
				out = append(out, netip.MustParseAddr(a))
			}
			return out, nil
		}
	}
	rules := func(addrs ...string) (string, bool, error) {
		return New(Options{Resolve: answer(addrs...)}).resolverRules(context.Background())
	}
	got, ok, err := rules("93.184.216.34")
	if err != nil || !ok || got != "MAP www.kci.go.kr 93.184.216.34, MAP * ~NOTFOUND" {
		t.Fatalf("public = %q, %v, %v", got, ok, err)
	}
	if got, ok, err := rules("2606:2800:220:1:248:1893:25c8:1946"); err != nil || !ok || !strings.Contains(got, "MAP www.kci.go.kr [2606:2800:220:1:248:1893:25c8:1946]") {
		t.Fatalf("ipv6 = %q, %v, %v", got, ok, err)
	}
	if got, _, _ := rules("2606:2800:220:1:248:1893:25c8:1946", "93.184.216.34"); !strings.Contains(got, "93.184.216.34") {
		t.Fatalf("an IPv4 answer is preferred: %q", got)
	}
	for _, bad := range [][]string{{"10.0.0.5"}, {"127.0.0.1"}, {"169.254.169.254"}, {"93.184.216.34", "192.168.1.1"}, {"100.64.0.1"}} {
		if got, ok, err := rules(bad...); ok || err != nil || got != "" {
			t.Errorf("non-public %v = %q, %v, %v; want refused", bad, got, ok, err)
		}
	}
	if _, _, err := New(Options{Resolve: answer()}).resolverRules(context.Background()); err == nil {
		t.Error("an empty answer was accepted")
	}
}

// Without the test switch, a host that is not public never reaches Chrome:
// Fetch reports blocked_address and an off-list URL reports unexpected_page.
func TestFetchRefusesBeforeStartingABrowser(t *testing.T) {
	d := New(Options{AllowedHosts: []string{"127.0.0.1"}, ChromePath: "/nonexistent/never-started"})
	f, err := d.Fetch(context.Background(), "https://127.0.0.1/kciportal/x")
	if err != nil || f.Code != archive.ReasonBlockedAddress {
		t.Fatalf("loopback host = %+v, %v", f, err)
	}
	f, err = New(Options{ChromePath: "/nonexistent/never-started"}).Fetch(context.Background(), "https://example.org/x")
	if err != nil || f.Code != archive.ReasonUnexpectedPage {
		t.Fatalf("other host = %+v, %v", f, err)
	}
}

// haveChrome gates every test that launches a real browser. They are opt-in
// (COGVAULT_BROWSER_TESTS=1): the container workflow gates image publishing on
// `go test ./...`, and a browser launch on an unknown runner must not be able to
// block publishing the MCP server image. Run them locally and in the kci image
// (docs/deployment/collector.md).
func haveChrome(t *testing.T) {
	t.Helper()
	if os.Getenv("COGVAULT_BROWSER_TESTS") != "1" {
		t.Skip("set COGVAULT_BROWSER_TESTS=1 to run the tests that launch a real Chrome or Chromium")
	}
	if _, err := os.Stat("/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"); err == nil {
		return
	}
	for _, n := range []string{"google-chrome", "chromium", "chromium-browser", "chrome"} {
		if _, err := exec.LookPath(n); err == nil {
			return
		}
	}
	t.Skip("no Chrome or Chromium found")
}

const pdfBody = "%PDF-1.4\n1 0 obj<<>>endobj\ntrailer<<>>\n%%EOF\n"

// portal serves pages that copy the markup of the official KCI detail page
// (a javascript: link with onclick="fncDown('KCI_FI...')" and the visible
// label, whose handler points a hidden iframe at the download endpoint), plus
// the variants the contract must tell apart.
func portal(t *testing.T) (*httptest.Server, func(path string) string) {
	t.Helper()
	const handler = `<script>function fncDown(id){ orteDownFrame.location.href='/download?id='+id }</script><iframe name="orteDownFrame" style="display:none"></iframe>`
	page := func(extra, control string) string {
		return `<!doctype html><html><head><meta charset="utf-8"><title>article</title></head><body>` + handler + extra +
			`<ul><li class="green">` + control + `</li><li><a href="/policy" target="_blank"><span>학술지 저작권 정책 내려받기</span></a></li></ul></body></html>`
	}
	official := `<a href="javascript:;" onclick="fncDown('KCI_FI001')"><em><i class="far fa-file-pdf"></i></em><span>KCI 원문 내려받기</span></a>`
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, page("", official)) })
	mux.HandleFunc("/big", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, page("", official)) })
	mux.HandleFunc("/nobutton", func(w http.ResponseWriter, r *http.Request) {
		// The handler exists on every detail page; this article has no control (not public).
		io.WriteString(w, page("", `<a href="/policy"><span>학술지 저작권 정책 내려받기</span></a>`))
	})
	mux.HandleFunc("/notdetail", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `<html><body><h1>no such article</h1></body></html>`)
	})
	click := func(js string) func(http.ResponseWriter, *http.Request) {
		return func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, page("", `<a href="javascript:;" onclick="`+js+`"><span>KCI 원문 내려받기</span></a>`))
		}
	}
	mux.HandleFunc("/external", func(w http.ResponseWriter, r *http.Request) {
		click("location.href='http://localhost:"+portOf(srv)+"/elsewhere'")(w, r)
	})
	mux.HandleFunc("/tologin", click("location.href='/member/login'"))
	mux.HandleFunc("/tochallenge", click("location.href='/verify'"))
	mux.HandleFunc("/toconsent", click("alert('이용약관에 동의하시겠습니까?')"))
	mux.HandleFunc("/tonothing", click("orteDownFrame.location.href='/not-a-file'"))
	mux.HandleFunc("/member/login", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `<html><body><form><input type="text" name="id"><input type="password" name="pw"></form></body></html>`)
	})
	mux.HandleFunc("/verify", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `<html><body><p>자동입력 방지 문자를 입력하세요</p></body></html>`)
	})
	mux.HandleFunc("/not-a-file", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "<html>plain page</html>") })
	mux.HandleFunc("/elsewhere", func(w http.ResponseWriter, r *http.Request) {
		t.Error("the browser reached a host outside the allowlist")
	})
	mux.HandleFunc("/download", func(w http.ResponseWriter, r *http.Request) {
		downloadHits.Add(1)
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", `attachment; filename="KCI_FI001.pdf"`)
		if r.Referer() != "" && strings.HasSuffix(r.Referer(), "/big") {
			w.Write([]byte("%PDF-"))
			w.Write(bytes.Repeat([]byte("x"), 200_000))
			return
		}
		io.WriteString(w, pdfBody)
	})
	// A download the page starts by itself, before anyone clicks.
	mux.HandleFunc("/stray.pdf", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", `attachment; filename="stray.pdf"`)
		io.WriteString(w, strayBody)
	})
	const strayFrame = `<iframe style="display:none" src="/stray.pdf"></iframe>`
	mux.HandleFunc("/stray-ok", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, page(strayFrame, official)) })
	mux.HandleFunc("/stray-only", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, page(strayFrame, `<a href="javascript:;" onclick="orteDownFrame.location.href='/not-a-file'"><span>KCI 원문 내려받기</span></a>`))
	})
	// An unrelated subresource on another host, blocked at load, is not a failed download.
	mux.HandleFunc("/analytics", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, page(`<img src="http://localhost:`+hostPort(r)+`/pixel.png">`, official))
	})
	mux.HandleFunc("/pixel.png", func(w http.ResponseWriter, r *http.Request) {
		t.Error("the browser reached a host outside the allowlist")
	})
	// A click that opens another site in a popup.
	mux.HandleFunc("/popup", func(w http.ResponseWriter, r *http.Request) {
		click("window.open('http://localhost:"+hostPort(r)+"/elsewhere')")(w, r)
	})
	// The download iframe shows a login, a challenge, or a consent page.
	mux.HandleFunc("/frame-login", click("orteDownFrame.location.href='/frame-login-page'"))
	mux.HandleFunc("/frame-challenge", click("orteDownFrame.location.href='/verify'"))
	mux.HandleFunc("/frame-consent", click("orteDownFrame.location.href='/frame-consent-page'"))
	mux.HandleFunc("/frame-login-page", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `<html><body><form><input type="password" name="pw"></form></body></html>`)
	})
	mux.HandleFunc("/frame-consent-page", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `<html><body><p>이용약관에 동의하세요</p></body></html>`)
	})
	// Pages that stand in for the detail page before anything is clicked.
	mux.HandleFunc("/initial-challenge", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `<html><body><p>자동입력 방지 문자를 입력하세요</p></body></html>`)
	})
	mux.HandleFunc("/initial-consent", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `<html><body><p>이용약관에 동의해야 합니다</p></body></html>`)
	})
	// The right path and handler but another article: by a query rewritten after
	// load, and by a redirect to the same path with another id.
	mux.HandleFunc("/rewritten", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, page(`<script>history.replaceState(null, '', location.pathname + '?sereArticleSearchBean.artiId=ART2')</script>`, official))
	})
	mux.HandleFunc("/same-path", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("sereArticleSearchBean.artiId") == "ART1" {
			http.Redirect(w, r, "/same-path?sereArticleSearchBean.artiId=ART2", http.StatusFound)
			return
		}
		io.WriteString(w, page("", official))
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, func(p string) string { return srv.URL + p }
}

func portOf(s *httptest.Server) string { return s.URL[strings.LastIndex(s.URL, ":")+1:] }

// testDriver stands a local loopback server in for the portal: only the test
// switches, which no setting reaches, admit a private address and plain http.
// NoSandbox is set for the tests only: CI runners and containers often cannot
// create the user namespaces Chrome's sandbox needs, and these tests load
// nothing but a local page this package serves.
func testDriver() *Driver {
	d := New(Options{AllowedHosts: []string{"127.0.0.1"}, Timeout: 60 * time.Second, DownloadWait: 4 * time.Second, MaxBytes: 50_000, NoSandbox: true})
	d.allowPrivate, d.allowHTTP = true, true
	return d
}

// The official control is found by its label, clicked like a person would, and
// the file the portal serves comes back byte for byte. The recorded evidence
// names the host and file, no address.
func TestFetchClicksTheOfficialControlAndReturnsTheFile(t *testing.T) {
	haveChrome(t)
	_, u := portal(t)
	f, err := testDriver().Fetch(context.Background(), u("/ok"))
	if err != nil || f.Code != "" || string(f.Body) != pdfBody {
		t.Fatalf("fetch = code %q, %d bytes, %v", f.Code, len(f.Body), err)
	}
	var ev evidence
	if err := json.Unmarshal([]byte(f.Evidence), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Executor != "browser" || ev.Control != archive.KCIControlLabel || ev.DownloadHost != "127.0.0.1" || ev.SuggestedName != "KCI_FI001.pdf" || ev.Size != int64(len(pdfBody)) || len(ev.BlockedHosts) != 0 {
		t.Fatalf("evidence = %+v", ev)
	}
}

// Every case where the portal does not hand over a file ends in a fixed code.
// None of them is worked around: nothing is typed, accepted, or retried.
func TestFetchClassifiesWhatThePortalDoesNotOffer(t *testing.T) {
	haveChrome(t)
	_, u := portal(t)
	for _, tc := range []struct{ path, want string }{
		{"/nobutton", archive.ReasonFulltextNotPublic},
		{"/notdetail", archive.ReasonUnexpectedPage},
		{"/external", archive.ReasonExternalSite},
		{"/tologin", archive.ReasonLoginRequired},
		{"/tochallenge", archive.ReasonChallenge},
		{"/toconsent", archive.ReasonConsentRequired},
		{"/tonothing", archive.ReasonDownloadNotStarted},
		{"/big", archive.ReasonTooLarge},
	} {
		t.Run(strings.TrimPrefix(tc.path, "/"), func(t *testing.T) {
			f, err := testDriver().Fetch(context.Background(), u(tc.path))
			if err != nil || f.Code != tc.want || len(f.Body) != 0 {
				t.Fatalf("code = %q (%d bytes), err = %v, want %s", f.Code, len(f.Body), err, tc.want)
			}
		})
	}
}

// A click that leads off the allowed hosts is blocked before any request goes
// out, and the host is recorded for the operator.
func TestFetchRecordsTheBlockedHostWithoutReachingIt(t *testing.T) {
	haveChrome(t)
	_, u := portal(t)
	f, err := testDriver().Fetch(context.Background(), u("/external"))
	if err != nil || f.Code != archive.ReasonExternalSite {
		t.Fatalf("fetch = %+v, %v", f, err)
	}
	var ev evidence
	if err := json.Unmarshal([]byte(f.Evidence), &ev); err != nil || len(ev.BlockedHosts) != 1 || ev.BlockedHosts[0] != "localhost" {
		t.Fatalf("evidence = %+v, %v", ev, err)
	}
}

// downloadHits counts requests to the portal's download endpoint, so a test can
// tell that a page that was refused was never clicked.
var downloadHits atomic.Int32

const strayBody = "%PDF-1.4 stray, started by the page itself\n"

func hostPort(r *http.Request) string { return r.Host[strings.LastIndex(r.Host, ":")+1:] }

const artiQuery = "?sereArticleSearchBean.artiId=ART1"

// A page that starts a download of its own before the click does not get it
// stored as the article: the stray is cancelled and counted, and the file the
// official control serves is the one returned. Limit (not tested, cannot be from
// here): a download the page starts in the few milliseconds between the click
// mark and the click itself would count as the control's.
func TestFetchIgnoresADownloadTheOfficialControlDidNotStart(t *testing.T) {
	haveChrome(t)
	_, u := portal(t)
	f, err := testDriver().Fetch(context.Background(), u("/stray-ok"))
	if err != nil || f.Code != "" || string(f.Body) != pdfBody {
		t.Fatalf("stray then control = code %q, body %q, %v; want the control's file", f.Code, f.Body, err)
	}
	var ev evidence
	if err := json.Unmarshal([]byte(f.Evidence), &ev); err != nil || ev.StrayDownloads < 1 {
		t.Fatalf("evidence = %+v, %v; want the stray counted", ev, err)
	}
	f, err = testDriver().Fetch(context.Background(), u("/stray-only"))
	if err != nil || f.Code != archive.ReasonDownloadNotStarted || len(f.Body) != 0 {
		t.Fatalf("stray only = code %q, body %q, %v; want download_not_started and no bytes", f.Code, f.Body, err)
	}
}

// A blocked subresource that has nothing to do with the click is recorded but
// does not turn the legitimate download into external_site.
func TestFetchKeepsWaitingAfterAnUnrelatedBlockedRequest(t *testing.T) {
	haveChrome(t)
	_, u := portal(t)
	f, err := testDriver().Fetch(context.Background(), u("/analytics"))
	if err != nil || f.Code != "" || string(f.Body) != pdfBody {
		t.Fatalf("fetch = code %q, %d bytes, %v", f.Code, len(f.Body), err)
	}
	var ev evidence
	if err := json.Unmarshal([]byte(f.Evidence), &ev); err != nil || len(ev.BlockedHosts) != 1 || ev.BlockedHosts[0] != "localhost" {
		t.Fatalf("evidence = %+v, %v; want the blocked host recorded", ev, err)
	}
}

// A control that opens another site in a popup: no popup is opened (so it has no
// requests of its own to guard), the target is recorded, and the job ends as
// external_site with nothing fetched from it.
func TestFetchReadsAPopupToAnotherSiteAsExternal(t *testing.T) {
	haveChrome(t)
	_, u := portal(t)
	f, err := testDriver().Fetch(context.Background(), u("/popup"))
	if err != nil || f.Code != archive.ReasonExternalSite || len(f.Body) != 0 {
		t.Fatalf("popup = code %q, %d bytes, %v", f.Code, len(f.Body), err)
	}
	var ev evidence
	if err := json.Unmarshal([]byte(f.Evidence), &ev); err != nil || len(ev.BlockedHosts) != 1 || ev.BlockedHosts[0] != "localhost" {
		t.Fatalf("evidence = %+v, %v", ev, err)
	}
}

// A login, challenge, or consent page that opens in the download iframe (not in
// the detail page) is classified by its own wording, consent included.
func TestFetchClassifiesWhatTheDownloadFrameShows(t *testing.T) {
	haveChrome(t)
	_, u := portal(t)
	for path, want := range map[string]string{
		"/frame-login":     archive.ReasonLoginRequired,
		"/frame-challenge": archive.ReasonChallenge,
		"/frame-consent":   archive.ReasonConsentRequired,
		// A challenge or consent page in place of the detail page, before any click.
		"/initial-challenge": archive.ReasonChallenge,
		"/initial-consent":   archive.ReasonConsentRequired,
	} {
		f, err := testDriver().Fetch(context.Background(), u(path))
		if err != nil || f.Code != want || len(f.Body) != 0 {
			t.Errorf("%s = code %q (%d bytes), %v; want %s", path, f.Code, len(f.Body), err, want)
		}
	}
}

// The page must carry exactly the article the job asked for. The right path
// with another id (rewritten after load, or reached by a redirect) is not that
// article: it is not clicked, nothing is downloaded, and no bytes come back.
func TestFetchRequiresTheRequestedArticleID(t *testing.T) {
	haveChrome(t)
	_, u := portal(t)
	f, err := testDriver().Fetch(context.Background(), u("/ok"+artiQuery))
	if err != nil || f.Code != "" || string(f.Body) != pdfBody {
		t.Fatalf("the requested article = code %q, %d bytes, %v", f.Code, len(f.Body), err)
	}
	for _, path := range []string{"/rewritten", "/same-path"} {
		before := downloadHits.Load()
		f, err := testDriver().Fetch(context.Background(), u(path+artiQuery))
		if err != nil || f.Code != archive.ReasonUnexpectedPage || len(f.Body) != 0 {
			t.Errorf("%s = code %q (%d bytes), %v; want unexpected_page and no bytes", path, f.Code, len(f.Body), err)
		}
		if n := downloadHits.Load(); n != before {
			t.Errorf("%s: the download endpoint was hit %d time(s) for another article", path, n-before)
		}
	}
}
