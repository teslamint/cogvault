# 0024: Wiki Git Safety Net (Draft)

- Status: draft (구현 전; 승인 필요)
- Date: 2026-08-25
- Context owner: this decision; amends `docs/deployment/remote-mcp.md` "Security posture" and `SPEC.md` §8.7/§8.8 on acceptance.

## Problem

`wiki_write` overwrites unconditionally without committing; nothing commits on
ingest. Only `wiki_delete` auto-commits — and because nothing else ever
tracked the file, that commit records the removal of content that was never in
git. An attacker (or bug) holding a valid credential can silently overwrite or
delete the entire wiki with no restore path. The current canon assigns the
snapshot duty to the operator (`docs/deployment/remote-mcp.md`: "cogvault will
never do this step for you"), which leaves the default configuration with no
recovery.

## Decision (proposed)

Make the safety net available but **opt-in**, preserving the current default
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

Awaiting approval. On approval: implement, then promote this file from draft
to accepted per `docs/decisions/README.md`.
