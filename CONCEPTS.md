# Concepts

Canonical vocabulary for cogvault. One term per concept; extend as learnings land.

## Ingest ledger

The SQLite table (`ingest_ledger`) recording every digestion outcome, keyed by (source path, content hash). A source file is "processed" when a `success` row exists for its current hash. *Avoid: "processed-files table", "history table".*

## Error classes (ingest)

Three-way classification of per-file ingest failures: **transient** (quota/rate limit, timeout, CLI transport — retried indefinitely, no attempt consumed), **permanent** (malformed LLM output, schema-invalid page — consumes one of 3 attempts), **infra** (write/index/ledger failures — recorded, no attempt consumed). Only permanent failures can exhaust a file.

## Single-writer lock

The exclusive `flock` on `<db_dir>/ingest.lock` that makes ingest runs single-instance across processes; the first defense against SQLite write-write conflicts. *Avoid: "mutex file".*

## DSN pragma

A SQLite pragma passed in the connection string (`?_pragma=busy_timeout(5000)`) so **every** connection in a `database/sql` pool inherits it — as opposed to `db.Exec("PRAGMA ...")`, which configures exactly one pooled connection.

## SQLITE_BUSY_SNAPSHOT

The immediate (non-waiting) failure of a DEFERRED transaction that read under a snapshot made stale by a concurrent writer before upgrading to write. Not curable by `busy_timeout`; handled in cogvault by the single-writer lock plus infra error classification.

## Settle window

The 2-minute mtime quiet period a source file must satisfy before ingest will hash it — guards against digesting mid-download/mid-sync partial files.
