# Plan: Batch Report Sum Verification

Spec: docs/specs/2026-08-07-batch-report-sum-verification-design.md (approved)

## Units

### U1: Report struct + SumCheck + String (report.go)

- Add `Scanned`, `NotExamined`, `SumMismatch` fields to Report
- Add `SumCheck() error` method
- Update `String()`: prepend `scanned=N`, append `not-examined=N` when >0, append `!! <mismatch>` when SumMismatch non-empty

### U2: Scan + main loop counting (ingest.go)

- `scan()`: increment `report.Scanned++` for every regular non-symlink file (after the IsDir/symlink check, before any filtering)
- `scan()`: handle Lstat error — increment `Scanned++` and `Skipped++` with `"stat: " + err.Error()`
- Main loop: on `--limit` break, set `report.NotExamined = len(entries) - i`
- End of `Run()` normal path: call `SumCheck()`, set `report.SumMismatch` if error

### U3: Tests (ingest_test.go)

- Verify SumCheck passes on existing test patterns (add assertions to TestRunDigestsNewPDF and TestRunLimitOne)
- New test: TestSumCheckWithLimit — 3 files, limit=1 → Scanned=3, Digested=1, NotExamined=2, SumCheck nil
- New test: TestSumCheckMismatch — directly construct a Report with wrong counts → SumCheck returns error

### U4: SPEC §10.4 update

- Add scanned, refused, source-errors, not-examined to count list
- State the sum invariant
- Document Lstat error → skipped with stat error
