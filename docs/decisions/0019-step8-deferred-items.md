# 0019-step8-deferred-items

Status: deferred
Date: 2026-04-07

## Context

Step 8 implementation and three rounds of review surfaced items that are known, understood, and intentionally not addressed.

## Decision

The following items are deferred:

1. **Bounded staleness integration test**. All integration tests use `ConsistencyInterval: 0` for determinism. This means bounded staleness (force=false skipping within interval) is never exercised at integration level. The contract is covered by `TestCheckConsistencySkipWithinInterval` in `index/sqlite_test.go`. If bounded staleness behavior becomes a regression target (e.g., after changing the CheckConsistency algorithm), a dedicated integration test with non-zero interval and time control should be added.

2. **MCP initialize response instructions verification**. The plan originally included `TestIntegration_SchemaInstructions` to verify schema content in MCP `initialize` response. This was removed because: (a) `TestSchemaInstructions` in `tools_test.go` already locks truncation and fallback behavior at unit level; (b) extracting instructions from the `initialize` response requires mcp-go SDK internals that may change between versions. If mcp-go exposes a stable API for inspecting server instructions, this test should be reconsidered.

3. **`serve` command full-flow integration test**. The `serve` per-file warning gap (0016 item 2) is resolved by the `handleConsistencyResult` helper extraction (0018 D1). However, the full `serve` flow (bootstrap → consistency → ServeStdio → cleanup) is not tested end-to-end. Testing this would require a stdin/stdout pipe harness. Candidate for v0.2 if SSE transport is added (which would make the server testable via HTTP).

4. **`basic/` and `security/` fixture usage in tests**. These fixtures exist per SPEC Appendix B but are not directly used by integration tests — `basic/` is a minimal smoke test fixture, and `security/` tests construct their scenarios at runtime using `t.TempDir()` + `os.WriteFile`. The fixtures serve as documentation and reference data. If a future step adds fixture-driven parametric tests, these directories are ready.

## Resolved from prior steps

- ~~0016 item 2: `serve` per-file error warning~~ — resolved by `handleConsistencyResult` extraction. See 0018 D1.
- ~~0009 item 1: adapter exclude contract in CheckConsistency~~ — resolved by `TestCheckConsistencyExcludeContract` (sqlite_test.go, Step 4). Integration test `TestIntegration_Security_ExcludeNotInSearch` additionally validates the full pipeline.
