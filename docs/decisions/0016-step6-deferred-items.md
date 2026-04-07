# 0016-step6-deferred-items

Status: deferred
Date: 2026-04-07

## Context

Step 6 implementation and review surfaced items that are known, understood, and intentionally not addressed yet.

## Decision

The following items are deferred:

1. ~~**Schema content as `const` in `cmd/cogvault/init.go`**~~. Resolved in Step 7: migrated to `//go:embed` in `internal/schema/` package.

2. ~~**`serve` per-file error warning has no CLI-level test**~~. Resolved in Step 8: extracted `handleConsistencyResult()` shared helper in `cmd/cogvault/consistency.go`. Both `init` and `serve` now call this function. Tested in `consistency_test.go`. See `docs/decisions/0018-step8-integration-test-decisions.md` D1.

3. **`--json` output flag for `search`**. The plan deferred JSON output to v0.2. When added, it should be a `--json` flag on the search command that outputs `[]index.Result` as JSON.

4. **`bootstrap()` cleanup on future extension**. Currently `bootstrap()` creates `SQLiteIndex` as its last fallible operation, so no cleanup is needed on error. If future steps add more fallible operations after index creation, the function must `idx.Close()` on those error paths. A comment documents this obligation.
