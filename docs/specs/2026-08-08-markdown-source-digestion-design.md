---
title: Markdown-source digestion
status: approved
feature: markdown-source-digestion
created: 2026-08-08
---

# Markdown-Source Digestion

Restore markdown-source coverage lost in the v2 refounding (ROADMAP §Capture expansion;
project-background §Later Phases: "digest raw vault/markdown notes so full-text search
over source notes returns").

## Problem

The ingest pipeline scans, hashes, and tracks source files by extension via
`sources[].types` — already file-type agnostic. However, `buildPrompt()` in
`internal/llm/claudecode.go:148` hardcodes "Read the PDF file at path:", which
misleads the LLM when the source is a non-PDF file. Users who set `types: [pdf, md]`
get functional digestion today, but with a prompt that says "PDF" for markdown files.

SPEC §2 line 29 also says "digest each PDF" instead of "each source file".

## Interpretation

"Restore v1 full-text coverage" means making markdown source files first-class
ingest targets — same summarize-and-synthesize contract as PDFs. The LLM produces
a wiki page (summary + key points), which FTS5 indexes. This restores search
coverage over content that was vault-visible in v1 but dropped in v2.

This is NOT full-text preservation (copying source content verbatim into the wiki
page). The `_schema.md` rule "소스 원문을 그대로 복사하지 않고 요약·합성한다" applies
equally to markdown sources.

## Changes

### C1: Add `SourceExt` to `DigestRequest`

`internal/llm/llm.go` — add `SourceExt string` to `DigestRequest`.

### C2: Populate `SourceExt` in `digestOne`

`internal/ingest/ingest.go:digestOne` — set `SourceExt: filepath.Ext(entry.absPath)`.

### C3: Make `buildPrompt` file-type-aware

`internal/llm/claudecode.go:buildPrompt` — replace the hardcoded "Read the PDF file"
with a type-aware phrase based on `req.SourceExt`:

| Extension | Phrase |
|-----------|--------|
| `.pdf`    | "Read the PDF file at path:" |
| `.md`     | "Read the markdown file at path:" |
| other     | "Read the file at path:" |

No other prompt changes.

### C4: SPEC updates

- §2 line 29: "digest each PDF" → "digest each source file"
- §3.1: `types` example `(e.g. [pdf])` → `(e.g. [pdf, md])`

### C5: DESIGN updates

- §2.6: `DigestRequest` struct → add `SourceExt` field
- §2.6: `buildPrompt` description → note type-aware phrasing

### C6: Tests

- Unit: `TestBuildPrompt` verifies type-aware phrasing for `.pdf`, `.md`, and unknown
  extensions.
- Integration: verify that an `.md` source file produces a wiki page through the
  full pipeline (existing test helpers already use `Types: []string{"md"}`).

## Known limitations

- Category taxonomy (article/legal/reference) was designed for documents. Personal
  markdown notes may not fit neatly — the LLM defaults to `article` when uncertain,
  which is acceptable for unstructured notes.
- Markdown sources that are very short (a few lines) produce thin wiki pages. This
  is consistent with PDF behavior for short documents — not a regression.

## Out of scope

- New prompt template or different digestion strategy for markdown.
- `_schema.md` changes (rules already generic).
- Config schema changes (`types` already accepts any string).
- Full-text preservation mode (different feature — synthesis layer territory).
- Recursive directory scanning (current top-level-only scan is by design).

## Success criteria

1. `buildPrompt` output contains "markdown file" when `SourceExt` is `.md`,
   "PDF file" when `.pdf`, and "file" otherwise. Verified by unit test.
2. Integration test: `.md` source file scans, digests, and produces a wiki page
   with valid frontmatter (`type: source`, `source_path`, `ingested_at`).
3. All existing tests pass (no regression).
4. SPEC §2 and §3.1 updated. DESIGN §2.6 updated.
