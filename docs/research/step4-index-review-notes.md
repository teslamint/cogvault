# Step 4 Index Layer — Review Notes & Decision Context

Date: 2026-04-07

This document captures context from the Step 4 planning and review process (8 self-review rounds + 10 Codex review rounds) that cannot be derived from code, SPEC, or DESIGN alone. It supplements `docs/decisions/0010-step4-index-decisions.md`.

---

## Why content_hash instead of mod_time for change detection

**Original DESIGN**: `os.Stat` mod_time comparison.

**Problem chain** (discovered during Codex R1):
1. Using `os.Stat` in Index makes it a "second filesystem authority," bypassing Storage's trust boundary.
2. Without `os.Stat`, there's no cheap way to get file modification time.
3. `time.RFC3339` truncates to seconds — sub-second changes are missed (Codex R1 medium).
4. Storing `time.Now()` as mod_time confuses "file modification time" with "indexing time" (Codex R2 medium).

**Rejected alternatives**:
- `meta["mod_time"]` as hidden side channel in Add → rejected because SPEC 6.10 only defines title/type/tags; adding undocumented keys creates layer drift (Codex R2 medium).
- `Storage.Stat()` method → would require changing Storage interface (Step 2 change), rejected for scope.
- `adapter.Source.ModTime` field → adapter doesn't stat files, only reads them.

**Resolution**: Remove mod_time entirely. content_hash (SHA-256 of raw bytes via `store.Read`) is the sole change detection mechanism. `IndexedAt` replaces mod_time's informational role.

**Trade-off accepted**: Every file is read on every CheckConsistency. Benchmarked at ~4ms/100 files on Apple M4 Max. v0.2 may add mod_time pre-filter if large vault performance becomes an issue.

---

## Why raw bytes for FTS content (not body-only)

**Problem** (discovered during Codex R2 HIGH):
- CheckConsistency reads files via `store.Read` (raw bytes including frontmatter).
- `adapter.Parse.Content` strips frontmatter and returns body only.
- If hash basis differs between paths (raw vs body), every frontmatter file triggers unnecessary re-indexing on every consistency check.

**Rejected alternative**: FTS content = Parse body, hash = raw bytes.
- Requires `addLocked` to accept both rawData and ftsContent separately.
- Public `Add` would need adapter access to extract body → breaks "pure indexing API" contract.
- Two data representations flowing through the same code path invite future mismatch.

**Resolution**: FTS content = raw bytes everywhere. Same bytes go to hash and FTS. Frontmatter YAML appears in search index — accepted as MVP trade-off. Typical frontmatter is small; YAML syntax (`---`, `:`) rarely matches real search queries.

---

## Why all-or-nothing TX for apply (not best-effort per-item)

**Evolution** (4 iterations):
1. Original plan: "단일 TX apply" (single TX)
2. First implementation: per-item TX (each addLocked/removeLocked had own TX)
3. Review found phantom transaction bug — removed outer TX, left per-item
4. Codex R11 caught 3-way drift: plan said per-item, DESIGN said single TX, code was per-item

**Why all-or-nothing won**:
- Per-item TX allows partial state: 3 out of 5 files committed, then crash → index is inconsistent in a way that's hard to diagnose.
- Single TX: either all changes commit or none do. After rollback, the index is in its previous known-good state.
- CheckConsistency's scan phase already handles per-file failures (skip + accumulate errors). The apply phase only processes files that were successfully scanned. Apply failures indicate DB-level issues (disk full, corruption), not per-file problems. All-or-nothing is appropriate for DB-level failures.

**Implementation**: `addTx`/`removeTx` accept `*sql.Tx`. CheckConsistency creates one TX, passes it to all apply operations, commits once at the end. First failure → return error → `defer tx.Rollback()` cleans up.

---

## Why ccMu.Lock (blocking) instead of TryLock

**Original proposal**: `ccMu.TryLock()` — if another CheckConsistency is running, skip (return 0,0,0,nil).

**Problem** (Codex R4 HIGH): `force=true` callers expect immediate execution (SPEC 6.9). TryLock would silently skip a forced consistency check if another one is in progress. This breaks the "즉시 실행" contract.

**Resolution**: `ccMu.Lock()` (blocking wait). Concurrent callers wait for the running check to finish. The lock-free interval check (`atomic.Int64`) before `ccMu.Lock` ensures most calls return immediately without touching ccMu at all.

---

## Why Index doesn't store root as a "filesystem authority"

**Codex R1 HIGH** flagged that `root` in SQLiteIndex makes Index a second filesystem authority alongside Storage.

