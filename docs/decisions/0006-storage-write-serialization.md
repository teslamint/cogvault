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
- It avoids introducing lock bookkeeping or lifecycle bugs before there is evidence that write concurrency is a bottleneck.

Originally this decision also rested on the MCP stdio model being effectively
single-connection and low-concurrency. That premise no longer holds: the `sse`
and `http` transports added in 2026-08 serve concurrent requests, so concurrent
`wiki_write` and `wiki_delete` calls are reachable in normal operation rather
than theoretical. The decision itself is unchanged and still correct — a single
global mutex serializes every path, which is strictly more conservative than
the same-path serialization the contract requires, so there is no race. What
changed is that the mutex now serializes traffic that can genuinely arrive in
parallel, which is a throughput question rather than a correctness one.

## Alternatives Considered

- Path-keyed locking
  Deferred. More precise, but adds bookkeeping and cleanup complexity too early.
- No storage-level write lock
  Rejected because same-path concurrent writes would be racy and violate the intended contract.

## Revisit Triggers

- Ingest throughput becomes a measurable bottleneck.
- CLI or engine workflows introduce meaningful concurrent write traffic.
  **Partially fired (2026-08)**: the remote `sse`/`http` transports moved
  concurrent writes from theoretical to reachable. The next step is
  measurement under real remote usage, not a rewrite — path-keyed locking
  remains the premature complexity this decision rejected until there is
  evidence the single mutex is an actual bottleneck.
- Multiple reviewers identify the single mutex as a real scalability limit rather than a theoretical one.

## Related Files

- `CLAUDE.md`
- `DESIGN.md`
- `internal/storage/fs.go`
- `internal/storage/fs_test.go`
