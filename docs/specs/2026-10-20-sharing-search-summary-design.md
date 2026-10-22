---
status: approved
---
# Sharing conditions in CLI and MCP search

## User direction

The user accepted a result layout that shows applicability, where, level, conditions, evidence, and review status, provided both CLI and MCP search expose it.
Implement the same metadata projection for `cogvault search` and MCP `wiki_search`.
This extends the explicit terms inspection workflow; it does not add automatic legal classification.

## Smallest implementation

- Add optional `where` and `level` text to the existing `usage_terms` record. Reuse `scope`, `conditions`, `evidence.reference`, and `status`.
- Preserve/protect where and level like the other explicit declaration fields.
- Put review-state inspection and search summarization in `internal/terms` so CLI and MCP cannot diverge.
- Keep the existing search path, title, type/category, snippet, score, order, limits, and query behavior.
- Read each result through existing storage permissions. Do not fetch sources, follow evidence references, use LLMs, or change the index schema.
- CLI appends human-readable applicability/where/level/how/evidence/review lines beneath each result.
- MCP keeps the array result envelope and adds `usage_terms` to each result. Its fields are `scope`, `where`, `level`, `conditions`, `evidence.reference`, `status`, and `reasons`.
- In the summary, absent, blank, or non-text fields use the explicit marker `unverified`; CLI displays that marker as 미확인. This is unknown information, never unrestricted permission.
- Review status retains the existing inspection semantics. `recorded` means a complete human record, not shareability or legal clearance. Missing where/level are still displayed as unknown even on an older complete record.
- Malformed/unreadable result pages get an entirely unverified summary and a reason. Never use partial bytes from a failed storage read or abort unrelated search results.
- Detailed evidence quotes and reviewer/date fields remain available through `terms`/`wiki_parse`; search is a summary, not another policy store.

## Acceptance

1. Failing-before CLI and MCP tests show the same declared scope, where, level, conditions, evidence reference, and review status without a separate detail call.
2. Missing/malformed fields remain visibly unknown; unreadable result pages never expose their metadata.
3. The shared inspection definition keeps existing `terms` behavior and never maps recorded to a sharing-allowed verdict.
4. Existing search tests and the full Go suite pass. No DB migration or new dependency.
5. An actual compiled CLI and a new isolated stdio MCP server return matching summaries for explicitly fictional examples.
6. Preserve the original selected repo documents and avoid personal wiki/config/DB/MCP; clean temporary runtime state.
