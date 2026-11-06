package archive

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func submit(t *testing.T, a *Archive, req JobRequest) Job {
	t.Helper()
	if req.Submitter == "" {
		req.Submitter = "tester"
	}
	j, _, err := a.SubmitJob(ctx, req)
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}
	return j
}

func getJob(t *testing.T, a *Archive, id string) Job {
	t.Helper()
	j, err := a.GetJob(ctx, id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	return j
}

// The same request from another client returns the same job, an explicit key
// reused for a different URL is a conflict, and a URL the acquirer refuses
// never enters the queue.
func TestSubmitJobDedupesAndRefusesUnsafeURLs(t *testing.T) {
	a, _ := openTest(t)
	first := submit(t, a, JobRequest{URL: "http://public.test/a.pdf", Submitter: "claude", ClaimRef: "p. 3"})
	again, existing, err := a.SubmitJob(ctx, JobRequest{URL: "http://public.test/a.pdf", Submitter: "chatgpt", ClaimRef: "p. 3"})
	if err != nil || !existing || again.JobID != first.JobID {
		t.Fatalf("second client got %+v existing=%v err=%v, want job %s", again, existing, err, first.JobID)
	}
	if again.Submitter != "claude" {
		t.Fatalf("existing job changed submitter to %q", again.Submitter)
	}
	other := submit(t, a, JobRequest{URL: "http://public.test/a.pdf", ClaimRef: "p. 9"})
	if other.JobID == first.JobID {
		t.Fatal("a different claim reference must be a different job")
	}

	k1 := submit(t, a, JobRequest{URL: "http://public.test/k1.pdf", Key: "same"})
	if _, _, err := a.SubmitJob(ctx, JobRequest{URL: "http://public.test/k2.pdf", Key: "same", Submitter: "x"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("key reuse for another URL: %v, want ErrConflict (job %s)", err, k1.JobID)
	}

	for name, req := range map[string]JobRequest{
		"query":        {URL: "http://public.test/a.pdf?token=1"},
		"userinfo":     {URL: "https://user:pw@public.test/a.pdf"},
		"fragment":     {URL: "http://public.test/a.pdf#p3"},
		"scheme":       {URL: "file:///etc/passwd"},
		"no submitter": {URL: "http://public.test/x", Submitter: " "},
		"bad seen":     {URL: "http://public.test/x", SeenSHA256: "abc"},
		"bad budget":   {URL: "http://public.test/x", MaxAttempts: 99},
		"bad exec":     {URL: "http://public.test/x", Executor: "shell"},
		"no report":    {URL: "http://public.test/x", ReportCaptureID: "cap_missing"},
	} {
		if req.Submitter == "" {
			req.Submitter = "tester"
		} else if strings.TrimSpace(req.Submitter) == "" {
			req.Submitter = ""
		}
		if _, _, err := a.SubmitJob(ctx, req); err == nil {
			t.Errorf("%s: SubmitJob accepted %+v", name, req)
		}
	}
	var n int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("jobs = %d, %v; refused submissions must not be stored (want 3)", n, err)
	}
}

// A job is linked to the report that cited it, and only a report capture
// qualifies.
func TestSubmitJobLinksReportAndRejectsNonReport(t *testing.T) {
	a, _ := openTest(t)
	src := mustCapture(t, a, verified("src\n", "s.md", "https://example.org/s"))
	rep, err := a.ImportReport(ctx, ReportRequest{Content: strings.NewReader("report"), OriginalName: "r.md"})
	if err != nil {
		t.Fatal(err)
	}
	j := submit(t, a, JobRequest{URL: "http://public.test/a.pdf", ReportCaptureID: rep.CaptureID})
	if j.ReportCaptureID != rep.CaptureID {
		t.Fatalf("report = %q, want %q", j.ReportCaptureID, rep.CaptureID)
	}
	if _, _, err := a.SubmitJob(ctx, JobRequest{URL: "http://public.test/b.pdf", Submitter: "t", ReportCaptureID: src.CaptureID}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("source capture as report: %v, want ErrInvalid", err)
	}
}

// One worker run stores the bytes through the capture registry and ends the
// job held (completeness unknown). The capture's status names the job and
// says the bytes were recovered after research; when the submitter supplied
// the digest it read, a match is seen_during_research and a mismatch stays
// recovered with a note.
func TestRunNextJobStoresCaptureAndRecordsProvenance(t *testing.T) {
	a, _ := openTest(t)
	body := "%PDF-1.4\nbody\n%%EOF\n"
	f, q := newFakeNet(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		fmt.Fprint(w, body)
	}))
	_ = f
	plain := submit(t, a, JobRequest{URL: "http://public.test/a.pdf"})
	job, err := a.RunNextJob(ctx, q, "w1", DefaultJobLease)
	if err != nil || job == nil {
		t.Fatalf("RunNextJob = %+v, %v", job, err)
	}
	if job.JobID != plain.JobID || job.State != JobHeld || job.Provenance != ProvRecovered || job.ResultCaptureID == "" {
		t.Fatalf("job = %+v", job)
	}
	if len(job.Attempts) != 1 || job.Attempts[0].Outcome != JobHeld || job.Attempts[0].CaptureID != job.ResultCaptureID ||
		!strings.Contains(job.Attempts[0].Response, "93.184.216.34") {
		t.Fatalf("attempts = %+v", job.Attempts)
	}
	c := mustStatus(t, a, job.ResultCaptureID)
	if c.Completeness != "unknown" || c.Ingest.State != "held" || !strings.HasPrefix(c.Supplier, JobSupplierPrefix+job.JobID) {
		t.Fatalf("capture = %+v", c)
	}
	if len(c.Collections) != 1 || c.Collections[0].Provenance != ProvRecovered || c.Collections[0].JobID != job.JobID {
		t.Fatalf("collections = %+v", c.Collections)
	}

	// The same bytes under a job that carries the digest the research read.
	seen := submit(t, a, JobRequest{URL: "http://public.test/b.pdf", SeenSHA256: c.ObjectID})
	got, err := a.RunNextJob(ctx, q, "w1", DefaultJobLease)
	if err != nil || got == nil || got.JobID != seen.JobID || got.Provenance != ProvSeen {
		t.Fatalf("seen job = %+v, %v", got, err)
	}
	wrong := submit(t, a, JobRequest{URL: "http://public.test/c.pdf", SeenSHA256: "sha256:" + strings.Repeat("0", 64)})
	got, err = a.RunNextJob(ctx, q, "w1", DefaultJobLease)
	if err != nil || got == nil || got.JobID != wrong.JobID || got.Provenance != ProvRecovered ||
		!strings.Contains(got.Attempts[0].Detail, "differ") {
		t.Fatalf("mismatch job = %+v, %v", got, err)
	}
	if none, err := a.RunNextJob(ctx, q, "w1", DefaultJobLease); err != nil || none != nil {
		t.Fatalf("empty queue = %+v, %v", none, err)
	}
}

