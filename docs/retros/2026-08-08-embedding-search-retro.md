# Retro: Embedding-based SearchSimilar

Date: 2026-08-08
Mode: PR-merge (PR #21)
Commits: 2 (squash-merged as 03b8d1d)
Changed non-test lines: ~625 insertions across 9 Go files

## Release data

| Metric | Value |
|--------|-------|
| Duration | 1 session |
| PRs merged | 1 (#21) |
| Review rounds | 1 (13 findings: 5M + 8L, all addressed) |
| Test packages passing | 12/12 |
| go vet | clean |
| New files | 6 (embedder.go, ollama_embed.go, embedding.go, embed.go, similar.go, + tests) |

## Measured vs. Declared

Spec: `.release-loop/briefs/embedding-search-spec.md`

| # | Criterion | Measured | Verdict |
|---|-----------|----------|---------|
| SC1 | `cogvault embed` computes embeddings for all indexed pages when `embedding_model` is set | verified: TestStalePaths confirms hash-based staleness detection; TestStoreAndSearchEmbedding stores and retrieves 3 pages; EmbeddingCount validated in TestEmbeddingsTableSurvivesSchemaRecreate | Met |
| SC2 | `cogvault similar <path>` returns semantically related pages using embedding cosine similarity | verified: TestStoreAndSearchEmbedding — vecA (Go programming) ranks closest to vecB (Go concurrency), not vecC (cooking), matching semantic not title similarity | Met |
| SC3 | When `embedding_model` is empty, SearchSimilar uses FTS fallback identically to current behavior | verified: TestSearchSimilarFTSFallback — no embeddings configured, FTS returns "Go concurrency channels" for "Go concurrency" query | Met |
| SC4 | Embedding failure does not block ingest | verified: TestOllamaEmbedderHTTPError — HTTP 404 returns error; postIngestEmbed warns and returns without failing ingest (code path: ingest.go:89-91 prints warning, does not return error) | Met |
| SC5 | Embeddings table survives schemaVersion bump | verified: TestEmbeddingsTableSurvivesSchemaRecreate — PRAGMA user_version set to 2, initSchema re-runs DROP+CREATE on wiki_fts/file_meta, embeddings count remains 1 | Met |

## Carry-forward from previous retro

Previous retro: `docs/retros/2026-08-08-roadmap-clearance-session-retro.md`

Previous doc shape: pre-schema, exempt

| # | Item | Status | Evidence |
|---|------|--------|---------|
| 1 | Tags/dataview inside code blocks still unfiltered (P3) | Not started | No work this cycle — remains future enhancement |
| 2 | Section validation in wiki_write (P3) | Not started | No work this cycle — remains deferred |
| 3 | Replace SearchSimilar with real embeddings (P2) | Done | PR #21 merged (03b8d1d) |

## Carry-forward (this cycle)

| # | Type | Priority | Item | Tracker |
|---|------|----------|------|---------|
| 1 | feature | P3 | MCP `wiki_similar` tool (expose embedding search to MCP clients) | spec out-of-scope, follow-up |
| 2 | feature | P3 | Tags/dataview inside code blocks still unfiltered | carried from previous retro |
| 3 | feature | P3 | Section validation in wiki_write | carried from previous retro |
| 4 | docs | P3 | SPEC §1.3 line 48 lists shipped features as out-of-scope (wiki_delete, auto-commit, ontology graph) | pre-existing canon violation, one-commit cleanup |

## Interview Transcript

Independence level: self-checklist
Rounds used: 0

Review was performed by a heterogeneous-model code-reviewer agent (Fable). The 13 findings (5M + 8L) were all addressed in the fix commit. No interview dispatches warranted — measurement is mechanical (test pass per criterion).

## Findings

### What Worked Well

- **Advisor pre-gate caught 7 design issues**: decoupling from `llm.backend`, keeping embeddings out of Add/CheckConsistency, default OFF, requiring a consumer, schema survival, and SPEC stale notice. All incorporated before user approval.
- **Code review caught PK design flaw (M5)**: `path TEXT PRIMARY KEY` silently destroyed vectors on model switch. Fixed to `(path, model)` composite PK before merge.
- **buildEmbedText poison prevention (M1)**: reviewer identified that read failures would embed the bare filename and mark it as "fresh" via content_hash match — a subtle staleness bug.

### What to Improve

- **First implementation had variable shadowing**: `var s scored` shadowed the receiver `s *SQLiteIndex` inside `searchSimilarEmbedding`. Go allows this but it's confusing. Caught in review.
- **Schema survival test was initially trivial**: the test re-called `initSchema()` without lowering `user_version`, so the drop-recreate branch never ran. A test that passes vacuously is worse than no test.
- **Post-ingest embed was unbatched**: the first version sent all stale paths in one request. Refactored to share the batching logic with `cogvault embed`.

### Process Observations

- Ollama `/api/embed` accepts `input` as a string array for batch embedding — this is the current API, not the legacy `/api/embeddings` (singular) endpoint.
- `modernc.org/sqlite` (pure Go) does not support sqlite-vec, confirming the decision to use brute-force cosine in Go. At personal wiki scale this is sub-millisecond.

## Lessons

1. "Default OFF for optional network dependencies — an empty config field means disabled, not 'try localhost'."
2. "Tests that pass trivially (re-running initSchema without changing preconditions) are worse than missing tests — they give false confidence about an invariant nobody actually checked."

## Compounding

Not attempted — no reusable lesson this cycle reached the bar for a standalone solution doc.

Retrospective complete — docs/retros/2026-08-08-embedding-search-retro.md
