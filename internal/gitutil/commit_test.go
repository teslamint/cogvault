package gitutil

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.name", "cogvault test"},
		{"config", "user.email", "test@example.com"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return dir
}

func writePage(t *testing.T, dir, name, content string) string {
	t.Helper()
	abs := filepath.Join(dir, name)
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return abs
}

func commitSubjects(t *testing.T, dir string) []string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "log", "--format=%s").CombinedOutput()
	if err != nil {
		// A repository with no commits yet exits nonzero.
		return nil
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

func shrink(t *testing.T, timeout, grace time.Duration) {
	t.Helper()
	origTimeout, origGrace := CommitTimeout, TerminateGrace
	CommitTimeout, TerminateGrace = timeout, grace
	t.Cleanup(func() { CommitTimeout, TerminateGrace = origTimeout, origGrace })
}

func TestCommitStagesAndCommits(t *testing.T) {
	dir := initRepo(t)
	abs := writePage(t, dir, "page.md", "# Page")

	stage, err := Commit(context.Background(), dir, []string{abs}, "wiki: write page.md")
	if err != nil {
		t.Fatalf("Commit failed at stage %q: %v", stage, err)
	}
	if stage != StageNone {
		t.Fatalf("stage = %q, want StageNone on success", stage)
	}
	if subjects := commitSubjects(t, dir); len(subjects) != 1 || subjects[0] != "wiki: write page.md" {
		t.Fatalf("commit subjects = %v, want one write commit", subjects)
	}
}

func TestCommitReportsAddStage(t *testing.T) {
	dir := initRepo(t)

	// A pathspec matching nothing fails `git add` with exit 128, before any
	// commit is attempted.
	stage, err := Commit(context.Background(), dir, []string{filepath.Join(dir, "absent.md")}, "wiki: write absent.md")
	if err == nil {
		t.Fatal("Commit succeeded for a nonexistent pathspec, want an add failure")
	}
	if stage != StageAdd {
		t.Fatalf("stage = %q, want StageAdd so callers can log the right step", stage)
	}
}

// TestCommitSerializesConcurrentCallers is the regression test for the
// intra-process race: git refuses concurrent index operations on one
// repository, so unsynchronized callers lose commits outright — the failure
// mode the auto-commit safety net exists to prevent. Every caller that
// reports success must have produced a commit.
func TestCommitSerializesConcurrentCallers(t *testing.T) {
	dir := initRepo(t)

	const callers = 8
	var wg sync.WaitGroup
	errs := make([]error, callers)
	stages := make([]Stage, callers)

	for i := range callers {
		name := fmt.Sprintf("page%d.md", i)
		writePage(t, dir, name, "# Page")
		wg.Add(1)
		go func() {
			defer wg.Done()
			stages[i], errs[i] = Commit(
				context.Background(),
				dir,
				[]string{filepath.Join(dir, name)},
				"wiki: write "+name,
			)
		}()
	}
	wg.Wait()

	succeeded := 0
	for i, err := range errs {
		if err != nil {
			t.Errorf("caller %d failed at stage %q: %v", i, stages[i], err)
			continue
		}
		succeeded++
	}

	subjects := commitSubjects(t, dir)
	if len(subjects) != succeeded {
		t.Fatalf("git recorded %d commits but %d callers reported success (subjects: %v); a reported success with no commit is the silent drop this lock prevents", len(subjects), succeeded, subjects)
	}
	if succeeded != callers {
		t.Fatalf("%d/%d callers succeeded; serialized commits must all land", succeeded, callers)
	}
}

// TestCommitTimeoutLeavesNoStaleIndexLock is the regression test for using
// exec's default SIGKILL on a timed-out git subprocess. Both `git add` and
// `git commit` hold .git/index.lock while running; SIGKILL cannot be
// trapped, so the lock survives with no cleanup path in cogvault and every
// later commit fails until an operator removes it by hand — manufacturing
// exactly the wedged index.lock the timeout is supposed to defend against.
// SIGTERM lets git remove its own lock on the way out.
func TestCommitTimeoutLeavesNoStaleIndexLock(t *testing.T) {
	dir := initRepo(t)
	abs := writePage(t, dir, "page.md", "# Page")

	binDir, err := filepath.Abs("testdata/bin")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// The fake git holds index.lock and sleeps, mimicking a real add/commit
	// that is still working when its budget expires. It removes the lock
	// from a SIGTERM trap, exactly as real git does.
	t.Setenv("GIT_FAKE_LOCK_DIR", filepath.Join(dir, ".git"))
	t.Setenv("GIT_FAKE_ADD_SLEEP", "10")
	shrink(t, 100*time.Millisecond, 2*time.Second)

	started := time.Now()
	stage, err := Commit(context.Background(), dir, []string{abs}, "wiki: write page.md")
	elapsed := time.Since(started)

	if err == nil {
		t.Fatal("Commit succeeded against a wedged fake git, want a timeout failure")
	}
	if stage != StageAdd {
		t.Fatalf("stage = %q, want StageAdd", stage)
	}
	if elapsed >= 5*time.Second {
		t.Fatalf("Commit took %s, want bounded by the shrunk CommitTimeout", elapsed)
	}

	lockPath := filepath.Join(dir, ".git", "index.lock")
	if _, statErr := os.Stat(lockPath); statErr == nil {
		t.Fatal("index.lock survived the timeout; the subprocess must be terminated with a signal git can trap, so it removes its own lock")
	}
}

// TestCommitLockTimesOutRatherThanHanging pins that a caller blocked behind
// a wedged commit gives up on its own budget instead of waiting forever.
// Unbounded queueing behind a stuck holder would reintroduce the indefinite
// tool-call block the timeout exists to prevent.
func TestCommitLockTimesOutRatherThanHanging(t *testing.T) {
	dir := initRepo(t)
	abs := writePage(t, dir, "page.md", "# Page")

	release, err := lockRepo(context.Background(), dir)
	if err != nil {
		t.Fatalf("seed lock: %v", err)
	}
	defer release()

	shrink(t, 150*time.Millisecond, 200*time.Millisecond)

	started := time.Now()
	stage, err := Commit(context.Background(), dir, []string{abs}, "wiki: write page.md")
	elapsed := time.Since(started)

	if err == nil {
		t.Fatal("Commit acquired a lock already held, want a lock timeout")
	}
	if stage != StageLock {
		t.Fatalf("stage = %q, want StageLock", stage)
	}
	if elapsed >= 5*time.Second {
		t.Fatalf("blocked caller waited %s, want bounded by the shrunk CommitTimeout", elapsed)
	}
	if subjects := commitSubjects(t, dir); len(subjects) != 0 {
		t.Fatalf("commit subjects = %v, want none; a caller that never got the lock must not have run git", subjects)
	}
}

// TestCommitLockIsPerRepository guards against over-serializing: two
// distinct wikis in one process must not block each other.
func TestCommitLockIsPerRepository(t *testing.T) {
	first := initRepo(t)
	second := initRepo(t)

	release, err := lockRepo(context.Background(), first)
	if err != nil {
		t.Fatalf("seed lock: %v", err)
	}
	defer release()

	shrink(t, 2*time.Second, 200*time.Millisecond)

	abs := writePage(t, second, "page.md", "# Page")
	stage, err := Commit(context.Background(), second, []string{abs}, "wiki: write page.md")
	if err != nil {
		t.Fatalf("commit against an unlocked repository failed at stage %q: %v", stage, err)
	}
}

// TestCommitLockFileLivesOutsideTheWorkingTree pins that the lock cannot be
// swept into the post-ingest `git add -A -- .` snapshot and committed as
// wiki content.
func TestCommitLockFileLivesOutsideTheWorkingTree(t *testing.T) {
	dir := initRepo(t)

	dirfd, _, path, err := lockTarget(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Close(dirfd) }()
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(path, resolvedDir+string(os.PathSeparator)) {
		t.Fatalf("lock path %q is inside the wiki working tree %q; the post-ingest whole-tree add would commit it", path, resolvedDir)
	}
}

// TestCommitGivesEachStageAnIndependentDeadline is the regression test for
// the pre-0024-correction implementation sharing one context.WithTimeout
// across both the add and commit subprocesses: a slow-but-not-wedged
// `git add` (a large working-tree scan) consumed most of the shared budget,
// leaving `git commit` too little time and turning a merely slow add into a
// spurious commit failure — the write lands on disk while the safety net's
// history entry silently never appears.
//
// It substitutes the command runner and reads the deadline each stage
// actually receives. Three earlier revisions of this test discriminated on
// elapsed wall-clock time and each flaked under load, because the observable
// failure mode is a stage exceeding its own budget, which no margin can rule
// out on a contended machine. Deadlines are exact and need no sleeps at all.
func TestCommitGivesEachStageAnIndependentDeadline(t *testing.T) {
	dir := initRepo(t)
	abs := writePage(t, dir, "page.md", "# Page")
	shrink(t, 10*time.Second, 2*time.Second)

	// `simulatedNow` is a bookkeeping timestamp, not a clock: nothing here
	// intercepts time. It is the origin each stage's deadline is computed
	// from, and advancing it between stages models the elapsed time a slow
	// `git add` would have consumed. The deadlines below are still armed on
	// real timers by context.WithDeadline, but none of them can fire —
	// the substituted runner returns immediately — so the assertions read
	// pure arithmetic rather than anything the scheduler influences.
	simulatedNow := time.Now()
	elapsedDuringAdd := 9 * time.Second

	origTimeout := withTimeout
	// Each stage's deadline is derived from simulatedNow rather than the
	// wall clock, so the simulated elapsed time is visible in the deadline
	// arithmetic. Real context.WithTimeout would read the wall clock and
	// see ~0 elapsed between the two stages.
	withTimeout = func(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
		return context.WithDeadline(parent, simulatedNow.Add(d))
	}
	t.Cleanup(func() { withTimeout = origTimeout })

	type observation struct {
		stage     string
		deadline  time.Time
		startedAt time.Time
	}
	var seen []observation

	origRun := runGit
	runGit = func(ctx context.Context, args []string) error {
		stage := "other"
		for _, a := range args {
			if a == "add" || a == "commit" {
				stage = a
				break
			}
		}
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Errorf("%s ran with no deadline; every git subprocess must be bounded", stage)
			return nil
		}
		seen = append(seen, observation{stage: stage, deadline: deadline, startedAt: simulatedNow})
		// The slow-add scenario: add burns most of a budget before commit
		// even starts. Under a shared context this is exactly what starved
		// commit; under independent budgets it must not matter.
		if stage == "add" {
			simulatedNow = simulatedNow.Add(elapsedDuringAdd)
		}
		return nil
	}
	t.Cleanup(func() { runGit = origRun })

	if stage, err := Commit(context.Background(), dir, []string{abs}, "wiki: write page.md"); err != nil {
		t.Fatalf("Commit failed at stage %v: %v", stage, err)
	}

	if len(seen) != 2 || seen[0].stage != "add" || seen[1].stage != "commit" {
		t.Fatalf("observed stages = %+v, want add then commit", seen)
	}

	// The property under test, stated as the thing that actually broke:
	// after add consumed elapsedDuringAdd, commit must still receive a
	// full CommitTimeout measured from its own start. A shared context
	// would leave it CommitTimeout-elapsedDuringAdd (here 1s of 10s).
	commitBudget := seen[1].deadline.Sub(seen[1].startedAt)
	if commitBudget != CommitTimeout {
		t.Fatalf("commit budget = %s, want the full CommitTimeout %s; a slow add (%s) must not eat into commit's own budget",
			commitBudget, CommitTimeout, elapsedDuringAdd)
	}
	if addBudget := seen[0].deadline.Sub(seen[0].startedAt); addBudget != CommitTimeout {
		t.Fatalf("add budget = %s, want the full CommitTimeout %s", addBudget, CommitTimeout)
	}
}

