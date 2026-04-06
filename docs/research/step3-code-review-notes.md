# Step 3 Code Review Notes

Date: 2026-04-06
Scope: Post-implementation review of adapter code (internal review + Codex review)

## Summary

Tests passed but contract-level issues were found. All issues were fixed in the same session.

## Findings and Resolutions

### File-level exclude not applied in Scan (Codex: high)

Scan only checked exclude in `d.IsDir()` branch. Files like `private/secret.md` with `exclude: ["private"]` were excluded (directory skipped), but `exclude: ["toplevel-secret.md"]` had no effect on file entries.

Fix: Added `adapter.IsExcluded(rel, exclude)` check before `fn(rel)` call in both adapters.

### Markdown `![alt](path)` classified as Links (Codex: medium)

Regex `\[...\]\(...\)` matched both `[text](url)` and `![alt](img)`. All relative paths went to Links, including images. This diverges from Obsidian adapter which separates `![[file]]` → Attachments.

Fix: Changed regex to `!?\[...\]\(...\)`, detect `!` prefix, route to Attachments.

### href TrimSpace ordering (Codex: medium)

`isExternalLink` was called before TrimSpace on href. `( https://example.com )` bypassed external check.

Fix: TrimSpace applied before isExternalLink call.

### isExternalLink dead code (internal review)

`/` check before `//` made the `//` branch unreachable. Both are "external" so behavior was correct, but dead code.

Fix: Reordered to `//` before `/`.

### Security function duplication (internal review: P1)

6 functions identically copied between obsidian and markdown packages. One-sided fix = security divergence.

Fix: Extracted to `internal/adapter/pathutil.go` and `internal/adapter/extract.go`.

### go.mod indirect mislabel (internal review: P0)

`github.com/adrg/frontmatter` directly imported but marked `// indirect`.

Fix: `go mod tidy`.

### Wikilink regex matches newlines (internal review: P2)

`[^\]]+` matches `\n`. Obsidian doesn't recognize multi-line wikilinks.

Fix: Changed to `[^\]\n]+`.

### Markdown tags not deduplicated (internal review: P1)

`extractFrontmatterTags` lacked `seen` map unlike obsidian's `extractTags`.

Fix: Added dedup via shared `ExtractFrontmatterTags` in `extract.go`.

## Observations

- "Tests pass" did not catch any of these issues. Tests were designed for happy paths and explicit error cases, not contract boundary enforcement.
- File-level exclude and image classification were semantic bugs invisible to unit tests that only tested directory-level exclude.
- Security function duplication was the strongest argument for immediate extraction vs planned v0.2 extraction.
