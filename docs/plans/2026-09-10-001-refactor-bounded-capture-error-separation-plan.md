---
schema: plan/v1
title: Separate bounded capture errors from output consumption
type: refactor
status: draft
date: 2026-09-10
execution: code
origin: docs/specs/2026-09-09-bounded-capture-error-separation-design.md
---

# Bounded Capture Error Separation Plan

## Goal

Make capacity rejection explicit without stopping subprocess output consumption.
Protect failure ordering and prove bounded ingest retries with the real Claude Code adapter.
This draft is not finalized: the release-loop verification runner remains a planning-time dependency.

## Architecture notes

Keep the change inside the existing adapter file.
Introduce a private `boundedBuffer` with a concrete `bytes.Buffer` and a byte limit.
Its `Write([]byte) (int, error)` rejects a whole oversized input with `(0, errOutputLimit)`.
It does not retain an overflow state. A later smaller input can fit when used directly.

Retain `cappedWriter` as the subprocess-facing wrapper, with its own sticky overflow flag and a concrete bounded buffer.
It handles only `errOutputLimit`, reports discarded bytes as consumed, and propagates other returned errors.
Keep `Bytes() []byte`, `String() string`, and `Overflowed() bool` on the wrapper.
Construct each stdout/stderr wrapper explicitly with its existing limit.
Do not add a generic writer interface, exported error, configuration knob, or goroutine.

Keep `Digest` classification order unchanged: cancellation, captured refusal, stdout overflow, deadline, other process errors, result parsing.
Stored byte length remains bounded; allocation capacity is not promised to equal that length.
Use a subtraction-based remaining-capacity check to avoid overflow in length addition.
All production construction sites supply nonnegative limits; zero permits only empty input.

Known Pattern: `internal/llm/claudecode.go` already consumes excess output and checks overflow after `cmd.Run`.
Known Pattern: `TestRunPermanentErrorExhausts` provides ledger and report assertions, but currently supplies a mock failure.
Known Pattern: `docs/solutions/best-practices/measure-timing-margins-not-repeated-runs.md` favors explicit markers over narrow timing assertions.
The launchd marker solution is not a subprocess API contract; no launchd polling behavior is imported here.

## Assumption Recheck

All commands ran at `2026-09-09T21:30:51Z` against unchanged implementation files at `539ff59d84e8d97352a8cd73fb336cd0078c9be1`.

- Whole crossing chunk rejection: `sed -n '30,49p' internal/llm/claudecode.go` returned the existing flag-and-discard implementation. Outcome: match.
- Failure ordering: `sed -n '104,131p' internal/llm/claudecode.go` returned cancellation, refusal, overflow, then deadline checks. Outcome: match.
- Retry bound: `rg -n 'maxAttempts|class :=|ErrTransient|ErrRefused' internal/ingest/ingest.go` returned default permanence and a three-attempt bound. Outcome: match.

A later contradiction blocks finalization until a separate committed deviation addendum exists; preserve the approved spec.
Unavailable evidence remains a planning-time unknown. Neither condition can be treated as a match.

## File structure

- `internal/llm/claudecode.go`: private buffer and capture responsibilities.
- `internal/llm/claudecode_test.go`: boundary and subprocess regressions.
- `internal/llm/testdata/bin/claude`: deterministic high-volume output and marker modes.
- `internal/ingest/ingest_test.go`: real-adapter overflow and ledger verification.
- `DESIGN.md`: capture architecture contract.
- `ROADMAP.md`: evidence-backed closure of only the two F16 maintenance rows.

No new production or test file is required. Existing base-checkout operational edits remain untouched.

## Scenario coverage map

| Scenario | Ordered unit chain | Evidence |
|---|---|---|
| S1 explicit rejection | U1 -> U2 | Direct buffer assertions plus wrapper integration. Covers S1. |
| S2 output consumption | U1 -> U2 | Fake child writes beyond the cap and reaches its completion marker. Covers S2. |
| S3 classification and retries | U1 -> U2 -> U3 | Successful stderr-overflow digest and three real-adapter failures followed by an exhausted skip. Covers S3. |

