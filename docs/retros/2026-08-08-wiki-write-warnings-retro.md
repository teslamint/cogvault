# wiki_write Warnings — Release Retro

Date: 2026-08-08
PR: #20

## Outcome

`wiki_write` now validates `.md` content and returns frontmatter warnings.
Writes are never blocked — warnings are informational only.

## Success criteria — all met

1. Warnings returned for missing frontmatter, missing title, and incomplete source pages.
2. Writes succeed regardless of validation result.
3. Full test suite passes.

## What went well

- Existing `warnings` field in the response made integration seamless.
- 6 unit test cases cover the validation matrix.

## What to improve

- `frontmatter.Parse` doesn't error on content without `---` delimiters — needed an explicit prefix check. Caught by test.

## Carry-forwards

- Section validation (## 요약, ## 핵심 포인트) deferred — too aggressive for write-time.