// A worker that stops mid-job loses its lease; the next claim records the
// interrupted try, requeues after a backoff, and the stale worker cannot
// overwrite the new try.
func TestJobLeaseExpiryResumesAndFencesStaleWorker(t *testing.T) {
	a, _ := openTest(t)
	clk := withClock(a)
	j := submit(t, a, JobRequest{URL: "http://public.test/a.pdf"})
	first, err := a.ClaimJob(ctx, ExecutorHTTP, "w1", time.Minute)
	if err != nil || first == nil || first.AttemptsMade != 1 || first.State != "running" {
		t.Fatalf("first claim = %+v, %v", first, err)
	}
	if again, err := a.ClaimJob(ctx, ExecutorHTTP, "w2", time.Minute); err != nil || again != nil {
		t.Fatalf("a leased job was claimed again: %+v, %v", again, err)
	}
	clk.t = clk.t.Add(2 * time.Minute) // w1's lease is over; its backoff (1 min) has not passed for the requeue
	if got, err := a.ClaimJob(ctx, ExecutorHTTP, "w2", time.Minute); err != nil || got != nil {
		t.Fatalf("claim during backoff = %+v, %v", got, err)
	}
	mid := getJob(t, a, j.JobID)
	if mid.State != "queued" || len(mid.Attempts) != 1 || mid.Attempts[0].Outcome != "lease_expired" || mid.Attempts[0].Owner != "w1" {
		t.Fatalf("after reclaim = %+v", mid)
	}
	clk.t = clk.t.Add(2 * time.Minute)
	second, err := a.ClaimJob(ctx, ExecutorHTTP, "w2", time.Minute)
	if err != nil || second == nil || second.AttemptsMade != 2 || second.LeaseOwner != "w2" {
		t.Fatalf("second claim = %+v, %v", second, err)
	}
	if _, err := a.CompleteJob(ctx, j.JobID, "w1", 1, JobOutcome{State: JobFailed, Detail: "late"}); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale worker completion: %v, want ErrLeaseLost", err)
	}
	done, err := a.CompleteJob(ctx, j.JobID, "w2", 2, JobOutcome{State: JobFailed, Detail: "gone"})
	if err != nil || done.State != JobFailed || len(done.Attempts) != 2 || done.Attempts[1].Number != 2 {
		t.Fatalf("complete = %+v, %v", done, err)
	}
	if _, err := a.CompleteJob(ctx, j.JobID, "w2", 2, JobOutcome{State: JobFailed}); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("second completion of a finished job: %v, want ErrLeaseLost", err)
	}
}

