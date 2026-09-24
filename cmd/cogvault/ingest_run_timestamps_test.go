package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/teslamint/cogvault/internal/config"
	"github.com/teslamint/cogvault/internal/ingest"
	"golang.org/x/sys/unix"
)

var kstZone = time.FixedZone("KST", 9*60*60)

// fixedIngestClock replaces the ingestNow seam with a clock that returns the
// given times in order, and restores the original with t.Cleanup. It fails the
// test if the code under test asks for more times than were supplied, so an
// extra (or missing) clock call is caught rather than silently reusing a value.
func fixedIngestClock(t *testing.T, times ...time.Time) {
	t.Helper()
	original := ingestNow
	i := 0
	ingestNow = func() time.Time {
		if i >= len(times) {
			t.Fatalf("ingestNow called %d times, but only %d value(s) supplied", i+1, len(times))
		}
		v := times[i]
		i++
		return v
	}
	t.Cleanup(func() { ingestNow = original })
}

func ingestTimestamp(t *testing.T, sec int) time.Time {
	t.Helper()
	return time.Date(2026, 9, 24, 1, 0, sec, 0, kstZone)
}

// agedNoteSource writes a source file older than the ingest settle window so a
// dry run counts it as pending (mirrors TestIngestDryRunListsPending).
func agedNoteSource(t *testing.T, srcDir string) {
	t.Helper()
	srcFile := filepath.Join(srcDir, "note.md")
	if err := os.WriteFile(srcFile, []byte("# Note\n\nSome ingestible content."), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-5 * time.Minute)
	if err := os.Chtimes(srcFile, old, old); err != nil {
		t.Fatal(err)
	}
}

func TestIngestRunTimestampsDryRunSuccess(t *testing.T) {
	fakeClaudeOnPath(t)
	fixedIngestClock(t, ingestTimestamp(t, 0), ingestTimestamp(t, 5))
	configPath, srcDir := newIngestVault(t)
	agedNoteSource(t, srcDir)

	stdout, stderr, err := executeCommand("ingest", "--config", configPath, "--dry-run")
	if err != nil {
		t.Fatalf("ingest --dry-run failed: %v", err)
	}

	summary := strings.SplitN(stdout, "\n", 2)[0]
	if !strings.Contains(summary, "digested=1") {
		t.Fatalf("fixture did not produce digested=1; stdout summary: %q", summary)
	}
	want := "2026-09-24T01:00:00+09:00 ingest start origin=interactive\n" +
		"2026-09-24T01:00:05+09:00 ingest end origin=interactive result=ok " + summary + "\n"
	if stderr != want {
		t.Errorf("stderr mismatch\n got: %q\nwant: %q", stderr, want)
	}
	if strings.Contains(stdout, " ingest start ") || strings.Contains(stdout, " ingest end ") {
		t.Errorf("stdout must not carry bracket lines, got: %q", stdout)
	}
}

func TestIngestRunTimestampsScheduledOrigin(t *testing.T) {
	fakeClaudeOnPath(t)
	fixedIngestClock(t, ingestTimestamp(t, 0), ingestTimestamp(t, 5))

	originalNotify := runIngestNotify
	t.Cleanup(func() { runIngestNotify = originalNotify })
	runIngestNotify = func(reportNotifier, *ingest.Report, bool, error) {}

	configPath, srcDir := newIngestVault(t)
	agedNoteSource(t, srcDir)

	_, stderr, err := executeCommand("ingest", "--config", configPath, "--dry-run", "--scheduled")
	if err != nil {
		t.Fatalf("ingest --dry-run --scheduled failed: %v", err)
	}
	if !strings.HasPrefix(stderr, "2026-09-24T01:00:00+09:00 ingest start origin=scheduled\n") {
		t.Errorf("expected scheduled start line, got: %q", stderr)
	}
	if !strings.Contains(stderr, " ingest end origin=scheduled result=ok ") {
		t.Errorf("expected scheduled end line, got: %q", stderr)
	}
}

func TestIngestRunTimestampsMissingConfig(t *testing.T) {
	fixedIngestClock(t, ingestTimestamp(t, 0), ingestTimestamp(t, 5))
	absent := filepath.Join(t.TempDir(), "absent.yaml")

	_, stderr, err := executeCommand("ingest", "--config", absent)
	if err == nil {
		t.Fatal("expected error for missing config")
	}
	want := "2026-09-24T01:00:00+09:00 ingest start origin=interactive\n" +
		"2026-09-24T01:00:05+09:00 ingest end origin=interactive result=error\n"
	if stderr != want {
		t.Errorf("stderr mismatch\n got: %q\nwant: %q", stderr, want)
	}
}

