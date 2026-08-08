# wiki_write Warnings — Design Spec

Status: draft
Date: 2026-08-08
Owner: ROADMAP.md § Consume / tooling expansion

## Context

`wiki_write` returns `{"status":"written","path":"…","bytes":N,"warnings":[]}`.
The `warnings` array is always empty. The wiki schema defines frontmatter rules
that are not validated at write time — violations are silent until `wiki_parse`
or ingest encounters them.

## Scope

Add frontmatter validation to `handleWikiWrite` for `.md` files. Return
warnings in the existing `warnings` array; never block the write.

### Validation rules

For any `.md` file:
1. **No frontmatter** — `"missing YAML frontmatter"`
2. **No title** — `"missing frontmatter field: title"`

For `type: source` pages (detected from frontmatter):
3. **Missing source_path** — `"source page missing field: source_path"`
4. **Missing ingested_at** — `"source page missing field: ingested_at"`

### Changes

1. **New function** `validateFrontmatter(content string) []string` in
   `internal/mcp/tools.go` — parses frontmatter, returns warning strings
2. **Modify** `handleWikiWrite`: call `validateFrontmatter` for `.md` files,
   populate the `warnings` array
3. **Tests**: verify warnings for each case (no-fm, no-title, source missing
   fields, clean file returns no warnings)

## Success criteria

1. Write succeeds regardless of validation result (warnings, not errors)
2. Warnings array populated for violations
3. Full test suite passes

## Out of scope

- Section validation (## 요약, ## 핵심 포인트) — too aggressive for write-time
- Blocking writes on validation failure — would break agent workflows
