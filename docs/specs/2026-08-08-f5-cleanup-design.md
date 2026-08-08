---
title: "F5 Cleanup: MCP Schema Fallback Fix + Dead Fixture Removal"
status: draft
feature: f5-cleanup
follow_up: F5
---

# F5 Cleanup: MCP Schema Fallback Fix + Dead Fixture Removal

Resolves F5 from `docs/research/v2-follow-ups.md`.

## Problem

Two live issues remain from F5; one is stale:

1. **MCP schema fallback 404** — `defaultSchemaInstructions()` in
   `internal/mcp/server.go` tells agents `wiki_read("_schema.md")` when the file
   is absent on disk. The agent call 404s because there is no file to read. The
   embedded `schema.DefaultContent` exists but is not used in this path.

2. **Dead v1-shaped fixtures** — `testdata/fixtures/{basic,real}/.cogvault.yaml`
   contain v1 fields (`adapter: obsidian`, `consistency_interval: 0`, no
   `sources[]`). No test loads them via `config.Load()`; `setupRealVault` builds
   its own `config.Config` struct. The files are dead and misleading.

3. **`contentHash()` — stale** — F5 listed it as dead code. It is now used
   extensively in production (ledger, orphan sweep, archive). No action needed.

## Changes

| # | File | Change |
|---|------|--------|
| C1 | `internal/mcp/server.go` | `defaultSchemaInstructions()`: embed `schema.DefaultContent` (truncated to `maxSchemaLen` runes) instead of telling agents to read a missing file |
| C2 | `internal/mcp/tools_test.go` or `integration_test.go` | Add test: MCP server with no `_schema.md` on disk → instructions contain schema content, not a 404-prone reference |
| C3 | `testdata/fixtures/{basic,real}/.cogvault.yaml` | Delete both files |
| C4 | `docs/research/v2-follow-ups.md` | Mark F5 Done with PR reference |

## Success criteria

| # | Criterion | Verification |
|---|-----------|-------------|
| SC1 | MCP server without `_schema.md` on disk serves embedded schema as instructions | Unit test: `defaultSchemaInstructions` output contains "Wiki Schema" header |
| SC2 | No test references the deleted fixture YAML files | `grep -r '.cogvault.yaml' internal/ testdata/` returns zero hits |
| SC3 | All existing tests pass | `go test ./...` — 0 failures |

## Non-goals

- Changing the schema content itself.
- Adding `_schema.md` auto-creation to MCP startup (already handled by `cogvault init`).
