# Code-Block Wikilink Exclusion — Design Spec

Status: draft
Date: 2026-08-08
Owner: ROADMAP.md § Consume / tooling expansion

## Context

`extractWikilinks` in `internal/adapter/obsidian/parser.go` uses a regex
`\[\[([^\]\n]+)\]\]` that matches inside fenced code blocks and inline code
spans. The existing test `TestParseCodeBlockFalsePositive` explicitly documents
this as an MVP limitation ("accepts false positives in code blocks").

Fixture `testdata/fixtures/obsidian/code-block.md` has three wikilinks:
- `[[real-link]]` — body text (should extract)
- `[[false-positive-in-code]]` — fenced block (should skip)
- `[[also-false]]` — inline code (should skip)

## Scope

Add a `codeSpans` function that returns byte ranges for fenced code blocks
and inline code spans, then filter wikilink regex matches that fall within
those ranges.

### Changes

1. **New function** `codeSpans(body string) [][2]int` in `parser.go`:
   - Fenced blocks: lines starting with ``` or ~~~ (3+ chars), toggle on/off
   - Inline code: backtick-delimited spans (`` ` `` or ``` `` ```)
   - Returns sorted, non-overlapping `[start, end)` byte ranges

2. **Modify** `extractWikilinks`: after regex matching, skip matches whose
   `matchStart` falls within any code span

3. **Update** `TestParseCodeBlockFalsePositive`: assert only `[[real-link]]`
   is extracted (exactly 1 link, not ≥2)

4. **Add** unit tests for `codeSpans` covering: fenced block, inline code,
   nested backticks, no code, unclosed fence

## Success criteria

1. `extractWikilinks` skips wikilinks inside fenced blocks and inline code
2. `TestParseCodeBlockFalsePositive` updated to assert correct behavior
3. Full test suite passes
4. Tags and dataview inside code blocks: out of scope (same pattern, future)

## Risks

- Edge case: wikilink syntax `[[` at the boundary of a code span — the
  `matchStart` check handles this correctly (match start must be inside span)
