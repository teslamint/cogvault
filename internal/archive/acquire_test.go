package archive

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPublicAddr(t *testing.T) {
	for s, want := range map[string]bool{
		"93.184.216.34": true, "2606:2800:220:1:248:1893:25c8:1946": true,
		"127.0.0.1": false, "::1": false, "10.1.2.3": false, "172.16.0.1": false, "192.168.1.1": false,
		"169.254.169.254": false, "fe80::1": false, "100.64.0.1": false, "100.100.100.200": false,
		"0.0.0.0": false, "::": false, "224.0.0.1": false, "255.255.255.255": false, "fc00::1": false,
		"fd00:ec2::254": false, "::ffff:127.0.0.1": false, "::ffff:10.0.0.1": false, "64:ff9b::a00:1": false,
		"2002:a00:1::": false, "198.18.0.1": false, "192.0.2.1": false, "2001:db8::1": false,
	} {
		if got := PublicAddr(netip.MustParseAddr(s)); got != want {
			t.Errorf("PublicAddr(%s) = %v, want %v", s, got, want)
		}
	}
}

// fakeNet routes every checked dial to one httptest server while the
// address check runs on what the fake resolver returns.
type fakeNet struct {
	mu      sync.Mutex
	hosts   map[string][]string // host -> answers; consumed one per lookup when several
	lookups map[string]int
	dialed  []string
	target  string
}

func (f *fakeNet) resolve(_ context.Context, host string) ([]netip.Addr, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	answers, ok := f.hosts[host]
	if !ok {
		return nil, fmt.Errorf("no such host %s", host)
	}
	n := f.lookups[host]
	f.lookups[host]++
	ans := answers[0]
	if n < len(answers) {
		ans = answers[n]
	}
	var out []netip.Addr
	for _, s := range strings.Split(ans, ",") {
		out = append(out, netip.MustParseAddr(s))
	}
	return out, nil
}

func (f *fakeNet) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	f.mu.Lock()
	f.dialed = append(f.dialed, addr)
	f.mu.Unlock()
	return (&net.Dialer{}).DialContext(ctx, network, f.target)
}

func newFakeNet(t *testing.T, h http.Handler) (*fakeNet, Acquirer) {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	u, _ := url.Parse(ts.URL)
	f := &fakeNet{hosts: map[string][]string{"public.test": {"93.184.216.34"}}, lookups: map[string]int{}, target: u.Host}
	return f, Acquirer{Resolve: f.resolve, Dial: f.dial, Timeout: 5 * time.Second}
}

func port(t *testing.T, f *fakeNet) string {
	_, p, _ := net.SplitHostPort(f.target)
	return p
}

func TestAcquireStoresExactBytesWithProvenance(t *testing.T) {
	a, _ := openTest(t)
	var gotHeaders http.Header
	f, q := newFakeNet(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "s3cret"})
			http.Redirect(w, r, "/doc.html", http.StatusFound)
			return
		}
		gotHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("ETag", `"v1"`)
		fmt.Fprint(w, "<html><title>T</title><p>body</p></html>")
	}))
	base := "http://public.test:" + port(t, f)
	res, rec, err := a.Acquire(ctx, q, base+"/start")
	if err != nil {
		t.Fatal(err)
	}
	if gotHeaders.Get("Cookie") != "" || gotHeaders.Get("Authorization") != "" || gotHeaders.Get("Accept-Encoding") != "identity" {
		t.Fatalf("request headers = %v", gotHeaders)
	}
	if rec.FinalURL != base+"/doc.html" || len(rec.Hops) != 2 || rec.Hops[0].Status != 302 || rec.Hops[1].Status != 200 || rec.Headers["ETag"] != `"v1"` {
		t.Fatalf("record = %+v", rec)
	}
	for _, d := range f.dialed {
		if !strings.HasPrefix(d, "93.184.216.34:") {
			t.Fatalf("dialed an unchecked address %s", d)
		}
	}
	c := mustStatus(t, a, res.CaptureID)
	if c.Completeness != "unknown" || c.OriginalName != "doc.html" || c.URL != base+"/doc.html" || c.Supplier != AcquireSupplier || c.Download == nil || c.Download.RequestedURL != base+"/start" {
		t.Fatalf("capture = %+v", c)
	}
	again, _, err := a.Acquire(ctx, q, base+"/start")
	if err != nil || again.CaptureID == res.CaptureID || again.ObjectID != res.ObjectID {
		t.Fatalf("second acquisition = %+v, %v; want a new capture of the same object", again, err)
	}
}

