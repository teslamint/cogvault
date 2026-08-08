# Retro: F5 Cleanup

PR: #17 | Merged: 2026-08-08 | Branch: feat/f5-cleanup

## Success criteria results

| # | Criterion | Result |
|---|-----------|--------|
| SC1 | MCP fallback serves embedded schema, no 404-prone wiki_read reference | PASS — `defaultSchemaInstructions` returns `schema.DefaultContent` with self-reference replaced; test asserts no `wiki_read("_schema.md")` in output. |
| SC2 | No test references deleted fixture YAML files | PASS — `grep -r '.cogvault.yaml' internal/ testdata/` returns zero hits. |
| SC3 | All existing tests pass | PASS — `go test ./... -count=1` 12 packages, 0 failures. |

## What went well

- F5 scope was smaller than originally described. Orientation discovered
  `contentHash()` is no longer dead (actively used by ledger and orphan sweep),
  reducing the work to two concrete items.
- Code review caught the embedded content's self-referential `wiki_read` pointer
  — a subtle issue where the fix would have still emitted the 404 instruction
  within the content body. Fixed with a render-time string replacement.
- Guard test (`TestDefaultContentFitsMaxSchemaLen`) prevents silent regression
  if the schema grows past the truncation threshold.

## What could improve

- F5 was written during v2 Phase 1 retro (July 2023). The `contentHash()` item
  was stale by the time work began. Follow-up items should be re-triaged
  before starting a release loop.
- The design spec's "non-goal" of not changing `default_schema.md` content
  was correct, but the review rightly flagged that the fix needed to account
  for content inside the embedded asset, not just the wrapper function.

## Lessons

- Follow-up items can become stale as subsequent features (orphan sweep, archive)
  reuse previously dead code. Always verify the current state of each sub-item
  during orientation.
- When embedding content as instructions, the content itself may contain
  self-referential instructions that break in the new context. Render-time
  rewriting handles this without modifying the source asset.
