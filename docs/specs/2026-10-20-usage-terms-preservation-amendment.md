---
status: approved
supersedes: 2026-10-20-usage-terms-design.md (automatic record replacement rule only)
---
# Preserve explicit usage conditions independently of review status

## Authority and defect

The user rejected the observed loss of explicit MIT conditions and evidence.
The user requires explicit usage-permission conditions to remain recorded regardless of the write path.
This instruction authorizes the correction; no second approval gate is required.
The prior design incorrectly equated an unverified review status with removal of supplied evidence.

## Corrected contract

1. `unverified` is a review state, not a statement that no terms exist. Explicit purpose, conditions, scope, and evidence remain visible even without completed human review.
2. Automatic ingest reads structured `usage_terms` directly from supported Markdown input, independently of LLM summarization. For text-input backends, use extracted source text. Do not infer terms from a URL, free access, subscription, or a repository license that was not supplied for that input.
3. Choose a supplied input record first, an existing wiki record second, and a generated candidate third. Preserve the selected record's condition/evidence fields exactly as data. Set only review status to `unverified` and remove automatic assertions of `reviewed_by`/`reviewed_at`. A model candidate is not a legal decision.
4. Regeneration without a new source record retains the existing record's conditions and evidence, while requiring review again. New input declarations may replace values but must retain populated protected fields or supply their non-empty replacements. No automatic combination or legal resolution of conflicting declarations.
5. All Markdown writes through storage, including MCP writes and generated-output commands, reject an overwrite that omits or empties parseable existing purpose, conditions, scope, or evidence fields. Reject before writing so the old page and index remain intact. A deliberate non-empty replacement remains possible; no silent inheritance or index/content divergence. Malformed existing metadata remains repairable; malformed supplied ingest records still fail without overwriting.
6. Existing MCP read/parse, CLI search, and `terms` expose stored evidence. The `terms` inspector may report unverified and missing review fields, but must still display actual recorded conditions and evidence.
7. Link-only digest/concept pages do not acquire a blanket license from linked works. This correction preserves declarations where supplied; it does not invent inherited permission.
8. Plain prose still passes through the LLM; instruct it to retain explicit terms and verbatim evidence as unverified data. Guaranteed deterministic carry-through applies to structured input records. No automated legal interpretation or corpus classification is added.
9. URL capture keeps the complete response body byte-identical. A parseable supplied terms declaration is also copied into capture metadata as unverified data, with remote reviewer/date claims removed from the copy. This records evidence, not inferred permission. The same overwrite-loss guard protects existing captures.

## Shared implementation boundary

Add the leaf package `internal/terms` using existing YAML/frontmatter dependencies:

- `Parse(content []byte) (map[string]any, []byte, error)` returns metadata and body with string-keyed YAML maps.
- `Encode(metadata map[string]any, body []byte) ([]byte, error)` renders YAML frontmatter and the unchanged body.
- `Automatic(source, previous, generated map[string]any) map[string]any` returns a copy of generated metadata with the selected record, using the precedence above; it resets review status and removes reviewer/date claims. No candidate produces only `{status: unverified}`.
- `CheckOverwrite(existing, incoming []byte) error` rejects dropped/empty protected fields before storage mutation.

The ingest and storage slices share these APIs. The CLI inspector reuses Parse rather than introducing another YAML convention.
Preserve current path/symlink/exclusion checks and the existing single-writer mutex. No new dependency or DB migration.

## Acceptance evidence

- Regression RED proves a source record survives even when model output omits or contradicts it.
- Both path-input and extracted-text paths preserve supplied structured records; absent evidence remains unverified without fabricated fields.
- Regeneration and manual/MCP overwrites cannot silently lose existing conditions or evidence.
- Index and file contain the same preserved evidence.
- Real MIT-bearing review input produces `terms` output with the exact notice obligation, evidence location, and quotation under `unverified`, and search finds the condition text.
- Original review/LICENSE bytes remain unchanged; verification uses isolated wiki/config/DB and no personal MCP.
