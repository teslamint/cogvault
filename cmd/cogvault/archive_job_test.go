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
	"github.com/teslamint/cogvault/internal/archivegate"
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
	err := archivegate.RunLoop(context.Background(), func() (*archive.Archive, error) { return archive.Open(root) },
		func() error { return nil },
		func(ctx context.Context, a *archive.Archive) (*archive.Job, error) {
			return a.RunNextJob(ctx, archive.Acquirer{}, "w-test", archive.DefaultJobLease)
		},
		archivegate.Options{Poll: time.Millisecond, Once: true}, &wout, &werr)
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
		done <- archivegate.RunLoop(ctx, func() (*archive.Archive, error) { opened.Add(1); return archive.Open(root) },
			func() error {
				if open.Load() {
					return nil
				}
				return &archivegate.Error{Code: "capture_disabled", Keys: []string{"archive.capture_enabled"}}
			},
			func(ctx context.Context, a *archive.Archive) (*archive.Job, error) {
				return a.RunNextJob(ctx, archive.Acquirer{}, "w", archive.DefaultJobLease)
			},
			archivegate.Options{Poll: 5 * time.Millisecond}, &out, &errOut)
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
	if err := archivegate.WorkerAllowed(cfg, root, allow); err == nil || !strings.Contains(err.Error(), "capture_disabled") {
		t.Fatalf("capture off: %v", err)
	}
	cfg = writeWorkerConfig(t, base, root, true)
	if err := archivegate.WorkerAllowed(cfg, root, allow); err != nil {
		t.Fatalf("capture on with an allowing check: %v", err)
	}
	if err := archivegate.WorkerAllowed(cfg, filepath.Join(base, "SECRET-other"), allow); err == nil || !strings.Contains(err.Error(), "archive.root") || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("another root: %v", err)
	}
	// The real production check: no deployment settings, so it stays blocked.
	if err := archivegate.WorkerAllowed(cfg, root, archivegate.ProductionActivation); err == nil || !strings.Contains(err.Error(), "deployment_settings_missing") {
		t.Fatalf("unproven deployment: %v", err)
	}
	// Turning the switch off again blocks the worker again.
	cfg = writeWorkerConfig(t, base, root, false)
	if err := archivegate.WorkerAllowed(cfg, root, allow); err == nil {
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

func writeDeploymentConfig(t *testing.T, base, root, backupTarget, report, replica string) string {
	t.Helper()
	wiki := filepath.Join(base, "wiki")
	if err := os.MkdirAll(wiki, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(base, "config.yaml")
	body := "wiki_dir: " + wiki + "\ndb_path: " + filepath.Join(base, "index.db") + "\nadapter: obsidian\narchive:\n  root: " + root +
		"\n  capture_enabled: true\n  deployment:\n    backup_target: " + backupTarget + "\n    backup_schedule: daily\n    restore_test_report: " + report +
		"\n    permissions_owner: operator\n    handoff_mount: hm\n    deletion_log_replica: " + replica + "\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// The gate reads the restore-test report and the deletion-log replica from
// the paths in archive.deployment, and compares the report's recorded backup
// and replica paths to the same config. So a container that mounts the
// evidence at different paths than the restore test saw, or does not mount a
// file at all, stays blocked. The layout mirrors the rollout: backups/ is the
// backup target (never mounted into the long-running containers; only
// compared as a path), reports/ and replica/ are the read-only mounts.
func TestWorkerGateReadsEvidenceFromConfiguredMountPaths(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "archive")
	ev := filepath.Join(base, "evidence")
	for _, d := range []string{"backups", "reports", "replica"} {
		if err := os.MkdirAll(filepath.Join(ev, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a, err := archive.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	res, err := a.CaptureSource(ctx, archive.SourceRequest{Content: strings.NewReader("evidence fixture"), OriginalName: "e.md"})
	if err != nil {
		t.Fatal(err)
	}
	im, _ := a.ImpactOf(ctx, res.ObjectID)
	if err := a.Quarantine(ctx, res.ObjectID, im.Digest); err != nil { // a non-empty deletion log
		t.Fatal(err)
	}
	a.Close()

	backup := filepath.Join(ev, "backups", "b1")
	b, err := archive.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Backup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	b.Close()
	live, err := os.ReadFile(filepath.Join(root, "deletions", "log.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	replica := filepath.Join(ev, "replica", "deletions-log.jsonl")
	if err := os.WriteFile(replica, live, 0o600); err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(ev, "reports", "restore-test.json")
	if _, _, err := executeCommand("archive", "--root", root, "restore-test", "--backup", backup, "--report", report, "--deletion-log", replica, "--scratch", t.TempDir()); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(ev, "backups")
	allowed := func(cfg string) error { return archivegate.WorkerAllowed(cfg, root, archivegate.ProductionActivation) }
	if err := allowed(writeDeploymentConfig(t, base, root, target, report, replica)); err != nil {
		t.Fatalf("evidence at the configured paths was refused: %v", err)
	}

	cases := []struct {
		name, target, report, replica, want string
	}{
		{"report not mounted", target, filepath.Join(base, "unmounted", "restore-test.json"), replica, "restore_test_not_passed"},
		{"replica not mounted", target, report, filepath.Join(base, "unmounted", "log.jsonl"), "deletion_log_replica_not_current"},
		// The restore test saw the backup under evidence/backups; a config whose
		// backup_target is another path (host path versus container path) is refused.
		{"backup_target at another path", filepath.Join(base, "elsewhere"), report, replica, "restore_report_not_for_backup_target"},
		// The report names the replica it used by path; a config that points at a
		// copy mounted at another path is refused even though that copy is current.
		{"replica at another path", target, report, copyTo(t, replica, filepath.Join(base, "other-mount", "log.jsonl")), "restore_report_without_replica"},
	}
	for _, tc := range cases {
		err := allowed(writeDeploymentConfig(t, base, root, tc.target, tc.report, tc.replica))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %s", tc.name, err, tc.want)
		} else if strings.Contains(err.Error(), base) {
			t.Errorf("%s: the refusal exposes a path: %v", tc.name, err)
		}
	}
}

func copyTo(t *testing.T, from, to string) string {
	t.Helper()
	data, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return to
}
