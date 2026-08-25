package main

import (
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// initTestGitRepo creates a git repository at dir with a local (not global)
// identity, so the test never depends on the host's ~/.gitconfig having
// user.name/user.email set.
func initTestGitRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	run("init", "-q")
	run("config", "user.name", "cogvault-test")
	run("config", "user.email", "cogvault-test@example.com")
}

// gitLogSubjects returns the commit subject lines for dir, oldest first.
func gitLogSubjects(t *testing.T, dir string) []string {
	t.Helper()
	cmd := exec.Command("git", "log", "--reverse", "--format=%s")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

func TestIngestGitCommit_WriteIngestModeCommitsWholeTree(t *testing.T) {
	fakeClaudeOnPath(t)
	t.Setenv("CLAUDE_FAKE_MODE", "ok")
	configPath, srcDir, wikiDir, _ := setupIngestVault(t)
	initTestGitRepo(t, wikiDir)
	appendConfig(t, configPath, "git:\n  auto_commit: write+ingest\n")

	writeAgedSource(t, srcDir, "one.pdf", "git commit fixture")

	stdout, _, err := executeCommand("ingest", "--config", configPath)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if !strings.Contains(stdout, "digested=1") {
		t.Fatalf("expected digested=1, got: %q", stdout)
	}

	subjects := gitLogSubjects(t, wikiDir)
	want := "wiki: ingest snapshot"
	if len(subjects) != 1 || subjects[0] != want {
		t.Fatalf("git log subjects = %v, want [%q]", subjects, want)
	}
}

// TestIngestGitCommit_NestedWikiDirDoesNotStageOutsideFiles is the
// regression test for the git.auto_commit: write+ingest bug where `git -C
// wikiDir add -A` (without a pathspec) resolves against the enclosing git
// repo's root, not wikiDir, when wikiDir is a plain subdirectory of a
// larger repository rather than its own git root. That staged and
// committed every dirty file anywhere in the outer repo, not just wikiDir.
func TestIngestGitCommit_NestedWikiDirDoesNotStageOutsideFiles(t *testing.T) {
	fakeClaudeOnPath(t)
	t.Setenv("CLAUDE_FAKE_MODE", "ok")
	configPath, srcDir, wikiDir, _ := setupIngestVault(t)
	outerRepo := filepath.Dir(wikiDir) // wikiDir's parent, the outer repo root
	initTestGitRepo(t, outerRepo)
	appendConfig(t, configPath, "git:\n  auto_commit: write+ingest\n")

	// A dirty, untracked file that lives in the outer repo but outside
	// wikiDir. It must never be staged or committed by the ingest command.
	outsidePath := filepath.Join(outerRepo, "unrelated-secret.txt")
	if err := os.WriteFile(outsidePath, []byte("not part of the wiki"), 0o644); err != nil {
		t.Fatal(err)
	}

	writeAgedSource(t, srcDir, "one.pdf", "nested repo fixture")

	stdout, _, err := executeCommand("ingest", "--config", configPath)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if !strings.Contains(stdout, "digested=1") {
		t.Fatalf("expected digested=1, got: %q", stdout)
	}

	subjects := gitLogSubjects(t, outerRepo)
	want := "wiki: ingest snapshot"
	if len(subjects) != 1 || subjects[0] != want {
		t.Fatalf("git log subjects = %v, want [%q]", subjects, want)
	}

	statusCmd := exec.Command("git", "status", "--short")
	statusCmd.Dir = outerRepo
	out, err := statusCmd.Output()
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	status := strings.TrimSpace(string(out))
	if !strings.Contains(status, "unrelated-secret.txt") {
		t.Fatalf("git status = %q, want unrelated-secret.txt still untracked (it must not have been staged/committed)", status)
	}

	logCmd := exec.Command("git", "show", "--stat", "--format=", "HEAD")
	logCmd.Dir = outerRepo
	logOut, err := logCmd.Output()
	if err != nil {
		t.Fatalf("git show: %v", err)
	}
	if strings.Contains(string(logOut), "unrelated-secret.txt") {
		t.Fatalf("commit accidentally includes unrelated-secret.txt: %s", logOut)
	}
}

func TestIngestGitCommit_DefaultOffDoesNotCommit(t *testing.T) {
	fakeClaudeOnPath(t)
	t.Setenv("CLAUDE_FAKE_MODE", "ok")
	configPath, srcDir, wikiDir, _ := setupIngestVault(t)
	initTestGitRepo(t, wikiDir)
	// No git: block appended — git.auto_commit defaults to "off".

	writeAgedSource(t, srcDir, "one.pdf", "no commit fixture")

	stdout, _, err := executeCommand("ingest", "--config", configPath)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if !strings.Contains(stdout, "digested=1") {
		t.Fatalf("expected digested=1, got: %q", stdout)
	}

	if subjects := gitLogSubjects(t, wikiDir); len(subjects) != 0 {
		t.Fatalf("git log subjects = %v, want none (git.auto_commit defaults to off)", subjects)
	}
}

func TestIngestGitCommit_WriteModeAloneDoesNotTriggerIngestCommit(t *testing.T) {
	fakeClaudeOnPath(t)
	t.Setenv("CLAUDE_FAKE_MODE", "ok")
	configPath, srcDir, wikiDir, _ := setupIngestVault(t)
	initTestGitRepo(t, wikiDir)
	// "write" alone covers only wiki_write (the MCP path); cogvault ingest
	// writes through internal/storage directly, not through the MCP
	// handlers, so this mode must not commit an ingest run either.
	appendConfig(t, configPath, "git:\n  auto_commit: write\n")

	writeAgedSource(t, srcDir, "one.pdf", "write-mode-only fixture")

	stdout, _, err := executeCommand("ingest", "--config", configPath)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if !strings.Contains(stdout, "digested=1") {
		t.Fatalf("expected digested=1, got: %q", stdout)
	}

	if subjects := gitLogSubjects(t, wikiDir); len(subjects) != 0 {
		t.Fatalf("git log subjects = %v, want none (git.auto_commit: write must not commit ingest runs)", subjects)
	}
}

func TestIngestGitCommit_DryRunDoesNotCommit(t *testing.T) {
	fakeClaudeOnPath(t)
	t.Setenv("CLAUDE_FAKE_MODE", "ok")
	configPath, srcDir, wikiDir, _ := setupIngestVault(t)
	initTestGitRepo(t, wikiDir)
	appendConfig(t, configPath, "git:\n  auto_commit: write+ingest\n")

	writeAgedSource(t, srcDir, "one.pdf", "dry run fixture")

	stdout, _, err := executeCommand("ingest", "--config", configPath, "--dry-run")
	if err != nil {
		t.Fatalf("ingest --dry-run: %v", err)
	}
	if !strings.Contains(stdout, "would-digest") {
		t.Fatalf("expected would-digest in dry-run report, got: %q", stdout)
	}

	if subjects := gitLogSubjects(t, wikiDir); len(subjects) != 0 {
		t.Fatalf("git log subjects = %v, want none (--dry-run must not commit)", subjects)
	}
}

func TestIngestGitCommit_NoDigestedFilesDoesNotCommit(t *testing.T) {
	fakeClaudeOnPath(t)
	configPath, _, wikiDir, _ := setupIngestVault(t)
	initTestGitRepo(t, wikiDir)
	appendConfig(t, configPath, "git:\n  auto_commit: write+ingest\n")

	// No source files at all: report.Digested stays 0.
	stdout, _, err := executeCommand("ingest", "--config", configPath)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if !strings.Contains(stdout, "digested=0") {
		t.Fatalf("expected digested=0, got: %q", stdout)
	}

	if subjects := gitLogSubjects(t, wikiDir); len(subjects) != 0 {
		t.Fatalf("git log subjects = %v, want none (a run that digests nothing must not commit)", subjects)
	}
}

func TestIngestGitCommit_TimeoutBoundsWedgedCommit(t *testing.T) {
	fakeClaudeOnPath(t)
	t.Setenv("CLAUDE_FAKE_MODE", "ok")
	configPath, srcDir, wikiDir, _ := setupIngestVault(t)
	initTestGitRepo(t, wikiDir)
	appendConfig(t, configPath, "git:\n  auto_commit: write+ingest\n")
	writeAgedSource(t, srcDir, "one.pdf", "timeout fixture")

	binDir, err := filepath.Abs("../../internal/mcp/testdata/bin")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GIT_FAKE_COMMIT_SLEEP", "10")

	original := ingestGitCommitTimeout
	ingestGitCommitTimeout = 50 * time.Millisecond
	t.Cleanup(func() { ingestGitCommitTimeout = original })

	started := time.Now()
	stdout, _, err := executeCommand("ingest", "--config", configPath)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if !strings.Contains(stdout, "digested=1") {
		t.Fatalf("expected digested=1, got: %q", stdout)
	}
	if elapsed := time.Since(started); elapsed >= 5*time.Second {
		t.Fatalf("ingest took %s, want bounded by the shrunk ingestGitCommitTimeout (fake git sleeps 10s unbounded)", elapsed)
	}
}

// TestIngestGitCommit_SlowAddDoesNotStarveCommitTimeout is the regression
// test for the initial implementation sharing one context.WithTimeout
// across both the add and commit subprocesses: a slow-but-not-wedged
// `git add -A -- .` (e.g. a large working tree scan) would consume most of
// the shared budget, leaving `git commit` too little time and turning a
// merely slow add into a spurious commit failure — silently dropping the
// ingest snapshot commit. With independent per-command timeouts, an add
// that takes most of ingestGitCommitTimeout must not prevent the commit
// from landing.
func TestIngestGitCommit_SlowAddDoesNotStarveCommitTimeout(t *testing.T) {
	fakeClaudeOnPath(t)
	t.Setenv("CLAUDE_FAKE_MODE", "ok")
	configPath, srcDir, wikiDir, _ := setupIngestVault(t)
	initTestGitRepo(t, wikiDir)
	appendConfig(t, configPath, "git:\n  auto_commit: write+ingest\n")
	writeAgedSource(t, srcDir, "one.pdf", "slow add fixture")

	binDir, err := filepath.Abs("../../internal/mcp/testdata/bin")
	if err != nil {
		t.Fatal(err)
	}
	// The fake git binary on PATH shadows real git for every subprocess
	// call this test's own process makes too (e.g. a gitLogSubjects
	// helper), not only postIngestGitCommit's — so completion is asserted
	// via captured log output plus elapsed time, not by re-invoking `git
	// log`.
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// add (200ms) + commit (150ms) = 350ms, which exceeds a 250ms budget: a
	// shared context would let add consume most of the budget and kill
	// commit partway through. Each individually fits under 250ms, so
	// independent per-command timeouts must let both succeed.
	t.Setenv("GIT_FAKE_ADD_SLEEP", "0.2")
	t.Setenv("GIT_FAKE_COMMIT_SLEEP", "0.15")

	original := ingestGitCommitTimeout
	ingestGitCommitTimeout = 250 * time.Millisecond
	t.Cleanup(func() { ingestGitCommitTimeout = original })

	var buf strings.Builder
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	started := time.Now()
	stdout, _, err := executeCommand("ingest", "--config", configPath)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if !strings.Contains(stdout, "digested=1") {
		t.Fatalf("expected digested=1, got: %q", stdout)
	}

	// Positive proof postIngestGitCommit actually ran both subprocesses
	// sequentially (not skipped by a CommitsOnIngest() wiring bug): if both
	// the 200ms fake add and the 150ms fake commit executed, elapsed must
	// be at least their sum. A near-zero elapsed here would mean the
	// negative log assertions below are vacuously passing.
	if elapsed < 350*time.Millisecond {
		t.Fatalf("elapsed = %s, want >= 350ms; postIngestGitCommit's add+commit may not have run at all", elapsed)
	}

	logs := buf.String()
	if strings.Contains(logs, "post-ingest git commit failed") {
		t.Fatalf("commit must get its own full timeout budget, not the remainder after a slow add; logs: %s", logs)
	}
	if strings.Contains(logs, "post-ingest git add failed") {
		t.Fatalf("add must succeed within its own budget; logs: %s", logs)
	}
}