**Resolution**: `root` is stored but its usage is restricted to two call sites:
- `adpt.Scan(s.root, ...)` — adapter enumerates files
- `adpt.Parse(s.root, ...)` — adapter extracts metadata

Index never passes `root` to `os.Stat`, `os.ReadFile`, or any stdlib filesystem function. All file content comes through `store.Read()`, respecting Storage's exclude_read policy. The adapter was already designed to receive root per-call; Index merely holds it to avoid adding root to the CheckConsistency signature (which would break SPEC 6.1).

**Step 5 prerequisite**: `docs/decisions/0009-step3-deferred-items.md` requires a root factory in Step 5 that ensures Storage and Index receive the same validated root.

---

## Why SPEC 6.9 is not modified in Step 4

Multiple Codex rounds debated whether to change "반환 전 정합성 보장" to acknowledge partial failures.

**Rejected in Step 4** because:
- SPEC 6.9 describes MCP tool behavior (`wiki_list`, `wiki_search`), not Index layer behavior.
- How upper-layer handlers react to CheckConsistency errors (log + continue vs propagate) is a Step 5 MCP handler decision.
- Changing SPEC 6.9 without implementing the handler behavior creates a spec-implementation gap in the opposite direction.

**Step 4 contract**: CheckConsistency returns `(counts, error)` transparently. Error non-nil means some files could not be processed. The Index itself remains usable (last-known-good state). What the MCP handler does with this error is Step 5's scope.

---

## Tokenizer detection — why not just CREATE IF NOT EXISTS

**Problem** (Codex R11): `CREATE VIRTUAL TABLE IF NOT EXISTS ... tokenize='trigram'` succeeds even if the table already exists with `unicode61`. The `IF NOT EXISTS` clause skips creation entirely, so `useTrigram` would be set to `true` based on the DDL attempt succeeding, not the actual tokenizer in use.

**Resolution**: Query `sqlite_master` for the actual CREATE statement and check for 'trigram'. This is authoritative — it reads what SQLite actually created, not what we tried to create.

**Schema migration**: If `file_meta` has a `mod_time` column (old schema), both `wiki_fts` and `file_meta` are DROPped and recreated. This is safe because Rebuild (called during init) will re-index everything. Dropping only `file_meta` would leave orphaned FTS rows; dropping both ensures consistency.

---

## SourceType vs frontmatter type — a near-miss bug

**Codex R11 HIGH**: The plan mapped `src.SourceType` to `meta["type"]`. But `Source.SourceType` is the adapter name (`"obsidian"` / `"markdown"`), while SPEC expects page type from frontmatter (`"source"`, `"entity"`, etc.).

This was caught in review, not by tests. If shipped, `wiki_list` and `wiki_search` would show `type: "obsidian"` for every file instead of `type: "source"`.

**Resolution**: `BuildMeta()` extracts `src.Frontmatter["type"].(string)`. Both CheckConsistency and write-then-index (Step 5) must use this function for consistency. Exported so Step 5's MCP handler can reuse it.

---

## Path normalization scope

**Problem** (Codex R13): Stored paths use `/` (forward slash) after normalization, but `adapter.Scan` returns paths with OS separator (`filepath.Rel` result). On Windows, `a\b.md` in Scan vs `a/b.md` in DB would cause every file to appear as "new" on every consistency check.

**Scope of normalization**: Applied at every boundary:
- Public methods (Add, Remove, GetMeta): normalize input path
- CheckConsistency Scan callback: normalize callback path before indexed map lookup
- Scope filter: `normalizePath(cfg.WikiDir) + "/%"`

**Why `strings.ReplaceAll(cleaned, "\\", "/")` instead of `filepath.ToSlash`**: On macOS/Linux, `filepath.ToSlash` is a no-op because `\` is a valid filename character, not a separator. To handle cross-platform paths consistently (e.g., paths stored on Windows, read on Linux), explicit replacement is needed.

---

## Deferred to Step 5

These items are explicitly NOT resolved in Step 4:

1. **MCP handler error policy**: How `wiki_search`/`wiki_list` handle CheckConsistency errors (log + continue, propagate, warnings field).
2. **SPEC 6.9 wording**: "반환 전 정합성 보장" may need qualification for partial failure.
3. **MCP read-path latency**: Synchronous CheckConsistency before search causes interval-expiry-duration delay. Async patterns or background consistency are Step 5 design choices.
4. **FTS body-only normalization**: v0.2 may strip frontmatter from FTS content to reduce search noise.
5. **Root factory**: Single factory ensuring Storage and Index receive the same validated root (0009 deferred item).
