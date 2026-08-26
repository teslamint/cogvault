// Package gitutil owns every `git` subprocess cogvault runs on behalf of the
// wiki auto-commit safety net (docs/decisions/0024-wiki-git-safety-net.md).
//
// It is a leaf package: it imports nothing from cogvault, so both
// internal/mcp (per-file commits from wiki_write/wiki_delete) and
// cmd/cogvault (the whole-tree post-ingest snapshot) can depend on it
// without violating DESIGN.md's unidirectional dependency graph, the same
// way both already depend on internal/config and internal/errors.
//
// Centralizing the commit mechanism here is what makes the safety net
// actually safe: git refuses concurrent index operations on one repository
// (`.git/index.lock`), so two unsynchronized callers racing produce a
// best-effort "failure" that silently drops a commit the safety net exists
// to guarantee. Commit serializes callers both inside one process and
// across processes (a live `serve` versus a scheduled `ingest`), and
// terminates a timed-out subprocess in a way that lets git clean up its own
// lock file.
package gitutil

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// CommitTimeout bounds each individual git subprocess, and separately bounds
// how long a caller waits for the cross-process commit lock. `add` and
// `commit` each get their own full budget rather than sharing one: a slow
// (not wedged) `git add` scanning a large working tree would otherwise
// starve `git commit` of its budget, turning a merely slow add into a
// spurious commit failure.
//
// Worst-case wall time for one successful Commit is therefore 3 ×
// CommitTimeout (lock wait, then add, then commit), plus TerminateGrace per
// subprocess that has to be force-killed.
//
// A var, not a const, so tests can shrink it to exercise the timeout paths
// without multi-second sleeps.
var CommitTimeout = 10 * time.Second

// TerminateGrace is how long a timed-out git subprocess gets to exit after
// SIGTERM before Go force-kills it.
//
// This grace period is the difference between a recoverable timeout and a
// permanently broken repository. `git add` and `git commit` both hold
// `.git/index.lock` while running, and git removes that lock from its own
// SIGTERM handler. Go's exec default for a cancelled CommandContext is
// SIGKILL, which git cannot trap: the lock file survives on disk with no
// cleanup path anywhere in cogvault, so every subsequent auto-commit fails
// until an operator deletes it by hand. That is precisely the wedged
// index.lock that 0024's timeout exists to defend against.
var TerminateGrace = 2 * time.Second

// lockRetryInterval is how often a blocked caller re-attempts the
// cross-process lock. flock's blocking mode cannot be interrupted by a
// context, so the wait is a bounded non-blocking retry loop instead.
const lockRetryInterval = 20 * time.Millisecond

// Stage identifies which step of Commit failed, so callers can keep their
// own log wording and their existing operational contracts.
type Stage string

const (
	// StageNone is the zero value, returned when Commit succeeds.
	StageNone Stage = ""
	// StageLock means the repository lock could not be acquired in time;
	// no git subprocess ran and nothing was staged or committed.
	StageLock Stage = "lock"
	// StageAdd means `git add` failed; no commit was attempted.
	StageAdd Stage = "add"
	// StageCommit means staging succeeded but `git commit` failed. Note
	// that git exits nonzero for a genuinely empty commit ("nothing to
	// commit"), which callers treat as benign.
	StageCommit Stage = "commit"
)

// Commit stages pathspecs and commits them against repoDir with message.
//
// Callers are serialized per repository: first by a process-wide mutex
// (concurrent MCP tool handlers), then by an advisory file lock (a live
// `serve` racing a scheduled `ingest`). Both waits are bounded by
// CommitTimeout.
//
// It reports the failing Stage alongside the error so callers can log with
// their own wording; a nil error always comes with StageNone. Commit never
// panics and performs no logging of its own — the best-effort
// "failures log, never fail the caller" policy stays with the callers.
func Commit(ctx context.Context, repoDir string, pathspecs []string, message string) (Stage, error) {
	release, err := lockRepo(ctx, repoDir)
	if err != nil {
		return StageLock, err
	}
	defer release()

	addArgs := append([]string{"-C", repoDir, "add"}, pathspecs...)
	if err := runStage(ctx, addArgs); err != nil {
		return StageAdd, err
	}

	if err := runStage(ctx, []string{"-C", repoDir, "commit", "-m", message}); err != nil {
		return StageCommit, err
	}
	return StageNone, nil
}