// The fence is the attempt, not the owner name: a worker that restarts under
// the same --owner after its lease was reclaimed must not let the old run
// complete the new try.
func TestStaleRunCannotCompleteNewAttemptWithSameOwner(t *testing.T) {
	a, _ := openTest(t)
	clk := withClock(a)
	j := submit(t, a, JobRequest{URL: "http://public.test/a.pdf"})
	old, err := a.ClaimJob(ctx, ExecutorHTTP, "worker", time.Minute)
	if err != nil || old == nil || old.AttemptsMade != 1 {
		t.Fatalf("first claim = %+v, %v", old, err)
	}
	clk.t = clk.t.Add(2 * time.Minute) // lease over; the sweep requeues behind a backoff
	if c, err := a.ClaimJob(ctx, ExecutorHTTP, "worker", time.Minute); err != nil || c != nil {
		t.Fatalf("sweep = %+v, %v", c, err)
	}
	clk.t = clk.t.Add(2 * time.Minute)
	fresh, err := a.ClaimJob(ctx, ExecutorHTTP, "worker", time.Minute)
	if err != nil || fresh == nil || fresh.AttemptsMade != 2 || fresh.LeaseOwner != old.LeaseOwner {
		t.Fatalf("second claim = %+v, %v", fresh, err)
	}
	if _, err := a.CompleteJob(ctx, j.JobID, "worker", old.AttemptsMade, JobOutcome{State: JobFailed, Detail: "stale"}); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale run completed the new attempt: %v, want ErrLeaseLost", err)
	}
	if got := getJob(t, a, j.JobID); got.State != "running" || len(got.Attempts) != 1 || got.Attempts[0].Outcome != "lease_expired" {
		t.Fatalf("job after the refused completion = %+v", got)
	}
	if _, err := a.CompleteJob(ctx, j.JobID, "worker", fresh.AttemptsMade, JobOutcome{State: JobFailed, Detail: "current"}); err != nil {
		t.Fatalf("current run could not complete: %v", err)
	}
}

// A job whose tries all expire ends failed instead of looping forever.
func TestJobLeaseExpiryHonorsAttemptBudget(t *testing.T) {
	a, _ := openTest(t)
	clk := withClock(a)
	j := submit(t, a, JobRequest{URL: "http://public.test/a.pdf", MaxAttempts: 2})
	for i := range 2 {
		if c, err := a.ClaimJob(ctx, ExecutorHTTP, "w", time.Minute); err != nil || c == nil {
			t.Fatalf("claim %d = %+v, %v", i, c, err)
		}
		clk.t = clk.t.Add(2 * time.Hour) // the lease is over
		if c, err := a.ClaimJob(ctx, ExecutorHTTP, "w", time.Minute); err != nil || c != nil {
			t.Fatalf("reclaim sweep %d = %+v, %v; the requeued job is still in backoff", i, c, err)
		}
		clk.t = clk.t.Add(2 * time.Hour) // the backoff is over
	}
	if c, err := a.ClaimJob(ctx, ExecutorHTTP, "w", time.Minute); err != nil || c != nil {
		t.Fatalf("third claim = %+v, %v", c, err)
	}
	if got := getJob(t, a, j.JobID); got.State != JobFailed || got.AttemptsMade != 2 || len(got.Attempts) != 2 {
		t.Fatalf("job = %+v", got)
	}
}

