# 0018-step8-integration-test-decisions

Status: accepted
Date: 2026-04-07

## Context

Step 8 adds integration tests and fixture data. During planning and three rounds of review (internal plan review, Codex review, re-review), several decisions were made that are not captured in SPEC.md, DESIGN.md, or prior decision records.

## Decision

### D1: handleConsistencyResult shared helper extraction

`init.go` (lines 69-74) and `serve.go` (lines 34-39) contained identical error classification logic for CheckConsistency results:

```go
if errors.Is(ccErr, index.ErrConsistencySystemic) { return ccErr }
cmd.PrintErrln("warning: ...", ccErr)
```

This was extracted to `cmd/cogvault/consistency.go` as `handleConsistencyResult(cmd, err) error`. Both callers now delegate to this function.

This resolves 0016-step6-deferred-items item 2: "serve per-file error warning has no CLI-level test." The shared helper is directly testable without blocking on `server.ServeStdio`, and `consistency_test.go` covers nil, systemic, and per-file cases.

### D2: Integration test placement — existing packages, not separate test/ directory

Integration tests live in `internal/mcp/integration_test.go` (package `mcp_test`) and `cmd/cogvault/integration_test.go` (package `main`).

A separate `test/integration/` package was considered but rejected: the existing test helpers (`setupIntegration`, `callTool`, `extractText`, `executeCommand`) are unexported and package-scoped. Moving to a separate package would require either exporting them (polluting production API surface) or duplicating them.

Placing MCP-level tests in `mcp_test` and CLI-level tests in `cmd/cogvault` follows the existing pattern and keeps helpers accessible without duplication.

### D3: Duplicate elimination — Step 8 tests cover only uncovered contracts

Three reviews identified that early plan versions duplicated contracts already locked by existing tests (e.g., `TestRoundTrip` covers write→search, list title/type, parse include_content). The final plan eliminated all duplicates.

Criterion for inclusion: a test is added only if it exercises a scenario that no existing test covers. The coverage matrix in the plan maps every SPEC Appendix A item to either an existing test or a planned test.

Tests removed during review:
- `WriteThenSearch` → covered by `TestRoundTrip` steps 1+3
- `WriteListVerifyMeta` → covered by `TestRoundTrip` step 4
- `WriteThenSearchScope` → covered by `TestSearchScopeFiltering` (cli_test.go)
- `MultipleWritesThenSearch` → marginal value over `TestRoundTrip`
- `SchemaInstructions` / `SchemaInstructionsTruncated` → covered by `TestSchemaInstructions` (tools_test.go)
- CLI `InitSearchRoundTrip` → covered by `TestInitIndexesExistingFiles`
- CLI `SearchAllScopes` → covered by `TestSearchScopeFiltering`

### D4: ConsistencyInterval: 0 in all integration tests

All integration tests use `ConsistencyInterval: 0` to ensure CheckConsistency runs on every tool call. This eliminates timing-dependent flakiness.

Bounded staleness behavior (force=false skipping within interval) is covered at the unit level by `TestCheckConsistencySkipWithinInterval` in `index/sqlite_test.go`. No integration test re-verifies this contract.

### D5: callToolMayError removed — callTool already sufficient

The initial plan introduced `callToolMayError` as a helper that "doesn't t.Fatal on IsError." Review revealed this was based on a misunderstanding: `callTool` already returns `*CallToolResult` without inspecting `result.IsError`. Both functions fataled only on RPC-level errors (malformed response, nil result), not on tool-level IsError. Since the two functions were identical, `callToolMayError` was removed. All integration tests use `callTool` directly.

### D6: real/ fixture is synthetic, not actual user data

DESIGN.md section 8 specifies "testdata/fixtures/real". The fixture contains synthetic data that mimics a realistic Obsidian vault structure: Korean and English notes, multiple page types (source, entity, concept), wikilinks between pages, dataview fields, binary file (diagram.png), and cross-language linking.

No actual user vault data is included. The fixture is checked into version control.

### D7: Schema content in fixture files — full copy, not abbreviated

Fixture `_schema.md` files contain the full `schema.DefaultContent` text (byte-for-byte match with `internal/schema/default_schema.md`). This aligns with 0017-step7-schema-decisions D3 (byte-for-byte equivalence) and ensures fixture-based tests exercise the same schema content that `init` would produce.

### D8: Unit-only contracts explicitly documented

Two SPEC Appendix A contracts are tested at unit level only, with explicit rationale:

- **`exclude_read Exists=false`** — No MCP tool calls `Storage.Exists` directly. The contract is locked by `TestExcludeAndExcludeReadSemantics` in `fs_test.go`.
- **`GetMeta ErrNotFound`** — `GetMeta` is an internal Index API not exposed as an MCP tool. The success path is exercised through `wiki_list` metadata enrichment. The error path is locked by `TestGetMetaNotFound` in `sqlite_test.go`.

## Why

### Why extract a helper instead of testing serve directly

`serve` blocks on `server.ServeStdio(mcpSrv)`, which reads from stdin until EOF. Testing the full serve flow would require a stdin/stdout pipe harness that adds complexity disproportionate to the value. The error classification is the only untested behavior; extracting it into a pure function makes it directly testable.

### Why not a separate test package

Go's `internal` package boundary means unexported helpers cannot be accessed from outside the package. The existing helpers (`setupIntegration`, `callTool`, `executeCommand`) are designed as package-internal test utilities. Creating a `test/integration/` package would break this design or force duplication.

### Why eliminate duplicates aggressively

Each test has maintenance cost: it must compile, pass, and remain meaningful as the codebase evolves. A test that only re-verifies a contract already locked elsewhere adds cost without coverage. Step 8's value is in the uncovered gaps: eventual consistency edge cases, MCP-boundary security, fixture-driven end-to-end, and race detection.

### Why synthetic fixtures instead of real vault data

Real vault data contains personal information, is not reproducible across environments, and may change over time. Synthetic data is deterministic, version-controllable, and designed to exercise specific contracts (Korean content, cross-language links, multiple page types, binary files).
