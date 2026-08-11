# Roadmap — cogvault

Status: non-canonical forward-looking summary
Last updated: 2026-08-11

This file is a navigational index into canonical owners — it carries no
independent scope claims. Every item references the document or decision that
owns its boundary. For the operational follow-up tracker, see
[docs/research/v2-follow-ups.md](docs/research/v2-follow-ups.md). For broader
historical context and rejected alternatives, see
[docs/context/project-background.md](docs/context/project-background.md).

---

## Phase 1 — capture→digest→consume pipeline (done)

All four success criteria met (F1 in v2-follow-ups.md, 2026-07-29).

| Delivered | Owner |
|-----------|-------|
| Single mode: `wiki_dir` sole root, `sources[]` read-only | [0021](docs/decisions/0021-v2-refounding.md) D1, [SPEC](SPEC.md) §2 |
| `cogvault ingest`: scan → hash → LLM digest → validate → write → index → ledger | [0021](docs/decisions/0021-v2-refounding.md) D2/D4, [SPEC](SPEC.md) §10 |
| `internal/llm` adapter + `claudecode` backend | [0021](docs/decisions/0021-v2-refounding.md) D3, [DESIGN](DESIGN.md) §2.6 |
| Error classes: transient / permanent / infra / refused | [CONCEPTS](CONCEPTS.md), [SPEC](SPEC.md) §4.2 (3-class), F6 (refused) |
| launchd zero-touch scheduled ingest | [0021](docs/decisions/0021-v2-refounding.md) D2, README §5 |
| MCP six-tool server (scope removed) | [0021](docs/decisions/0021-v2-refounding.md) D5, [SPEC](SPEC.md) §8 |
| CLI: init, search, serve, ingest | [SPEC](SPEC.md) §9 |
| SQLite FTS5 trigram + Korean LIKE fallback | [DESIGN](DESIGN.md) §2.5 |
| Configurable `max_file_size_mb` | F9, [SPEC](SPEC.md) §3.1 |
| Source category classification (article/legal/reference) | F8, [DESIGN](DESIGN.md) §2.5 |
| Unicode-aware slug generation | F7, [SPEC](SPEC.md) §10.2 |
| Makefile with adhoc codesign for macOS FDA | F10, [DESIGN](DESIGN.md) §5 |

### Open follow-ups from Phase 1

Status lives in [v2-follow-ups.md](docs/research/v2-follow-ups.md) — not here.

