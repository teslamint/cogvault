# 0006-storage-write-serialization

Status: accepted
Date: 2026-04-06

## Context

`FSStorage.Write` must prevent write races, but the MVP does not yet have measured ingest throughput requirements or concurrent multi-writer workloads.
The design review considered both a single global mutex and path-keyed locking.

## Decision

The MVP uses a single global write mutex in `FSStorage`.
All writes are serialized, even when they target different paths.

## Why

- It guarantees same-path write serialization with minimal implementation complexity.
- The current MCP stdio model is effectively single-connection and low-concurrency.
- It avoids introducing lock bookkeeping or lifecycle bugs before there is evidence that write concurrency is a bottleneck.

## Alternatives Considered

- Path-keyed locking
  Deferred. More precise, but adds bookkeeping and cleanup complexity too early.
- No storage-level write lock
  Rejected because same-path concurrent writes would be racy and violate the intended contract.

## Revisit Triggers

- Ingest throughput becomes a measurable bottleneck.
- CLI or engine workflows introduce meaningful concurrent write traffic.
- Multiple reviewers identify the single mutex as a real scalability limit rather than a theoretical one.

## Related Files

- `CLAUDE.md`
- `DESIGN.md`
- `internal/storage/fs.go`
- `internal/storage/fs_test.go`