// A transient server error is retried with backoff until the budget is used
// up, every try is in the history, and an operator retry gets a fresh budget
// with attempt numbers that keep counting.
func TestJobTransientErrorsRetryWithBackoffThenFail(t *testing.T) {
	a, _ := openTest(t)
	clk := withClock(a)
	var hits atomic.Int32
	_, q := newFakeNet(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "busy", http.StatusServiceUnavailable)
	}))
	j := submit(t, a, JobRequest{URL: "http://public.test/a.pdf", MaxAttempts: 3})
	run := func() *Job {
		t.Helper()
		got, err := a.RunNextJob(ctx, q, "w", DefaultJobLease)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := run(); got == nil || got.State != "queued" || got.AttemptsMade != 1 {
		t.Fatalf("after try 1 = %+v", got)
	}
	if got := run(); got != nil {
		t.Fatalf("job ran again before its backoff ended: %+v", got)
	}
	clk.t = clk.t.Add(2 * time.Minute)
	if got := run(); got == nil || got.State != "queued" || got.AttemptsMade != 2 {
		t.Fatalf("after try 2 = %+v", got)
	}
	clk.t = clk.t.Add(10 * time.Minute)
	last := run()
	if last == nil || last.State != JobFailed || len(last.Attempts) != 3 || !strings.Contains(last.Attempts[2].Detail, "attempts used up") {
		t.Fatalf("after try 3 = %+v", last)
	}
	if hits.Load() != 3 {
		t.Fatalf("server hits = %d, want 3", hits.Load())
	}

	retried, err := a.RetryJob(ctx, j.JobID)
	if err != nil || retried.State != "queued" || retried.MaxAttempts != 6 {
		t.Fatalf("RetryJob = %+v, %v", retried, err)
	}
	clk.t = clk.t.Add(time.Minute)
	if got := run(); got == nil || got.AttemptsMade != 4 || len(got.Attempts) != 4 || got.Attempts[3].Number != 4 {
		t.Fatalf("after retry = %+v", got)
	}
	if _, err := a.RetryJob(ctx, "job_missing"); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("RetryJob of a missing job: %v", err)
	}
}

// A permanent 404 is not retried.
func TestJobPermanentStatusFailsOnFirstTry(t *testing.T) {
	a, _ := openTest(t)
	var hits atomic.Int32
	_, q := newFakeNet(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.NotFound(w, r)
	}))
	j := submit(t, a, JobRequest{URL: "http://public.test/gone.pdf"})
	got, err := a.RunNextJob(ctx, q, "w", DefaultJobLease)
	if err != nil || got == nil || got.JobID != j.JobID || got.State != JobFailed || len(got.Attempts) != 1 || hits.Load() != 1 {
		t.Fatalf("job = %+v, hits %d, %v", got, hits.Load(), err)
	}
}

// Access control is never bypassed: a 403 ends the job as
// human_action_required after one request, with no credential or cookie
// sent, and the job stores no bytes.
func TestJobAccessRestrictedNeedsAPerson(t *testing.T) {
	a, _ := openTest(t)
	var hits atomic.Int32
	var sawAuth atomic.Bool
	_, q := newFakeNet(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			sawAuth.Store(true)
		}
		http.Error(w, "login required", http.StatusForbidden)
	}))
	j := submit(t, a, JobRequest{URL: "http://public.test/paywalled.pdf"})
	got, err := a.RunNextJob(ctx, q, "w", DefaultJobLease)
	if err != nil || got == nil || got.JobID != j.JobID || got.State != JobHuman || got.ResultCaptureID != "" || hits.Load() != 1 || sawAuth.Load() {
		t.Fatalf("job = %+v hits=%d auth=%v err=%v", got, hits.Load(), sawAuth.Load(), err)
	}
	if !strings.Contains(got.Attempts[0].Detail, "never uses credentials") {
		t.Fatalf("detail = %q", got.Attempts[0].Detail)
	}
}

