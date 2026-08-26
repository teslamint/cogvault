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
// Callers are serialized per repository by an advisory file lock, which
// covers both concurrent MCP tool handlers in one process and a live
// `serve` racing a scheduled `ingest` — flock excludes per open file
// description, so goroutines contend exactly as processes do and no
// separate in-process mutex is needed. Both the lock wait and each git
// subprocess are bounded by CommitTimeout.
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
// The lock file lives in an owner-only directory under os.UserCacheDir(),
// keyed by a hash of the resolved repository path, rather than inside
// repoDir: a lock file in the working tree would be swept into the
// post-ingest `git add -A -- .` snapshot and committed as wiki content.
// Symlinks are resolved first so two callers reaching the same directory by
// different paths share one lock.
//
// The wait is a bounded non-blocking retry loop rather than flock's own
// blocking mode, because a blocking flock cannot be interrupted by a
// context: a caller queued behind a wedged commit would hang indefinitely,
// which is the exact failure the 0024 timeout exists to prevent.
func lockRepo(ctx context.Context, repoDir string) (func(), error) {
	dirfd, name, display, err := lockTarget(repoDir)
	if err != nil {
		return nil, err
	}
	// The directory descriptor is only needed to anchor the openat below;
	// once the lock file is open, the file descriptor itself keeps the
	// inode alive regardless of what happens to the directory entry.
	defer func() { _ = unix.Close(dirfd) }()

	f, err := openLockFileAt(dirfd, name, display)
	if err != nil {
		return nil, err
	}
	path := display

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

// openLockFileAt opens the commit lock relative to an already-validated
// directory descriptor, refusing anything that is not a plain owner-owned
// regular file.
//
// Relative to dirfd, not by full path, and this is the whole point:
// O_NOFOLLOW only refuses a symlink at the *final* component. Validating
// `.../cogvault/locks` by path and then opening `.../cogvault/locks/x.lock`
// by path leaves the intermediate component unbound — a process with the
// same euid can rename `locks` and drop a symlink in its place between the
// two, and the kernel follows it. The lock would then be taken on a file in
// a directory nobody validated, while another caller still locks the
// original: commit serialization silently breaks, which is the failure the
// lock exists to prevent.
//
// openat resolves against the directory *inode* the caller already checked,
// so a rename or symlink swap afterwards cannot redirect it.
//
// O_CLOEXEC keeps the descriptor out of the `git` children this package
// forks while holding the lock. The Fstat inspects the descriptor rather
// than re-stat'ing a path, so nothing can be substituted between the check
// and the use; it does not detect a swap that won before the open, it
// guarantees what the descriptor being flocked refers to.
func openLockFileAt(dirfd int, name, display string) (*os.File, error) {
	fd, err := unix.Openat(dirfd, name, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("gitutil: open commit lock %s: %w", display, err)
	}
	f := os.NewFile(uintptr(fd), display)

	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("gitutil: stat commit lock %s: %w", display, err)
	}
	if st.Mode&syscall.S_IFMT != syscall.S_IFREG {
		_ = f.Close()
		return nil, fmt.Errorf("gitutil: commit lock %s is not a regular file", display)
	}
	if int(st.Uid) != os.Geteuid() {
		_ = f.Close()
		return nil, fmt.Errorf("gitutil: commit lock %s is owned by uid %d, not %d", display, st.Uid, os.Geteuid())
	}
	if perm := st.Mode & 0o777; perm&0o077 != 0 {
		_ = f.Close()
		return nil, fmt.Errorf("gitutil: commit lock %s is group- or world-accessible (mode %o)", display, perm)
	}
	return f, nil
}

// lockTarget returns a validated directory descriptor for the lock
// directory, the basename of this repository's lock file within it, and a
// display path for error messages.
//
// The caller owns dirfd and must close it.
func lockTarget(repoDir string) (dirfd int, name, display string, err error) {
	abs, err := filepath.Abs(repoDir)
	if err != nil {
		return -1, "", "", fmt.Errorf("gitutil: resolve %s: %w", repoDir, err)
	}
	// EvalSymlinks fails when the directory does not exist yet; the
	// unresolved absolute path is a fine lock key in that case, because a
	// commit against a nonexistent repository is going to fail anyway.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}

	dir, dirfd, err := openLockDir()
	if err != nil {
		return -1, "", "", err
	}

	sum := sha256.Sum256([]byte(abs))
	name = "git-" + hex.EncodeToString(sum[:8]) + ".lock"
	return dirfd, name, filepath.Join(dir, name), nil
}

// userCacheDir is a seam so tests can redirect the lock directory into a
// temp dir. Tests that exercise the directory guards must never mutate the
// real one: a concurrent `cogvault serve` or `ingest` on the same machine
// shares it, and renaming or symlinking it out from under a live process
// would break its commit lock mid-run.
var userCacheDir = os.UserCacheDir

