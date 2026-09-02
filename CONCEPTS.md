# Concepts

Canonical vocabulary for cogvault. One term per concept; extend as learnings land.

## Ingest ledger

The SQLite table (`ingest_ledger`) recording every digestion outcome, keyed by (source path, content hash). A source file is "processed" when a `success` row exists for its current hash. *Avoid: "processed-files table", "history table".*

## Error classes (ingest)

Four-way classification of per-file ingest failures: **transient** (quota/rate limit, timeout, CLI transport — retried indefinitely, no attempt consumed), **permanent** (malformed LLM output, schema-invalid page — consumes one of 3 attempts), **infra** (write/index/ledger failures — recorded, no attempt consumed), **refused** (provider policy/AUP refusal — terminal under the same model, re-attempted only when the configured LLM model changes, consumes no attempt). Only permanent failures can exhaust a file.

## Single-writer lock

The exclusive `flock` on `<db_dir>/ingest.lock` that makes ingest runs single-instance across processes; the first defense against SQLite write-write conflicts. *Avoid: "mutex file".*

## DSN pragma

A SQLite pragma passed in the connection string (`?_pragma=busy_timeout(5000)`) so **every** connection in a `database/sql` pool inherits it — as opposed to `db.Exec("PRAGMA ...")`, which configures exactly one pooled connection.

## SQLITE_BUSY_SNAPSHOT

The immediate (non-waiting) failure of a DEFERRED transaction that read under a snapshot made stale by a concurrent writer before upgrading to write. Not curable by `busy_timeout`; handled in cogvault by the single-writer lock plus infra error classification.

## Settle window

The 2-minute mtime quiet period a source file must satisfy before ingest will hash it — guards against digesting mid-download/mid-sync partial files.

## Deviation addendum

A committed record for observable behavior discovered after spec or plan approval. It preserves the approved artifact and authorizes a separately reviewed, user-approved remediation plan. *Avoid: "silent plan correction".*

## Review artifact

A reviewer-authored result that proves a review actually ran, such as a submitted review or review thread. A check or status context can pass while no review artifact exists, so a required external review is satisfied only by the artifact or an explicit waiver. *Avoid: treating a green status as proof of review.*

## Resource server

cogvault's role under OAuth 2.1: it **validates** access tokens and serves Protected Resource Metadata, but never issues, refreshes, or revokes them. Token issuance belongs to a user-supplied identity provider. *Avoid: calling cogvault an "auth server" or "OAuth server".*

## Protected Resource Metadata

The unauthenticated `/.well-known/oauth-protected-resource` document (RFC 9728) that tells a client which authorization server guards this resource. Clients reach it either by following the `resource_metadata` pointer in a `401` challenge or by probing the well-known path. Its advertised `resource` value and the expected token `aud` must be identical.

## Stream lifetime bound

The deadline that closes a long-lived MCP event stream at its token's `exp` (or at a fixed cap when the credential has no expiry). Necessary because a streaming connection is authorized once at establishment and never revalidated, so without the bound an expired token keeps receiving.

## Measured worst-case margin

A timing-test safety margin sized against a directly measured worst-case
overhead (e.g. subprocess fork/exec time under synthetic CPU-saturation
load), not against a count of passing runs. "5 back-to-back passes" proves
one load profile cooperated 5 times; it does not bound the margin against a
load profile that hasn't occurred yet. *Avoid: validating a wall-clock test
margin by repeated-run count alone.*
