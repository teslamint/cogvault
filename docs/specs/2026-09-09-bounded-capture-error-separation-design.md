---
title: Bounded capture error separation
status: approved
date: 2026-09-09
schema: spec/v1
---

# Bounded Capture Error Separation Design

## Overview

Separate capacity rejection from subprocess output consumption in the Claude Code adapter.
The bounded buffer reports a capacity error. The capture writer handles that error without stopping pipe consumption.
This maintenance change preserves user-visible behavior. It does not recover the unused tail of a rejected chunk.

## User Scenarios

### S1: Reject an oversized write explicitly

A maintainer submits 20 bytes to a buffer with 10 bytes remaining.
The buffer stores none of that input and returns zero bytes with a recognizable capacity error.

### S2: Complete output consumption after overflow

During `cogvault ingest`, the CLI continues to emit output after the capture limit.
The capture writer consumes and discards later output until EOF or existing process cleanup ends collection.
Overflow alone does not close the pipe or terminate the child.

### S3: Preserve diagnostics and bounded retries

An oversized stdout result remains a permanent failure during `cogvault ingest`.
Three permanent failures exhaust unchanged content under the same digest profile; the next scheduled run skips it.
Captured policy-refusal evidence still outranks overflow. Stderr overflow alone does not fail an otherwise successful digest.

## Scope

### In

- Private buffer and capture responsibilities within `internal/llm`.
- Boundary, subprocess, and ingest-ledger regression tests in existing test files where practical.
- Canonical architecture documentation and evidence-backed closure of the two F16 maintenance candidates.

### Out

- Changes to output limits, public APIs, configuration, error classes, or retry policy.
- Early child termination, partial crossing-chunk retention, new dependencies, or a generic writer framework.
- Real provider calls, production ingest, signing, installation, or permission checks.
- Unrelated operational documents already modified in the base checkout.

## Assumptions and Preconditions

Evidence was inspected at the starting commit `33ee955eb140ca1ccb3c5c8b8295c9f676c851d6`.

| Claim | Command | Observed at | Observed result | Evidence source |
|---|---|---|---|---|
| Capture currently drops the whole crossing chunk. | `sed -n '30,49p' internal/llm/claudecode.go` | 2026-09-09T11:36:17Z | Overflow sets a flag and reports consumed input without storage. | Starting commit, `internal/llm/claudecode.go` |
| Cancellation and refusal precede overflow; overflow precedes deadline failure. | `sed -n '104,131p' internal/llm/claudecode.go` | 2026-09-09T11:36:17Z | The check order matches the retained contract. | Starting commit, `internal/llm/claudecode.go` |
| Permanent attempts are bounded at three. | `rg -n 'maxAttempts\|class :=\|ErrTransient\|ErrRefused' internal/ingest/ingest.go` | 2026-09-09T11:36:17Z | Default permanent classification and three-attempt bound exist. | Starting commit, `internal/ingest/ingest.go` |

`SPEC.md` and decision 0021 retain authority over retry semantics. `DESIGN.md` owns the capture architecture.

## Architecture

Keep both responsibilities private to the Claude Code adapter:

- The bounded buffer owns stored bytes and capacity checks. It returns a private sentinel error for an oversized input.
- The capture writer owns the sticky overflow state. It handles only the capacity error and continues to report discarded input as consumed.
- `Digest` inspects captured bytes and overflow state after `cmd.Run`, using the existing failure order.

Stdout and stderr retain independent buffers and overflow state. No shared mutable state or new goroutine is required.
The capture writer reports unrelated write errors unchanged; it must not hide arbitrary failures.
Use the existing concrete storage implementation. Do not introduce an injection interface solely to manufacture unreachable errors.

## Boundary Contract

1. An input that fits writes all bytes. Exact-capacity input succeeds.
2. An oversized input changes no stored bytes and returns `(0, errOutputLimit)` from the bounded buffer.
3. An empty write returns `(0, nil)`, including at capacity.
4. Buffer rejection does not itself latch discard mode; the capture writer owns that policy.
5. After its first capacity error, the capture writer discards all later inputs, including smaller inputs that would otherwise fit.
6. Default limits remain stdout 4 MiB and stderr 1 MiB. Stored byte length never exceeds its limit.

The byte-length bound is not a claim that allocator capacity or total process memory equals the configured limit.

## Testing

First add regression coverage for existing externally observable behavior.
Then add failing buffer-contract tests before implementing the split.
Check exact return counts, stored contents, empty inputs, and repeated writes after rejection.

Use a deterministic fake CLI with the real Claude Code adapter, not a real provider.
Prove the child reaches a completion marker after writing beyond capture and pipe capacity.
Test stdout and stderr overflow independently, plus cancellation, deadline, and captured-refusal precedence.
Do not equate absence of a deadlock in a small-output test with proof of pipe consumption.

Run the real adapter through an ingest Runner with temporary sources, wiki, and ledger.
Assert three recorded permanent attempts, exhaustion, and no fourth child invocation for unchanged content and profile.
Do not stub the adapter error to manufacture permanence.

## Risks

- Returning a capacity error to `os/exec` could stop pipe consumption. Keep capacity handling inside the capture writer.
- Partial capture could change refusal evidence. Preserve whole-chunk rejection and the existing captured-prefix limitation.
- Tests can hang when consumption breaks. Give fake-process tests bounded cleanup and verify completion explicitly.
- Timing assertions can be flaky. Prefer process markers and invocation counts over narrow duration thresholds.

## Success Criteria

1. Buffer and capture contracts satisfy S1 and S2.
   - Measured by: `go test ./internal/llm -count=1`; boundary and child-completion assertions pass.
2. Failure ordering and stderr nonfatal behavior satisfy S3.
   - Measured by: `go test ./internal/llm -count=1`; overflow, cancellation, refusal, deadline, and successful-stderr-overflow cases pass.
3. Actual adapter overflow consumes bounded ingest attempts.
   - Measured by: `go test ./internal/ingest -count=1`; ledger and child-invocation assertions prove exhaustion after three attempts.
4. The repository retains its validation baseline.
   - Measured by: `gofmt -l .` prints nothing; `go build ./...`, `go vet ./...`, and `go test -race ./... -count=1` exit zero.
5. Maintenance documentation states the accepted limitation accurately.
   - Measured by: review confirms `DESIGN.md` describes both responsibilities and ROADMAP cites completion evidence without claiming full-capacity retention.

## Open Decisions

None. Planning chooses private type names and deterministic fixture mechanics within these contracts.
