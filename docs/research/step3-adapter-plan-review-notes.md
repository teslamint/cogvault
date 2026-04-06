# Step 3 Adapter Plan Review Notes

Date: 2026-04-06
Scope: Step 3 adapter implementation plan review for `internal/adapter`, including expected interactions with Step 4 (`index`) and Step 5 (`mcp`)

## Summary

Step 3 went through multiple review passes focused on adapter contract shape, path safety boundaries, markdown link semantics, and downstream consistency with indexing and MCP tools.

The main outcome was not a new product decision, but a set of implementation guardrails that were easy to miss from `SPEC.md` and `DESIGN.md` alone.

## Context That Should Not Be Lost

- `Source.Path` must be treated as a normalized identifier, not as an echo of user input. The working rule is `filepath.Clean(relPath)` so Step 4 and Step 5 can use a single canonical path string.
- `relPath` is untrusted input and must be defended inside the adapter. Path traversal, empty path, `"."`, and symlink traversal are adapter concerns even if callers are well-behaved.
- `root` is treated as trusted input for Step 3, but that trust is only acceptable because a later step is expected to create a single root-construction path. This is a staged trust boundary, not an inherent property of the interface.
- `exclude_read` is intentionally not pushed into the adapter API. The adapter owns path safety; callers own policy. This keeps the adapter config-free, but it creates a hard requirement that all callers pass `cfg.AllExcluded()` to `Scan` and apply `exclude_read` checks before direct `Parse` calls.
- Markdown fallback should only emit internal-reference candidates into `Links`. External URLs, protocol-relative URLs, heading-only anchors, absolute paths, and Windows drive paths should be excluded.
- Markdown fallback stores raw relative hrefs except for dropping the `#section` suffix. Path resolution is deferred to future resolve/lint work rather than guessed in Step 3.
- Code-block wikilink false positives remain explicitly accepted in MVP. This is a deliberate simplification, not an oversight.

## Review Themes That Recurred

- Contract drift risk between `SPEC.md`, `DESIGN.md`, and implementation details.
- Identity stability for path strings shared across adapter, index, and MCP responses.
- The danger of relying on caller discipline for security-sensitive behavior.
- The need to test markdown link classification explicitly instead of assuming regex intent is obvious.

## Practical Follow-up Checkpoints

- Step 4 should add tests that lock the `cfg.AllExcluded()` calling contract for consistency checks.
- Step 5 should enforce trusted root creation through a single initialization path rather than scattered call sites.
- If adapter path helper duplication grows beyond the current small scope, promote it into a shared internal utility instead of allowing quiet divergence.
- If markdown link handling becomes more than a fallback convenience, document its exact semantics in a decision record rather than leaving it as review lore.

## Notes

- These notes capture review-only context that influenced the implementation plan but does not yet belong in a decision record.
- If any of the staged assumptions above become long-term architecture, promote them into `docs/decisions/` rather than expanding this file indefinitely.
