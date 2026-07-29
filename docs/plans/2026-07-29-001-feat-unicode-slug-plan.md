---
schema: plan/v1
title: "F7: Non-ASCII slug quality — preserve Unicode word characters"
type: feat
status: approved
date: 2026-07-29
execution: code
---

## Goal

`slugFor()` preserves Unicode letters/digits in wiki page slugs so Korean/CJK source filenames produce readable slugs instead of falling back to `src-<hash8>`.

## Architecture notes

**Decision: `unicode.IsLetter`/`unicode.IsDigit` filter replaces ASCII-only check.**
The current `[a-z0-9._-]` character filter in `slugFor()` strips all non-ASCII characters. Replace with Go's `unicode.IsLetter(ch) || unicode.IsDigit(ch)` plus the existing `._-` allowlist. `strings.ToLower()` is already Unicode-aware — no change needed. `collapseDashes()` operates on `-` only and is unaffected. The `src-<hash8>` fallback stays for the (now rare) case of filenames with zero letters/digits.

**Decision: no migration.** Existing `src-<hash8>` pages stay until source content changes. A re-ingest of a changed file writes a new Unicode-slug page while the old `src-<hash8>.md` remains on disk (still indexed, still MCP-readable). This creates two pages for one source until the old page is manually removed. The trade-off is acceptable: automatic cleanup would require ledger+filesystem coordination logic disproportionate to the problem.

**Decision: no transliteration dependency.** Unicode preservation is simpler, produces more readable slugs for the primary user (Korean speaker), and adds zero dependencies.

**Verified: storage/adapter layer accepts non-ASCII paths.** `FSStorage.resolvePath` checks only traversal (`..`), absolute paths, and symlinks — no character-set restriction. `ValidateRelPath` likewise. Unicode page paths pass through write → index → MCP-read without filtering.

**Verified: byte-length budget.** APFS filename limit is 255 UTF-8 bytes. Slug overhead is 20 bytes (`sources/` + `-<hash8>` + `.md`), leaving 235 bytes for the slug (~78 Korean syllables). Typical Korean filenames are 10–30 chars. No byte-length cap needed.

**NFC/NFD note.** APFS preserves the normalization form the file was created with (verified: Korean filename round-trips as NFC). The slug is derived from `filepath.Base(absPath)` which returns the exact bytes the OS stored. Since the same filesystem walk returns the same bytes every run, the slug is stable. Cross-filesystem NFD→NFC drift (e.g., HFS+ to APFS migration) would change the source path itself, triggering a new ledger entry. No normalization code needed in `slugFor`.

## Assumption Recheck

No origin spec; no approved live assumptions to recheck.

## File structure

| File | Action | Responsibility |
|------|--------|----------------|
| `internal/ingest/ingest.go` | Modify | `slugFor` character filter change |
| `internal/ingest/slug_test.go` | Create | Dedicated slug unit tests |
| `SPEC.md` | Modify | §10.2 slug algorithm description |

`DESIGN.md` does not describe the slug algorithm (line 128 mentions `DigestRequest.PageSlug` struct field only) — no change needed.

## Scenario coverage map

No origin spec with User Scenarios section. Coverage is verified through unit tests on `slugFor` and an end-to-end ingest validation in U2.

## Implementation Units

## U1: slugFor Unicode character filter + tests
Execution note: test-first
Files:
  Create: `internal/ingest/slug_test.go`
  Modify: `internal/ingest/ingest.go`
  Test: `internal/ingest/slug_test.go`
Interfaces:
  Consumes: `filepath.Base`, `filepath.Ext`, `strings.ToLower`, `unicode.IsLetter`, `unicode.IsDigit`
  Produces: `slugFor(absPath, hash string) string` — unchanged signature, expanded character set
Test scenarios:
  happy: Korean filename `판례_95도250.pdf` → `판례_95도250`; mixed `AI시대-뉴스.pdf` → `ai시대-뉴스`
  edge: pure CJK `땡겨요.pdf` → `땡겨요` (no longer falls back to hash); all-punctuation `!!!.pdf` → `src-<hash8>` fallback; empty after strip → `src-<hash8>`; ASCII-only `notes-2024.pdf` → `notes-2024` (regression guard)
  error: n/a — `slugFor` is pure, no error return
  integration: existing `TestRunSlugCollisionDifferentSource` exercises the slug → pagePath → write chain; full-suite regression confirms no breakage