func TestAcquireRefusesNonPublicDestinations(t *testing.T) {
	a, _ := openTest(t)
	f, q := newFakeNet(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/to-private":
			http.Redirect(w, r, "http://private.test:"+r.Host[strings.LastIndex(r.Host, ":")+1:]+"/x", http.StatusFound)
		case "/to-literal":
			http.Redirect(w, r, "http://127.0.0.1/x", http.StatusFound)
		default:
			fmt.Fprint(w, "ok")
		}
	}))
	p := port(t, f)
	f.hosts["private.test"] = []string{"10.0.0.5"}
	f.hosts["mixed.test"] = []string{"93.184.216.34,192.168.0.1"}
	f.hosts["metadata.test"] = []string{"169.254.169.254"}
	for name, raw := range map[string]string{
		"loopback literal": "http://127.0.0.1:" + p + "/",
		"private host":     "http://private.test:" + p + "/",
		"mixed answers":    "http://mixed.test:" + p + "/",
		"metadata":         "http://metadata.test:" + p + "/",
		"redirect private": "http://public.test:" + p + "/to-private",
		"redirect literal": "http://public.test:" + p + "/to-literal",
		"ipv6 loopback":    "http://[::1]:" + p + "/",
		"mapped loopback":  "http://[::ffff:127.0.0.1]:" + p + "/",
	} {
		before := len(f.dialed)
		if _, _, err := a.Acquire(ctx, q, raw); !errors.Is(err, ErrBlockedAddress) {
			t.Errorf("%s: err = %v, want ErrBlockedAddress", name, err)
		}
		for _, d := range f.dialed[before:] {
			if !strings.HasPrefix(d, "93.184.216.34:") {
				t.Errorf("%s: dialed %s", name, d)
			}
		}
	}
	if list, _ := a.List(ctx); len(list) != 0 {
		t.Fatalf("refused acquisitions stored captures: %d", len(list))
	}
}

// TestAcquireDialsOnlyTheCheckedAddress: the resolver answers public first
// and private on any later lookup (DNS rebinding). The connection must go to
// the address that was checked, and the host is looked up once per hop.
func TestAcquireDialsOnlyTheCheckedAddress(t *testing.T) {
	a, _ := openTest(t)
	f, q := newFakeNet(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "x") }))
	f.hosts["rebind.test"] = []string{"93.184.216.34", "127.0.0.1"}
	if _, _, err := a.Acquire(ctx, q, "http://rebind.test:"+port(t, f)+"/"); err != nil {
		t.Fatal(err)
	}
	if f.lookups["rebind.test"] != 1 || len(f.dialed) != 1 || !strings.HasPrefix(f.dialed[0], "93.184.216.34:") {
		t.Fatalf("lookups %d, dialed %v", f.lookups["rebind.test"], f.dialed)
	}
}

func TestAcquireRefusesCredentialsQueriesAndFragmentsWithoutEchoing(t *testing.T) {
	a, _ := openTest(t)
	f, q := newFakeNet(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/next?token=REDIRECT-SECRET", http.StatusFound)
	}))
	p := port(t, f)
	for name, raw := range map[string]string{
		"query":    "http://public.test:" + p + "/doc?sig=QUERY-SECRET",
		"bare ?":   "http://public.test:" + p + "/doc?",
		"fragment": "http://public.test:" + p + "/doc#FRAG-SECRET",
		"userinfo": "http://user:PASS-SECRET@public.test:" + p + "/doc",
		"scheme":   "ftp://public.test/doc",
		"redirect": "http://public.test:" + p + "/doc",
	} {
		before := len(f.dialed)
		_, rec, err := a.Acquire(ctx, q, raw)
		if err == nil {
			t.Fatalf("%s: accepted", name)
		}
		if strings.Contains(err.Error(), "SECRET") || strings.Contains(rec.RequestedURL+rec.FinalURL, "SECRET") {
			t.Fatalf("%s: secret echoed: %v %+v", name, err, rec)
		}
		for _, h := range rec.Hops {
			if strings.Contains(h.URL, "SECRET") {
				t.Fatalf("%s: secret in hops: %+v", name, rec.Hops)
			}
		}
		if name != "redirect" && len(f.dialed) != before {
			t.Fatalf("%s: dialed before refusing", name)
		}
	}
}

