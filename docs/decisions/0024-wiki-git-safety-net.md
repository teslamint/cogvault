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
     successful ingest run (`git add -A` + one commit), so digested pages are
     captured too.
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
  and does not error, the timeout bounds a wedged commit, a slow-but-not-
  wedged add does not starve commit's own timeout budget
  (`TestGitAutoCommit_SlowAddDoesNotStarveCommitTimeout`) via a fake `git`
  binary at `internal/mcp/testdata/bin/git`); `cmd/cogvault/ingest_git_commit_test.go`
  (`write+ingest` commits after a digesting run, `off` and `write`-alone do
  not, `--dry-run` does not, a zero-digest run does not, a `wikiDir` nested
  inside a larger repo does not stage or commit files outside `wikiDir`
  (`TestIngestGitCommit_NestedWikiDirDoesNotStageOutsideFiles`, confirmed
  to fail against the pre-fix `-A`-without-pathspec code), a slow-but-not-
  wedged add does not starve commit's own budget
  (`TestIngestGitCommit_SlowAddDoesNotStarveCommitTimeout`, confirmed to
  fail against the pre-fix shared-context code), the timeout bounds a
  wedged commit reusing the same fake `git` binary).

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
