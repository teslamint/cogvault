# Step 9 MCP Unblock Plan

Date: 2026-04-08
Status: approved

## Goal

Unblock Step 9 by fixing MCP tool-call behavior without changing the current public contract.

## Scope

- Keep `wiki_scan`, `wiki_list`, `wiki_search` return shapes as arrays.
- Preserve `wiki_parse(path, include_content)` request/response shape.
- Add regression tests at the MCP boundary.
- Rebuild and re-verify against the real vault after changes.

## Plan

1. Reproduce the array-result serialization failure at the stdio/MCP boundary.
2. Fix array-returning tools so they keep their JSON-array contract without sending invalid structured content.
3. Add MCP-boundary regression tests for `wiki_scan`, `wiki_list`, and `wiki_search`.
4. Measure large-note `wiki_parse` behavior across adapter output, server response, and observed client payload.
5. If large-note truncation is server-side, fix it without changing the API.
6. If large-note truncation is client-side, record it as a Step 9 client limitation and keep the server contract unchanged.
7. Run targeted tests, then broader regression tests.
8. Rebuild the binary and re-run real-vault checks before retrying Day 1 ingest.

## Step 9 Resume Criteria

- `wiki_scan`, `wiki_list`, and `wiki_search` work in the real MCP client without serialization errors.
- `wiki_parse(include_content=true)` returns usable content for Day 1 note sizes.
- Automated tests lock the regression.
- Re-running Day 1 does not reproduce the original high-friction failure.
