# 0024: Wiki Git Safety Net

- Status: accepted
- Date: 2026-08-25
- Context owner: this decision; amends `docs/deployment/remote-mcp.md` "Security posture" and `SPEC.md` §3.1/§8.3/§8.8/§9.4.

## Problem

`wiki_write` overwrites unconditionally without committing; nothing commits on
ingest. Only `wiki_delete` auto-commits — and because nothing else ever
tracked the file, that commit records the removal of content that was never in
git. An attacker (or bug) holding a valid credential can silently overwrite or
delete the entire wiki with no restore path. The current canon assigns the
snapshot duty to the operator (`docs/deployment/remote-mcp.md`: "cogvault will
never do this step for you"), which leaves the default configuration with no
recovery.

## Decision

The safety net is available but **opt-in**, preserving the pre-0024 default
behavior and the documented operator responsibility:

1. New config key `git.auto_commit`:
   - `off` (default): exactly today's behavior — only `wiki_delete` commits
     its own deletion. Canon unchanged.
   - `write`: after each successful `wiki_write`, run the existing
     `gitAutoCommit` path (`git add <path>` + `git commit -m "wiki: write
     <path>"`). Failures log, never tool errors (same contract as §8.8).
   - `write+ingest`: additionally commit the whole tree once after a
     successful ingest run (`git add -A -- .` + one commit — the `-- .`
     pathspec is load-bearing, see the correction below), so digested pages
     are captured too.
2. The commit subprocess gains a context timeout (e.g. 10s) so a wedged
   `index.lock` cannot block a tool call indefinitely — this part is a plain
   robustness fix and may land independently of the opt-in.
3. `docs/deployment/remote-mcp.md` gains an `auto_commit` note: it narrows the
   window but is not a backup (same-repo history is attacker-writable);
   off-repo snapshots remain the real safety net.

## Alternatives considered

- **Commit always, no config** — rejected: changes the write path's failure
  profile for every user, and 0006/0021 treat wiki_dir as plain storage, not a
  git-managed tree. Silent behavior change on a security-relevant path needs a
  spec round.
- **Status quo only** — rejected as default: "no recovery" is a documented
  hole, not a virtue; opt-in closes it for operators who want it without
  taxing the rest.

## Consequences

- `internal/mcp/tools.go`: `gitAutoCommit` parameterized by mode; `wiki_write`
  handler calls it after successful write when configured.
- `internal/config`: new `GitConfig` block + validation (`off|write|write+ingest`).
- SPEC §8.7/§8.8, §3 config table, deployment guide updated on acceptance.
- Tests: commit invoked/not-invoked per mode (fake `git` on PATH), timeout path.

## Status

Accepted and implemented (2026-08-25):

- `internal/config`: `GitConfig{AutoCommit string}` + `ValidGitAutoCommitModes()`
  + `CommitsOnWrite()`/`CommitsOnIngest()` predicates. Default `"off"`;
  invalid values rejected.
- `internal/mcp/tools.go`: `gitAutoCommit(root, path, message)` takes an
  explicit commit message; `add` and `commit` each get their own
  independent `gitCommitTimeout`-bounded context (10s, a `var` so tests can
  shrink it). `wiki_write` calls it only when `cfg.Git.CommitsOnWrite()`;
  `wiki_delete`'s own call is unconditional (unchanged from before this
  decision), though the commit itself only lands when the deleted file was
  already git-tracked — `git add` on a never-tracked path fails with no
  matching pathspec.
