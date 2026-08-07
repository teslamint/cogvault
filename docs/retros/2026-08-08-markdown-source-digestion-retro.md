# Retro: Markdown-Source Digestion

PR: #16 | Merged: 2026-08-08 | Branch: feat/markdown-source-digestion

## Success criteria results

| # | Criterion | Result |
|---|-----------|--------|
| SC1 | `buildPrompt` type-aware phrasing | PASS — `.md` → "markdown file", `.pdf` → "PDF file", other → "file". 6-case unit test. |
| SC2 | Integration: `.md` source → wiki page | PASS — `TestDigestSourceExtPassedToLLM` + pre-existing md-pipeline tests. |
| SC3 | No regression | PASS — `go test ./...` 12 packages, 0 failures. |
| SC4 | SPEC/DESIGN updated | PASS — SPEC §1.2, §2 diagram, §3.1; DESIGN §2.6. |

## What went well

- Infrastructure was already file-type agnostic (scan, hash, ledger, slug). Only
  the LLM prompt layer needed changes.
- Small, surgical change: 3 lines of production code (1 struct field, 1 caller
  site, 1 prompt function) + 2 doc updates.
- Code review caught a spec/code mismatch (`.markdown` in code but not spec) and
  a stale SPEC diagram — both fixed before merge.

## What could improve

- Initial spec draft missed the `.markdown` extension case that the implementation
  added. Spec-first discipline works better when the spec enumerates cases fully.
- Advisor flagged that SC1 was already true before work started — need to write
  success criteria that are actually falsifiable at draft time.

## Lessons

- The ingest pipeline's type-agnostic design (config-driven `types[]`) made this
  feature nearly free. Good abstraction boundary at the scan layer.
- `project-background.md §Later Phases` is the authoritative interpretation source
  for roadmap items — read it before scoping.

## Follow-ups

- ROADMAP.md: mark "Markdown-source digestion" as done.
- Users should add `md` to their `sources[].types` config to enable markdown ingestion.