## Implementation Units

## U1: Protect current subprocess behavior

Execution note: characterization-first
Files:
  Create: none
  Modify: internal/llm/testdata/bin/claude
  Test: internal/llm/claudecode_test.go
Interfaces:
  Consumes: NewClaudeCode(binPath, model string, opts ...Option) *ClaudeCode; (*ClaudeCode).Digest(context.Context, DigestRequest) (*DigestResult, error)
  Produces: deterministic fake CLI modes and observable completion evidence; no production interface
Test scenarios:
  happy: valid stdout succeeds despite stderr overflow.
  edge: zero-length input and exact-capacity output retain existing behavior.
  error: cancellation outranks overflow; captured refusal outranks overflow; overflow outranks deadline.
  integration: large stdout and stderr streams complete without broken-pipe truncation. Covers S2. Successful stderr overflow preserves S3.
Steps:
  1. Extend the existing shell fixture with fixed-size streamed output, optional marker files, and invocation counting. Never place megabytes in environment variables.
  2. Require every fixture write to succeed before emitting its completion marker. Use shell builtins for bounded output loops where practical.
  3. Add tests with finite context deadlines and exact marker assertions. Write enough output to exceed capture and normal pipe capacity.
  4. Synchronize cancellation tests on a marker after confirmed overflow; do not sleep and assume overflow occurred. Retain existing deadline cleanup.
  5. Run `go test ./internal/llm -count=1`. Existing behavior must pass before the refactor. Commit: `test(llm): Protect overflow pipe consumption`.
Acceptance: focused tests pass against unchanged production code; completion markers prove the child reached the end of output.

## U2: Separate capacity rejection from capture policy

Execution note: test-first
Files:
  Create: none
  Modify: internal/llm/claudecode.go, DESIGN.md
  Test: internal/llm/claudecode_test.go
Interfaces:
  Consumes: bytes.Buffer.Write([]byte) (int, error); errors.Is(error, error) bool
  Produces: private boundedBuffer.Write([]byte) (int, error), errOutputLimit, and the retained cappedWriter accessors
Test scenarios:
  happy: fitting and exact-capacity writes preserve all bytes.
  edge: zero limit, empty input, 10 remaining bytes with 20 incoming bytes, and a smaller write after rejection.
  error: oversized input returns zero and the sentinel without changing stored content.
  integration: wrapper handles that error, latches overflow, and discards every later write. Covers S1 and S2.
Steps:
  1. Add direct buffer and wrapper contract tests in `internal/llm/claudecode_test.go`. Assert returned counts, errors, and exact stored contents.
  2. Run the new tests and record failure from the missing buffer contract before production edits.
  3. Add the concrete bounded buffer and private sentinel. Move sticky overflow ownership to the wrapper; preserve existing accessor semantics.
  4. Update both capture construction sites. Leave process lifecycle and classification branches unchanged. Update `DESIGN.md` to describe both responsibilities.
  5. Run `go test ./internal/llm -count=1`. Confirm U1 protections remain green. Commit: `ref(llm): Separate capacity rejection from capture policy`.
Acceptance: buffer-contract tests fail before implementation and pass afterward; stored length never exceeds the limit.

## U3: Prove real overflow exhausts ingest attempts

Execution note: characterization-first
Files:
  Create: none
  Modify: ROADMAP.md
  Test: internal/ingest/ingest_test.go
Interfaces:
  Consumes: llm.NewClaudeCode(binPath, model string, opts ...llm.Option) *llm.ClaudeCode; (*Runner).Run(context.Context, RunOptions) (*Report, error)
  Produces: real-adapter ledger regression and F16 completion evidence; no production interface
Test scenarios:
  happy: each completed fake invocation yields one recorded stdout-overflow failure.
  edge: unchanged content and digest profile produce attempts 1, 2, and 3; run four preserves attempts 3.
  error: unexpected transport error, missing marker, ledger lookup failure, or wrong failure class fails the test rather than counting as overflow.
  integration: the real adapter emits an overflow error through Runner; the fourth run reports exhausted without another child invocation. Covers S3.