- `cmd/cogvault/ingest.go`: `postIngestGitCommit` runs after a successful
  `cogvault ingest` run when `cfg.Git.CommitsOnIngest()` and
  `report.Digested > 0`; `git add -A -- .` + one `git commit -m "wiki: ingest
  snapshot"` over the whole `wiki_dir`, bounded by `ingestGitCommitTimeout`
  (10s, also a `var`). Duplicated rather than shared with `internal/mcp`'s
  helper — `cmd` depends on `mcp`/`ingest` per DESIGN.md's dependency graph,
  not the reverse.
  **Correction during review** (same day): the initial implementation used
  bare `git add -A`, which resolves against the enclosing git repository's
  root, not `wikiDir`, whenever `wikiDir` is a plain subdirectory of a
  larger repo rather than its own git root — it would have staged and
  committed dirty files anywhere in that outer repo. Fixed to
  `git add -A -- .`, which scopes the add to `wikiDir` regardless of repo
  nesting. `internal/mcp`'s per-file `gitAutoCommit` was unaffected — it
  passes an absolute single-file path, never `-A`.
- Docs updated: `SPEC.md` §1.3, §3.1, §8.3, §8.8, §9.4; `DESIGN.md` §2.2,
  §2.8, §2.9; `CLAUDE.md` Working Context invariant 6 (also reworded during
  the same review pass so "`write` or `write+ingest`" could not be misread
  as `write` alone also covering ingest runs — `write` covers `wiki_write`
  only, `write+ingest` is the one that additionally covers ingest);
  `docs/deployment/remote-mcp.md` §7 (Security posture) and its backup
  guidance.
- Tests: `internal/config/config_test.go` (defaults, all three modes,
  invalid rejection); `internal/mcp/tools_test.go` (`write` commits,
  `off` does not, `wiki_delete` calls the commit path regardless of mode
  when the deleted file was already git-tracked, a non-git `wiki_dir` logs
  and does not error, the timeout bounds a wedged commit and a wedged add,
  via a fake `git` binary at `internal/mcp/testdata/bin/git`);
  `cmd/cogvault/ingest_git_commit_test.go`
  (`write+ingest` commits after a digesting run, `off` and `write`-alone do
  not, `--dry-run` does not, a zero-digest run does not, a `wikiDir` nested
  inside a larger repo does not stage or commit files outside `wikiDir`
  (`TestIngestGitCommit_NestedWikiDirDoesNotStageOutsideFiles`, confirmed
  to fail against the pre-fix `-A`-without-pathspec code), the timeout
  bounds a wedged commit and a wedged add reusing the same fake `git`
  binary). The independent-budget property itself is covered once, in
  `internal/gitutil` where it now lives — see the fourth correction pass.

**Second correction pass** (same day, via `coderabbit review --committed
--base main` after the webhook-triggered PR review stalled for several
hours with zero artifacts): three findings, all reproduced before fixing.

1. `gitAutoCommit` and `postIngestGitCommit` shared one
   `context.WithTimeout` across both their `git add` and `git commit`
   subprocesses. A slow-but-not-wedged `add` (e.g. a large working-tree
   scan) could consume most of the shared budget, leaving `commit` too
   little time and silently dropping an otherwise-successful commit.
   Reproduced with `exec.CommandContext` against a 100ms shared budget
   split 80ms add / 50ms commit: add succeeded, commit was killed by
   context exhaustion. Fixed to two independent per-command timeout
   contexts in both functions; each is now bounded by its own full budget
   regardless of how long the other command took.
2. CLAUDE.md invariant 6 and `SPEC.md` §1.3/§8.8 claimed `wiki_delete`
   "always" auto-commits its deletion. Reproduced in `/tmp`: `git add` on a
   path git never tracked (the normal case for any page written while
   `git.auto_commit: off`, the default) fails with "did not match any
   files" (exit 128), so no commit is created — the delete leaves no git
   record at all in that case. The call is unconditional (it always
   *attempts*); the commit is not unconditional. Reworded in both
   documents, plus `DESIGN.md` §2.8.

**Third correction pass** (2026-08-26, post-merge multi-lane review of the
whole `c33d88d..HEAD` range): one P0 and five P1 findings, each independently
validated against the code before fixing.

