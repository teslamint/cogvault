---
module: internal/index, internal/ingest
date: "2026-07-23"
problem_type: database_issue
component: sqlite_access_layer
severity: high
symptoms:
  - "concurrent writer intermittently fails with \"database is locked\" despite PRAGMA busy_timeout being set"
  - "SQLITE_BUSY_SNAPSHOT returned immediately (no wait) from an FTS5 DELETE-then-INSERT while another process writes"
root_cause: "per-connection pragma applied to one pooled connection; busy_timeout by design never retries snapshot-invalidation conflicts on deferred-tx read-to-write upgrades"
resolution_type: code_fix
related_components:
  - "modernc.org/sqlite"
  - "database/sql"
tags:
  - sqlite
  - busy-timeout
  - fts5
  - connection-pool
  - concurrency
---

# SQLite: pool-wide pragmas need the DSN, and busy_timeout cannot fix BUSY_SNAPSHOT

## Problem

Two distinct failures when multiple processes (a scheduled batch writer and a long-lived server) share one SQLite database through Go's `database/sql` with `modernc.org/sqlite`.

## Symptoms

- Writes fail with `database is locked` even though the code executes `PRAGMA busy_timeout=5000` right after `sql.Open`.
- With the timeout correctly applied everywhere, an FTS5 update path (`DELETE FROM fts WHERE path=?` then `INSERT`) still fails **immediately** with `SQLITE_BUSY_SNAPSHOT` when another process commits a write concurrently — no 5-second wait ever happens.

## What Didn't Work

- `db.Exec("PRAGMA busy_timeout=5000")` after `sql.Open`: `database/sql` is a connection **pool**; the pragma lands on whichever single connection served that Exec. Every other connection the pool opens later runs with `busy_timeout=0`.
- Raising the timeout for the BUSY_SNAPSHOT case: `busy_timeout` retries lock waits, but a snapshot-invalidation conflict is not a lock wait. A DEFERRED transaction that reads first (the FTS DELETE reads shadow tables) pins a read snapshot; when it upgrades to a write and the snapshot is stale because another writer committed, SQLite returns `SQLITE_BUSY_SNAPSHOT` immediately — by design, retrying cannot help that transaction.

## Solution

1. **Pragmas in the DSN, not Exec** — `modernc.org/sqlite` applies `_pragma` DSN parameters to every connection the pool opens:

```go
sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
```

Prove it with a test that holds N `db.Conn(ctx)` open simultaneously (forcing N distinct pooled connections) and asserts `PRAGMA busy_timeout` returns the value on each (cogvault: `TestBusyTimeoutAppliesToAllConnections`, commit 23f89d0).

2. **Design around BUSY_SNAPSHOT instead of configuring it away** — treat cross-process write-write on the FTS path as a known conflict class: serialize your own writers (cogvault: an exclusive `flock` makes ingest single-instance), classify the residual conflict as an infrastructure failure that spares the retry budget and self-heals on the next run, and document the limitation in the contract docs rather than claiming busy_timeout covers it (cogvault: `docs/deviations/2026-07-22-busy-timeout-fts-write-write.md`, `TestE2EBusyTimeoutSerializesWriters`).

## Why This Works

DSN pragmas execute during connection establishment, so pool growth cannot bypass them. BUSY_SNAPSHOT is a correctness signal (your read snapshot is stale), not contention noise — the only honest responses are avoiding concurrent writers (locking), starting the transaction as IMMEDIATE (trading concurrency for early lock acquisition), or accepting-and-retrying at the operation level on a fresh transaction.

## Prevention

- Never configure connection-scoped SQLite state via `db.Exec` on a pool; use the driver's DSN mechanism (or `SetMaxOpenConns(1)` when a single connection is genuinely wanted).
- When a transaction reads before it writes (FTS5 delete-then-insert is the classic), assume `SQLITE_BUSY_SNAPSHOT` is possible under WAL with concurrent writers, and decide its handling explicitly.
- Any doc claiming "busy_timeout makes concurrent writes safe" should be checked against the read-then-write upgrade path before it ships.
