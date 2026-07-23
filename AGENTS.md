# AGENTS.md — cogvault

This is a short pointer/delta file for Codex and Gemini. Read `CLAUDE.md` first for the shared working context; do not treat either agent file as product canon.

## Before Editing

1. Read `CLAUDE.md` `## Working Context` before touching code or docs.
2. Choose the correct contract owner first:
   - behavior or user-facing contract -> `SPEC.md`
   - architecture or package/runtime boundary -> `DESIGN.md`
   - durable rationale or repository-wide rule -> `docs/decisions/`
   - background or archaeology -> `docs/context/project-background.md`
3. If the change touches schema or generated page shape, inspect `internal/schema/default_schema.md` and the canon that consumes it before editing.
4. If the change touches ingest retries or failure handling, use `docs/decisions/0021-v2-refounding.md`, `SPEC.md`, and `DESIGN.md` as the owners for transient/permanent/infra semantics.
5. If the change touches concurrency, SQLite busy behavior, or overlapping runs, use `DESIGN.md`, `docs/solutions/database-issues/sqlite-pool-pragma-and-busy-snapshot.md`, and `docs/decisions/0021-v2-refounding.md`; keep `docs/decisions/0006-storage-write-serialization.md` for the separate storage mutex boundary.
6. Treat `docs/plans/` as non-canonical working notes that may become stale; they never override `SPEC.md`, `DESIGN.md`, or accepted decisions.

## Pointers

- Core canon: `SPEC.md`, `DESIGN.md`, `CONCEPTS.md`, `docs/decisions/`
- Shared agent briefing: `CLAUDE.md`
- Background and historical context: `docs/context/project-background.md`
- Research notes: `docs/research/`
- Repository-wide working conventions: `docs/decisions/0022-repository-working-conventions.md`
- Approved v2 design: `docs/specs/2026-07-22-refound-capture-pipeline-design.md`

## Agent Delta

No Codex/Gemini-specific delta currently exists. If a future agent-only operational difference appears, record only that difference here.
