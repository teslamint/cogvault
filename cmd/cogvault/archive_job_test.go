package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/teslamint/cogvault/internal/archive"
	"github.com/teslamint/cogvault/internal/config"
)

// The queue is shared state, not process state: a job submitted by one
// command is read by another, the worker records why a loopback target was
// refused (no connection is made), and a retry puts the job back with its
// history intact.
func TestArchiveJobSubmitRunStatusRetry(t *testing.T) {
	root := filepath.Join(t.TempDir(), "archive")
	run := func(args ...string) (string, string) {
		t.Helper()
		out, errOut, err := executeCommand(append([]string{"archive", "--root", root}, args...)...)
		if err != nil {
			t.Fatalf("archive %s: %v\nstdout: %s\nstderr: %s", strings.Join(args, " "), err, out, errOut)
		}
		return out, errOut
	}

	out, _ := run("job", "submit", "http://127.0.0.1:9/secret", "--submitter", "chat-a", "--claim", "p. 3", "--json")
	var job archive.Job
	if err := json.Unmarshal([]byte(out), &job); err != nil {
		t.Fatalf("submit output is not a job: %v\n%s", err, out)
	}
	if job.State != "queued" || job.Submitter != "chat-a" || job.ClaimRef != "p. 3" {
		t.Fatalf("submitted job = %+v", job)
	}

	_, errOut := run("job", "submit", "http://127.0.0.1:9/secret", "--submitter", "chat-b", "--claim", "p. 3")
	if !strings.Contains(errOut, "existing job returned") {
		t.Fatalf("second client did not get the existing job: %q", errOut)
	}

	// The worker loop runs on this temporary root with an injected gate; the
	// command has no such option, so no CLI path reaches a root unchecked.
	var wout, werr bytes.Buffer
	err := runWorker(context.Background(), func() (*archive.Archive, error) { return archive.Open(root) },
		func() error { return nil },
		workerOptions{owner: "w-test", lease: archive.DefaultJobLease, poll: time.Millisecond, once: true}, &wout, &werr)
	if err != nil || !strings.Contains(wout.String(), job.JobID+": failed") {
		t.Fatalf("worker: %v\nstdout: %s\nstderr: %s", err, wout.String(), werr.String())
	}
	out, _ = run("job", "status", job.JobID)
	if !strings.Contains(out, "failed") || !strings.Contains(out, "not a public address") || !strings.Contains(out, "attempt 1 by w-test") {
		t.Fatalf("status = %q", out)
	}

	out, _ = run("job", "retry", job.JobID, "--json")
	var again archive.Job
	if err := json.Unmarshal([]byte(out), &again); err != nil {
		t.Fatal(err)
	}
	if again.State != "queued" || len(again.Attempts) != 1 || again.MaxAttempts != 6 {
		t.Fatalf("after retry = %+v", again)
	}

	if _, _, err := executeCommand("archive", "--root", root, "job", "submit", "http://example.org/a?token=1", "--submitter", "x"); err == nil {
		t.Fatal("a URL with a query was queued")
	}
	if _, _, err := executeCommand("archive", "--root", root, "job", "status", "job_missing"); err == nil {
		t.Fatal("status of a missing job succeeded")
	}
}

