# Code-Block Wikilink Exclusion — Release Retro

Date: 2026-08-08
PR: #19
Branch: feat/code-block-wikilink-exclusion

## Outcome

Wikilinks inside fenced code blocks and inline code spans are now excluded
from parsing. The MVP false-positive acceptance is replaced by correct behavior.

## Success criteria

1. **Wikilinks in code excluded** — Met. Fixture confirms only `[[real-link]]` extracted.
2. **Tests pass** — Met. 6 codeSpans unit tests + updated integration test.
3. **Full suite green** — Met. All 12 packages pass.

## What went well

- Single-file change, quick turnaround: Design → Implement → Ship in one pass.
- The `codeSpans` function is reusable — tags/dataview can use the same filter later.

## What to improve

- First implementation produced duplicate spans (fenced backticks matched by both scanners). Caught by unit test.

## Carry-forwards

- Tags and dataview inside code blocks remain unfiltered (out of scope per spec).
