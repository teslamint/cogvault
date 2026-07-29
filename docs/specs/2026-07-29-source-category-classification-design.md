# F8: Source Category Classification

```yaml
status: draft
created: 2026-07-29
tracker: F8
priority: P2
approach: A (category frontmatter field, type:source preserved)
```

## Problem

Phase 1 forces every ingested page to `type: source`, but the real corpus (91 pages) mixes articles, court rulings, terms of service, and technical documents. Without classification, browsing and filtering by content kind is impossible — all pages look identical in `wiki_list` and `wiki_search` results.

Evidence (F8 tracker, 2026-07-23 backlog): ~10 of 63 pages are reference material, not articles. Examples: 판례 95도250/97도508 (court rulings), 땡겨요 서비스 이용약관 (ToS), BOK reports.

## Decision: `category` field, not `type` change

SPEC §11 mandates `type: source` for ingest-generated pages. Changing `type` breaks the schema contract and requires migrating all existing pages. A separate `category` frontmatter field preserves the contract while adding classification.

Rejected: per-source-dir config-based classification (Approach C). The user's corpus lives in a single directory (`~/Downloads/_Articles`), making directory-level classification useless.

## Category Taxonomy

Three categories, default `article`:

| Category | Description | Corpus examples |
|---|---|---|
| `article` | News, opinion, analysis, blog posts, newsletters, reports | NYT, Bloomberg, otterletter, BOK |
| `legal` | Court rulings, legislation, terms of service, privacy policies, regulations | 판례 95도250, 땡겨요 약관 |
| `reference` | Technical documentation, API docs, framework guides, standards, manuals | (future: framework docs) |

The LLM picks one based on content. If uncertain, `article`.

## User Scenarios

1. **New ingest run**: `cogvault ingest` digests a PDF. The LLM classifies it as `legal` and generates frontmatter `category: legal`. The page lands in `sources/` with the category indexed.

2. **wiki_search with category visibility**: `wiki_search("판례")` returns results with `category: "legal"` alongside `type: "source"`. An MCP client can display or filter by category.

3. **wiki_list browsing**: `wiki_list("sources/")` returns entries with `category` field. Server-side filtering by category is out of scope — clients filter locally from the returned list.

4. **Existing pages**: Pages ingested before F8 have `category: ""` in index results. They remain fully functional; category is informational, not structural.

5. **CLI search**: `cogvault search "약관"` output includes category in the result line (e.g., `(source/legal)` instead of `(source)`). Current format in `cmd/cogvault/search.go:56-61`: `fmt.Sprintf("  (%s)", r.Type)`.

## Architecture

### LLM prompt change

`internal/llm/claudecode.go` `buildPrompt` (line 145-156):

Current:
```
Output ONLY a markdown wiki page (no preamble). Begin with YAML frontmatter carrying
the fields title, type: source, source_path: <path>, and ingested_at set to today's
date in ISO 8601 (YYYY-MM-DD).
```

New (append after existing instruction):
```
Also include category: <one of article, legal, reference> based on the document content.
article = news, opinion, analysis, blogs, newsletters, reports.
legal = court rulings, legislation, terms of service, privacy policies, regulations.
reference = technical docs, API docs, framework guides, standards, manuals.
Default to article if uncertain.
```

The category instruction lives in `buildPrompt` (code), not in `_schema.md`. This makes it deterministic regardless of the user's wiki `_schema.md` version. `readSchema()` (ingest.go:323) reads from `store.Read(cfg.SchemaPath())` and falls back to `schema.DefaultContent`; since `init` is idempotent and does not overwrite existing `_schema.md`, existing installs keep the old schema text. The prompt-level instruction ensures classification works for all users.

### Schema (`_schema.md`)

Update `internal/schema/default_schema.md` (the `go:embed` asset) to document `category`:
```
- source 페이지 필수 frontmatter: title, type: source, source_path, ingested_at
- source 페이지 선택 frontmatter: category (article | legal | reference)
```

Note: existing installs must manually update their wiki `_schema.md` or delete+reinit to pick up this documentation change. The digestion prompt carries the category instruction independently.

### Index schema (`internal/index/sqlite.go`)

Bump `schemaVersion` from 2 to 3. Add `category TEXT DEFAULT ''` to the `file_meta` CREATE TABLE statement. On first `CheckConsistency` call after upgrade (triggered by search, serve, or init), `initSchema` drops+recreates tables when `user_version < schemaVersion`, then `CheckConsistency` re-indexes all 91 pages from disk. Existing pages lack `category` in frontmatter, so they get `category = ''` — same end state as ALTER TABLE but follows the established migration pattern (DESIGN §2.5).

### Meta extraction

Both `BuildMeta` functions updated to extract and normalize `category`:

**`internal/index/sqlite.go` `BuildMeta`** (used by CheckConsistency):
```go
category, _ := src.Frontmatter["category"].(string)
category = normalizeCategory(category)
```

**`internal/ingest/ingest.go` `buildMeta`** (used by digest):
```go
category, _ := fm["category"].(string)
category = normalizeCategory(category)
```

**Normalization** (`NormalizeCategory`): lowercase + coerce off-taxonomy values to `article`. Empty string and non-string YAML values stay `""` (marks pre-F8 pages). The LLM may emit `Article`, `Legal`, or unexpected values; normalization ensures consistent grouping. Exported from `internal/index` (used by both `BuildMeta` and `ingest.buildMeta` since ingest already imports `internal/index`).

