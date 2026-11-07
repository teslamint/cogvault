package trial

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/teslamint/cogvault/internal/archive"
)

var ctx = context.Background()

const pdfBody = "%PDF-1.4\n1 0 obj<<>>endobj\ntrailer<<>>\n%%EOF\n"

// env builds a valid environment and applies overrides ("" deletes a key).
func env(root string, over map[string]string) []string {
	m := map[string]string{
		EnvAck: AckPhrase, EnvAcceptedBy: "tester 2026-11-07", EnvAllowedHosts: "pdf.test", EnvRoot: root,
	}
	for k, v := range over {
		if v == "" {
			delete(m, k)
		} else {
			m[k] = v
		}
	}
	var out []string
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

func newRoot(t *testing.T) string {
	t.Helper()
	parent := filepath.Join(t.TempDir(), "trial")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(parent, "data")
}

func mustConfig(t *testing.T, root string, over map[string]string) Config {
	t.Helper()
	c, err := FromEnviron(env(root, over))
	if err != nil {
		t.Fatalf("FromEnviron: %v", err)
	}
	return c
}

// site is a local server for pdf.test and other.test; every request is counted by host.
type site struct {
	mu   sync.Mutex
	hits map[string]int
	srv  *httptest.Server
}

func (s *site) count(host string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[host]
}

func newSite(t *testing.T) *site {
	t.Helper()
	s := &site{hits: map[string]int{}}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.Split(r.Host, ":")[0]
		s.mu.Lock()
		s.hits[host]++
		s.mu.Unlock()
		switch r.URL.Path {
		case "/ok.pdf":
			w.Header().Set("Content-Type", "application/pdf")
			w.Write([]byte(pdfBody))
		case "/notpdf":
			w.Header().Set("Content-Type", "application/pdf")
			w.Write([]byte("<html>not a pdf</html>"))
		case "/big.pdf":
			w.Write(append([]byte("%PDF-"), make([]byte, 8192)...))
		case "/redir":
			http.Redirect(w, r, "http://other.test/ok.pdf", http.StatusFound)
		default:
			w.Write([]byte(pdfBody))
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

// open starts a Service against the local site with the public-address rule
// intact (every host resolves to a public address; the dial goes to the server).
func (s *site) open(t *testing.T, cfg Config) *Service {
	t.Helper()
	svc, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { svc.Close() })
	u, _ := url.Parse(s.srv.URL)
	svc.Acquirer = archive.Acquirer{
		Resolve: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		},
		Dial: func(c context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(c, network, u.Host)
		},
		Timeout: 5 * time.Second,
	}
	svc.Owner = "trial-test"
	svc.relaxed = true
	svc.Cfg.MinInterval = 0 // the interval has its own test; the others must not wait
	return svc
}

func TestConfigDefaultsAndRefusals(t *testing.T) {
	root := newRoot(t)
	c := mustConfig(t, root, nil)
	if c.MaxQueued != DefaultMaxQueued || c.MaxFetchesHour != DefaultFetchesHour || c.MinInterval != DefaultMinInterval || c.MaxBytes != CapMaxBytes || !c.HostAllowed("PDF.test") || c.HostAllowed("pdf.test.evil.example") {
		t.Fatalf("defaults = %+v", c)
	}
	for name, over := range map[string]map[string]string{
		"no acknowledgement":      {EnvAck: ""},
		"changed acknowledgement": {EnvAck: AckPhrase + "."},
		"no accepted-by":          {EnvAcceptedBy: ""},
		"multi-line accepted-by":  {EnvAcceptedBy: "a\nb"},
		"no hosts":                {EnvAllowedHosts: ""},
		"ip host":                 {EnvAllowedHosts: "93.184.216.34"},
		"uppercase host":          {EnvAllowedHosts: "PDF.test"},
		"wildcard host":           {EnvAllowedHosts: "*.pdf.test"},
		"host with port":          {EnvAllowedHosts: "pdf.test:443"},
		"host with scheme":        {EnvAllowedHosts: "https://pdf.test"},
		"no dot":                  {EnvAllowedHosts: "localhost"},
		"relative root":           {EnvRoot: "trial/data"},
		"root slash":              {EnvRoot: "/"},
		"shallow root":            {EnvRoot: "/trial"},
		"unclean root":            {EnvRoot: "/trial/../trial/data"},
		"unclean forbidden":       {EnvForbiddenPaths: "/a/../b"},
		"queue over cap":          {EnvMaxQueued: "51"},
		"queue zero":              {EnvMaxQueued: "0"},
		"submits over cap":        {EnvMaxSubmitsPerDay: "101"},
		"hour over cap":           {EnvMaxFetchesHour: "61"},
		"day over cap":            {EnvMaxFetchesDay: "201"},
		"hour above day":          {EnvMaxFetchesHour: "30", EnvMaxFetchesDay: "20"},
		"interval below floor":    {EnvMinInterval: "1s"},
		"interval not a duration": {EnvMinInterval: "soon"},
		"attempts over cap":       {EnvMaxAttempts: "6"},
		"bytes over cap":          {EnvMaxBytes: "33554433"},
		"bytes tiny":              {EnvMaxBytes: "10"},
		"unknown trial setting":   {"COGVAULT_TRIAL_ALLOW_HOST": "pdf.test"},
	} {
		if _, err := FromEnviron(env(root, over)); !errors.Is(err, ErrConfig) {
			t.Errorf("%s: err = %v, want ErrConfig", name, err)
		}
	}
	// A refused value is never echoed back.
	_, err := FromEnviron(env(root, map[string]string{EnvAllowedHosts: "SECRET-Value.example"}))
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("the refusal echoes the value: %v", err)
	}
}

