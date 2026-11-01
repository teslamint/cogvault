# 0026: Research Evidence Record

Status: accepted
Date: 2026-11-02

## Context

Wiki pages can carry claims and cite source pages, but free-form text does not provide a consistent, inspectable structure. The record needs to be useful to tools without implying that cogvault verifies the claims or determines legal status.

## Decision

1. Pages may include an optional versioned `research_evidence` frontmatter record. Version 1 has required fields, closed enums, and exact wiki-root-relative Markdown references. Extra keys are ignored.
2. A shared pure `internal/evidence` validator and summarizer owns the record contract. Validation emits stable diagnostics; summaries are bounded and do not resolve source references.
3. `wiki_write` surfaces evidence findings as warnings and never blocks a write. Search exposes a bounded per-result summary; lint reports diagnostics in text or JSON.
4. MCP initialize instructions append server-owned evidence guidance after the `_schema.md` schema summary. The schema rune cap applies only to the schema portion.
5. The record is self-reported structured evidence, not a fact-check or legal judgment.

## Why

A shared validator keeps write, search, and lint interpretation aligned. Versioning and closed enums make the current contract explicit while leaving future format evolution possible. Non-blocking warnings preserve ordinary wiki editing, and exact-path references avoid ambiguous resolution behavior.

## Alternatives Considered

- Editing the wiki `_schema.md`: rejected because this feature is server-owned guidance and must not require changing a user-editable schema file.
- Fuzzy typo detection for record keys or reference paths: rejected; unknown keys are ignored and refs use exact paths.
- Blocking writes for invalid evidence records: rejected; findings are warnings so a malformed optional annotation does not prevent page writing.
- Resolving references during search: rejected; search returns a bounded summary without fetching or resolving source pages.

## Revisit Triggers

- A new record version or change to its required fields or closed enums.
- Evidence summarization needs that cannot be expressed within the current bounds.

## Related Files

- `SPEC.md` §§8.3, 8.5, 9.12, 11.1
- `DESIGN.md` §§1, 2, 5
- `internal/evidence`