func TestIngestRunTimestampsMissingClaude(t *testing.T) {
	fixedIngestClock(t, ingestTimestamp(t, 0), ingestTimestamp(t, 5))
	configPath, _ := newIngestVault(t)
	t.Setenv("PATH", "")

	_, stderr, err := executeCommand("ingest", "--config", configPath)
	if err == nil {
		t.Fatal("expected error when claude binary is absent")
	}
	if !strings.Contains(stderr, " ingest end origin=interactive result=error\n") {
		t.Errorf("expected result=error end line, got: %q", stderr)
	}
}

func TestIngestRunTimestampsLockHeld(t *testing.T) {
	fixedIngestClock(t, ingestTimestamp(t, 0), ingestTimestamp(t, 5))
	configPath, _ := newIngestVault(t)

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	lockPath := filepath.Join(filepath.Dir(cfg.DBPath), "ingest.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatalf("acquire test lock: %v", err)
	}
	defer unix.Flock(int(f.Fd()), unix.LOCK_UN)

	fakeClaudeOnPath(t)
	_, stderr, err := executeCommand("ingest", "--config", configPath)
	if err == nil {
		t.Fatal("expected error when lock is held")
	}
	if !strings.Contains(stderr, " ingest end origin=interactive result=error\n") {
		t.Errorf("expected result=error end line, got: %q", stderr)
	}
}

// TestIngestRunTimestampsRunErrorWithReport pins the end line when runner.Run
// returns a non-nil report together with an error — the production shape of a
// `_schema.md` read failure. The counts must appear on the end line and the
// process must still fail.
func TestIngestRunTimestampsRunErrorWithReport(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod 0000 does not deny reads to root")
	}
	fakeClaudeOnPath(t)
	fixedIngestClock(t, ingestTimestamp(t, 0), ingestTimestamp(t, 5))
	configPath, _ := newIngestVault(t)

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	schemaPath := filepath.Join(cfg.WikiDir, cfg.SchemaPath())
	if err := os.WriteFile(schemaPath, []byte("# schema\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(schemaPath, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(schemaPath, 0o644) })

	stdout, stderr, err := executeCommand("ingest", "--config", configPath)
	if err == nil {
		t.Fatal("expected error when _schema.md is unreadable")
	}
	summary := strings.SplitN(stdout, "\n", 2)[0]
	if summary == "" {
		t.Fatal("expected the report summary on stdout")
	}
	want := "2026-09-24T01:00:00+09:00 ingest start origin=interactive\n" +
		"2026-09-24T01:00:05+09:00 ingest end origin=interactive result=error " + summary + "\n"
	if stderr != want {
		t.Errorf("stderr mismatch\n got: %q\nwant: %q", stderr, want)
	}
}

// TestBracketIngestRunUTCPrintsNumericOffset pins the required numeric offset
// format: on a UTC host the timestamp must end in +00:00, not `Z`.
func TestBracketIngestRunUTCPrintsNumericOffset(t *testing.T) {
	fixedIngestClock(t,
		time.Date(2026, 9, 24, 1, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 24, 1, 0, 5, 0, time.UTC),
	)
	var buf bytes.Buffer

	if err := bracketIngestRun(&buf, "interactive", func() (*ingest.Report, error) {
		return &ingest.Report{}, nil
	}); err != nil {
		t.Fatalf("bracketIngestRun returned %v", err)
	}

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 bracket lines, got %d: %q", len(lines), buf.String())
	}
	if lines[0] != "2026-09-24T01:00:00+00:00 ingest start origin=interactive" {
		t.Errorf("unexpected start line: %q", lines[0])
	}
	if want := "2026-09-24T01:00:05+00:00 ingest end origin=interactive result=ok "; !strings.HasPrefix(lines[1], want) {
		t.Errorf("end line %q does not start with %q", lines[1], want)
	}
}

func TestBracketIngestRunPanic(t *testing.T) {
	fixedIngestClock(t, ingestTimestamp(t, 0), ingestTimestamp(t, 5))
	var buf bytes.Buffer

	func() {
		defer func() {
			rec := recover()
			if rec != "boom" {
				t.Errorf("expected panic value %q to propagate, got: %v", "boom", rec)
			}
		}()
		_ = bracketIngestRun(&buf, "interactive", func() (*ingest.Report, error) {
			panic("boom")
		})
	}()

	out := strings.TrimRight(buf.String(), "\n")
	lines := strings.Split(out, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 bracket lines, got %d: %q", len(lines), out)
	}
	if lines[0] != "2026-09-24T01:00:00+09:00 ingest start origin=interactive" {
		t.Errorf("unexpected start line: %q", lines[0])
	}
	if lines[1] != "2026-09-24T01:00:05+09:00 ingest end origin=interactive result=panic" {
		t.Errorf("unexpected end line: %q", lines[1])
	}
}
