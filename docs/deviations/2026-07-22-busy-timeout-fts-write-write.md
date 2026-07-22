# Deviation: busy_timeout does not serialize FTS5 write×write

Date: 2026-07-22
Area: multi-process concurrency (SPEC §6.4, §10.5; DESIGN §2.5)

## Original contract

SPEC §11 "Integration (U9)" stated:

> Concurrent serve+ingest → no "database is locked" (busy_timeout effective).

The U9 plan integration row
(`docs/plans/2026-07-22-001-feat-v2-capture-pipeline-plan.md`) stated:

> integration: concurrent smoke — `serve` process alive (stdio idle) while
> `ingest` runs; ingest completes without "database is locked" (busy_timeout
> effective)

Both imply a single `busy_timeout` guarantee covering *every* cross-process DB
contention, including a concurrent serve (reader/writer) against ingest (writer)
on the same DB file.

## Observed behavior

`index.Add` opens a DEFERRED transaction (`s.db.Begin()`) and runs
DELETE-then-INSERT against `wiki_fts`. FTS5's DELETE must read its shadow tables
to locate the row's tokens, so the DEFERRED tx acquires a **read** snapshot before
its first write. When another connection commits between that read and the write
upgrade, SQLite returns `SQLITE_BUSY_SNAPSHOT`. By design SQLite does **not** invoke
the busy handler for `BUSY_SNAPSHOT` (the snapshot is already stale — waiting cannot
help; the transaction must ROLLBACK and restart). So `busy_timeout` never applies to
this one path, and two concurrent FTS5 writers can collide with a hard error rather
than serializing.

## Why the original is unachievable as written

`busy_timeout` only serializes contention where a connection waits for a lock it
can still acquire (a write-first transaction, or a reader vs. a writer under WAL).
The FTS5 read→write upgrade is structurally a snapshot conflict, not a lock wait;
no `busy_timeout` value retries it. The blanket "busy_timeout effective for
concurrent serve+ingest" claim therefore cannot hold for the FTS5 write×write case,
and an e2e test asserting it would fail rather than pass.

## Resolution

- SPEC §6.4, §10.5, §11 and DESIGN §2.5 were corrected in this commit to carve out
  the FTS5 read→write tx-upgrade path and state its `SQLITE_BUSY_SNAPSHOT` behavior
  explicitly.
- Substituted tests (what actually exists, in `cmd/cogvault/ingest_integration_test.go`):
  - `TestE2EConcurrentIndexWriteDuringRead` — a writer (`index.Add`) and a
    concurrent reader (`Search`) on the production DSN complete with no "database
    is locked" (WAL read-during-write + busy_timeout).
  - `TestE2EBusyTimeoutSerializesWriters` — two write-first transactions on a plain
    (ledger-style) table serialize under busy_timeout instead of erroring. This is
    the ledger path, not the FTS path.
  - The FTS5 **write×write** case is intentionally **not** covered by an e2e test:
    it is the documented limitation this addendum records.
- Failure impact is bounded: on the ingest side the `BUSY_SNAPSHOT` surfaces from
  `idx.Add` and is classified **infra** (`classInfra`), so the file's attempt is
  spared and it self-heals on the next run. It never exhausts retries or corrupts
  state. The single-instance ingest `flock` already prevents ingest×ingest overlap;
  the residual window is ingest vs. a live serve that also writes the index.

## Traceability

- Spec: `SPEC.md` §6.4, §10.5, §11 (corrected this commit).
- Plan: `docs/plans/2026-07-22-001-feat-v2-capture-pipeline-plan.md`, unit U9.
- Commits: `69e7f88` (U9 integration tests), `3215e59` (U9 review round 1).
- This addendum: committed alongside the spec/DESIGN correction.