- ~~**F2** (P3) — Deferred review minors batch~~ **Done** (PR #18, 2026-08-08)
- ~~**F3** (P3) — FTS write-write `SQLITE_BUSY_SNAPSHOT` limitation~~ **Done** (closed as documented/accepted, 2026-08-08)
- **F4** (P3) — Spec self-contradiction: renamed-file re-digest under (path,hash) key
- ~~**F5** (P4) — Dead code cleanup + MCP schema fallback 404~~ **Done** (PR #17, 2026-08-08)

---

## Later phases (under consideration)

Each later phase gets its own spec when work begins
([SPEC](SPEC.md) §1.3 header). Items are candidates, not commitments.

### Capture expansion

| Item | Context | Owner |
|------|---------|-------|
| ~~Phone capture (share-sheet → synced inbox → pipeline)~~ | **Done** (2026-08-08). No native app needed: share-sheet → iCloud Drive/Dropbox folder → configure as `sources[].path` → scheduled ingest picks up files automatically. Pattern documented in README. | [project-background](docs/context/project-background.md) §Later Phases |
| ~~URL / web-article extraction (fetch + extract before digest)~~ | **Done** (2026-08-08). `cogvault fetch <url>` downloads content to source dir for ingest. | [project-background](docs/context/project-background.md) §Later Phases |
| ~~Markdown-source digestion (restore v1 full-text coverage)~~ | **Done** (PR #16, 2026-08-08): `buildPrompt` type-aware, `SourceExt` in `DigestRequest`. Users add `md` to `sources[].types`. | [project-background](docs/context/project-background.md) §Later Phases |
| ~~Supplementary file types (xlsx/csv/tsv alongside PDFs)~~ | **Done** (2026-08-08). `sourceTypePhrase` extended for csv/tsv/xlsx; pipeline already type-agnostic via `sources[].types` config. | [SPEC](SPEC.md) §1.3 |

### Digest expansion

| Item | Context | Owner |
|------|---------|-------|
| ~~Local LLM backend (second `llm.Adapter` — ollama / llama.cpp)~~ | **Done** (2026-08-08). `Ollama` adapter via `/api/generate`; `llm.backend: ollama` + `base_url` config. | [0021](docs/decisions/0021-v2-refounding.md) D3, [project-background](docs/context/project-background.md) |
| ~~Periodic digest (`cogvault digest` — daily/weekly summary page)~~ | **Done** (2026-08-08). `cogvault digest --days N` generates `digests/weekly-YYYY-MM-DD.md` from recent sources. | [project-background](docs/context/project-background.md) §Later Phases |
| ~~Batch report sum verification (assert `sum(counts) == scanned`)~~ | **Done** (PR #15, 2026-08-07). Retro: `docs/retros/2026-08-07-batch-report-sum-verification.md` | [SPEC](SPEC.md) §10.4 |

### Knowledge synthesis

"파일이 늘어나는 것과 지식이 축적되는 것은 다르다" — ingest가 source page 1개를
생성하고 끝나는 현 구조에서, 교차 문서 관계를 자동으로 유지하는 층을 추가.
Inspired by [llm-wiki-for-scientists][llm-wiki].

| Item | Context | Owner |
|------|---------|-------|
| ~~Synthesis layer — ingest 후처리로 관련 기존 페이지 검색 → concept/synthesis 페이지 자동 생성/갱신~~ | **Done** (2026-08-08). `cogvault synthesize` creates concept pages from cross-referenced links and shared tags (≥2 pages). | [SPEC](SPEC.md) §1.3 |
| ~~Question → wiki feedback loop~~ | **Done** (2026-08-08). `_schema.md`에 Q&A→Wiki 피드백 루프 워크플로 추가. 코드 변경 없음. | [SPEC](SPEC.md) §8 |

### Consume / tooling expansion

| Item | Context | Owner |
|------|---------|-------|
| ~~`wiki_delete` + git auto-commit (paired — deletion unsafe without auto-commit)~~ | **Done** (2026-08-08). `wiki_delete` MCP tool + `gitAutoCommit` on delete. Storage.Delete added to interface. | [project-background](docs/context/project-background.md) §Carry-Overs |
| ~~Lint (contradictions, orphans, broken links, frontmatter compliance; introduces `ResolveLink`)~~ | **Done** (2026-08-08). `cogvault lint` checks broken links, orphans, frontmatter compliance with `resolveWikilink`. | [project-background](docs/context/project-background.md) §Carry-Overs |
| ~~`wiki_write` warnings (frontmatter validation feedback)~~ | **Done** (PR #20, 2026-08-08). `validateFrontmatter` returns warnings array. | [project-background](docs/context/project-background.md) §Carry-Overs |
| ~~SSE transport (remote access via tunnel or cloud deploy)~~ | **Done** (2026-08-08). `cogvault serve --transport sse --addr host:port` via mcp-go SSEServer. | [project-background](docs/context/project-background.md) §Carry-Overs |
| ~~OAuth 2.1 / bearer authorization for remote access from the Claude apps and ChatGPT~~ | **Done** (2026-08-11). New `http` (Streamable HTTP) transport; `auth.mode: none\|bearer\|oauth` gates every `sse`/`http` request via `internal/httpauth`. See [docs/deployment/remote-mcp.md](docs/deployment/remote-mcp.md). | [docs/specs/2026-08-11-remote-mcp-server-design.md](docs/specs/2026-08-11-remote-mcp-server-design.md) |
| ~~Code-block wikilink exclusion (state machine for fenced/inline code)~~ | **Done** (PR #19, 2026-08-08). `codeSpans` filter in `extractWikilinks`. | [project-background](docs/context/project-background.md) §Carry-Overs |
| ~~Page-type expansion (entity/concept/synthesis schemas when usage justifies it)~~ | **Done** (2026-08-08). entity/concept/synthesis 타입 스키마 `_schema.md`에 추가. | [project-background](docs/context/project-background.md) §Carry-Overs |

### Longer horizon

| Item | Context | Owner |
|------|---------|-------|
| ~~Vector search (sqlite-vec / external embeddings)~~ | **Done** (2026-08-08). `SearchSimilar` upgraded to real embedding cosine similarity via Ollama `/api/embed` (PR #21). FTS fallback per D3. `cogvault embed` + `cogvault similar` CLI. | [0014](docs/decisions/0014-roadmap-adoption-boundaries.md) D3 |
| ~~RRF hybrid search~~ | **Done** (2026-08-08). `SearchSimilar` combines embedding vectors (primary) + FTS title-match (fallback). Full RRF deferred until multiple ranking signals coexist. | [project-background](docs/context/project-background.md) §Longer-Horizon |
| ~~Ontology graph~~ | **Done** (2026-08-08). `cogvault graph` outputs JSON link graph (nodes + edges) for visualization. | [project-background](docs/context/project-background.md) §Longer-Horizon |
| ~~Multi-wiki support~~ | **Done** (2026-08-08). Already supported via `--config <path>` per wiki. Each wiki has its own config, wiki_dir, db_path. No code change needed. | [project-background](docs/context/project-background.md) §Longer-Horizon |
| ~~Auto-generated read-only `_index.md` view~~ | **Done** (2026-08-08). `cogvault index` CLI command generates `_index.md` with wikilinks and titles. | [project-background](docs/context/project-background.md) §Longer-Horizon |

---

## Not planned

These are accepted boundary decisions, not deferrals. Revisit triggers are
documented in the owning decision.

| Item | Decision | Owner |
|------|----------|-------|
| Watch mode / resident daemon | Batch + launchd chosen instead; revisit only if schedule latency proves unacceptable | [0021](docs/decisions/0021-v2-refounding.md) D2 |
| AAAK-style compressed representation formats | Would redefine the core Markdown-visible abstraction | [0014](docs/decisions/0014-roadmap-adoption-boundaries.md) D2 |
| Vector search as a standalone FTS replacement | Vector must extend, not replace, the current retrieval core | [0014](docs/decisions/0014-roadmap-adoption-boundaries.md) D2/D3 |
| Temporal knowledge graph as a required core abstraction | Heavier reasoning model than the wiki builder needs | [0014](docs/decisions/0014-roadmap-adoption-boundaries.md) D2 |
| Conversation-mining as the primary product mode | Changes product identity from vault wiki tooling to a different input mode | [0014](docs/decisions/0014-roadmap-adoption-boundaries.md) D2 |
| Direct Anthropic API calls (bypassing Claude Code CLI) | Escape hatch is the local-LLM backend, not an API fallback | [0021](docs/decisions/0021-v2-refounding.md) D3 |

---

## Related documents

- [SPEC.md](SPEC.md) — canonical contract (scope in §1.2/§1.3)
- [DESIGN.md](DESIGN.md) — architecture and implementation order (§8)
- [docs/decisions/0021-v2-refounding.md](docs/decisions/0021-v2-refounding.md) — v2 refounding rationale
- [docs/decisions/0014-roadmap-adoption-boundaries.md](docs/decisions/0014-roadmap-adoption-boundaries.md) — adoption boundary decisions
- [docs/research/v2-follow-ups.md](docs/research/v2-follow-ups.md) — operational follow-up tracker
- [docs/context/project-background.md](docs/context/project-background.md) — historical context and full future-direction list
- [docs/research/llm-wiki-for-scientists-review.md](docs/research/llm-wiki-for-scientists-review.md) — llm-wiki-for-scientists analysis (2026-08-07)

[llm-wiki]: https://github.com/chaek-union/llm-wiki-for-scientists
