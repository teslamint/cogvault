# Deviation: attention rows use a fixed-width UTC sort key

Date: 2026-08-25
Area: ingest attention latest-row selection (`internal/ingest`)

## Original contract

The approved design and sealed plan select the latest ledger row with
`MAX(digested_at)`. They accept RFC3339Nano lexical misordering because normal
writes for one source path usually occur far apart. `MAX(rowid)` only resolves
equal timestamp ties.

## Discovered contradiction

CodeRabbit review on PR #33 demonstrated a valid counterexample. RFC3339Nano
removes trailing fractional zeros, so `...00.1Z` sorts after `...00.11Z` as
text even though it is earlier in time. An older exhausted row can therefore
hide a newer success row. `cogvault status` then reports a resolved file as
attention-worthy.

## Why documentation alone cannot fix it

The SQL query executes text `MAX` ordering. Existing tests use fixed-width,
second-precision timestamps and cannot detect the counterexample. Documentation
changes would leave the wrong row selection active.

## New observable behavior

The attention query converts each UTC `Z` timestamp to a fixed-width sort key
with exactly nine fractional digits. It selects the maximum sort key for each
`source_path`, then uses the largest `rowid` only for equal instants.

The ledger schema and stored timestamp text do not change. JSON continues to
return the original canonical UTC timestamp. An older exhausted or refused row
never hides a chronologically newer success or superseded row because their
fractional widths differ.

## Safety and consent boundaries

The query remains parameterized and read-only. The normalization assumes the
existing writer contract: `digested_at` is UTC RFC3339Nano ending in `Z`.
Malformed timestamps remain outside this remediation and continue to fail at
the status output boundary. No ledger migration or rewrite occurs.

The USER approved the fixed-width UTC sort-key remediation during PR #33 review
handling on 2026-08-25.

## Verification changes

- Seed an older exhausted row at `...00.1Z` and a newer success row at
  `...00.11Z`; the attention query must exclude the path.
- Reverse the terminal states to prove the chronologically newer exhausted row
  remains visible.
- Retain equal-instant `rowid` tie coverage.
- Run the targeted attention tests and the full race suite.

## Traceability

- Approved spec:
  `docs/specs/2026-08-24-ingest-attention-report-design.md`
- Approved sealed plan:
  `docs/plans/2026-08-25-001-feat-ingest-attention-report-plan.md`
- PR: `#33`
- Review: `5014483520`
- Review thread: `PRRT_kwDOR6utps6b63IO`
- Review comment: `3849154929`
- Code: `internal/ingest/ledger.go`
- Tests: `internal/ingest/ledger_test.go`
