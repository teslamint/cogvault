# Retro: Batch Report Sum Verification

**Feature**: `batch-report-sum-verification`
**Branch**: `feat/batch-report-sum-verification`
**PR**: #15 (squash-merged 81350c8)
**Duration**: 2026-08-07 — 2026-08-08
**Review rounds**: 1 (3 findings, all fixed)

## What shipped

Sum invariant for ingest batch reports: `scanned == digested + failed + refused + skipped + deferred + unchanged + not-examined`. Three new Report fields (`Scanned`, `NotExamined`, `SumMismatch`), `SumCheck()` method, Lstat error counting, `--limit` remainder tracking, SPEC §10.4 update.

## What went well

1. **Advisor caught two design gaps early**: the `--limit` remainder gap (files beyond the limit were unaccounted) and the no-logger problem (Runner has no logger, so SumMismatch had to live in Report). Both were addressed in the design spec before implementation began.
2. **Single review round**: code review found 2 MEDIUM + 1 LOW findings; all three were fixable in one commit without architectural changes.
3. **Test coverage is comprehensive**: 7 unit tests for SumCheck, 5 integration tests, 6 existing tests gained SumMismatch assertions — 18 total touch points.

## What could improve

1. **Dead test branch**: `TestSumCheckLstatError` targeted an unreachable code path on macOS (os.Lstat succeeds on mode-0o000 files). This was caught only in review, not during implementation. Lesson: verify platform-specific filesystem behavior before writing tests that depend on it.
2. **SPEC §10.4 per-file action divergence**: the per-file action strings don't map 1:1 to summary count buckets (e.g., `exhausted` action increments `skipped` count). This pre-existing design asymmetry required a clarifying note in SPEC. Future work could align action names with count buckets.
3. **Skipped field overloading**: `Skipped` absorbs scan-phase type/size/stat errors plus main-loop exhausted/refused-same-model outcomes. This makes debugging harder when multiple skip reasons coexist. A future decomposition (separate `TypeExcluded`, `SizeExcluded` counts) would improve observability.

## Metrics

| Metric | Value |
|--------|-------|
| Commits | 5 |
| Files changed | 6 |
| Lines added | 402 |
| Lines removed | 12 |
| Review findings | 3 (2 MEDIUM, 1 LOW) |
| Fix rounds | 1 |
| Tests added | ~18 assertions |

## Decisions recorded

- Archived and SourceErrors are excluded from the file-level sum invariant (different scope).
- Lstat errors count as `Scanned + Skipped` with `"stat: <err>"` per-file action.
- SumMismatch is stored as a string in Report (no logger available in Runner).