1. **P0 — `wiki_write` to git-controlled paths chained into remote code
   execution.** `storage.FSStorage.Write` rejected only `ExcludeRead` entries
   and `_schema.md`; it never excluded `.git/`. `git add` executes the clean
   filter that `.gitattributes` names, with the filter's command line read
   from `.git/config`. So a credential holder could `wiki_write`
   `.gitattributes` (`*.md filter=evil`) plus `.git/config` (`[filter "evil"]
   clean = <command>`), after which the *next* ordinary `wiki_write` by anyone
   ran that command as the cogvault server process. Reproduced end-to-end
   three times independently. This inverted the decision's own threat
   ceiling: 0024 assumed "a valid credential grants full read/write/delete
   access" as the worst case, and the auto-commit mechanism turned that into
   arbitrary execution.
   Fixed in `internal/storage/fs.go`: `isGitControlled` rejects any path with
   a `.git`, `.gitattributes`, or `.gitmodules` component, at any depth, on
   `Write`, `Delete`, and both ends of `Move`. It is deliberately **not** a
   `cfg.Exclude` default — an operator editing a list that otherwise only
   controls search visibility must not be able to re-open an RCE.
   `.gitignore` stays writable: it selects paths, it never names a command.

   **Follow-up within the same pass**: the first version of this guard
   compared components case-sensitively, which on APFS (macOS default, this
   project's primary platform) was no guard at all — `wiki_write` to
   `.GIT/config` and `.GitAttributes` resolves to the real files and the full
   exploit still fired, reproduced end-to-end through the live MCP stdio
   surface with the marker file created and `.git/config` poisoned. Now
   compared with `strings.EqualFold`, uniformly rather than by probing
   filesystem semantics, so the boundary does not depend on the host. The
   guard's tests carry case variants and were confirmed to fail against a
   case-sensitive mutant.
2. **P1 — concurrent commits silently dropped (intra- and cross-process).**
   Neither `gitAutoCommit` nor `postIngestGitCommit` held any lock, and git
   refuses concurrent index operations on one repository. Two MCP handlers,
   or a scheduled `ingest` racing a live `serve`, would have one side fail
   `git add`/`git commit` with only a `slog.Warn` while the tool call still
   reported success — the write lands on disk but the safety net's history
   entry never exists. Measured: 8 concurrent unsynchronized commits produced
   1 commit and 7 exit-128 failures.
   Fixed by serializing on one advisory `flock` per repository. `flock`
   excludes per *open file description*, so two goroutines in one process
   contend exactly as two processes do — verified by direct probe rather than
   assumed, since a first implementation carried a redundant in-process
   semaphore on the belief that `flock` was per-process. The lock file lives
   in an owner-only directory under `os.UserCacheDir()` keyed by a hash of
   the resolved repository path, not in the working tree, so the whole-tree
   `git add -A -- .` cannot commit it as wiki content. (It was originally
   placed directly in the OS temp directory; see the fifth correction pass.)
3. **P1 — the timeout could manufacture the wedged `index.lock` it defends
   against.** Neither call site set `Cancel`/`WaitDelay`, so Go's default for
   a cancelled `CommandContext` is `SIGKILL`. Both `git add` and `git commit`
   hold `.git/index.lock` for their duration and remove it from their own
   SIGTERM handler; `SIGKILL` cannot be trapped, so a timeout left that lock
   on disk with no cleanup path anywhere in cogvault — breaking every later
   auto-commit until an operator deleted it by hand, and unlike the original
   hypothetical wedge, persisting past the tool call.
   Fixed: timed-out subprocesses get `SIGTERM` plus a `TerminateGrace`
   (2s) `WaitDelay` before Go force-kills, so git cleans up after itself.
4. **P1 — no test proved `git add`'s own timeout binding, at either call
   site.** Only the `commit` half of each independent-timeout pair had a
   wedged-subprocess regression test; rebinding `add` to an unbounded context
   (silently reverting half of correction pass 2) left the entire suite
   green. Fixed with `TestGitAutoCommit_TimeoutBoundsWedgedAdd`,
   `TestIngestGitCommit_TimeoutBoundsWedgedAdd`, and
   `TestCommitTimeoutLeavesNoStaleIndexLock`; all three were confirmed to
   fail against a mutant that drops add's bounded context.
5. **P1 — `auto_commit` makes deleted content permanently recoverable, and
   no document said so.** Both 0024 and `remote-mcp.md` §7 framed committed
   history purely as a recovery benefit against an attacker. The inverse was
   never stated: once a page is auto-committed, a later `wiki_delete` removes
   only the working-tree copy, and content that turns out to be sensitive (a
   leaked secret, personal data) stays recoverable from history for as long
   as the repository exists — materially different from the pre-0024 default,
   where deleting an untracked page left no trace. Documented in
   `remote-mcp.md` §7 and `SPEC.md` §8.8 as a tradeoff of enabling the mode.

**Correction to this document's own rationale.** The paragraph above claiming
the commit logic must be duplicated because "`cmd` depends on `mcp`/`ingest`
per DESIGN.md's dependency graph, not the reverse" was a non-sequitur: sharing
requires a *third leaf* package, not an edge between those two.
`internal/config` and `internal/errors` are already exactly that — imported by
both `cmd/cogvault` and `internal/mcp` — and `cmd` already imports
`internal/mcp` directly. The duplication that rationale produced had already
drifted (`context.Background()` on one side, `cmd.Context()` on the other) and
would have needed all four fixes above applied twice. The mechanism now lives
in `internal/gitutil` (`Commit`, `CommitTimeout`, `TerminateGrace`), a leaf
package importing nothing from cogvault, and both call sites are thin wrappers
that only choose the pathspec and the log wording.

**Worst-case latency, now explicit.** One successful `Commit` is bounded by
3 × `CommitTimeout` (lock wait, then `add`, then `commit` — each independently
bounded), plus `TerminateGrace` per subprocess that must be force-killed.
SPEC.md previously described a single "10s subprocess timeout" in three
places, which understated the total; those now state the actual bound.

**Fourth correction pass** (2026-08-26, triggered by a real flake). Verifying
that the P0 commit built and tested standalone surfaced a failure in a test
the commit does not touch:
`TestGitAutoCommit_SlowAddDoesNotStarveCommitTimeout` failed with
`elapsed = 1.5005s` — exactly the shrunk 1500ms budget, meaning `git add` was
killed at its own deadline and `commit` never ran. Not reproducible in
isolation (10 runs, two revisions, with and without `-race`); it needed the
cold-build-cache contention of a fresh worktree running the full `-race` suite.

That test and its `cmd/cogvault` twin discriminated on elapsed wall-clock
time, and their own comments recorded three prior margin widenings plus an
explicit instruction not to widen a fourth time. The instruction was right:
the failure mode is a stage exceeding its *own* budget, which no margin can
rule out, because scheduler delay is unbounded.

Both tests are deleted, and the property they were reaching for is now tested
once, deterministically, in the package that owns it. `Commit` derives each
stage's deadline through a `withTimeout` seam and dispatches through a
`runGit` seam; `TestCommitGivesEachStageAnIndependentDeadline` substitutes
both, derives deadlines from a bookkeeping timestamp rather than the wall
clock, advances that timestamp by 9s between `add` and `commit`, and asserts
`commit` still receives a full 10s budget measured from its own start. To be
precise about the mechanism: nothing intercepts time. `context.WithDeadline`
still arms real timers; they are simply unreachable, because the substituted
runner returns immediately and no deadline is ever waited on. The elapsed
time is modelled arithmetically in the deadline origin, not simulated in a
clock. No sleeps, no subprocesses, no margins.

A first attempt — having the fake `git` write completion markers — was
abandoned before landing: markers prove a stage *finished*, not that a
shortfall was caused by a shared budget, and the design still depended on a
real sleep finishing inside a real timeout. It reproduced the same class of
flake with a smaller margin.

Verified: the shared-budget mutant fails with
`commit budget = 1s, want the full CommitTimeout 10s`; setting the simulated
add duration to zero makes that same mutant pass, confirming the advanced
timestamp is what does the discriminating rather than incidental ordering.
8 consecutive full `-race` runs of `gitutil`, `mcp`, and `cmd/cogvault` are
clean.

**Fifth correction pass** (2026-08-26, CodeRabbit review of PR #38): six
findings, all reproduced against the code before fixing.

1. **P1 — the commit lock was squattable, and squatting it silently disabled
   the safety net permanently.** `lockPath` returned a deterministic name
   directly under `os.TempDir()`. On a multi-user host any local user can
   pre-create that exact path with permissions cogvault cannot open;
   `os.OpenFile` then fails, `Commit` returns `StageLock` on every call, and
   because both call sites treat commit failure as best-effort and only log a
   warning, auto-commit stops happening with no visible error. Reproduced:
   pre-creating the path mode `0o400` produced
   `stage=lock ... permission denied` indefinitely.
   Fixed: the lock lives in an owner-only directory under
   `os.UserCacheDir()`, validated with `Lstat` (directory, owned by the
   current uid, no group/world bits) and never repaired.

   The first attempt at this fix was worse than the bug. It did `MkdirAll`
   followed by `Chmod(dir, 0o700)` — and `Chmod` follows symlinks, so an
   attacker-planted symlink at the lock directory would have had cogvault
   re-mode whatever it pointed at. Measured: an unrelated `0755` directory
   was silently narrowed to `0700`. Validation replaced repair for exactly
   this reason.

   The lock *file* open is hardened separately, because an owner-only
   directory excludes other users but not a compromised process running as
   the same user: `unix.Open` with `O_NOFOLLOW|O_CLOEXEC`, then `Fstat` on
   the resulting descriptor to confirm it is a regular file owned by the
   current euid with no group/world bits. `Fstat` rather than a path check
   because it inspects the descriptor itself, so nothing can be substituted
   between the check and the use — a path-based stat would be exactly that
   TOCTOU. It does not detect a swap that happened before the open won; the
   guarantee is about what the descriptor being flocked actually refers to.

   A symlink branch in the directory validation was written, then deleted:
   mutation testing showed removing it left every test passing, because with
   `Lstat` a symlink reports `IsDir() == false` and the directory check
   already rejects it. A guard no test can distinguish is not a guard.
2. **Orphaned comment.** `ingestGitCommitTimeout` was removed when the
   mechanism moved to `internal/gitutil`, but its doc comment survived,
   describing an identifier that no longer exists. Deleted.
3. **DESIGN.md misdescribed the dependency graph.** It claimed `internal/mcp`
   and `cmd/cogvault` depend on `gitutil` "without any edge between them",
   while the same document's graph shows `cmd/cogvault` importing
   `internal/mcp`. The leaf argument never needed that claim — sharing needs
   a common leaf, not the absence of an edge. Reworded.
4. **0024 contradicted itself on the ingest pathspec.** The Decision section
   still described bare `git add -A`, which resolves against an enclosing
   repository's root rather than `wiki_dir`; the correction to `-- .` was
   recorded 50 lines later. The earlier mention now carries the pathspec and
   points at the correction.
5. **DESIGN.md overstated the signal contract.** It read as though every
   stage exceeding its budget gets `SIGTERM` plus a grace period. The lock
   wait is an in-process retry loop with no process to signal; only the two
   git subprocesses are signalled. Scoped explicitly.
6. **Unchecked `f.Close()` in two error paths** where the same function
   already used `_ =` elsewhere. Made consistent.

Verified: `TestLockDirIsPrivateToTheUser`, `TestLockDirRejectsASymlink`,
`TestLockDirRejectsAPermissiveDirectory`, and
`TestOpenLockFileRejectsASymlink` each fail against a mutant that removes the
guard they cover. The lock-directory guard tests run against a `userCacheDir`
seam pointed at a temp dir — mutating the real per-user cache directory would
break the commit lock of any cogvault process running concurrently on the
same machine.

Not covered by a test: the directory's uid check. Neutering it leaves the
suite green, because a unit test cannot create a directory owned by a
different user. It is kept as the real defense on a multi-user host and
recorded here as an accepted coverage gap rather than removed to satisfy
mutation testing.

**Sixth correction pass** (2026-08-26, CodeRabbit re-review of the corrected
HEAD): one P1, plus two comments that had drifted from the code.

1. **P1 — the lock directory's validation was not bound to the lock file's
   open.** `O_NOFOLLOW` refuses a symlink only at the *final* path
   component. The fifth pass validated `.../cogvault/locks` by path with
   `Lstat`, then opened `.../cogvault/locks/<hash>.lock` by path — leaving
   the intermediate component unguarded. A process with the same euid could
   rename `locks` and leave a symlink behind between those two steps; the
   kernel follows an intermediate symlink regardless of `O_NOFOLLOW`, so the
   lock would be taken inside an unvalidated directory while another caller
   still locked the original. Commit serialization then silently breaks,
   which is precisely what the lock exists to prevent.

   This is the same validate-by-path/use-by-path mistake the fifth pass
   called out for the lock *file* and then reproduced one level up. Fixed by
   making the whole path fd-relative: `openLockDir` opens the directory with
   `O_DIRECTORY|O_NOFOLLOW|O_CLOEXEC` and validates *that descriptor* with
   `Fstat`; `openLockFileAt` then opens the lock with `Openat` against it.
   Validation and use now refer to one inode, and a rename or symlink swap
   afterwards cannot redirect the open.

   Verified: `TestLockSurvivesLockDirSwap` swaps the directory for a symlink
   after validation and asserts the lock still lands in the real directory.
   Against a mutant that reverts to the path-based open, it fails with the
   lock file created in the attacker's decoy instead.
2. **Two comments contradicted the code.** `Commit`'s doc described a
   "process-wide mutex" that does not exist — one flock covers both cases,
   because it excludes per open file description. `lockRepo`'s doc still
   placed the lock file in the OS temp directory, which the fifth pass had
   already changed. Both corrected.

One test was also found to pass for the wrong reason.
`TestLockDirRejectsASymlink` pointed its symlink at a `0755` directory, so
the permission check rejected it even with `O_NOFOLLOW` removed — confirmed
by mutation. The victim is now `0700`, leaving the refusal to follow the link
as the only thing that can reject it, and the mutant now fails.

**Seventh correction pass** (2026-08-26, CodeRabbit re-review of `a8c432f`):
the same defect one component higher, plus hardening of the trust anchor.

1. **P1 — the intermediate `cogvault` component was still resolved by
   path.** The sixth pass bound the lock *file* to a validated `locks`
   descriptor, but `<cache>/cogvault/locks` was still built as a joined
   string, and `O_NOFOLLOW` guards only the final component. A same-euid
   process could rename `cogvault`, leave a symlink pointing at a directory
   it controls, and place an owner-only `locks` inside it: every owner and
   mode check would pass while the lock silently moved to a different inode,
   so a second caller would lock elsewhere and serialization would break.
   Fixed by walking from the cache directory one component at a time with
   `Mkdirat`/`Openat`, validating each descriptor before opening the next.
2. **The anchor open did not refuse a symlink.** `os.UserCacheDir()` is the
   boundary of what this function can verify, but a symlink standing in its
   place would still have been traversed, contradicting the trust boundary
   the comment claimed. Now opened with `O_NOFOLLOW` and checked.
3. **The anchor accepted a group- or world-writable directory.** The first
   version of this pass skipped the anchor's permission check entirely,
   reasoning that the per-user cache is conventionally world-*readable*.
   That conflated readable with writable: a writable anchor lets another
   local user create or replace the `cogvault` entry inside it — exactly the
   squatting the descriptor walk defends against, reachable without winning
   any race. Write bits are now rejected; read bits are not.

   The asymmetry is deliberate and load-bearing in both directions.
   Requiring `0700` at the anchor would reject a normal install
   (`~/Library/Caches` is `0755` on macOS, verified on this host, the
   primary platform) and — since callers only log commit failures — silently
   disable the auto-commit safety net, which is the same class of bug this
   whole sequence exists to fix. Secrecy is enforced from `cogvault` down,
   which cogvault creates and owns.

Verified: `TestLockDirRejectsIntermediateComponentSwap` fails against a
mutant reverting to the joined path; `TestLockDirRejectsASymlinkedCacheDir`
fails against a mutant dropping `O_NOFOLLOW` from the anchor open; and
`TestLockDirCacheDirModePolicy` fails in both directions — against a mutant
removing the write-bit check (`0775`, `0777`, `0722` admitted) and against
one tightening it to `0700` (the macOS default rejected). The decoys are
constructed to be valid in every other respect, so only the intended guard
can distinguish them.

Not covered by a test: the anchor's owner check, for the same reason as the
directory uid check above — a unit test cannot produce a directory owned by
another user. Both are kept as the real multi-user defense and recorded here
rather than deleted to satisfy mutation testing.

**A pattern worth naming.** Three consecutive passes fixed the same mistake
at three levels: validate by path, then use by path. Each fix described the
error correctly while leaving the next component up unguarded. A path string
cannot carry a validation result — only a descriptor can — and the rule has
to be applied to the whole path, not to whichever component was under review.

**Eighth correction pass** (2026-08-26): a flake introduced by the seventh
pass, caught by running the gate without masking its exit status.

`TestCommitSerializesConcurrentCallers` began failing roughly one run in
five with `open commit lock ...: no such file or directory`. The gate command
had piped `go test` into `tail`, so the pipeline's exit status came from
`tail` and the failure was invisible; the commit went out on a red suite and
CI happened to pass, which a flake makes meaningless either way.

Root cause, isolated by probe rather than inspection: on APFS, concurrent
`openat(O_CREAT)` for the *same* name against one directory descriptor
intermittently returns `ENOENT` although the directory is live — `Fstat` on
the descriptor at the moment of failure reported a healthy directory with
`nlink=2`. The kernel-level mechanism was not established; what is measured
is that the error is transient and the same create succeeds on a retry. A
first hypothesis blamed `O_NOFOLLOW`; a controlled comparison disproved it —
64 goroutines racing one name produced 1–10 spurious `ENOENT`s per run both
with and without that flag.

This is precisely cogvault's shape: several MCP handlers committing to one
repository all resolve to one lock file. Unhandled, it surfaced as a caller
failing at `StageLock`, which callers only log — so on a busy wiki an
auto-commit would have been dropped silently. The same class of failure this
decision exists to prevent, reached by a different route.

Fixed with a bounded retry (`lockCreateAttempts = 32`, well above the worst
contention observed) on `ENOENT` only. Retrying is sound here because the
directory descriptor is already validated and cannot be redirected: a retry
re-attempts the same create in the same inode. Exhausting every attempt costs
~53ms of backoff, negligible against `CommitTimeout` and paid only on a path
that is already failing; a directory that really was removed reports `ENOENT`
through a held descriptor too (verified), so that error still surfaces.

Verified in two independent ways, because neither suffices alone. The stress
test reproduces the real race — 15/15 clean afterwards, against 4/12 before —
but proves nothing on a filesystem that never races. So an `openatFunc` seam
drives the loop deterministically: recovery after `lockCreateAttempts-1`
spurious errors, giving up after exactly `lockCreateAttempts`, and no retry
at all for a non-`ENOENT` error. Each fails against a matching mutant
(`attempts = 1`; retry-every-error; a loop running past its declared bound).

**Process note.** The masking was self-inflicted: `go test ... | tail` with
`&&`-chained `git commit` let a red suite reach the remote. Gate commands
need `set -o pipefail` or an unpiped status check — a filter that hides the
thing being gated on is worse than no gate.