### Index `addTx`

Extract `category` from meta, store in `file_meta`:
```go
category := meta["category"]
// INSERT OR REPLACE INTO file_meta(..., category) VALUES (..., ?)
```

### Result structs (`internal/index/index.go`)

```go
type Result struct {
    Path     string  `json:"path"`
    Title    string  `json:"title"`
    Type     string  `json:"type"`
    Category string  `json:"category"`
    Snippet  string  `json:"snippet"`
    Score    float64 `json:"score"`
}

type FileMeta struct {
    Path        string `json:"path"`
    Title       string `json:"title"`
    Type        string `json:"type"`
    Category    string `json:"category"`
    ContentHash string `json:"content_hash"`
    IndexedAt   string `json:"indexed_at"`
}
```

### Search queries

Both search paths updated:

1. **`searchFTS`** (line 255): `SELECT f.path, f.title, f.type, f.category, snippet(...)` + `rows.Scan` adds `&r.Category`.
2. **`searchLIKE`** (line 288): `SELECT f.path, f.title, f.type, f.category, wiki_fts.content` + `rows.Scan` adds `&r.Category`.

### `GetMeta` query

`GetMeta` (line 200): add `category` to SELECT and `FileMeta` scan.

### MCP tools

- **wiki_list** (`tools.go:122-126`): add `r["category"] = meta.Category` alongside existing title/type.
- **wiki_search**: `Result` serialized via JSON; `Category` field appears automatically.
- **wiki_parse**: frontmatter returned as-is; no change needed.

### CLI search

`cmd/cogvault/search.go:56-61`: change display format. When `category` is non-empty, format as `(type/category)` — e.g., `(source/legal)`. When empty, keep current `(source)`.

### Ingest validation

`parsePage` does NOT require `category`. Missing category is not a permanent failure — backward compatible with LLM outputs that omit it.

### Canon updates

**SPEC.md**:
- §8.4 `wiki_list`: add `category` to return shape `[{path, name, is_dir, title, type, category}]`.
- §8.5 `wiki_search`: add `category` to return shape `[{path, title, type, category, snippet, score}]`.
- §11: add `category` as optional source-page frontmatter field with taxonomy.

**DESIGN.md**:
- §2.5: update `schemaVersion` to 3; add `category` to `file_meta` column list; update §5 table `user_version=2` → `user_version=3`.
- §2.6: update `buildPrompt` description to include category instruction.

## Data Model

No ledger schema change. Category lives in:
1. Wiki page frontmatter (source of truth)
2. `file_meta.category` column (indexed cache for search/list)

## Integration

- Ingest → `buildPrompt` (category instruction) → LLM → frontmatter → `buildMeta` (normalize) → storage.Write → index.Add → file_meta
- CheckConsistency → adapter.Parse → frontmatter → `BuildMeta` (normalize) → file_meta
- MCP wiki_list/wiki_search → file_meta → JSON result with category

## Testing Strategy

1. **LLM prompt**: verify `buildPrompt` output contains category instruction.
2. **Index schema v3**: test that a v2 DB triggers drop+recreate and the new table has `category`.
3. **Index Add/Search**: test `Add` with category in meta map; verify both `searchFTS` and `searchLIKE` return category. Verify `GetMeta` returns category.
4. **Category normalization**: `"Legal"` → `"legal"`, `"unknown"` → `"article"`, `""` → `""` (pre-F8 marker preserved).
5. **Ingest**: mock LLM returns page with `category: legal`; verify `buildMeta` extracts and normalizes it.
6. **Ingest validation**: page without `category` still passes validation.
7. **MCP**: `wiki_list` and `wiki_search` results include `category` field.
8. **CLI**: search output displays `(source/legal)` when category present, `(source)` when empty.

## Risks and Mitigations

| Risk | Mitigation |
|---|---|
| LLM misclassifies content | 3 broad categories reduce ambiguity; default `article` is harmless |
| LLM emits unexpected category | `normalizeCategory` coerces off-taxonomy values to `article` |
| Existing pages lack category | Empty string is valid; no breakage, just missing metadata |
| Schema v3 triggers full re-index | 91 pages, stat-gated reads; one-time cost on first startup after upgrade |
| Live `_schema.md` stale | Category instruction in `buildPrompt` (code), not schema; MCP agents see category in results regardless |

## Success Criteria

Setup: one fresh ingest of a legal-category PDF (e.g., a court ruling or ToS document).

1. **Classification and visibility**: the newly ingested page has `category: legal` in frontmatter, `cogvault search` shows `(source/legal)`, and `wiki_list`/`wiki_search` MCP results include the `category` field. **Proving commands**: `grep '^category:' <page>`, `cogvault search "<term>"`, integration test assertions.
2. **Backward compatibility**: existing 91 pages remain searchable with `category: ""`. **Proving command**: `cogvault search "비트코인"` returns results.
3. **Tests pass**: `go test -race ./...`

## Open Decisions

None. Taxonomy expansion (adding categories) requires only a `buildPrompt` change and a `normalizeCategory` update — no schema or contract change, since `category` is stored as a free-form string with code-level normalization.
