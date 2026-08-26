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
	"syscall"
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

	f, err := openLockFile(path)
	if err != nil {
		return nil, err
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
			_ = f.Close()
			return nil, fmt.Errorf("gitutil: flock %s: %w", path, err)
		}

		select {
		case <-deadlineCtx.Done():
			_ = f.Close()
			return nil, fmt.Errorf("gitutil: commit lock %s busy: %w", path, deadlineCtx.Err())
		case <-time.After(lockRetryInterval):
		}
	}
}

// openLockFile opens the commit lock, refusing anything that is not a plain
// owner-owned regular file.
//
// The containing directory is already owner-only and validated, but that
// only excludes *other* users. It does not exclude a symlink planted at this
// exact path by a compromised process running as the same user, and a
// followed symlink would make cogvault flock — and create — a file somewhere
// else entirely.
//
// O_NOFOLLOW refuses the symlink at open time; O_CLOEXEC keeps the
// descriptor out of the `git` children this package forks while holding the
// lock. The post-open Fstat closes the gap O_NOFOLLOW alone leaves: it
// verifies what the descriptor actually refers to rather than what the path
// looked like, so a swap racing the open is caught too.
func openLockFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("gitutil: open commit lock %s: %w", path, err)
	}
	f := os.NewFile(uintptr(fd), path)

	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("gitutil: stat commit lock %s: %w", path, err)
	}
	if st.Mode&syscall.S_IFMT != syscall.S_IFREG {
		_ = f.Close()
		return nil, fmt.Errorf("gitutil: commit lock %s is not a regular file", path)
	}
	if int(st.Uid) != os.Geteuid() {
		_ = f.Close()
		return nil, fmt.Errorf("gitutil: commit lock %s is owned by uid %d, not %d", path, st.Uid, os.Geteuid())
	}
	if perm := st.Mode & 0o777; perm&0o077 != 0 {
		_ = f.Close()
		return nil, fmt.Errorf("gitutil: commit lock %s is group- or world-accessible (mode %o)", path, perm)
	}
	return f, nil
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

	dir, err := lockDir()
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256([]byte(abs))
	return filepath.Join(dir, "git-"+hex.EncodeToString(sum[:8])+".lock"), nil
}

// userCacheDir is a seam so tests can redirect the lock directory into a
// temp dir. Tests that exercise the directory guards must never mutate the
// real one: a concurrent `cogvault serve` or `ingest` on the same machine
// shares it, and renaming or symlinking it out from under a live process
// would break its commit lock mid-run.
var userCacheDir = os.UserCacheDir

// lockDir returns the directory holding commit locks, creating it if needed.
//
// It deliberately does not live under os.TempDir(). A deterministic name in a
// world-writable directory is squattable: another local user can pre-create
// that exact path with permissions cogvault cannot open, and because both
// call sites treat a commit failure as best-effort and only log a warning,
// the auto-commit safety net would then be silently and permanently disabled.
// os.UserCacheDir() is per-user and not world-writable on either supported
// platform.
//
// The directory is validated with Lstat rather than repaired with Chmod.
// Chmod follows symlinks, so "create then chmod 0700" on an attacker-planted
// symlink re-modes whatever it points at — verified: it silently narrowed an
// unrelated 0755 directory to 0700. A wrong owner, a symlink, or permissive
// bits are therefore reported, never corrected.
func lockDir() (string, error) {
	cache, err := userCacheDir()
	if err != nil {
		return "", fmt.Errorf("gitutil: locate user cache dir: %w", err)
	}
	dir := filepath.Join(cache, "cogvault", "locks")

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("gitutil: create lock dir %s: %w", dir, err)
	}

	// Lstat, not Stat: a symlink must be seen as a symlink rather than
	// followed to whatever it targets. With Lstat a symlink reports
	// IsDir() == false, so the directory check below rejects it — there is
	// deliberately no separate ModeSymlink branch, because mutation testing
	// showed it could be deleted with every test still passing.
	info, err := os.Lstat(dir)
	if err != nil {
		return "", fmt.Errorf("gitutil: stat lock dir %s: %w", dir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("gitutil: lock dir %s is not a directory (mode %v); refusing to use it", dir, info.Mode().Type())
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return "", fmt.Errorf("gitutil: lock dir %s is owned by uid %d, not %d", dir, st.Uid, os.Getuid())
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		return "", fmt.Errorf("gitutil: lock dir %s is group- or world-accessible (mode %o)", dir, mode)
	}
	return dir, nil
}
