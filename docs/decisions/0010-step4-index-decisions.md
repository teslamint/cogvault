# 0010-step4-index-decisions

Status: accepted
Date: 2026-04-07

## Context

Step 4 implements the index layer (internal/index/) with SQLite FTS5. Several design decisions deviate from or extend the original SPEC/DESIGN documents.

## Decisions

### D1: Trust boundary — no direct filesystem access

Index never calls `os.Stat` or `os.ReadFile`. File content is read via `store.Read()` (respecting Storage's exclude_read policy). File enumeration uses `adpt.Scan()`. The `root` field is only passed to adapter methods.

→ Background: `docs/research/step4-index-review-notes.md`

### D2: Change detection via content_hash

Instead of mod_time comparison (DESIGN original), CheckConsistency uses SHA-256 content hash from `store.Read` raw bytes. This detects sub-second changes and avoids the precision issue of RFC3339 truncation. mod_time column is removed from file_meta.

→ Background: `docs/research/step4-index-review-notes.md`

### D3: FileMeta.ModTime removed (SPEC 6.3 deviation)

Without `os.Stat`, actual file modification time cannot be obtained. `IndexedAt` answers "when was this indexed" and `ContentHash` answers "has it changed". SPEC 6.3 and 6.4 updated accordingly.

→ Background: `docs/research/step4-index-review-notes.md`

### D4: Rebuild signature changed (SPEC 6.1 deviation)

`Rebuild(store, adpt)` instead of `Rebuild()`. Rebuild performs clear + CheckConsistency(force=true) internally, matching the name's semantics of "full rebuild."

### D5: FTS content = raw bytes

All paths (Add and CheckConsistency) store raw file content in FTS, including frontmatter YAML. This ensures hash consistency — the same bytes go to both hash and FTS. Frontmatter noise in search results is a known trade-off accepted for MVP. v0.2 may introduce body-only FTS normalization.

→ Background: `docs/research/step4-index-review-notes.md`

### D6: Lock scope minimization

CheckConsistency uses a 2-phase approach: lock-free IO-bound scan, then brief lock for DB apply. `atomic.Int64` for lock-free interval check. `ccMu` serializes CheckConsistency calls. `mu` (RWMutex) protects DB access. Search/GetMeta use RLock and are not blocked during the scan phase.

→ Background: `docs/research/step4-index-review-notes.md`

### D7: Failure semantics

Two failure phases with different semantics:

→ Background: `docs/research/step4-index-review-notes.md`

**Scan phase (collect)**: Per-file Parse/Read errors during scanning skip the file and accumulate errors in `errs`. The file's existing index state is preserved (not removed, not updated). These errors are returned alongside counts.

**Apply phase (single TX)**: All collected changes (toAdd, toUpdate, toRemove) are applied in a single transaction. If any apply operation fails, the entire transaction rolls back — all-or-nothing. No partial state.

- Scan errors (systemic): skip toRemove, return error, lastConsistency NOT updated (retry on next call).
- Per-file scan errors: lastConsistency IS updated (prevents retry storm).
- Apply errors: transaction rolled back, lastConsistency NOT updated.
- Scan + apply errors simultaneously: apply failure takes precedence — TX rolled back, lastConsistency NOT updated, scan-phase errs discarded.
- CheckConsistency error is transparent — caller decides how to handle.

### D8: SPEC 6.9 unchanged

"반환 전 정합성 보장" wording is not modified in Step 4. How upper-layer MCP handlers handle CheckConsistency errors (log + continue vs error propagation) is deferred to Step 5.

### D9: Scan callback sequential contract

Added interface comment to `adapter.Adapter.Scan`: "fn is called sequentially from a single goroutine." CheckConsistency relies on this for safe concurrent map access in the callback.

### D10: Trigram fallback to unicode61

If FTS5 trigram tokenizer is unavailable, falls back to unicode61. In this mode, all searches use LIKE fallback (no FTS5 MATCH). LIKE `%query%` satisfies SPEC 6.8 Korean support for all query lengths. Performance trade-off: O(rows × content_size) vs indexed lookup.

→ Background: `docs/research/step4-index-review-notes.md`

### D11: Path normalization

All paths stored and queried with forward slash (`/`) via `normalizePath()` = `filepath.Clean` + `\` → `/`. Applied to all public method inputs and CheckConsistency's Scan callback paths.

→ Background: `docs/research/step4-index-review-notes.md`

### D12: Tokenizer detection on existing DB

`initSchema` queries `sqlite_master` to detect actual tokenizer of existing `wiki_fts` table. `IF NOT EXISTS` would silently keep an old tokenizer while incorrectly setting `useTrigram=true`. Schema mismatch (e.g., old `mod_time` column) triggers full DROP + recreate of both tables.

### D13: BuildMeta uses frontmatter type

`meta["type"]` = `src.Frontmatter["type"].(string)` (page type like "source"), NOT `src.SourceType` (adapter name like "obsidian"). Both CheckConsistency and write-then-index (Step 5) must use `BuildMeta()` for consistency.

→ Background: `docs/research/step4-index-review-notes.md`

## Specification Changes

### SPEC.md

| Section | Change | Reason |
|---------|--------|--------|
| 6.1 | `Rebuild() → error` → `Rebuild(storage, adapter) → error` | Rebuild internally calls CheckConsistency(force=true), which requires Storage and Adapter. Without these parameters, Rebuild would be a destructive clear-only operation with no way to re-index. See D4. |
| 6.3 | `FileMeta.ModTime` removed | Index does not call os.Stat (D1). No reliable source for actual file mtime. IndexedAt + ContentHash cover the original purposes of ModTime (temporal ordering and change detection). See D2, D3. |
| 6.4 | `file_meta.mod_time` column removed | Matches 6.3 FileMeta change. Column had no writer. |

### DESIGN.md

| Section | Change | Reason |
|---------|--------|--------|
| 2.5 struct | Added `root`, `ccMu`, `useTrigram`, `atomic.Int64`. Changed `sync.Mutex` → `sync.RWMutex`. | `root`: adapter call passthrough (D1). `ccMu`: CheckConsistency serialization without blocking reads (D6). `useTrigram`: trigram/unicode61 runtime detection (D10). `atomic.Int64`: lock-free interval check (D6). `RWMutex`: concurrent reads during scan phase (D6). |
| 2.5 schema | Removed `mod_time` column. Added trigram fallback comment. | D2, D3, D10. |
| 2.5 algorithm | Replaced 2-step (stat→scan) with Scan-based single pass + content_hash. Added lock-free interval check, ccMu serialization, 2-phase collect/apply. | Original algorithm used os.Stat for deletion/change detection. New algorithm uses Scan as the sole file enumerator (D1) and content_hash for change detection (D2). Lock scope minimized for concurrent read access (D6). |
| 2.5 new items | Added Rebuild, BuildMeta, path normalization, error handling descriptions. | These were implicit or missing in the original DESIGN. Made explicit after review rounds revealed ambiguity. |
| 6 concurrency | Updated lock model: RWMutex, ccMu, per-method lock types. | Original model had single Mutex. New model separates read/write (RWMutex) and CheckConsistency serialization (ccMu). See D6. |

## Related Files

- `internal/index/index.go` — interface + types
- `internal/index/sqlite.go` — SQLiteIndex implementation
- `internal/index/sqlite_test.go` — test suite
- `SPEC.md` — sections 6.1, 6.3, 6.4 updated
- `DESIGN.md` — sections 2.5, 6 updated
- `docs/decisions/0009-step3-deferred-items.md` — exclude contract test (D9 of 0009) fulfilled by TestCheckConsistencyExcludeContract
- `docs/research/step4-index-review-notes.md` — rejected alternatives, decision evolution, deferred items
