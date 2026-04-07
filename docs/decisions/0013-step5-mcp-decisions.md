# 0013-step5-mcp-decisions

Status: accepted
Date: 2026-04-07

## Context

Step 5 introduced the MCP server layer in `internal/mcp/` and connected it to the existing storage, adapter, and index components.

During implementation and review, two behaviors became durable project decisions rather than temporary implementation details:

1. how `wiki_scan(dir)` narrows results without breaking the adapter exclude contract,
2. how MCP handlers distinguish systemic `CheckConsistency` failures from per-file reconciliation errors.

These choices were not fully captured in the existing canon before the implementation landed.

## Decision

### D1: `wiki_scan(dir)` scans from the vault root and filters results afterward

When `wiki_scan` receives a non-empty `dir`, the MCP layer still calls `adapter.Scan(root, cfg.AllExcluded(), fn)` with the vault root as the scan root.

The `dir` parameter is applied as an MCP-layer result filter on the returned vault-relative paths.

Before scanning, `wiki_scan` validates that `dir`:

- is not absolute,
- does not contain `..`,
- exists under the vault root,
- refers to a directory, not a file.

If the directory does not exist or points to a file, `wiki_scan` returns `ErrNotFound` at the tool boundary.

### D2: Systemic `CheckConsistency` failures are classified with an index sentinel

The index layer now exposes `index.ErrConsistencySystemic`.

`CheckConsistency` wraps systemic failures with this sentinel, including:

- loading current index state,
- scan-level failures,
- transaction begin/apply/commit failures.

Per-file read/parse errors remain non-systemic and continue to be returned as joined ordinary errors after a successful transaction commit.

MCP handlers use `errors.Is(err, index.ErrConsistencySystemic)` to decide:

- systemic failure: return a tool error,
- per-file failure: log a warning and continue with the current index state.

## Why

### Why vault-root scan plus post-filter

The adapter exclude contract operates on vault-root-relative paths.

If `wiki_scan(dir)` were to change the adapter scan root to `filepath.Join(root, dir)`, exclude entries such as `notes/private` would be re-based incorrectly and could stop matching during the scan. Scanning from the vault root preserves one stable coordinate system for both exclude matching and returned paths.

The extra cost of scanning the full vault before filtering is accepted for MVP. Correctness and policy consistency are more important than subdirectory-scan efficiency at this stage.

### Why a systemic-failure sentinel

Step 4 established that `CheckConsistency` has asymmetric failure semantics:

- systemic failures mean the reconciliation result is not trustworthy,
- per-file failures can still leave a coherent committed index state.

The MCP layer needs to react differently to those two cases. String matching on formatted error messages would make that policy fragile. A sentinel keeps the classification explicit and stable across wrapping changes.

## Alternatives Considered

### A1: Re-root adapter scanning to `filepath.Join(root, dir)`

Rejected because it breaks exclude matching semantics by changing the path basis seen by `adapter.IsExcluded`.

### A2: Accept empty results for nonexistent `dir`

Rejected because `SPEC.md` defines `wiki_scan(dir)` to return `ErrNotFound` for missing directories and file-path inputs.

### A3: Detect systemic failures by matching error strings

Rejected because formatted message prefixes are not a durable classification contract.

### A4: Treat all `CheckConsistency` errors as tool errors

Rejected because Step 4 explicitly allows per-file reconciliation errors to coexist with a valid committed index state.

## Revisit Triggers

- The adapter interface gains first-class subdirectory scanning with preserved vault-relative exclude semantics.
- `wiki_scan(dir)` becomes a measurable performance bottleneck on real vaults.
- Reconciliation ownership moves out of the index layer into a higher engine/service layer.
- The project adopts structured error types that supersede the sentinel approach.

## Related Files

- `SPEC.md`
- `DESIGN.md`
- `docs/decisions/0010-step4-index-decisions.md`
- `docs/decisions/0011-step4-review-convergence.md`
- `internal/index/index.go`
- `internal/index/sqlite.go`
- `internal/mcp/tools.go`
- `internal/mcp/tools_test.go`
- `internal/mcp/server_test.go`