// A host that resolves to a private address is refused before any
// connection, fails the job without a retry, and leaves a record of why.
func TestJobSSRFTargetFailsWithoutConnecting(t *testing.T) {
	a, _ := openTest(t)
	f, q := newFakeNet(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the server was reached")
	}))
	f.hosts["internal.test"] = []string{"10.0.0.5"}
	f.hosts["mixed.test"] = []string{"93.184.216.34,169.254.169.254"}
	for _, host := range []string{"internal.test", "mixed.test"} {
		j := submit(t, a, JobRequest{URL: "http://" + host + "/secret"})
		got, err := a.RunNextJob(ctx, q, "w", DefaultJobLease)
		if err != nil || got == nil || got.JobID != j.JobID || got.State != JobFailed || len(got.Attempts) != 1 ||
			!strings.Contains(got.Attempts[0].Detail, "not a public address") {
			t.Fatalf("%s: job = %+v, %v", host, got, err)
		}
	}
	if len(f.dialed) != 0 {
		t.Fatalf("dialed %v", f.dialed)
	}
}

// A redirect to a private host fails the job; the redirect target is checked
// like the first URL.
func TestJobRedirectToPrivateHostFails(t *testing.T) {
	a, _ := openTest(t)
	f, q := newFakeNet(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://internal.test/x", http.StatusFound)
	}))
	f.hosts["internal.test"] = []string{"192.168.1.10"}
	submit(t, a, JobRequest{URL: "http://public.test/a.pdf"})
	got, err := a.RunNextJob(ctx, q, "w", DefaultJobLease)
	if err != nil || got == nil || got.State != JobFailed || !strings.Contains(got.Attempts[0].Detail, "not a public address") {
		t.Fatalf("job = %+v, %v", got, err)
	}
	if len(f.dialed) != 1 {
		t.Fatalf("dialed %v, want only the first, public hop", f.dialed)
	}
}