Steps:
  1. Create `internal/ingest/slug_test.go` with `TestSlugFor` table-driven test covering: ASCII-only (existing behavior), Korean-only, mixed Korean/ASCII, CJK characters, all-punctuation fallback, spaces→dashes, consecutive dashes collapsed, leading/trailing dash trimmed
  2. Run tests; confirm Korean cases fail (current `slugFor` strips all non-ASCII)
  3. In `internal/ingest/ingest.go:slugFor`, add `import "unicode"`, replace the `(ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9')` check with `unicode.IsLetter(ch) || unicode.IsDigit(ch)`
  4. Run tests; confirm all pass including existing `TestRunSlugCollisionDifferentSource`
  5. Commit: `feat(ingest): preserve Unicode word characters in slugFor (F7)`
Acceptance: `go test ./internal/ingest/ -run TestSlugFor` passes; Korean filename input produces Korean slug output

## U2: SPEC documentation + end-to-end validation
Execution note: skip-test-first
Files:
  Modify: `SPEC.md`
  Test: n/a
Interfaces:
  Consumes: n/a
  Produces: Updated §10.2 slug algorithm description
Test scenarios:
  happy: n/a — documentation unit
  edge: n/a
  error: n/a
  integration: n/a
Steps:
  1. In `SPEC.md` §10.2, change "dropping every character outside `[a-z0-9._-]`" to "dropping every character that is not a Unicode letter, Unicode digit, `.`, `_`, or `-`"
  2. Update the CJK fallback description: change "e.g. a CJK-only name" to "e.g. a name with no letters or digits" since CJK names now produce valid slugs
  3. Run full test suite: `go test ./...`
  4. End-to-end validation: copy a Korean-named PDF to a source directory, run `cogvault ingest`, verify the wiki page slug is readable Korean (not `src-<hash8>`), verify `cogvault search` returns the page with correct display, verify a second `ingest` run reports the file as already processed (not re-digested)
  5. Commit: `docs: update slug algorithm for Unicode support (F7)`
Acceptance: `grep -c "Unicode letter" SPEC.md` returns ≥1; Korean-named PDF produces a Korean-slug wiki page; CLI search displays it correctly

## Mutation/failure-state matrix

No stateful ceremony in the deliverable; no mutation/failure-state matrix required.

## Carry-forward trigger audit

Audited `docs/research/v2-follow-ups.md` at commit c12a155: 5 open rows, 1 fired (F7 — edit-based, `slugFor` in `internal/ingest/ingest.go` is in the planned file list), 0 unobservable.

| Tracker row | Trigger class | Fired by | Disposition |
|-------------|---------------|----------|-------------|
| F7 | edit-based | `internal/ingest/ingest.go` in planned file list | fold as U1+U2 |

Remaining open rows (F2, F3, F4, F5): none fired by this plan's file list. F2 touches review minors across multiple files but none overlap with `slugFor` or §10.2. F3 is event-based (wait for real collision). F4 is a spec decision independent of slug generation. F5 cleanup targets (`contentHash()`, v1 fixtures, MCP schema fallback) do not overlap.

## Deferred to Follow-Up Work

- **Tracker update**: Mark F7 Done in `docs/research/v2-follow-ups.md` after implementation is verified — not part of implementation units.
- **Retro**: Write F7 retrospective after completion.
- **Old page cleanup**: When a source with changed content re-ingests under a new Unicode slug, the old `src-<hash8>.md` page stays on disk. Manual deletion or a future `cogvault gc` command could clean these up.

## Open unknowns

### Planning-time
None.

### Implementation-time
- Exact Unicode categories included by `unicode.IsLetter`: includes Latin, CJK Unified Ideographs, Hangul Syllables, Hiragana, Katakana, Cyrillic, etc. All are valid filesystem characters on macOS/Linux. No restriction needed.
- Whether `strings.ToLower` produces unexpected results for specific scripts (e.g., Turkish dotted I): Go's `ToLower` uses Unicode case mapping, which is correct for all scripts. Korean/CJK have no case, so `ToLower` is a no-op for them.