// TestLockDirIsPrivateToTheUser pins the lock outside any world-writable
// directory.
//
// The original implementation put a deterministic name straight in
// os.TempDir(). Verified before the fix: pre-creating that exact path mode
// 0o400 made Commit return `stage=lock ... permission denied` on every call,
// and because both call sites treat commit failure as best-effort and only
// log a warning, the auto-commit safety net was silently and permanently
// disabled by an unprivileged local user.
func TestLockDirIsPrivateToTheUser(t *testing.T) {
	dir := initRepo(t)

	dirfd, _, path, err := lockTarget(dir)
	if err != nil {
		t.Fatalf("lockTarget: %v", err)
	}
	defer func() { _ = unix.Close(dirfd) }()

	parent := filepath.Dir(path)
	if parent == os.TempDir() {
		t.Fatalf("lock %q sits directly in the shared temp dir; a deterministic name there is squattable", path)
	}

	info, err := os.Lstat(parent)
	if err != nil {
		t.Fatalf("lstat lock dir: %v", err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		t.Fatalf("lock dir %q mode = %o, want no group/world bits", parent, mode)
	}
}

// TestLockDirRejectsASymlink pins O_NOFOLLOW on the directory open.
//
// The victim directory is deliberately 0700 and owned by this user: a
// permissive victim would be rejected by the mode check even if the symlink
// were followed, making the test pass for the wrong reason (verified — with
// a 0755 victim, removing O_NOFOLLOW left the test green). At 0700 the only
// thing that can refuse is the refusal to follow the link itself.
func TestLockDirRejectsASymlink(t *testing.T) {
	// Redirect the cache base into a temp dir. This test plants a symlink
	// where the lock directory belongs, and doing that to the real
	// per-user cache would break the commit lock of any cogvault process
	// running concurrently on this machine.
	base := fakeCacheDir(t)

	victim := t.TempDir()
	if err := os.Chmod(victim, 0o700); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(base, "cogvault", "locks")
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, dir); err != nil {
		t.Fatalf("planting symlink: %v", err)
	}

	if _, _, err := openLockDir(); err == nil {
		t.Fatal("openLockDir accepted a symlinked lock dir; it must refuse rather than follow it")
	}
}

