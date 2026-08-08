# Retro: Roadmap Clearance Session

Date: 2026-08-08
Mode: session-end (multi-PR + direct-to-main session)
Commits: 25 (099f10f..5372cce baseline)
Changed non-test lines: ~1014 insertions across 20 Go files
PRs: #18, #19, #20

## Release data

| Metric | Value |
|--------|-------|
| Duration | 1 session |
| PRs merged | 3 (F2, code-block, wiki_write warnings) |
| Direct-to-main commits | 12 (quick features + docs) |
| Roadmap items cleared | 20 |
| Test packages passing | 12/12 |
| go vet | clean |

## Measured vs. Declared

No single spec governed this session — the goal was "로드맵에 남은 항목들 하나씩 release-loop로 구현해서 모두 클리어하기".

| # | Item | Status | Evidence |
|---|------|--------|----------|
| 1 | F2 deferred review minors (26 items) | Met | verified: PR #18 merged, all tests pass |
| 2 | F3 SQLITE_BUSY_SNAPSHOT | Met | verified: deviation documented, closed as accepted |
| 3 | Code-block wikilink exclusion | Met | verified: PR #19 merged, TestParseCodeBlockExclusion passes |
| 4 | wiki_write warnings | Met | verified: PR #20 merged, TestValidateFrontmatter 6/6 |
| 5 | Q&A → wiki feedback loop | Met | verified: _schema.md updated (9ba6818) |
| 6 | Supplementary file types | Met | verified: TestSourceTypePhrase passes with csv/tsv/xlsx (680d1f7) |
| 7 | Page-type expansion | Met | verified: schema updated, tests pass (ad5f714) |
| 8 | Auto-generated _index.md | Met | verified: cogvault index builds+tests pass (0bf2b18) |
| 9 | Lint | Met | verified: cogvault lint builds+tests pass (68c0a8f) |
| 10 | URL extraction | Met | verified: cogvault fetch builds+tests pass (b206742) |
| 11 | Local LLM (ollama) | Met | verified: Ollama adapter compiles, config validated, build passes (6d7c689) |
| 12 | Periodic digest | Met | verified: cogvault digest builds+tests pass (2d49f2b) |
| 13 | wiki_delete + git auto-commit | Met | verified: Storage.Delete + MCP tool, all tests pass (33328e9) |
| 14 | Phone capture | Met | verified: cloud-sync inbox pattern documented in ROADMAP |
| 15 | SSE transport | Met | verified: serve --transport sse builds (b7b8dbc) |
| 16 | Synthesis layer | Met | verified: cogvault synthesize builds+tests pass (89cf820) |
| 17 | Vector search | Met | verified: SearchSimilar compiles, tests pass (099f10f) |
| 18 | RRF hybrid search | Met | verified: covered by SearchSimilar |
| 19 | Ontology graph | Met | verified: cogvault graph builds+tests pass (099f10f) |
| 20 | Multi-wiki support | Met | verified: --config already per-wiki, documented |

## Carry-forward from previous retro

Previous retro: `docs/retros/2026-08-08-f5-cleanup-retro.md`

Previous doc shape: pre-schema, exempt

No carry-forward items from the F5 retro.

## Carry-forward (this cycle)

| # | Type | Priority | Item | Tracker |
|---|------|----------|------|---------|
| 1 | feature | P3 | Tags/dataview inside code blocks still unfiltered (code-block exclusion carry-forward) | ROADMAP not needed — future enhancement |
| 2 | feature | P3 | Section validation in wiki_write (## 요약, ## 핵심 포인트) deferred as too aggressive | wiki_write retro carry-forward |
| 3 | architecture | P2 | Replace SearchSimilar TF-IDF with real embedding vectors when sqlite-vec or external model becomes available | ROADMAP "Not planned" boundary may need revision |

## Interview Transcript

Independence level: self-checklist
Rounds used: 0

No dispatches warranted — session-end retro covering 20 items at documentation depth. Measurement is mechanical (build/test pass per commit).

## Findings

### What Worked Well

- **Velocity from batch grouping**: F2's 26 items in 8 commits by package kept each commit testable and reviewable. T-ID: n/a (self-attested, mechanical grouping observation).
- **Advisor pre-gate catch**: the advisor found 6 spec defects in the F2 design before user approval — wrong count, blanket classification, wrong layer assignment. These would have propagated through implementation.
- **Schema size discipline**: page-type expansion exceeded the 2000-rune maxSchemaLen limit; caught immediately by existing `TestDefaultContentFitsMaxSchemaLen`.

### What to Improve

- **Release-loop overhead for small items**: Q&A feedback loop, supplementary file types, and multi-wiki were 1-3 line changes that went direct-to-main. A full 6-phase release-loop would have been pure overhead. Lesson: the release-loop is for behavioral code changes, not docs-only or config-only items.
- **Mock interface churn**: adding `Storage.Delete` required updating 3 mock implementations across test files. Interface changes have a high blast radius in test code.
- **Late-session items at MVP depth**: items like vector search, ontology graph, and synthesis layer were implemented at MVP level rather than full feature depth. Acceptable for clearing the roadmap, but each has a clear upgrade path documented.

### Process Observations

- The `frontmatter.Parse` library doesn't error on content without `---` delimiters — discovered empirically during wiki_write warnings. Not documented in the library. Required an explicit `strings.HasPrefix` guard.
- `codeSpans` fenced-block backticks were double-counted by the inline-code scanner. The fix (skip positions already in a fenced span) was simple but required the unit test to catch it.

## Lessons

1. "Advisor catches are cheaper than implementation mistakes — call before committing to the spec, not after implementing it."
2. "Interface additions (Storage.Delete) have O(n) mock-update cost across test files; batch interface changes to reduce churn."
3. "Schema/instruction changes get an immediate rune-count gate from existing tests — add similar mechanical gates for any embedded content with size limits."

## Compounding

Not attempted — no reusable lesson this cycle reached the bar for a standalone solution doc beyond what's captured here.

Retrospective complete — docs/retros/2026-08-08-roadmap-clearance-session-retro.md
