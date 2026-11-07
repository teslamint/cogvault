package archive

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// AllowHost narrows the public-address rule: it is asked about the first URL and
// about every redirect, and a refusal means nothing is sent to that host.
func TestAcquirerAllowHostCoversTheFirstURLAndEveryRedirect(t *testing.T) {
	var hits []string
	var elsewhere string // set once the server's port is known; the redirect target is not derived from the request
	f, q := newFakeNet(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.Host)
		if r.URL.Path == "/redir" {
			http.Redirect(w, r, elsewhere, http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Write([]byte("%PDF-1.4 ok"))
	}))
	f.hosts["elsewhere.test"] = []string{"93.184.216.35"}
	port := port(t, f)
	elsewhere = "http://elsewhere.test:" + port + "/doc"
	q.AllowHost = func(h string) bool { return h == "public.test" }

	if _, rec, err := q.fetch(ctx, "http://public.test:"+port+"/doc"); err != nil || rec.Size == 0 {
		t.Fatalf("an allowed host: %v", err)
	}
	n := len(hits)
	if _, _, err := q.fetch(ctx, "http://other.test:"+port+"/doc"); !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("another host: %v, want ErrHostNotAllowed", err)
	}
	if _, _, err := q.fetch(ctx, "http://public.test:"+port+"/redir"); !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("a redirect off the list: %v, want ErrHostNotAllowed", err)
	}
	for _, h := range hits[n:] {
		if strings.HasPrefix(h, "elsewhere.test") || strings.HasPrefix(h, "other.test") {
			t.Fatalf("a host that is not allowed was contacted: %v", hits[n:])
		}
	}
	if out := classifyAcquire(Result{}, AcquisitionRecord{}, ErrHostNotAllowed); out.State != JobFailed || !strings.Contains(out.Detail, "allowed list") {
		t.Fatalf("classification = %+v", out)
	}
}

func TestAcquirerPDFOnlyRefusesAnythingElseBeforeStoring(t *testing.T) {
	a, root := openTest(t)
	f, q := newFakeNet(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/pdf" {
			w.Write([]byte("%PDF-1.4 ok"))
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Write([]byte("<html>pretending</html>"))
	}))
	q.PDFOnly = true
	base := "http://public.test:" + port(t, f)
	if _, _, err := a.Acquire(ctx, q, base+"/pdf"); err != nil {
		t.Fatalf("a PDF: %v", err)
	}
	if _, _, err := a.Acquire(ctx, q, base+"/html"); !errors.Is(err, ErrNotPDF) {
		t.Fatalf("a page that claims to be a PDF: %v, want ErrNotPDF", err)
	}
	if n := countCaptures(t, a); n != 1 || len(objectFiles(t, root)) != 1 {
		t.Fatalf("captures = %d, objects = %v; the refused body was stored", n, objectFiles(t, root))
	}
}

// JobActivity counts what the queue did, from the registry, so a budget survives
// a restart and does not depend on how a job was queued.
func TestJobActivityCountsWaitingCreatedAndStartedTries(t *testing.T) {
	a, _ := openTest(t)
	clk := withClock(a)
	start := clk.t
	if act, err := a.JobActivity(ctx, start.Add(-time.Hour)); err != nil || act != (JobActivity{}) {
		t.Fatalf("empty queue = %+v, %v", act, err)
	}
	submit(t, a, JobRequest{URL: "http://public.test/a.pdf"})
	submit(t, a, JobRequest{URL: "http://public.test/b.pdf"})
	act, _ := a.JobActivity(ctx, start.Add(-time.Hour))
	if act.Active != 2 || act.Created != 2 || act.Fetches != 0 || !act.LastStart.IsZero() {
		t.Fatalf("two queued = %+v", act)
	}
	claimed, err := a.ClaimJob(ctx, ExecutorHTTP, "w", time.Minute)
	if err != nil || claimed == nil {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	if act, _ = a.JobActivity(ctx, start.Add(-time.Hour)); act.Fetches != 1 || !act.LastStart.Equal(start) {
		t.Fatalf("a running try = %+v", act)
	}
	if _, err := a.CompleteJob(ctx, claimed.JobID, "w", claimed.AttemptsMade, JobOutcome{State: JobFailed, Detail: "x"}); err != nil {
		t.Fatal(err)
	}
	clk.t = start.Add(2 * time.Hour)
	act, _ = a.JobActivity(ctx, clk.t.Add(-time.Hour))
	if act.Active != 1 || act.Created != 0 || act.Fetches != 0 {
		t.Fatalf("an hour on = %+v; the old try and job must fall out of the window", act)
	}
	if act, _ = a.JobActivity(ctx, start.Add(-time.Hour)); act.Fetches != 1 || act.Created != 2 {
		t.Fatalf("a wider window = %+v", act)
	}
}

// TestAcquirerAllowURLStopsRedirectBeforeNetwork tests the full URL policy on
// redirects, including scheme and port even when the host is unchanged.
func TestAcquirerAllowURLStopsRedirectBeforeNetwork(t *testing.T) {
	hits := 0
	var next string
	f, q := newFakeNet(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Redirect(w, r, next, http.StatusFound)
	}))
	allowedPort := port(t, f)
	q.AllowHost = func(h string) bool { return h == "public.test" }
	q.AllowURL = func(u *url.URL) bool { return u.Scheme == "http" && u.Port() == allowedPort }
	first := "http://public.test:" + allowedPort + "/redir"
	for _, target := range []string{
		"https://public.test:" + allowedPort + "/after",
		"http://public.test:8443/after?token=PRIVATE-REDIRECT-TOKEN",
	} {
		next = target
		before := hits
		if _, _, err := q.fetch(ctx, first); !errors.Is(err, ErrURLNotAllowed) {
			t.Fatalf("redirect to %s: %v, want ErrURLNotAllowed", target, err)
		} else if !strings.Contains(err.Error(), "URL is outside the allowed policy") || strings.Contains(err.Error(), "could not connect") || strings.Contains(err.Error(), "PRIVATE-REDIRECT-TOKEN") {
			t.Fatalf("redirect policy refusal was mislabeled: %v", err)
		}
		if hits != before+1 {
			t.Fatalf("redirect target was contacted: %d new hits", hits-before)
		}
	}
}
