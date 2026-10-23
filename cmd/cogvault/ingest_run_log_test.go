package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/teslamint/cogvault/internal/config"
	"github.com/teslamint/cogvault/internal/ingest"
	"golang.org/x/sys/unix"
)

const runLogTestName = "ingest.20260924T010000+0900.4242.err.log"

func fixedIngestPID(t *testing.T) {
	t.Helper()
	original := ingestPID
	ingestPID = func() int { return 4242 }
	t.Cleanup(func() { ingestPID = original })
}

func readRunLogLines(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read run log dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != runLogTestName {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("run log dir entries = %v, want [%s]", names, runLogTestName)
	}
	data, err := os.ReadFile(filepath.Join(dir, runLogTestName))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

func TestIngestRunLogRecordsStateChangesWithTimestamps(t *testing.T) {
	fakeClaudeOnPath(t)
	t.Setenv("CLAUDE_FAKE_MODE", "ok")
	t.Setenv("CLAUDE_FAKE_MODE_MATCH", "bad.pdf=garbage")
	fixedIngestPID(t)
	fixedIngestClock(t, ingestTimestamp(t, 0), ingestTimestamp(t, 1), ingestTimestamp(t, 2), ingestTimestamp(t, 5))
	configPath, srcDir, _, _ := setupIngestVault(t)
	bad := writeAgedSource(t, srcDir, "bad.pdf", "bad fixture")
	good := writeAgedSource(t, srcDir, "good.pdf", "good fixture")
	if err := os.WriteFile(filepath.Join(srcDir, "skip.txt"), []byte("type not allowed"), 0o644); err != nil {
		t.Fatal(err)
	}
	logDir := filepath.Join(t.TempDir(), "runs")

	stdout, stderr, err := executeCommand("ingest", "--config", configPath, "--run-log-dir", logDir)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	summary := strings.SplitN(stdout, "\n", 2)[0]
	if !strings.Contains(summary, "digested=1 failed=1") {
		t.Fatalf("fixture did not produce one digest and one failure: %q", summary)
	}
	wantShared := "2026-09-24T01:00:00+09:00 ingest start origin=interactive\n" +
		"2026-09-24T01:00:05+09:00 ingest end origin=interactive result=ok " + summary + "\n"
	if stderr != wantShared {
		t.Errorf("shared stderr changed\n got: %q\nwant: %q", stderr, wantShared)
	}

	lines := readRunLogLines(t, logDir)
	if len(lines) != 4 {
		t.Fatalf("run log has %d lines, want 4: %q", len(lines), lines)
	}
	if lines[0] != "2026-09-24T01:00:00+09:00 ingest start origin=interactive" {
		t.Errorf("start line = %q", lines[0])
	}
	if want := "2026-09-24T01:00:01+09:00 failed " + bad + "  digest: "; !strings.HasPrefix(lines[1], want) {
		t.Errorf("failure line = %q, want prefix %q", lines[1], want)
	}
	if want := "2026-09-24T01:00:02+09:00 digested " + good; lines[2] != want {
		t.Errorf("digest line = %q, want %q", lines[2], want)
	}
	if want := "2026-09-24T01:00:05+09:00 ingest end origin=interactive result=ok " + summary; lines[3] != want {
		t.Errorf("end line = %q, want %q", lines[3], want)
	}
}

func TestIngestRunLogSkipsQuietRun(t *testing.T) {
	fakeClaudeOnPath(t)
	fixedIngestPID(t)
	fixedIngestClock(t, ingestTimestamp(t, 0), ingestTimestamp(t, 5))
	configPath, _, _, _ := setupIngestVault(t)
	logDir := filepath.Join(t.TempDir(), "runs")

	if _, _, err := executeCommand("ingest", "--config", configPath, "--run-log-dir", logDir); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	entries, err := os.ReadDir(logDir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("quiet run created run log files: %v", entries)
	}
}

func TestIngestRunLogRecordsRunError(t *testing.T) {
	fakeClaudeOnPath(t)
	fixedIngestPID(t)
	fixedIngestClock(t, ingestTimestamp(t, 0), ingestTimestamp(t, 5))
	configPath, _, _, _ := setupIngestVault(t)
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	f, err := os.OpenFile(filepath.Join(filepath.Dir(cfg.DBPath), "ingest.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatalf("acquire test lock: %v", err)
	}
	defer unix.Flock(int(f.Fd()), unix.LOCK_UN)
	logDir := filepath.Join(t.TempDir(), "runs")

	if _, _, err := executeCommand("ingest", "--config", configPath, "--run-log-dir", logDir); err == nil {
		t.Fatal("expected error when lock is held")
	}
	want := []string{
		"2026-09-24T01:00:00+09:00 ingest start origin=interactive",
		"2026-09-24T01:00:05+09:00 ingest end origin=interactive result=error",
		"2026-09-24T01:00:05+09:00 ingest error: ingest already running (lock held)",
	}
	if got := readRunLogLines(t, logDir); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("run log\n got: %q\nwant: %q", got, want)
	}
}

func TestBracketIngestRunLogRecordsPanic(t *testing.T) {
	fixedIngestPID(t)
	fixedIngestClock(t, ingestTimestamp(t, 0), ingestTimestamp(t, 5))
	logDir := filepath.Join(t.TempDir(), "runs")
	var buf bytes.Buffer

	func() {
		defer func() {
			if rec := recover(); rec != "boom" {
				t.Errorf("expected panic value %q to propagate, got: %v", "boom", rec)
			}
		}()
		_ = bracketIngestRun(&buf, "scheduled", logDir, func(func(ingest.FileResult)) (*ingest.Report, error) {
			panic("boom")
		})
	}()

	want := []string{
		"2026-09-24T01:00:00+09:00 ingest start origin=scheduled",
		"2026-09-24T01:00:05+09:00 ingest end origin=scheduled result=panic",
		"2026-09-24T01:00:05+09:00 ingest panic: boom",
	}
	if got := readRunLogLines(t, logDir); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("run log\n got: %q\nwant: %q", got, want)
	}
}

func TestIngestRunLogUnwritableDirDoesNotFailRun(t *testing.T) {
	fakeClaudeOnPath(t)
	t.Setenv("CLAUDE_FAKE_MODE", "ok")
	fixedIngestPID(t)
	fixedIngestClock(t, ingestTimestamp(t, 0), ingestTimestamp(t, 1), ingestTimestamp(t, 5))
	configPath, srcDir, _, _ := setupIngestVault(t)
	writeAgedSource(t, srcDir, "good.pdf", "good fixture")
	notDir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notDir, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := executeCommand("ingest", "--config", configPath, "--run-log-dir", notDir)
	if err != nil {
		t.Fatalf("ingest failed because of the run log: %v", err)
	}
	if !strings.Contains(stdout, "digested=1") {
		t.Fatalf("expected digested=1, got: %q", stdout)
	}
}

func TestIngestRunLogDirMustBeAbsolute(t *testing.T) {
	configPath, _, _, _ := setupIngestVault(t)

	_, stderr, err := executeCommand("ingest", "--config", configPath, "--run-log-dir", "runs")
	if err == nil || !strings.Contains(err.Error(), "--run-log-dir must be an absolute path") {
		t.Fatalf("err = %v, want absolute-path rejection", err)
	}
	if strings.Contains(stderr, " ingest start ") {
		t.Errorf("rejected flag must not start a run, stderr: %q", stderr)
	}
}