// An oversize body and a short body (Content-Length mismatch) are recorded
// with the reason and store nothing: bytes are never kept as complete.
func TestJobOversizeFailsAndShortBodyRetries(t *testing.T) {
	a, _ := openTest(t)
	_, q := newFakeNet(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/big":
			fmt.Fprint(w, strings.Repeat("x", 200))
		case "/short":
			w.Header().Set("Content-Length", "500")
			fmt.Fprint(w, "only a part")
		}
	}))
	q.MaxBytes = 100
	big := submit(t, a, JobRequest{URL: "http://public.test/big"})
	short := submit(t, a, JobRequest{URL: "http://public.test/short"})
	gotBig, err := a.RunNextJob(ctx, q, "w", DefaultJobLease)
	if err != nil || gotBig == nil || gotBig.JobID != big.JobID || gotBig.State != JobFailed || gotBig.ResultCaptureID != "" {
		t.Fatalf("big = %+v, %v", gotBig, err)
	}
	gotShort, err := a.RunNextJob(ctx, q, "w", DefaultJobLease)
	if err != nil || gotShort == nil || gotShort.JobID != short.JobID || gotShort.State != "queued" || gotShort.ResultCaptureID != "" {
		t.Fatalf("short = %+v, %v", gotShort, err)
	}
	var objects int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM objects`).Scan(&objects); err != nil || objects != 0 {
		t.Fatalf("objects = %d, %v; failed downloads must store nothing", objects, err)
	}
}

// Jobs and their history are in the registry: a new handle (a restarted
// worker, another client) sees the same state, and a version 5 registry
// upgrades in place.
func TestJobsSurviveReopenAndUpgradeFromSchema5(t *testing.T) {
	a, root := openTest(t)
	j := submit(t, a, JobRequest{URL: "http://public.test/a.pdf", Submitter: "chat-1", EditionHint: "2024 ed."})
	if c, err := a.ClaimJob(ctx, ExecutorHTTP, "w1", time.Minute); err != nil || c == nil {
		t.Fatalf("claim = %+v, %v", c, err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	got := getJob(t, b, j.JobID)
	if got.State != "running" || got.LeaseOwner != "w1" || got.Submitter != "chat-1" || got.EditionHint != "2024 ed." {
		t.Fatalf("after reopen = %+v", got)
	}
	if _, err := b.db.Exec(`DROP TABLE job_attempts; DROP TABLE jobs; PRAGMA user_version = 5`); err != nil {
		t.Fatal(err)
	}
	b.Close()
	up, err := Open(root)
	if err != nil {
		t.Fatalf("upgrade from schema 5: %v", err)
	}
	defer up.Close()
	var v int
	if err := up.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != schemaVersion {
		t.Fatalf("user_version = %d, %v", v, err)
	}
	if _, _, err := up.SubmitJob(ctx, JobRequest{URL: "http://public.test/a.pdf", Submitter: "t"}); err != nil {
		t.Fatalf("SubmitJob after upgrade: %v", err)
	}
}

// Browser jobs wait for the interactive executor: the HTTP worker never
// claims them, and a browser executor uses the same claim/complete contract.
func TestBrowserJobsAreNotClaimedByHTTPWorker(t *testing.T) {
	a, _ := openTest(t)
	_, q := newFakeNet(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the HTTP worker fetched a browser job")
	}))
	j := submit(t, a, JobRequest{URL: "http://public.test/kci", Executor: ExecutorBrowser})
	if got, err := a.RunNextJob(ctx, q, "w", DefaultJobLease); err != nil || got != nil {
		t.Fatalf("HTTP worker = %+v, %v", got, err)
	}
	c, err := a.ClaimJob(ctx, ExecutorBrowser, "browser-1", time.Minute)
	if err != nil || c == nil || c.JobID != j.JobID {
		t.Fatalf("browser claim = %+v, %v", c, err)
	}
	done, err := a.CompleteJob(ctx, j.JobID, "browser-1", 1, JobOutcome{State: JobHuman, Detail: "official download needs a person to solve the check"})
	if err != nil || done.State != JobHuman {
		t.Fatalf("complete = %+v, %v", done, err)
	}
}

// CompleteJob rejects an outcome whose capture does not match its state.
func TestCompleteJobRejectsInconsistentOutcome(t *testing.T) {
	a, _ := openTest(t)
	j := submit(t, a, JobRequest{URL: "http://public.test/a.pdf"})
	if _, err := a.ClaimJob(ctx, ExecutorHTTP, "w", time.Minute); err != nil {
		t.Fatal(err)
	}
	for _, out := range []JobOutcome{
		{State: JobHeld}, {State: JobFailed, CaptureID: "cap_x"}, {State: "done"},
	} {
		if _, err := a.CompleteJob(ctx, j.JobID, "w", 1, out); !errors.Is(err, ErrInvalid) {
			t.Errorf("CompleteJob(%+v) = %v, want ErrInvalid", out, err)
		}
	}
	if got := getJob(t, a, j.JobID); got.State != "running" || len(got.Attempts) != 0 {
		t.Fatalf("rejected outcomes changed the job: %+v", got)
	}
}

// A transport error carries the archive host's resolver and socket
// addresses. The stored reason names the class only, so job status can be
// shown to a remote client.
func TestJobReasonsDoNotLeakNetworkAddresses(t *testing.T) {
	a, _ := openTest(t)
	f, q := newFakeNet(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	q.Dial = func(context.Context, string, string) (net.Conn, error) {
		return nil, fmt.Errorf("dial tcp 10.1.2.3:5353: connect: connection refused (resolver 192.168.1.1:53)")
	}
	f.hosts["down.test"] = []string{"93.184.216.34"}
	submit(t, a, JobRequest{URL: "http://down.test/a.pdf"})
	got, err := a.RunNextJob(ctx, q, "w", DefaultJobLease)
	if err != nil || got == nil || got.State != "queued" || len(got.Attempts) != 1 {
		t.Fatalf("job = %+v, %v", got, err)
	}
	if d := got.Attempts[0].Detail; strings.Contains(d, "10.1.2.3") || strings.Contains(d, "192.168") || !strings.HasPrefix(d, "network error") {
		t.Fatalf("detail = %q", d)
	}
}
