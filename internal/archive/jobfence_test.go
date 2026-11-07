package archive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func countCaptures(t *testing.T, a *Archive) int {
	t.Helper()
	var n int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM captures`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// reclaim moves the clock past w1's lease and its requeue backoff and lets w2
// claim the job, the way a restarted worker would.
func reclaim(t *testing.T, a *Archive, clk *clock, executor, owner string) *Job {
	t.Helper()
	clk.t = clk.t.Add(2 * time.Minute)
	if got, err := a.ClaimJob(ctx, executor, owner, time.Minute); err != nil || got != nil {
		t.Fatalf("claim during backoff = %+v, %v", got, err)
	}
	clk.t = clk.t.Add(2 * time.Minute)
	got, err := a.ClaimJob(ctx, executor, owner, time.Minute)
	if err != nil || got == nil || got.AttemptsMade != 2 {
		t.Fatalf("reclaim = %+v, %v", got, err)
	}
	return got
}

type runResult struct {
	job *Job
	err error
}

// A try whose lease was reclaimed while its download was in flight must leave
// no capture behind: the capture is stored behind the same lease check that
// completes the job, so a stale worker has neither a capture nor an attempt.
func TestReclaimedHTTPTryStoresNoCapture(t *testing.T) {
	a, root := openTest(t)
	clk := withClock(a)
	arrived, release := make(chan struct{}), make(chan struct{})
	f, q := newFakeNet(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(arrived)
		<-release
		w.Header().Set("Content-Type", "application/pdf")
		w.Write([]byte("%PDF-1.4 the stale worker's copy"))
	}))
	j := submit(t, a, JobRequest{URL: "http://public.test:" + port(t, f) + "/a.pdf"})
	done := make(chan runResult, 1)
	go func() {
		job, err := a.RunNextJob(ctx, q, "w1", time.Minute)
		done <- runResult{job, err}
	}()
	<-arrived
	second := reclaim(t, a, clk, ExecutorHTTP, "w2")
	close(release)
	r := <-done
	if !errors.Is(r.err, ErrLeaseLost) {
		t.Fatalf("stale run = %+v, %v; want ErrLeaseLost", r.job, r.err)
	}
	if n := countCaptures(t, a); n != 0 {
		t.Fatalf("a stale try left %d capture(s)", n)
	}
	if files := objectFiles(t, root); len(files) != 0 {
		t.Fatalf("a stale try left object files: %v", files)
	}
	got := getJob(t, a, j.JobID)
	if got.State != "running" || got.LeaseOwner != "w2" || got.ResultCaptureID != "" || len(got.Attempts) != 1 || got.Attempts[0].Outcome != "lease_expired" {
		t.Fatalf("job after the stale run = %+v", got)
	}
	// The current claim is untouched and can still finish with a capture.
	res := mustCapture(t, a, verified("fresh", "a.pdf", "http://public.test/a.pdf"))
	if _, err := a.CompleteJob(ctx, j.JobID, "w2", second.AttemptsMade, JobOutcome{State: JobHeld, CaptureID: res.CaptureID, Detail: "ok"}); err != nil {
		t.Fatalf("current claim could not finish: %v", err)
	}
}

func TestReclaimedBrowserTryStoresNoCapture(t *testing.T) {
	a, root := openTest(t)
	clk := withClock(a)
	arrived, release := make(chan struct{}), make(chan struct{})
	d := &fakeBrowser{fn: func(context.Context, string) (BrowserFetch, error) {
		close(arrived)
		<-release
		return BrowserFetch{Body: []byte("%PDF-1.4 the stale worker's copy"), Evidence: `{"executor":"browser"}`}, nil
	}}
	j := submit(t, a, JobRequest{URL: kciFixtureURL, Executor: ExecutorBrowser})
	done := make(chan runResult, 1)
	go func() {
		job, err := a.RunNextBrowserJob(ctx, d, "w1", time.Minute)
		done <- runResult{job, err}
	}()
	<-arrived
	reclaim(t, a, clk, ExecutorBrowser, "w2")
	close(release)
	r := <-done
	if !errors.Is(r.err, ErrLeaseLost) {
		t.Fatalf("stale run = %+v, %v; want ErrLeaseLost", r.job, r.err)
	}
	if n := countCaptures(t, a); n != 0 || len(objectFiles(t, root)) != 0 {
		t.Fatalf("a stale try left %d capture(s), files %v", n, objectFiles(t, root))
	}
	if got := getJob(t, a, j.JobID); got.State != "running" || got.LeaseOwner != "w2" || got.ResultCaptureID != "" {
		t.Fatalf("job after the stale run = %+v", got)
	}
}

// The job a store transaction finished is published only after the commit.
// A rolled-back transaction leaves done empty, so no caller can report success
// for a job that was not recorded.
func TestBindingPublishesTheJobOnlyAfterCommit(t *testing.T) {
	a, _ := openTest(t)
	j := submit(t, a, JobRequest{URL: "http://public.test/a.pdf"})
	claimed, err := a.ClaimJob(ctx, ExecutorHTTP, "w1", time.Minute)
	if err != nil || claimed == nil {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	res := mustCapture(t, a, verified("x", "x.pdf", "http://public.test/x.pdf"))
	b := &jobBinding{id: j.JobID, owner: "w1", attempt: claimed.AttemptsMade,
		outcome: func(Result, AcquisitionRecord) JobOutcome {
			return JobOutcome{State: JobHeld, CaptureID: res.CaptureID, Detail: "ok"}
		}}
	rolledBack := func() {
		unlock, err := a.lock(ctx, false)
		if err != nil {
			t.Fatal(err)
		}
		defer unlock()
		tx, err := a.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := a.finishBinding(ctx, tx, b, Result{CaptureID: res.CaptureID}); err != nil {
			t.Fatal(err)
		}
		if b.done != nil {
			t.Fatal("the job was published before the commit")
		}
		tx.Rollback()
	}
	rolledBack()
	if b.done != nil {
		t.Fatal("a rolled-back transaction published the job")
	}
	if got := getJob(t, a, j.JobID); got.State != "running" || got.ResultCaptureID != "" {
		t.Fatalf("a rolled-back finish changed the job: %+v", got)
	}
}

// acquired promises a capture that is eligible for hand-off. A job may end
// acquired only when its capture's completeness is verified; held is the state
// for everything else.
func TestCompleteJobAcquiredNeedsAVerifiedCapture(t *testing.T) {
	a, _ := openTest(t)
	j := submit(t, a, JobRequest{URL: "http://public.test/a.pdf"})
	claimed, err := a.ClaimJob(ctx, ExecutorHTTP, "w1", time.Minute)
	if err != nil || claimed == nil {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	unknown, err := a.CaptureSource(ctx, SourceRequest{Content: strings.NewReader("u"), OriginalName: "u.pdf", URL: "http://public.test/u.pdf"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.CompleteJob(ctx, j.JobID, "w1", claimed.AttemptsMade, JobOutcome{State: JobAcquired, CaptureID: unknown.CaptureID}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("acquired with an unknown capture: %v, want ErrInvalid", err)
	}
	if got := getJob(t, a, j.JobID); got.State != "running" || len(got.Attempts) != 0 {
		t.Fatalf("the refused completion changed the job: %+v", got)
	}
	if h, err := a.CompleteJob(ctx, j.JobID, "w1", claimed.AttemptsMade, JobOutcome{State: JobHeld, CaptureID: unknown.CaptureID}); err != nil || h.State != JobHeld {
		t.Fatalf("held with the same capture: %+v, %v", h, err)
	}

	j2 := submit(t, a, JobRequest{URL: "http://public.test/b.pdf"})
	c2, err := a.ClaimJob(ctx, ExecutorHTTP, "w1", time.Minute)
	if err != nil || c2 == nil || c2.JobID != j2.JobID {
		t.Fatalf("claim = %+v, %v", c2, err)
	}
	ver := mustCapture(t, a, verified("v", "v.pdf", "http://public.test/v.pdf"))
	if d, err := a.CompleteJob(ctx, j2.JobID, "w1", c2.AttemptsMade, JobOutcome{State: JobAcquired, CaptureID: ver.CaptureID}); err != nil || d.State != JobAcquired {
		t.Fatalf("acquired with a verified capture: %+v, %v", d, err)
	}
}

// After a download, a storage failure is a storage failure, not a network
// error: the job fails at once (a retry would download the same file again for
// the same fault) with a fixed code, and nothing about the host is in what a
// remote client can read.
func TestStorageFailureIsClassifiedAndLeaksNothing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file permissions do not stop root")
	}
	for _, tc := range []struct {
		name string
		run  func(t *testing.T, a *Archive, q Acquirer, base string) (*Job, error)
	}{
		{"http", func(t *testing.T, a *Archive, q Acquirer, base string) (*Job, error) {
			submit(t, a, JobRequest{URL: base + "/a.pdf", MaxAttempts: 3})
			return a.RunNextJob(ctx, q, "w1", time.Minute)
		}},
		{"browser", func(t *testing.T, a *Archive, q Acquirer, base string) (*Job, error) {
			submit(t, a, JobRequest{URL: kciFixtureURL, Executor: ExecutorBrowser, MaxAttempts: 3})
			d := &fakeBrowser{fn: func(context.Context, string) (BrowserFetch, error) {
				return BrowserFetch{Body: []byte("%PDF-1.4 x"), Evidence: `{"executor":"browser"}`}, nil
			}}
			return a.RunNextBrowserJob(ctx, d, "w1", time.Minute)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, root := openTest(t)
			f, q := newFakeNet(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/pdf")
				w.Write([]byte("%PDF-1.4 x"))
			}))
			tmp := filepath.Join(root, "objects", "tmp")
			if err := os.Chmod(tmp, 0o500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chmod(tmp, 0o700) })
			got, err := tc.run(t, a, q, "http://public.test:"+port(t, f))
			if err != nil || got == nil {
				t.Fatalf("run = %+v, %v", got, err)
			}
			if got.State != JobFailed || got.AttemptsMade != 1 || len(got.Attempts) != 1 || got.ResultCaptureID != "" {
				t.Fatalf("job = %+v; want failed at once after one try, no capture", got)
			}
			at := got.Attempts[0]
			if !strings.Contains(at.Detail, "storage_failed (permission_denied)") {
				t.Fatalf("detail = %q", at.Detail)
			}
			for _, s := range []string{at.Detail, at.Response} {
				if strings.Contains(s, root) || strings.Contains(strings.ToLower(s), "permission denied") || strings.Contains(s, "stage-") || strings.Contains(s, "network error") {
					t.Fatalf("job status leaks the host or misreports the fault: %q", s)
				}
			}
		})
	}
}

func TestStoreFaultCodesAreFixedAndTextFree(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{&os.PathError{Op: "open", Path: "/srv/archive/objects/tmp/x", Err: syscall.ENOSPC}, "no_space"},
		{&os.PathError{Op: "open", Path: "/srv/archive/objects/tmp/x", Err: syscall.EACCES}, "permission_denied"},
		{&os.PathError{Op: "open", Path: "/srv/archive/objects/tmp/x", Err: syscall.EROFS}, "read_only"},
		{errors.New("INSERT INTO captures: database is locked (5)"), "registry_busy"},
		{errors.New("INSERT INTO captures: constraint failed"), "storage_error"},
	} {
		if got := storeFaultCode(tc.err); got != tc.want {
			t.Errorf("storeFaultCode(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

// A held job that is retried and stores other bytes keeps each capture's own
// provenance: the first capture stays seen_during_research (its bytes match the
// digest the research read), and the later one is recovered_after_research. A
// later completion must not rewrite the first capture's provenance.
func TestRetriedHeldJobKeepsEachCapturesProvenance(t *testing.T) {
	a, _ := openTest(t)
	bodies := []string{"%PDF-1.4 first copy\n", "%PDF-1.4 a different second copy\n"}
	var served atomic.Int32
	_, q := newFakeNet(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := int(served.Add(1)) - 1
		w.Header().Set("Content-Type", "application/pdf")
		w.Write([]byte(bodies[i]))
	}))
	sum := sha256.Sum256([]byte(bodies[0]))
	j := submit(t, a, JobRequest{URL: "http://public.test/a.pdf", SeenSHA256: "sha256:" + hex.EncodeToString(sum[:])})
	first, err := a.RunNextJob(ctx, q, "w", DefaultJobLease)
	if err != nil || first == nil || first.State != JobHeld || first.Provenance != ProvSeen {
		t.Fatalf("first run = %+v, %v", first, err)
	}
	if _, err := a.RetryJob(ctx, j.JobID); err != nil {
		t.Fatal(err)
	}
	second, err := a.RunNextJob(ctx, q, "w", DefaultJobLease)
	if err != nil || second == nil || second.State != JobHeld || second.Provenance != ProvRecovered || second.ResultCaptureID == first.ResultCaptureID {
		t.Fatalf("second run = %+v, %v", second, err)
	}
	for _, tc := range []struct{ capture, want string }{{first.ResultCaptureID, ProvSeen}, {second.ResultCaptureID, ProvRecovered}} {
		c := mustStatus(t, a, tc.capture)
		if len(c.Collections) != 1 || c.Collections[0].JobID != j.JobID || c.Collections[0].Provenance != tc.want {
			t.Fatalf("collections of %s = %+v, want one with %s", tc.capture, c.Collections, tc.want)
		}
	}
}

// The object file is published before the registry transaction (the order the
// archive has always had), so a failure after publish and before commit can
// leave a file no capture refers to. It must not leave a capture or change the
// job, and the same bytes must store normally afterwards: the file is content
// addressed, and a later store verifies and adopts it.
func TestFailureAfterPublishLeavesNoCaptureAndTheSameBytesStoreLater(t *testing.T) {
	a, root := openTest(t)
	j := submit(t, a, JobRequest{URL: "http://public.test/a.pdf"})
	claimed, err := a.ClaimJob(ctx, ExecutorHTTP, "w1", time.Minute)
	if err != nil || claimed == nil {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	b := &jobBinding{id: j.JobID, owner: "w1", attempt: claimed.AttemptsMade,
		// An outcome the job cannot take: finishing fails inside the store
		// transaction, after the object file was published.
		outcome: func(Result, AcquisitionRecord) JobOutcome { return JobOutcome{State: "bogus"} }}
	body := "%PDF-1.4 adopted later\n"
	req := SourceRequest{Content: strings.NewReader(body), OriginalName: "a.pdf", URL: "http://public.test/a.pdf", bind: b}
	if _, err := a.captureSource(ctx, req, "", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("store with a failing finish: %v, want ErrInvalid", err)
	}
	if n := countCaptures(t, a); n != 0 {
		t.Fatalf("a failed finish left %d capture(s)", n)
	}
	if b.done != nil {
		t.Fatal("a failed finish published a job")
	}
	if got := getJob(t, a, j.JobID); got.State != "running" || got.ResultCaptureID != "" || len(got.Attempts) != 0 {
		t.Fatalf("a failed finish changed the job: %+v", got)
	}
	res, err := a.CaptureSource(ctx, SourceRequest{Content: strings.NewReader(body), OriginalName: "a.pdf", URL: "http://public.test/a.pdf"})
	if err != nil {
		t.Fatalf("the same bytes after the failure: %v", err)
	}
	if files := objectFiles(t, root); len(files) != 1 || "sha256:"+files[0] != res.ObjectID {
		t.Fatalf("object files = %v, object = %s", files, res.ObjectID)
	}
	if c := mustStatus(t, a, res.CaptureID); c.Completeness != "unknown" {
		t.Fatalf("capture = %+v", c)
	}
}