// openLockDir creates the lock directory if needed and returns its path
// together with an open, validated descriptor for it.
//
// It deliberately does not live under os.TempDir(). A deterministic name in a
// world-writable directory is squattable: another local user can pre-create
// that exact path with permissions cogvault cannot open, and because both
// call sites treat a commit failure as best-effort and only log a warning,
// the auto-commit safety net would then be silently and permanently disabled.
// os.UserCacheDir() is per-user and not world-writable on either supported
// platform, so it is the trust anchor.
//
// From that anchor the path is walked one component at a time with mkdirat
// and openat, never as a joined string. O_NOFOLLOW only refuses a symlink at
// the *final* component, so resolving `<cache>/cogvault/locks` by path would
// leave `cogvault` unguarded: a same-euid process could rename it, drop a
// symlink in its place pointing at a directory it controls, and put an
// owner-only `locks` inside. The final directory would then pass every owner
// and mode check while being an entirely different inode, so a second caller
// would take its lock somewhere else and commit serialization would break —
// exactly what the lock exists to prevent. Walking descriptors makes each
// component's validation bind to the next one's open.
//
// Nothing is repaired, only rejected. An earlier version did MkdirAll
// followed by Chmod(0700); Chmod follows symlinks, so a planted symlink made
// cogvault re-mode whatever it pointed at — measured, an unrelated 0755
// directory silently became 0700.
func openLockDir() (string, int, error) {
	cache, err := userCacheDir()
	if err != nil {
		return "", -1, fmt.Errorf("gitutil: locate user cache dir: %w", err)
	}
	// The anchor is created by path — it is the boundary of what this
	// function can verify — but it is still opened with O_NOFOLLOW and
	// checked, so a symlink standing where the cache directory belongs is
	// refused rather than traversed.
	//
	// The anchor's permission check is narrower than the components below,
	// and the distinction is between readable and writable. Requiring 0700
	// here would reject a normal install: the per-user cache directory is
	// conventionally group- and world-*readable* (macOS ships
	// `~/Library/Caches` as 0755, verified on the primary platform), and
	// because callers only log commit failures, rejecting it would silently
	// disable the auto-commit safety net — the same class of bug this change
	// exists to fix. Group- or world-*writable* is a different matter: it
	// lets another local user create or replace the `cogvault` entry inside
	// it, which is precisely the squatting this walk defends against. So
	// read bits are tolerated at the anchor and write bits are not. Secrecy
	// is enforced from `cogvault` down, which cogvault creates and owns.
	if err := os.MkdirAll(cache, 0o700); err != nil {
		return "", -1, fmt.Errorf("gitutil: create cache dir %s: %w", cache, err)
	}
	fd, err := unix.Open(cache, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", -1, fmt.Errorf("gitutil: open cache dir %s: %w", cache, err)
	}
	var anchor syscall.Stat_t
	if err := syscall.Fstat(fd, &anchor); err != nil {
		_ = unix.Close(fd)
		return "", -1, fmt.Errorf("gitutil: stat cache dir %s: %w", cache, err)
	}
	if int(anchor.Uid) != os.Geteuid() {
		_ = unix.Close(fd)
		return "", -1, fmt.Errorf("gitutil: cache dir %s is owned by uid %d, not %d", cache, anchor.Uid, os.Geteuid())
	}
	if perm := anchor.Mode & 0o777; perm&0o022 != 0 {
		_ = unix.Close(fd)
		return "", -1, fmt.Errorf("gitutil: cache dir %s is group- or world-writable (mode %o); another user could squat the lock directory", cache, perm)
	}

	dir := cache
	for _, component := range []string{"cogvault", "locks"} {
		dir = filepath.Join(dir, component)

		next, err := openDirAt(fd, component, dir)
		_ = unix.Close(fd)
		if err != nil {
			return "", -1, err
		}
		fd = next
	}
	return dir, fd, nil
}

// openDirAt creates and opens one path component relative to parent,
// refusing a symlink, a non-directory, a foreign owner, or permissive bits.
// display is the full path, used only for error messages.
func openDirAt(parent int, name, display string) (int, error) {
	if err := unix.Mkdirat(parent, name, 0o700); err != nil && !errors.Is(err, unix.EEXIST) {
		return -1, fmt.Errorf("gitutil: create lock dir %s: %w", display, err)
	}

	// O_NOFOLLOW rejects a symlink standing where the directory belongs;
	// O_DIRECTORY rejects anything that is not a directory.
	fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, fmt.Errorf("gitutil: open lock dir %s: %w", display, err)
	}

	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("gitutil: stat lock dir %s: %w", display, err)
	}
	// Geteuid, not Getuid: the effective uid is what the kernel checks on
	// open, and it is what openLockFileAt compares against — the two must
	// agree or a setuid context would pass one guard and fail the other.
	if int(st.Uid) != os.Geteuid() {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("gitutil: lock dir %s is owned by uid %d, not %d", display, st.Uid, os.Geteuid())
	}
	if perm := st.Mode & 0o777; perm&0o077 != 0 {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("gitutil: lock dir %s is group- or world-accessible (mode %o)", display, perm)
	}
	return fd, nil
}
