# Roadmap — cogvault

Status: non-canonical forward-looking summary
Last updated: 2026-07-30

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

- **F2** (P3) — Deferred review minors batch
- **F3** (P3) — FTS write-write `SQLITE_BUSY_SNAPSHOT` limitation
- **F4** (P3) — Spec self-contradiction: renamed-file re-digest under (path,hash) key
- **F5** (P4) — Dead code cleanup + MCP schema fallback 404

---

## Later phases (under consideration)

Each later phase gets its own spec when work begins
([SPEC](SPEC.md) §1.3 header). Items are candidates, not commitments.

### Capture expansion

| Item | Context | Owner |
|------|---------|-------|
| Phone capture (share-sheet → synced inbox → pipeline) | S5 in [SPEC](SPEC.md) §1.3 | [project-background](docs/context/project-background.md) §Later Phases |
| URL / web-article extraction (fetch + extract before digest) | [SPEC](SPEC.md) §1.3 | [project-background](docs/context/project-background.md) §Later Phases |
| Markdown-source digestion (restore v1 full-text coverage) | v1 coverage lost per [0021](docs/decisions/0021-v2-refounding.md) | [project-background](docs/context/project-background.md) §Later Phases |

### Digest expansion

| Item | Context | Owner |
|------|---------|-------|
| Local LLM backend (second `llm.Adapter` — ollama / llama.cpp) | Primary mitigation for Claude CLI change risk | [0021](docs/decisions/0021-v2-refounding.md) D3, [project-background](docs/context/project-background.md) |
| Periodic digest (`cogvault digest` — daily/weekly summary page) | S6 in [SPEC](SPEC.md) §1.3 | [project-background](docs/context/project-background.md) §Later Phases |

### Consume / tooling expansion

| Item | Context | Owner |
|------|---------|-------|
| `wiki_delete` + git auto-commit (paired — deletion unsafe without auto-commit) | [SPEC](SPEC.md) §1.3 | [project-background](docs/context/project-background.md) §Carry-Overs |
| Lint (contradictions, orphans, broken links, frontmatter compliance; introduces `ResolveLink`) | | [project-background](docs/context/project-background.md) §Carry-Overs |
| `wiki_write` warnings (frontmatter validation feedback) | | [project-background](docs/context/project-background.md) §Carry-Overs |
| SSE transport (remote access via tunnel or cloud deploy) | | [project-background](docs/context/project-background.md) §Carry-Overs |
| Code-block wikilink exclusion (state machine for fenced/inline code) | | [project-background](docs/context/project-background.md) §Carry-Overs |
| Page-type expansion (entity/concept/synthesis schemas when usage justifies it) | | [project-background](docs/context/project-background.md) §Carry-Overs |

### Longer horizon

| Item | Context | Owner |
|------|---------|-------|
| Vector search (sqlite-vec / external embeddings) | Must complement FTS, not replace it | [0014](docs/decisions/0014-roadmap-adoption-boundaries.md) D3 |
| RRF hybrid search | Depends on vector search | [project-background](docs/context/project-background.md) §Longer-Horizon |
| Ontology graph | | [project-background](docs/context/project-background.md) §Longer-Horizon |
| Multi-wiki support | | [project-background](docs/context/project-background.md) §Longer-Horizon |
| Auto-generated read-only `_index.md` view | | [project-background](docs/context/project-background.md) §Longer-Horizon |

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