func writeWorkerConfig(t *testing.T, base, archiveRoot string, enabled bool) string {
	t.Helper()
	wiki := filepath.Join(base, "wiki")
	if err := os.MkdirAll(wiki, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(base, "config.yaml")
	body := "wiki_dir: " + wiki + "\ndb_path: " + filepath.Join(base, "index.db") + "\nadapter: obsidian\narchive:\n  root: " + archiveRoot +
		"\n  capture_enabled: " + map[bool]string{true: "true", false: "false"}[enabled] + "\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// The worker command has one start condition, the activation gate: it needs
// an explicit --config, and there is no flag that skips the gate.
func TestJobRunNeedsExplicitConfigAndHasNoBypass(t *testing.T) {
	root := filepath.Join(t.TempDir(), "archive")
	if _, _, err := executeCommand("archive", "--root", root, "job", "run", "--once"); err == nil || !strings.Contains(err.Error(), "--config") {
		t.Fatalf("job run without --config: %v", err)
	}
	for _, flag := range []string{"--isolated-trial", "--no-gate", "--skip-activation"} {
		if _, _, err := executeCommand("archive", "--root", root, "job", "run", "--once", flag); err == nil || !strings.Contains(err.Error(), "unknown flag") {
			t.Fatalf("job run %s: %v, want unknown flag", flag, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "registry.db")); !os.IsNotExist(err) {
		t.Fatal("a refused worker opened the archive")
	}
}

// A closed gate pauses the long-running loop: it does not open the archive,
// claims nothing, and logs the pause once. It resumes without a restart when
// the gate opens.
func TestWorkerLoopPausesWithoutOpeningArchiveThenResumes(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "archive")
	// A job is queued through a separate handle first, so there is something
	// the worker could claim; "not opened" is checked by counting calls to
	// the worker's open function.
	a, err := archive.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	j, _, err := a.SubmitJob(context.Background(), archive.JobRequest{URL: "http://127.0.0.1:9/x", Submitter: "t"})
	if err != nil {
		t.Fatal(err)
	}
	a.Close()

	var open atomic.Bool
	var opened atomic.Int32
	var out, errOut syncBuffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runWorker(ctx, func() (*archive.Archive, error) { opened.Add(1); return archive.Open(root) },
			func() error {
				if open.Load() {
					return nil
				}
				return &activationError{code: "capture_disabled", keys: []string{"archive.capture_enabled"}}
			},
			workerOptions{owner: "w", lease: archive.DefaultJobLease, poll: 5 * time.Millisecond}, &out, &errOut)
	}()
	waitFor := func(what string, cond func() bool) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); !cond(); time.Sleep(5 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s\nstdout: %s\nstderr: %s", what, out.String(), errOut.String())
			}
		}
	}
	waitFor("the pause message", func() bool {
		return strings.Contains(errOut.String(), "paused: ") && strings.Contains(errOut.String(), "capture_disabled")
	})
	time.Sleep(50 * time.Millisecond)
	if opened.Load() != 0 {
		t.Fatal("a gated worker opened the archive")
	}
	if got := strings.Count(errOut.String(), "paused: "); got != 1 {
		t.Fatalf("pause logged %d times, want once per change", got)
	}
	check, err := archive.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := check.GetJob(context.Background(), j.JobID); err != nil || got.State != "queued" || got.AttemptsMade != 0 {
		t.Fatalf("a gated worker touched the job: %+v, %v", got, err)
	}
	check.Close()

	open.Store(true)
	waitFor("the job to run", func() bool { return strings.Contains(out.String(), j.JobID+": failed") })
	if !strings.Contains(errOut.String(), "resumed") {
		t.Fatalf("no resume message:\n%s", errOut.String())
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("worker exit: %v", err)
	}
}

// workerAllowed follows the same capture switch and activation check as the
// MCP tools: a root other than archive.root, capture off, or an unproven
// deployment each block it, and the refusal names keys, never values.
func TestWorkerAllowedFollowsCaptureSwitchAndActivation(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "archive")
	allow := func(*config.Config) error { return nil }

	cfg := writeWorkerConfig(t, base, root, false)
	if err := workerAllowed(cfg, root, allow); err == nil || !strings.Contains(err.Error(), "capture_disabled") {
		t.Fatalf("capture off: %v", err)
	}
	cfg = writeWorkerConfig(t, base, root, true)
	if err := workerAllowed(cfg, root, allow); err != nil {
		t.Fatalf("capture on with an allowing check: %v", err)
	}
	if err := workerAllowed(cfg, filepath.Join(base, "SECRET-other"), allow); err == nil || !strings.Contains(err.Error(), "archive.root") || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("another root: %v", err)
	}
	// The real production check: no deployment settings, so it stays blocked.
	if err := workerAllowed(cfg, root, productionActivation); err == nil || !strings.Contains(err.Error(), "deployment_settings_missing") {
		t.Fatalf("unproven deployment: %v", err)
	}
	// Turning the switch off again blocks the worker again.
	cfg = writeWorkerConfig(t, base, root, false)
	if err := workerAllowed(cfg, root, allow); err == nil {
		t.Fatal("worker still allowed after capture_enabled was turned off")
	}
}

// With capture enabled but item 14 unproven, the worker refuses to start
// and leaves a queued job untouched.
func TestJobRunWithUnprovenDeploymentClaimsNothing(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "archive")
	cfg := writeWorkerConfig(t, base, root, true)
	out, _, err := executeCommand("archive", "--root", root, "job", "submit", "http://example.org/a.pdf", "--submitter", "t", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var job archive.Job
	if err := json.Unmarshal([]byte(out), &job); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCommand("archive", "--root", root, "--config", cfg, "job", "run", "--once"); err == nil || !strings.Contains(err.Error(), "deployment_settings_missing") {
		t.Fatalf("worker with an unproven deployment: %v", err)
	}
	out, _, err = executeCommand("archive", "--root", root, "job", "status", job.JobID, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var after archive.Job
	if err := json.Unmarshal([]byte(out), &after); err != nil {
		t.Fatal(err)
	}
	if after.State != "queued" || after.AttemptsMade != 0 {
		t.Fatalf("a blocked worker touched the job: %+v", after)
	}
}
