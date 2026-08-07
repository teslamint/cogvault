# Batch Report Sum Verification

Status: approved
Date: 2026-08-07

## Problem

The ingest Report has eight count fields (Digested, Failed, Refused, Skipped,
Deferred, Unchanged, Archived, SourceErrors) but no total and no invariant
assertion. A bug that loses a file silently — skipping it without incrementing
any counter — produces a report whose counts do not add up, and nobody notices.

Inspired by llm-wiki-for-scientists ch.6: "report denominator/numerator so the
sum equals the scanned count; never count unexamined as passed."

## Current state

- `Report` struct: 8 named int fields + `[]FileResult`.
- No `Scanned` field.
- `scan()` increments Skipped/Deferred directly and returns the surviving
  entries. The number of regular files encountered is never recorded.
- Main loop increments Digested/Failed/Refused/Skipped/Unchanged. Note:
  `Skipped` is shared between scan-phase skips (type, size, read) and
  main-loop skips (exhausted, refused-same-model).
- `sweepOrphans` increments Archived (wiki-page scope, not source-file scope).
- `SourceErrors` counts directory-level read failures (not per-file).
- SPEC §10.4 lists only six counts; Refused and SourceErrors are present in
  code but absent from the spec.
- `--limit` CLI flag caps digested files per run; entries beyond the limit
  are never examined and increment no counter.

## Design

### 1. Add `Scanned` and `NotExamined` fields to Report

```go
type Report struct {
    Scanned      int  // total regular files found across source dirs
    NotExamined  int  // entries the main loop never reached (--limit)
    // ... existing fields
}
```

Increment `Scanned` in `scan()` for every regular, non-symlink file entry
(line 218 check passes). This counts files before any type/size/settle/hash
filtering.

Increment `NotExamined` when the main loop breaks on `--limit`. The count
is `len(entries) - i` where `i` is the loop index at break.

### 2. Define the invariant

```
Scanned == Digested + Failed + Refused + Skipped + Deferred + Unchanged + NotExamined
```

- `Archived` is excluded (comes from wiki-page sweep, not source files).
- `SourceErrors` is excluded (directory-level, not file-level).

### 3. Add `Report.SumCheck() error`

A method that computes the sum and returns an error if the invariant fails.
Returns nil on success.

```go
func (r *Report) SumCheck() error {
    sum := r.Digested + r.Failed + r.Refused + r.Skipped + r.Deferred + r.Unchanged + r.NotExamined
    if sum != r.Scanned {
        return fmt.Errorf("report sum mismatch: scanned=%d sum=%d (digested=%d failed=%d refused=%d skipped=%d deferred=%d unchanged=%d not-examined=%d)",
            r.Scanned, sum, r.Digested, r.Failed, r.Refused, r.Skipped, r.Deferred, r.Unchanged, r.NotExamined)
    }
    return nil
}
```

### 4. Surface SumCheck result in the Report

`Runner` has no logger. Instead of logging the mismatch, store it in the
Report itself:

- Add `SumMismatch string` field to Report.
- At the end of the normal-return path in `Run()` (after the main loop,
  before `return report, nil`), call `SumCheck()`. If it returns an error,
  set `report.SumMismatch = err.Error()`.
- `String()` renders the mismatch on the summary line when non-empty.
- SumCheck is NOT called on error-return paths (ctx cancelled, ledger
  lookup error) — partial runs legitimately have unbalanced counts.

### 5. Include `scanned=` and `not-examined=` in String() output

Update the summary line:

```
scanned=N digested=N failed=N refused=N skipped=N deferred=N unchanged=N archived=N source-errors=N not-examined=N
```

Omit `not-examined=` when zero for readability.

When `SumMismatch` is non-empty, append `  !! <mismatch message>` after
the summary line.

### 6. Update SPEC §10.4

- Add `scanned`, `refused`, `source-errors`, `not-examined` to the count
  list (documenting existing behavior for refused/source-errors).
- State the sum invariant.
- Document that Lstat failures produce a `skipped` per-file entry with
  error `"stat: <err>"`.

### 7. Lstat error handling (ingest.go ~line 215)

Currently, `os.Lstat` failure silently continues without counting. This is
a file that exists in the directory listing but whose stat fails — it should
count as `Scanned` + `Skipped` with error `"stat: <err>"` and a PerFile
entry, so the invariant holds.

## Out of scope

- Separating scan-phase Skipped from main-loop Skipped into distinct fields.
- Changing Archived/SourceErrors semantics.

## Success criteria

- SC1: `Report.SumCheck()` returns nil for all existing tests.
- SC2: A new test with `--limit` verifies NotExamined is set correctly and
  SumCheck passes.
- SC3: `go test -race ./...` passes.
- SC4: SPEC §10.4 documents the invariant.
