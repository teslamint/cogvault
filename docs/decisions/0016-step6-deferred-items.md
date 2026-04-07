# 0016-step6-deferred-items

Status: deferred
Date: 2026-04-07

## Context

Step 6 implementation and review surfaced items that are known, understood, and intentionally not addressed yet.

## Decision

The following items are deferred:

1. **Schema content as `const` in `cmd/cogvault/init.go`**. Step 7 replaces this with `//go:embed schema/default_schema.md`. When migrating, the embedded file should live in an `internal/` package (not `cmd/`) so that future entry points (test harness, HTTP handler) can reuse it without importing from `cmd/`.

2. **`serve` per-file error warning has no CLI-level test**. The `serve` command's per-file error → warning path mirrors `init` (tested in `TestInitPerFileErrorContinues`), but `serve` itself blocks on `ServeStdio`, making it difficult to test the warning output in a CLI test. A unit test of the error classification logic (shared helper extraction) would close this gap. Candidate for Step 8 integration tests.

3. **`--json` output flag for `search`**. The plan deferred JSON output to v0.2. When added, it should be a `--json` flag on the search command that outputs `[]index.Result` as JSON.

4. **`bootstrap()` cleanup on future extension**. Currently `bootstrap()` creates `SQLiteIndex` as its last fallible operation, so no cleanup is needed on error. If future steps add more fallible operations after index creation, the function must `idx.Close()` on those error paths. A comment documents this obligation.