// runStage derives the per-command timeout context and hands it to the
// command runner.
//
// The derivation lives here, not inside runGit, so a substituted runner
// receives exactly the bounded context the subprocess would have run under.
// That is the seam the independent-deadline test uses: add and commit must
// each get their own full CommitTimeout budget rather than sharing one, and
// that property is otherwise only observable as elapsed wall-clock time.
// Three successive revisions of a timing-based test for it flaked under
// load — such a test cannot be made safe against unbounded scheduler delay,
// while reading the two deadlines is exact.
func runStage(ctx context.Context, args []string) error {
	cmdCtx, cancel := withTimeout(ctx, CommitTimeout)
	defer cancel()
	return runGit(cmdCtx, args)
}

// withTimeout is context.WithTimeout behind a seam, so a test can advance a
// fake clock between stages and observe that the second stage's budget is
// measured from when that stage starts rather than inherited from the first.
// Without it, "independent deadlines" is only observable by making a real
// subprocess sleep, which is what made the three prior tests flaky.
var withTimeout = context.WithTimeout

// runGit executes one git subprocess under the already-bounded ctx,
// terminating it with SIGTERM (not SIGKILL) so git can remove
// .git/index.lock on its way out. A var only so tests can substitute it.
var runGit = func(ctx context.Context, args []string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Cancel = func() error { return cmd.Process.Signal(unix.SIGTERM) }
	cmd.WaitDelay = TerminateGrace
	return cmd.Run()
}

// lockRepo acquires the commit lock for repoDir, returning a release
// function. The wait is bounded by CommitTimeout.
//
// One flock covers both the intra-process race (two MCP tool handlers) and
// the cross-process race (a live `serve` versus a scheduled `ingest`):
// flock excludes per open file description, and each caller opens the lock
// file independently, so two goroutines in one process contend exactly as
// two processes do — verified directly rather than assumed, since flock's
// per-description semantics are easy to mistake for per-process ones.
//
// The lock file lives in the OS temp directory, keyed by a hash of the
// resolved repository path, rather than inside repoDir: a lock file in the
// working tree would be swept into the post-ingest `git add -A -- .`
// snapshot and committed as wiki content. Symlinks are resolved first so two
// callers reaching the same directory by different paths share one lock.
//
// The wait is a bounded non-blocking retry loop rather than flock's own
// blocking mode, because a blocking flock cannot be interrupted by a
// context: a caller queued behind a wedged commit would hang indefinitely,
// which is the exact failure the 0024 timeout exists to prevent.
func lockRepo(ctx context.Context, repoDir string) (func(), error) {
	path, err := lockPath(repoDir)
	if err != nil {
		return nil, err
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("gitutil: open commit lock %s: %w", path, err)
	}

	deadlineCtx, cancel := context.WithTimeout(ctx, CommitTimeout)
	defer cancel()

	for {
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return func() {
				_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
				_ = f.Close()
			}, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			f.Close()
			return nil, fmt.Errorf("gitutil: flock %s: %w", path, err)
		}

		select {
		case <-deadlineCtx.Done():
			f.Close()
			return nil, fmt.Errorf("gitutil: commit lock %s busy: %w", path, deadlineCtx.Err())
		case <-time.After(lockRetryInterval):
		}
	}
}

func lockPath(repoDir string) (string, error) {
	abs, err := filepath.Abs(repoDir)
	if err != nil {
		return "", fmt.Errorf("gitutil: resolve %s: %w", repoDir, err)
	}
	// EvalSymlinks fails when the directory does not exist yet; the
	// unresolved absolute path is a fine lock key in that case, because a
	// commit against a nonexistent repository is going to fail anyway.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	sum := sha256.Sum256([]byte(abs))
	return filepath.Join(os.TempDir(), "cogvault-git-"+hex.EncodeToString(sum[:8])+".lock"), nil
}
