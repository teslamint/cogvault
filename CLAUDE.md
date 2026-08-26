# CLAUDE.md — cogvault

This is a shared agent briefing, not product canon. Contract, architecture, and durable rules live in `SPEC.md`, `DESIGN.md`, and accepted decisions under `docs/decisions/` per `docs/decisions/0003-canonical-context-locations.md` and `docs/decisions/0012-agent-documentation-governance.md`.

## Working Context

Read these non-obvious invariants before editing:

1. v2 trust boundary: `wiki_dir` is the only read-write storage/MCP root; read-only `sources[]` are read directly by ingest and are never MCP-addressable.
   Owner: `docs/decisions/0021-v2-refounding.md` D1, `SPEC.md`, `DESIGN.md`
2. `internal/schema/default_schema.md` is a runtime contract asset, not a casual note.
   Owner: `DESIGN.md`, `internal/schema/default_schema.md`
3. Ingest failures are classified as transient, permanent, or infra; only permanent failures consume bounded attempts.
   Owner: `docs/decisions/0021-v2-refounding.md` D4, `SPEC.md`, `DESIGN.md`
4. SQLite `busy_timeout` belongs in the DSN and does not solve `SQLITE_BUSY_SNAPSHOT`.
   Owner: `DESIGN.md`, `docs/solutions/database-issues/sqlite-pool-pragma-and-busy-snapshot.md`
5. Ingest has a cross-process single-writer lock so scheduled and manual runs never overlap.
   Owner: `docs/decisions/0021-v2-refounding.md` D2/D4, `DESIGN.md`
6. `wiki_delete` attempts to auto-commit its own deletion to git, but only
   actually commits when the deleted file was already git-tracked —
   `git add` on a path git never tracked fails (exit 128, no matching
   pathspec), so a page `wiki_write` never committed (the default,
   `git.auto_commit: off`) produces no delete-commit either. `wiki_write`
   additionally auto-commits when `git.auto_commit` is `write` or
   `write+ingest`; `cogvault ingest` additionally auto-commits only under
   `write+ingest` specifically — `write` alone covers `wiki_write` but not
   ingest runs (default `off`, neither; 0024). Every auto-commit path runs
   through `internal/gitutil`: one advisory lock per repository (git refuses
   concurrent index operations, so unsynchronized commits are silently
   dropped), independent 10s bounds on lock/add/commit, and SIGTERM-with-grace
   so a timed-out `git` clears its own `.git/index.lock`. Paths containing a
   `.git`, `.gitattributes`, or `.gitmodules` component are rejected by every
   mutating storage method, outside the configurable exclude lists — `git add`
   executes filters those files define, so writing them is remote code
   execution, not a visibility question. Even fully enabled, git inside the
   wiki is not a backup: it narrows, but does not eliminate, the "no recovery
   from a compromised credential" gap, and committed content survives a later
   `wiki_delete` in history.
   Owner: `SPEC.md` §3.1/§8.3/§8.8/§8.8.1/§9.4, `docs/decisions/0024-wiki-git-safety-net.md`, `docs/deployment/remote-mcp.md` (Security posture)
7. `SPEC.md`, `DESIGN.md`, and accepted decisions override plans; `docs/plans/` are non-canonical working notes and may become stale.
   Owner: `docs/decisions/0012-agent-documentation-governance.md`

## Documentation Map

- `SPEC.md`: public behavior and contract canon.
- `DESIGN.md`: architecture, package boundaries, and component boundaries.
- `CONCEPTS.md`: shared terminology and vocabulary reference.
- `docs/decisions/`: durable project decision records.
- `docs/research/`: investigation notes and review artifacts before promotion to decisions.
- `docs/solutions/`: resolved problem writeups and reusable learnings.
- `docs/deviations/`: committed post-approval behavior addenda that authorize separately approved remediation plans.
- `docs/plans/`: non-canonical working notes; useful for execution context, but stale-prone and never higher priority than canon.
- `docs/specs/2026-07-22-refound-capture-pipeline-design.md`: approved v2 refounding design that complements, not overrides, `SPEC.md`/`DESIGN.md`.
- `docs/specs/2026-08-11-remote-mcp-server-design.md`: approved remote MCP server design (transport, `internal/httpauth` authorization boundary) that complements, not overrides, `SPEC.md`/`DESIGN.md`.
- `docs/deployment/remote-mcp.md`: operator-facing deployment guide for the `sse`/`http` transports — tunnel setup, `--public-url`, identity-provider prerequisites, startup guards, and the security posture; referenced by Working Context invariant 6.
- `docs/context/project-background.md`: neutral background, archaeology, rejected alternatives, and historical v1 context.
- `docs/decisions/0022-repository-working-conventions.md`: neutral owner for repository-wide completion and verification conventions; `docs/decisions/0023-stale-agent-convention-reconciliation.md` resolves two stale agent-only rules.

## Current-State Orientation

- The repository is in v2 single-mode operation: capture sources feed digest, digest writes wiki pages, and consume happens through MCP plus CLI search.
- Obsidian-vault hybrid assumptions are historical background only unless current canon explicitly says otherwise.
- When implementation changes repository behavior or durable operating assumptions, update the affected canonical docs before calling the work complete.

## Claude Delta

No Claude-specific delta currently exists. If a future Claude-only workflow or constraint appears, record only that delta here and keep project-wide rules in neutral canon.
