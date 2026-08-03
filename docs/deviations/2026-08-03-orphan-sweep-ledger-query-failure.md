# Deviation: orphan sweep ledger query failure

Date: 2026-08-03
Area: orphan sweep run-level failure handling

## Original contract

The approved design is `docs/specs/2026-08-03-orphan-sweep-archive-design.md`.
The approved plan is `docs/plans/2026-08-03-001-feat-orphan-sweep-archive-plan.md`.

The plan tells `sweepOrphans` to log a `successRows()` error and return.
`Runner.Run` then continues with the main file scan.
The report does not expose the skipped sweep.

The canonical v2 failure contract classifies errors during individual files.
It does not authorize a silent run-level ledger failure before the file loop.

## Discovered contradiction

Review round 2 found that a failed `successRows()` query appears successful.
The failure can come from SQLite contention, I/O failure, corruption, or closure.
Automation cannot distinguish this partial run from a completed orphan sweep.
Stale orphan pages can remain searchable while the command reports success.

The behavior follows the approved plan.
It conflicts with the canonical run-level failure boundary.

## Necessity

Logging alone cannot make the failure observable to a caller.
A report-only warning would also require a new run-level report contract.

The smallest correction returns the ledger query error from `sweepOrphans`.
`Runner.Run` already returns and wraps sweep errors.

## Observable behavior

If `ledger.successRows()` fails, `Runner.Run` returns a wrapped error.
The run stops before the sweep or main file loop mutates state.
The returned partial report contains no completed file actions.

A later run retries the query and continues normally after the ledger recovers.
Interactive and scheduled origins use the same error path.

## Safety and consent boundaries

The correction does not write to `sources[]`.
It does not change storage or ledger schemas.
It does not add an outward publication step.
It does not add a new approval prompt.

The ingest lock remains held until the failed run returns.
The failure path performs no wiki, index, or ledger mutation.

## Verification changes

The remediation adds tests that prove:

- a closed disposable ledger connection makes `Runner.Run` return an error;
- the error preserves the `ingest.ledger.successRows` cause;
- no digest, wiki write, index write, or ledger mutation occurs;
- reopening the same disposable ledger lets the next run continue;
- scheduled origin returns the same error without a prompt; and
- cancellation before the query still returns the existing cancellation error.

Run `go test -race ./internal/ingest` first.
Then run `go test -race ./...` and `go vet ./...`.

## Traceability

- Approved spec: `docs/specs/2026-08-03-orphan-sweep-archive-design.md`.
- Conflicting approved plan: `docs/plans/2026-08-03-001-feat-orphan-sweep-archive-plan.md`.
- Prior deviation: `docs/deviations/2026-08-03-orphan-sweep-review-remediation.md`.
- Review record: `.release-loop/reviews/round2-findings.md`.
- Affected code: `internal/ingest/ingest.go` and `internal/ingest/ingest_test.go`.
- Canonical docs: `SPEC.md` and `DESIGN.md`.
