---
schema: plan/v1
title: "Expose orphan sweep ledger query failure"
type: fix
status: draft
date: 2026-08-03
execution: code
---

## Goal

Make an orphan-sweep ledger query failure observable to every caller.
Stop the run before later mutations and preserve retry behavior.
Clear the final P2 finding from review round 2.

## Architecture notes

**Decision: a failed success-row query aborts the ingest run.**
`sweepOrphans` returns the wrapped `ledger.successRows()` error.
`Runner.Run` already wraps and returns sweep errors.
No new error type or report field is needed.

The failure occurs before the sweep selects a candidate.
It also occurs before the main file loop starts.
The partial report contains no completed file actions.
The failure preserves the underlying database cause.

**Decision: recovery uses the existing next-run retry.**
The implementation does not add an in-run database retry.
After database availability recovers, the next invocation opens a new ledger
connection to the same database and continues.
Interactive and scheduled origins use the same path.

Authoritative deviation:
`docs/deviations/2026-08-03-orphan-sweep-ledger-query-failure.md`.

## Assumption Recheck

No origin spec; no approved live assumptions to recheck.

The conflicting approved plan is preserved unchanged.
Deviation commit `c89b19d` authorizes the run-level failure correction.

## File structure

| File | Action | Responsibility |
|---|---|---|
| `internal/ingest/ingest.go` | Modify | Return a failed success-row query to `Runner.Run` |
| `internal/ingest/ingest_test.go` | Modify | Prove failure visibility, no mutation, recovery, and scheduled parity |
| `.release-loop/evidence/U10/` | Create locally | Record T4 failure, recovery, headless, and cancellation evidence |

## Scenario coverage map

No origin spec; no User Scenarios section exists for this review correction.
U10 proves failure isolation, next-run recovery, and scheduled parity.

## Review traceability

| Finding | Authority | Test and assertions | Unit |
|---|---|---|---|
| Round 2 P2: silent `successRows()` failure | deviation Observable behavior and Verification changes | `TestRunReturnsSweepLedgerQueryFailure` proves returned cause, empty report, zero LLM calls, unchanged wiki/index/ledger | U10 |

## Implementation Units

### U10: Propagate the sweep ledger query failure

Execution note: test-first
Files:
  Modify: `internal/ingest/ingest.go`
  Test: `internal/ingest/ingest_test.go`
  Create locally: `.release-loop/evidence/U10/T4-*.md`
Interfaces:
  Consumes: `(*ledger).successRows() ([]ledgerRow, error)` and `sweepOrphans(ctx context.Context, report *Report, dryRun bool) error`
  Produces: a wrapped run-level error from `Runner.Run`
Test scenarios:
  happy: a new ledger connection to the same disposable database lets the next run converge
  edge: the recovery run reacquires the ingest lock and proves the failed run released it
  error: a closed disposable ledger makes `Runner.Run` return the database cause before any LLM, wiki, index, or ledger mutation
  integration: scheduled origin returns the same non-interactive error; cancellation before the query keeps the existing context error
Steps:
  1. Write failing tests `TestRunReturnsSweepLedgerQueryFailure`, `TestRunRetriesAfterSweepLedgerQueryFailure`, and `TestRunScheduledReturnsSweepLedgerQueryFailure`.
  2. Run the targeted tests and confirm each failed query currently returns a successful run.
  3. Return the wrapped `successRows()` error from `sweepOrphans`.
  4. Reopen the same disposable database with `openLedger`, replace the runner ledger, and prove the next `Runner.Run` reacquires the ingest lock.
  5. Assert zero LLM calls, an unchanged wiki tree, unchanged index results, unchanged ledger rows, and no report actions before retry.
  6. Write the four T4 evidence records from fresh exact commands.
  7. Run `go test -race ./internal/ingest -count=1`.
  8. Run `go test -race ./...`, `go vet ./...`, and `git diff --check`.
  9. Commit: `fix(ingest): Surface orphan sweep ledger failures`.
Acceptance: all verification commands pass, the database cause is inspectable, and the failure leaves LLM, wiki, index, ledger, and report state unchanged.

## Mutation/failure-state matrix

Evidence records use disposable `t.TempDir` fixtures.
No record may reference a configured user source, wiki, or database.

| Transition | Pre-state | Action | Success | Forced failure | Rerun | Rollback or compensation | Headless | Cancellation or abort | Unit / evidence owner |
|---|---|---|---|---|---|---|---|---|---|
| T4 read sweep ledger rows | ingest lock held; disposable ledger connection open; no file action completed | query `success` rows before the sweep and main file loop | the recovery record proves a successful query and resumed file processing | close the disposable ledger; return the database cause with zero LLM calls and unchanged wiki, index, ledger, and report state | open a new ledger connection to the same database; reacquire the ingest lock; run again | the failure is non-mutating; the recovery rerun is the compensation | scheduled origin returns the same error without a prompt | not applicable after T4 begins because `successRows` has no context interface; an already-canceled context aborts before T4 through the existing tested guard | U10; `.release-loop/evidence/U10/T4-*` |

### Evidence production

Each record names this plan, matrix row, source commit, timestamp, fixture root, configured targets, stub identity, and boundary sentinel.
Each record includes the exact command, exit status, pre-state, post-state, next-run result, and mechanism check.

| Record | Proving test command | Required observations |
|---|---|---|
| `U10/T4-forced-failure.md` | `go test -race ./internal/ingest -run '^TestRunReturnsSweepLedgerQueryFailure$' -count=1 -v` | closed ledger returns the database cause; LLM, wiki, index, ledger, and report state remain unchanged |
| `U10/T4-recovery.md` | `go test -race ./internal/ingest -run '^TestRunRetriesAfterSweepLedgerQueryFailure$' -count=1 -v` | new connection and lock reacquisition let the next run converge; this proves success, rerun, and compensation |
| `U10/T4-headless.md` | `go test -race ./internal/ingest -run '^TestRunScheduledReturnsSweepLedgerQueryFailure$' -count=1 -v` | scheduled origin returns the same error without prompting or mutation |
| `U10/T4-cancellation.md` | `go test -race ./internal/ingest -run '^TestRunContextCanceledWithoutOrphanCandidates$' -count=1 -v` | the existing guard returns cancellation before T4 or any mutation |

## Carry-forward trigger audit

| Tracker row | Trigger class | What fired it | Disposition |
|---|---|---|---|
| F5 dead `contentHash()` cleanup | edit-based | this plan modifies `internal/ingest/ingest.go` | defer; deletion is unrelated to the run-level error contract |

F2 is unclassifiable and is not feature-relevant to this run-level correction.
The F5 fixture and MCP targets are edit-based and did not fire.

Unobservable drift-based row:

| Tracker row | Named record | Why unobservable |
|---|---|---|
| F3 FTS `BUSY_SNAPSHOT` | real scheduled-ingest and serve collision logs | this local plan does not inspect production logs |

Audited `docs/research/v2-follow-ups.md` at `c89b19d`: 3 open rows, 1 fired, 1 unobservable.

## Deferred to Follow-Up Work

- F5 dead code, fixtures, and MCP fallback remain in `docs/research/v2-follow-ups.md`.
- In-run SQLite retries remain out of scope; the next invocation is the retry boundary.

## Open unknowns

### Planning-time

No planning-time unknowns remain.

### Implementation-time

No implementation-time unknowns remain.