func TestAcquireLimitsSizeTimeAndRedirects(t *testing.T) {
	a, _ := openTest(t)
	f, q := newFakeNet(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/big":
			w.Write([]byte(strings.Repeat("x", 2048)))
		case "/slow":
			time.Sleep(300 * time.Millisecond)
			w.Write([]byte("late"))
		case "/forbidden":
			w.WriteHeader(http.StatusForbidden)
		case "/missing":
			w.WriteHeader(http.StatusNotFound)
		case "/loop":
			// A fixed self-redirect: the client must stop at its hop limit.
			http.Redirect(w, r, "/loop", http.StatusFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	p := "http://public.test:" + port(t, f)
	q.MaxBytes = 1024
	if _, _, err := a.Acquire(ctx, q, p+"/big"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("big: %v", err)
	}
	q.Timeout = 100 * time.Millisecond
	if _, _, err := a.Acquire(ctx, q, p+"/slow"); err == nil {
		t.Fatal("slow response accepted")
	}
	q.Timeout = 5 * time.Second
	if _, _, err := a.Acquire(ctx, q, p+"/loop"); !errors.Is(err, ErrRemote) {
		t.Fatalf("redirect loop: %v", err)
	}
	if _, _, err := a.Acquire(ctx, q, p+"/forbidden"); !errors.Is(err, ErrAccessRestricted) {
		t.Fatalf("403: %v", err)
	}
	if _, _, err := a.Acquire(ctx, q, p+"/missing"); !errors.Is(err, ErrRemote) {
		t.Fatalf("404: %v", err)
	}
	if list, _ := a.List(ctx); len(list) != 0 {
		t.Fatalf("failed acquisitions stored captures: %d", len(list))
	}
}

func TestReacquireRecordsAttempts(t *testing.T) {
	a, _ := openTest(t)
	state := "forbidden"
	f, q := newFakeNet(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if state == "forbidden" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		fmt.Fprint(w, "now public")
	}))
	url := "http://public.test:" + port(t, f) + "/paper.txt"
	old := mustCapture(t, a, SourceRequest{Content: strings.NewReader("snippet"), OriginalName: "s.md", URL: url, Completeness: "incomplete"})
	if _, _, err := a.Reacquire(ctx, q, old.CaptureID, "access_restricted"); !errors.Is(err, ErrAccessRestricted) {
		t.Fatalf("forbidden: %v", err)
	}
	state = "open"
	res, _, err := a.Reacquire(ctx, q, old.CaptureID, "access_restricted")
	if err != nil {
		t.Fatal(err)
	}
	c := mustStatus(t, a, old.CaptureID)
	if len(c.Attempts) != 2 || c.Attempts[0].Result != "human_action_required" || c.Attempts[1].Result != "acquired" || c.Attempts[1].ResultCaptureID != res.CaptureID {
		t.Fatalf("attempts = %+v", c.Attempts)
	}
	if c.Acquisition != "partial" || mustStatus(t, a, res.CaptureID).Completeness != "unknown" {
		t.Fatal("reacquisition changed the old capture or promoted completeness")
	}
	withQuery := mustCapture(t, a, SourceRequest{Content: strings.NewReader("q"), OriginalName: "q.md", URL: url + "?token=ATTEMPT-SECRET"})
	if _, _, err := a.Reacquire(ctx, q, withQuery.CaptureID, "other"); err == nil {
		t.Fatal("query URL reacquired")
	}
	for _, at := range mustStatus(t, a, withQuery.CaptureID).Attempts {
		if strings.Contains(at.Tried+at.RemainingGap, "ATTEMPT-SECRET") {
			t.Fatalf("attempt records the query: %+v", at)
		}
	}
}

func TestAcquiredContentCodedBodyIsNeverExtractedAsHTML(t *testing.T) {
	a, _ := openTest(t)
	f, q := newFakeNet(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/declared.html" {
			// A declared coding over bytes that still look like HTML: the
			// header alone must stop extraction, not the byte sniff.
			w.Header().Set("Content-Encoding", "x-custom")
			fmt.Fprint(w, "<html><p>looks plain</p></html>")
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		fmt.Fprint(gz, "<html><p>compressed</p></html>")
		gz.Close()
	}))
	res, rec, err := a.Acquire(ctx, q, "http://public.test:"+port(t, f)+"/page.html")
	if err != nil {
		t.Fatal(err)
	}
	c := mustStatus(t, a, res.CaptureID)
	if rec.Headers["Content-Encoding"] != "gzip" || c.OriginalName != "page.bin" {
		t.Fatalf("content-coded body: name %s, record %+v", c.OriginalName, rec)
	}
	if _, err := a.ExtractText(ctx, res.CaptureID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("extract of a gzip body: err = %v", err)
	}
	if n := len(mustStatus(t, a, res.CaptureID).Extractions); n != 0 {
		t.Fatal("refused extraction left a record")
	}
	declared, _, err := a.Acquire(ctx, q, "http://public.test:"+port(t, f)+"/declared.html")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ExtractText(ctx, declared.CaptureID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("extract of a body with a declared coding: err = %v", err)
	}
	// The same bytes captured without a coding are extracted first; the
	// coded capture must still be refused, whatever the order.
	plain := mustCapture(t, a, SourceRequest{Content: strings.NewReader("<html><p>looks plain</p></html>"), OriginalName: "plain.html", MediaType: "text/html"})
	if plain.ObjectID != declared.ObjectID {
		t.Fatalf("fixture: objects %s and %s differ", plain.ObjectID, declared.ObjectID)
	}
	if _, err := a.ExtractText(ctx, plain.CaptureID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ExtractText(ctx, declared.CaptureID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("coded capture after its bytes were extracted: err = %v", err)
	}
}