Steps:
  1. Reuse the existing ingest harness and replace its runner adapter with a real ClaudeCode adapter targeting the existing fake CLI.
  2. Stream more than the default 4 MiB stdout cap from the fake process. Use temporary source, wiki, ledger, and invocation-count paths.
  3. Run Runner four times. Assert failure messages name stdout overflow, attempts advance to three, no wiki page is written, and run four skips.
  4. Run `go test ./internal/ingest -count=1`. Retain a characterization result without claiming a new ingest bug was fixed.
  5. Update only the F16 roadmap scope and closure evidence. Run `gofmt -l .`, `go build ./...`, `go vet ./...`, and `go test -race ./... -count=1`. Commit: `test(ingest): Pin bounded retries for real CLI overflow`.
Acceptance: the fourth run leaves the child count at three and reports exhausted; full validation passes.

## Mutation/failure-state matrix

No stateful ceremony in the deliverable; no mutation/failure-state matrix required.

The product change adds no persistent-state transition. U3 exercises the existing ledger in disposable test directories.
No unit pushes, merges, installs, or accesses production data. Release-loop publication and archive contracts remain unresolved below.

## Carry-forward trigger audit

Audited ROADMAP.md at 539ff59d84e8d97352a8cd73fb336cd0078c9be1: 7 open rows, 2 fired, 0 unobservable.

| Row | Class | Trigger or disposition |
|---|---|---|
| F16 ingest regression coverage | edit-based, fired | Planned ingest regression work fires it; fold into U3. |
| F16 capture boundary review | edit-based, fired | Capture implementation changes fire it; fold into U1 and U2, close in U3. |
| Retrieval quality evaluation | event-based | No retrieval-quality evaluation event in this scope; defer. |
| Inbox consume-and-archive | event-based | No measured inbox-friction event in this scope; defer. |
| Consolidate temporary access-check evidence | event-based | Separate operational work; preserve base-checkout edits and defer reconciliation. |
| Local review fallback | event-based | Shared workflow owner is compound-loop; defer, no remote status claim. |
| Required probes before dependent implementation | event-based | Shared workflow owner is compound-loop; defer, no remote status claim. |

The completed decision-policy row is excluded. Historical F16 rows are the same two tracked candidates, not additional work.

## Deferred to Follow-Up Work

- Retrieval and inbox candidates: independent product work without approved scope here.
- Access-check records: separate dirty base-checkout work, excluded from this feature.
- Compound-loop issues 35 and 36: shared workflow ownership; not implemented in cogvault.
- Full-capacity prefix retention: deliberately excluded; whole crossing-chunk rejection remains accepted.
- Provider prompts, schemas, and extraction behavior: unchanged; deterministic fake-process tests suffice, no new LLM evaluation dataset.

## Open unknowns

### Planning-time

- The installed release-loop requires a V1 runner with `adapter_eligibility` and a supported V2 archive contract.
  Package-wide skill search found the eligibility requirement only in release-loop/SKILL.md, without an invocation contract or implementation.
  The integrity CLI provides receipt validation and artifact custody, not that runner's eligibility result.
  Owner: release-loop orchestrator with user direction on a compatible packaged workflow or an explicitly narrowed local workflow.
  Do not finalize, approve, or commit this plan until the required execution path is verified. Do not invent acceptance receipts.
  Resolution (2026-11-04): the user directed automatic completion of the roadmap. This plan runs as an explicitly narrowed local workflow:
  units U1–U3 in order on branch `refactor/f16-capture-separation` from main `069b889`, the repository gate after each unit, then the `pr-review-loop` skill.
  No release-loop runner receipt is claimed. The approved spec and the assumption recheck above are unchanged: `internal/llm/claudecode.go` is identical at `069b889`.

### Implementation-time

- Exact deterministic fake-mode names and marker mechanics, resolved in U1 without new dependencies or exposed APIs.
- Final test names, resolved in their owning unit and recorded in completion evidence.