// TestLockDirRejectsAPermissiveDirectory pins the mode check. MkdirAll
// returns nil for an existing directory without narrowing its bits, so a
// pre-existing group- or world-accessible lock dir must be reported rather
// than silently used.
func TestLockDirRejectsAPermissiveDirectory(t *testing.T) {
	base := fakeCacheDir(t)

	dir := filepath.Join(base, "cogvault", "locks")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}

	if _, _, err := openLockDir(); err == nil {
		t.Fatal("openLockDir accepted a world-accessible lock dir; permissive bits must be reported, not used")
	}
}

// fakeCacheDir points the lock directory at a temp dir for the duration of
// one test, so guard tests never mutate the shared per-user cache.
func fakeCacheDir(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	orig := userCacheDir
	userCacheDir = func() (string, error) { return base, nil }
	t.Cleanup(func() { userCacheDir = orig })
	return base
}

// TestOpenLockFileRejectsASymlink pins the file-level guard. The validated
// owner-only directory excludes other users, but not a symlink planted at
// this exact path by a compromised process running as the same user; a
// followed symlink would have cogvault create and flock a file elsewhere.
func TestOpenLockFileRejectsASymlink(t *testing.T) {
	fakeCacheDir(t)
	dir := initRepo(t)

	dirfd, name, path, err := lockTarget(dir)
	if err != nil {
		t.Fatalf("lockTarget: %v", err)
	}
	defer func() { _ = unix.Close(dirfd) }()
	_ = os.Remove(path)

	target := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.Symlink(target, path); err != nil {
		t.Fatalf("planting symlink: %v", err)
	}

	if _, err := openLockFileAt(dirfd, name, path); err == nil {
		t.Fatal("openLockFileAt followed a symlink; O_NOFOLLOW must refuse it")
	}
	if _, err := os.Stat(target); err == nil {
		t.Fatalf("symlink target %s was created; the open must not reach through the link", target)
	}
}