func TestRootGuard(t *testing.T) {
	base := t.TempDir()
	mk := func(rel string) string {
		p := filepath.Join(base, rel)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write := func(p string) {
		t.Helper()
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	prod := mk("prod/archive")
	write(filepath.Join(prod, "registry.db"))

	for name, tc := range map[string]struct {
		root  func() string
		extra []string
	}{
		"the production root itself":   {func() string { return prod }, nil},
		"inside the production root":   {func() string { return filepath.Join(prod, "trial") }, nil},
		"a protected path":             {func() string { return mk("forbidden") }, []string{filepath.Join(base, "forbidden")}},
		"inside a protected path":      {func() string { return mk("forbidden2/x") }, []string{filepath.Join(base, "forbidden2")}},
		"containing a protected path":  {func() string { mk("outer/protected"); return filepath.Join(base, "outer") }, []string{filepath.Join(base, "outer", "protected")}},
		"a directory with content":     {func() string { p := mk("content"); write(filepath.Join(p, "notes.md")); return p }, nil},
		"a wiki beside its config":     {func() string { p := mk("wiki"); mk("wiki/.obsidian"); return p }, nil},
		"inside a dir with an archive": {func() string { return filepath.Join(prod, "..", "archive", "sub") }, nil},
		"a file":                       {func() string { p := filepath.Join(base, "afile"); write(p); return p }, nil},
		"a symbolic link":              {func() string { l := filepath.Join(base, "link"); os.Symlink(mk("target"), l); return l }, nil},
		"a missing parent":             {func() string { return filepath.Join(base, "nope", "deeper", "root") }, nil},
		"a wrong marker":               {func() string { p := mk("badmarker"); write(filepath.Join(p, MarkerName)); return p }, nil},
	} {
		root := tc.root()
		c := mustConfig(t, filepath.Clean(root), map[string]string{EnvForbiddenPaths: strings.Join(tc.extra, ",")})
		if err := c.CheckRoot(); !errors.Is(err, ErrRoot) {
			t.Errorf("%s: CheckRoot = %v, want ErrRoot", name, err)
		}
		if _, err := Open(c); !errors.Is(err, ErrRoot) {
			t.Errorf("%s: Open = %v, want ErrRoot", name, err)
		}
	}
	// Through a symbolic link into a protected path.
	link := filepath.Join(base, "viaLink")
	if err := os.Symlink(prod, link); err != nil {
		t.Fatal(err)
	}
	c := mustConfig(t, filepath.Join(link, "x"), nil)
	if err := c.CheckRoot(); !errors.Is(err, ErrRoot) {
		t.Errorf("a path through a link into the production root: %v", err)
	}
	// The production registry was not touched by any refusal above.
	if entries, _ := os.ReadDir(prod); len(entries) != 1 {
		t.Fatalf("the production directory changed: %v", entries)
	}

	// A new root is created and marked; a second start reuses the marker; the
	// root with content and its marker is accepted, an unmarked copy is not.
	root := filepath.Join(base, "good", "data")
	mk("good")
	c = mustConfig(t, root, nil)
	svc, err := Open(c)
	if err != nil {
		t.Fatal(err)
	}
	id := svc.Marker.ID
	svc.Close()
	svc2, err := Open(c)
	if err != nil || svc2.Marker.ID != id || !strings.HasPrefix(id, "trial_") {
		t.Fatalf("second start = %v, %v (marker %q)", err, svc2, id)
	}
	svc2.Close()
	if err := os.Remove(filepath.Join(root, MarkerName)); err != nil {
		t.Fatal(err)
	}
	if err := c.CheckRoot(); !errors.Is(err, ErrRoot) {
		t.Fatalf("a used root whose marker is gone: %v", err)
	}
}

func TestSubmitPolicyAndLimits(t *testing.T) {
	site := newSite(t)
	svc := site.open(t, mustConfig(t, newRoot(t), map[string]string{EnvMaxQueued: "2", EnvMaxSubmitsPerDay: "3"}))
	svc.relaxed = false
	for name, raw := range map[string]string{
		"plain http":   "http://pdf.test/a.pdf",
		"another host": "https://other.test/a.pdf",
		"a subdomain":  "https://x.pdf.test/a.pdf",
		"a look-alike": "https://pdf.test.evil.example/a.pdf",
		"userinfo":     "https://user@pdf.test/a.pdf",
		"a port":       "https://pdf.test:8443/a.pdf",
		"an address":   "https://93.184.216.34/a.pdf",
		"not a URL":    "pdf.test/a.pdf",
		"a file URL":   "file:///etc/passwd",
		"too long":     "https://pdf.test/" + strings.Repeat("a", 2100),
	} {
		if _, _, err := svc.Submit(ctx, raw, ""); !errors.Is(err, ErrPolicy) {
			t.Errorf("%s: err = %v, want ErrPolicy", name, err)
		}
	}
	if _, _, err := svc.Submit(ctx, "https://pdf.test/a.pdf?token=x", ""); err == nil {
		t.Error("a URL with a query was accepted")
	}
	if act, _ := svc.A.JobActivity(ctx, time.Now().Add(-time.Hour)); act.Created != 0 {
		t.Fatalf("a refused submission was queued: %+v", act)
	}
	a, _, err := svc.Submit(ctx, "https://PDF.test/a.pdf", "me")
	if err != nil || a.MaxAttempts != svc.Cfg.MaxAttempts || a.Executor != archive.ExecutorHTTP {
		t.Fatalf("submit = %+v, %v", a, err)
	}
	if _, _, err := svc.Submit(ctx, "https://pdf.test/b.pdf", ""); err != nil {
		t.Fatal(err)
	}
	// The queue holds two: a third is refused until one finishes.
	if _, _, err := svc.Submit(ctx, "https://pdf.test/c.pdf", ""); !errors.Is(err, ErrLimit) {
		t.Fatalf("a full queue: %v", err)
	}
}

func TestWorkerFetchesOnePDFAndKeepsItAcrossARestart(t *testing.T) {
	site := newSite(t)
	cfg := mustConfig(t, newRoot(t), nil)
	svc := site.open(t, cfg)
	j, _, err := svc.Submit(ctx, "http://pdf.test/ok.pdf", "smoke")
	if err != nil {
		t.Fatal(err)
	}
	got, why, err := svc.RunOnce(ctx)
	if err != nil || why != "" || got == nil || got.JobID != j.JobID || got.State != archive.JobHeld || got.ResultCaptureID == "" {
		t.Fatalf("RunOnce = %+v, %q, %v", got, why, err)
	}
	sum := sha256.Sum256([]byte(pdfBody))
	wantObj := "sha256:" + hex.EncodeToString(sum[:])
	c, err := svc.A.Status(ctx, got.ResultCaptureID)
	if err != nil || c.ObjectID != wantObj || c.Completeness != "unknown" || c.Ingest.State != "held" {
		t.Fatalf("capture = %+v, %v; want %s held with unknown completeness", c, err, wantObj)
	}
	if len(got.Attempts) != 1 || !strings.Contains(got.Attempts[0].Response, `"fetched_at"`) || !strings.Contains(got.Attempts[0].Response, `"size"`) {
		t.Fatalf("the attempt does not record the response: %+v", got.Attempts)
	}
	if n := site.count("pdf.test"); n != 1 {
		t.Fatalf("requests = %d, want 1", n)
	}

	// A restart: the same job and capture are read back from the same root.
	svc.Close()
	again, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if again.Marker.ID != svc.Marker.ID {
		t.Fatalf("marker changed across the restart: %s -> %s", svc.Marker.ID, again.Marker.ID)
	}
	read, err := again.A.GetJob(ctx, j.JobID)
	if err != nil || read.State != archive.JobHeld || read.ResultCaptureID != got.ResultCaptureID || len(read.Attempts) != 1 {
		t.Fatalf("after the restart = %+v, %v", read, err)
	}
	if c2, err := again.A.Status(ctx, got.ResultCaptureID); err != nil || c2.ObjectID != wantObj {
		t.Fatalf("capture after the restart = %+v, %v", c2, err)
	}
}

func TestWorkerRefusesWhatThePolicyDoesNotAllow(t *testing.T) {
	site := newSite(t)
	svc := site.open(t, mustConfig(t, newRoot(t), map[string]string{EnvMaxBytes: "4096"}))
	run := func(u string) *archive.Job {
		t.Helper()
		if _, _, err := svc.Submit(ctx, u, ""); err != nil {
			t.Fatalf("submit %s: %v", u, err)
		}
		j, _, err := svc.RunOnce(ctx)
		if err != nil || j == nil {
			t.Fatalf("RunOnce(%s) = %+v, %v", u, j, err)
		}
		return j
	}
	if j := run("http://pdf.test/notpdf"); j.State != archive.JobFailed || !strings.Contains(j.Attempts[0].Detail, "not a PDF") || j.ResultCaptureID != "" {
		t.Fatalf("a non-PDF = %+v", j)
	}
	if j := run("http://pdf.test/big.pdf"); j.State != archive.JobFailed || j.ResultCaptureID != "" {
		t.Fatalf("an oversize body = %+v", j)
	}
	before := site.count("other.test")
	if j := run("http://pdf.test/redir"); j.State != archive.JobFailed || !strings.Contains(j.Attempts[0].Detail, "allowed list") {
		t.Fatalf("a redirect off the allowlist = %+v", j)
	}
	if site.count("other.test") != before {
		t.Fatal("a host outside the allowlist was contacted by a redirect")
	}
	if n, _ := svc.A.JobActivity(ctx, time.Now().Add(-time.Hour)); n.Fetches != 3 {
		t.Fatalf("fetches = %d", n.Fetches)
	}
}

// A job queued by another route (the generic `archive job submit` on the same
// root, or any direct write to the registry) does not widen the policy: the
// worker checks the claimed job before sending anything.
func TestWorkerReChecksJobsQueuedByAnotherRoute(t *testing.T) {
	site := newSite(t)
	svc := site.open(t, mustConfig(t, newRoot(t), nil))
	svc.relaxed = false // the real scheme rule: only https is fetched
	for name, req := range map[string]archive.JobRequest{
		"another host":   {URL: "https://other.test/x.pdf", Submitter: "exec"},
		"a look-alike":   {URL: "https://pdf.test.evil.example/x.pdf", Submitter: "exec"},
		"plain http":     {URL: "http://pdf.test/x.pdf", Submitter: "exec"},
		"an address":     {URL: "https://93.184.216.34/x.pdf", Submitter: "exec"},
		"a large budget": {URL: "https://other.test/y.pdf", Submitter: "exec", MaxAttempts: 20},
	} {
		if _, _, err := svc.A.SubmitJob(ctx, req); err != nil {
			t.Fatalf("%s: queue = %v", name, err)
		}
		j, why, err := svc.RunOnce(ctx)
		if err != nil || why != "" || j == nil || j.State != archive.JobFailed || !strings.Contains(j.Attempts[0].Detail, "nothing was sent") {
			t.Fatalf("%s: RunOnce = %+v, %q, %v", name, j, why, err)
		}
	}
	if site.count("pdf.test")+site.count("other.test") != 0 {
		t.Fatal("a refused job sent a request")
	}
	// A browser job is never claimed by this worker.
	kci := "https://www.kci.go.kr/kciportal/ci/sereArticleSearch/ciSereArtiView.kci?sereArticleSearchBean.artiId=ART002605798"
	kj, _, err := svc.A.SubmitJob(ctx, archive.JobRequest{URL: kci, Submitter: "exec", Executor: archive.ExecutorBrowser})
	if err != nil {
		t.Fatal(err)
	}
	if j, why, err := svc.RunOnce(ctx); err != nil || j != nil || why != "" {
		t.Fatalf("RunOnce with only a browser job = %+v, %q, %v", j, why, err)
	}
	if cur, _ := svc.A.GetJob(ctx, kj.JobID); cur.State != "queued" || cur.AttemptsMade != 0 {
		t.Fatalf("the browser job was touched: %+v", cur)
	}
}

func TestWorkerBudgetAndPause(t *testing.T) {
	site := newSite(t)
	cfg := mustConfig(t, newRoot(t), map[string]string{EnvMaxFetchesHour: "2", EnvMaxFetchesDay: "3"})
	svc := site.open(t, cfg)
	for _, p := range []string{"a", "b", "c", "d"} {
		if _, _, err := svc.Submit(ctx, "http://pdf.test/"+p+".pdf", ""); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if j, why, err := svc.RunOnce(ctx); err != nil || why != "" || j == nil {
			t.Fatalf("within budget: %+v, %q, %v", j, why, err)
		}
	}
	before := site.count("pdf.test")
	j, why, err := svc.RunOnce(ctx)
	if err != nil || j != nil || !strings.Contains(why, "hourly") {
		t.Fatalf("over the hourly limit: %+v, %q, %v", j, why, err)
	}
	if site.count("pdf.test") != before {
		t.Fatal("a request was sent over the budget")
	}
	if cur := mustCount(t, svc); cur != 2 {
		t.Fatalf("a refused pass claimed a job: %d tries", cur)
	}

	// The minimum interval is counted from the last try, across restarts.
	svc.Cfg.MinInterval = time.Hour
	svc.Cfg.MaxFetchesHour = 50
	if _, why, _ := svc.RunOnce(ctx); !strings.Contains(why, "minimum interval") {
		t.Fatalf("interval: %q", why)
	}
	svc.Cfg.MinInterval = 0
	if j, why, err := svc.RunOnce(ctx); err != nil || why != "" || j == nil {
		t.Fatalf("after the interval: %+v, %q, %v", j, why, err)
	}
	if _, why, _ := svc.RunOnce(ctx); !strings.Contains(why, "daily") {
		t.Fatalf("daily: %q", why)
	}

	// Pause stops claims without stopping the process; resume continues.
	svc.Cfg.MaxFetchesDay = 100
	if err := svc.Cfg.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	if _, why, _ := svc.RunOnce(ctx); why != "paused" {
		t.Fatalf("paused: %q", why)
	}
	if err := svc.Allowed(); err == nil {
		t.Fatal("Allowed passed while paused")
	}
	if err := svc.Cfg.SetPaused(false); err != nil {
		t.Fatal(err)
	}
	if err := svc.Allowed(); err != nil {
		t.Fatalf("Allowed after resume: %v", err)
	}
	if j, _, err := svc.RunOnce(ctx); err != nil || j == nil {
		t.Fatalf("after resume: %+v, %v", j, err)
	}
}

func mustCount(t *testing.T, s *Service) int {
	t.Helper()
	a, err := s.A.JobActivity(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return a.Fetches
}

// The trial binary links neither the ingest pipeline, the MCP server, the
// storage and index layers, nor the production activation gate, so no run of it
// can hand a trial capture to ingest or reach the wiki.
func TestTrialPackageDoesNotLinkIngestOrTheProductionGate(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Fatal("go tool is required for the dependency gate")
	}
	out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", "github.com/teslamint/cogvault/internal/trial", "github.com/teslamint/cogvault/cmd/cogvault-trial").CombinedOutput()
	if err != nil {
		t.Fatalf("go list failed: %v: %s", err, out)
	}
	for _, banned := range []string{"internal/ingest", "internal/mcp", "internal/storage", "internal/index", "internal/archivegate", "internal/adapter", "internal/config"} {
		for _, dep := range strings.Fields(string(out)) {
			if strings.HasSuffix(dep, banned) || strings.Contains(dep, banned+"/") {
				t.Errorf("internal/trial depends on %s", dep)
			}
		}
	}
}

type syncBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// The worker loop pauses without exiting while the PAUSE file exists, says so
// once, claims nothing, and resumes by itself when the file is removed.
func TestLoopPausesLogsOnceAndResumes(t *testing.T) {
	site := newSite(t)
	cfg := mustConfig(t, newRoot(t), nil)
	svc := site.open(t, cfg)
	j, _, err := svc.Submit(ctx, "http://pdf.test/ok.pdf", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	var out, errOut syncBuf
	lctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- svc.Loop(lctx, LoopOptions{Poll: 5 * time.Millisecond}, &out, &errOut) }()
	time.Sleep(120 * time.Millisecond)
	if n := site.count("pdf.test"); n != 0 {
		t.Fatalf("a paused worker sent %d request(s)", n)
	}
	if err := cfg.SetPaused(false); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cur, _ := svc.A.GetJob(ctx, j.JobID); cur.State == archive.JobHeld {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Loop = %v", err)
	}
	if cur, _ := svc.A.GetJob(ctx, j.JobID); cur.State != archive.JobHeld {
		t.Fatalf("the job did not run after resume: %+v", cur)
	}
	if n := strings.Count("\n"+errOut.String(), "\npaused: "); n != 1 || !strings.Contains(errOut.String(), "resumed") {
		t.Fatalf("log = %q; want one pause line and a resume line", errOut.String())
	}
}

// A single pass (--once) returns the root error instead of pausing, so a
// misplaced root fails loudly under a script.
func TestLoopOnceReturnsARootError(t *testing.T) {
	site := newSite(t)
	cfg := mustConfig(t, newRoot(t), nil)
	svc := site.open(t, cfg)
	if err := cfg.SetPaused(true); err != nil {
		t.Fatal(err)
	}
	var out, errOut syncBuf
	if err := svc.Loop(ctx, LoopOptions{Poll: time.Millisecond, Once: true}, &out, &errOut); err == nil {
		t.Fatal("a paused --once run returned no error")
	}
}

// TestConcurrentWorkersShareTheRequestBudget checks the last permitted fetch
// cannot be claimed twice by two processes sharing one trial root.
func TestConcurrentWorkersShareTheRequestBudget(t *testing.T) {
	site := newSite(t)
	cfg := mustConfig(t, newRoot(t), map[string]string{EnvMaxFetchesHour: "1", EnvMaxFetchesDay: "1"})
	first := site.open(t, cfg)
	second := site.open(t, cfg)
	second.Owner = "trial-second-worker"
	for _, name := range []string{"one", "two"} {
		if _, _, err := first.Submit(ctx, "http://pdf.test/"+name+".pdf", ""); err != nil {
			t.Fatal(err)
		}
	}
	unlock, err := first.claimLock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	lockCtx, cancel := context.WithTimeout(ctx, 40*time.Millisecond)
	_, _, err = second.RunOnce(lockCtx)
	cancel()
	unlock()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second worker ignored claim lock: %v", err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, svc := range []*Service{first, second} {
		go func(s *Service) {
			<-start
			_, _, err := s.RunOnce(ctx)
			results <- err
		}(svc)
	}
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if got := site.count("pdf.test"); got != 1 {
		t.Fatalf("requests with budget 1: %d", got)
	}
	activity, err := first.A.JobActivity(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if activity.Fetches != 1 || activity.Active != 1 {
		t.Fatalf("activity = %+v, want 1 fetch and 1 waiting", activity)
	}
}

// TestTrialAcquirerAllowURLPredicate checks the policy callback
// installed on the Acquirer that sees every redirect.
func TestTrialAcquirerAllowURLPredicate(t *testing.T) {
	site := newSite(t)
	svc := site.open(t, mustConfig(t, newRoot(t), nil))
	svc.relaxed = false
	check := svc.acquirer().AllowURL
	for raw, want := range map[string]bool{
		"https://pdf.test/a.pdf":      true,
		"http://pdf.test/a.pdf":       false,
		"https://pdf.test:8443/a.pdf": false,
		"https://other.test/a.pdf":    false,
	} {
		u, _ := url.Parse(raw)
		if got := check(u); got != want {
			t.Errorf("%s: allowed=%v, want %v", raw, got, want)
		}
	}
}

// An absent protected path can still overlap the new root through a symlink
// in its nearest existing ancestor. The missing suffix must not hide that.
func TestRootGuardResolvesMissingProtectedPathThroughSymlink(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	parent := filepath.Join(real, "trial")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "data")
	protected := filepath.Join(alias, "trial", "data", "missing", "child")
	cfg := mustConfig(t, root, map[string]string{EnvForbiddenPaths: protected})
	if err := cfg.CheckRoot(); !errors.Is(err, ErrRoot) {
		t.Fatalf("CheckRoot = %v, want overlap refusal", err)
	}
	if _, err := Open(cfg); !errors.Is(err, ErrRoot) {
		t.Fatalf("Open = %v, want overlap refusal", err)
	}
	if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused root was created: %v", err)
	}
}