// TestLockSurvivesLockDirSwap is the regression test for the finding that
// O_NOFOLLOW alone left an intermediate-component race open.
//
// The old code validated `.../cogvault/locks` by path and then opened
// `.../cogvault/locks/<hash>.lock` by path. Between those two steps a
// process with the same euid could rename the directory and leave a symlink
// in its place; the kernel follows an intermediate symlink regardless of
// O_NOFOLLOW, so the lock would be taken in an unvalidated location while
// another caller still locked the original — commit serialization silently
// broken, which is exactly what the lock exists to prevent.
//
// Opening the lock file with openat against the already-validated directory
// descriptor binds validation and use to one inode: the swap below happens
// after the descriptor exists, and the open must still land in the real
// directory.
func TestLockSurvivesLockDirSwap(t *testing.T) {
	base := fakeCacheDir(t)
	dir := initRepo(t)

	dirfd, name, _, err := lockTarget(dir)
	if err != nil {
		t.Fatalf("lockTarget: %v", err)
	}
	defer func() { _ = unix.Close(dirfd) }()

	// Swap the lock directory for a symlink to an attacker-controlled one,
	// after validation, before the file open.
	real := filepath.Join(base, "cogvault", "locks")
	decoy := t.TempDir()
	if err := os.Rename(real, real+".moved"); err != nil {
		t.Fatalf("renaming lock dir: %v", err)
	}
	if err := os.Symlink(decoy, real); err != nil {
		t.Fatalf("planting symlink: %v", err)
	}

	f, err := openLockFileAt(dirfd, name, name)
	if err != nil {
		t.Fatalf("openLockFileAt after a directory swap: %v", err)
	}
	defer func() { _ = f.Close() }()

	// The lock file must exist in the renamed-aside real directory, not in
	// the decoy the symlink points at.
	if _, err := os.Stat(filepath.Join(real+".moved", name)); err != nil {
		t.Fatalf("lock file missing from the validated directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(decoy, name)); err == nil {
		t.Fatalf("lock file landed in the swapped-in decoy %s; openat must resolve against the validated inode", decoy)
	}
}

func mustMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}
