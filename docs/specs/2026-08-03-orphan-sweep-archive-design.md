# F4: Orphan Sweep + Archive

```yaml
status: approved
created: 2026-08-03
tracker: F4
priority: P3
approach: C (orphan sweep with archive, no delete)
```

## Problem

The ingest ledger keys on `(source_path, content_hash)`. When a source file is renamed (`A.pdf` → `B.pdf`) or deleted, the ledger and wiki page for the old path persist:

- `lookup(B.pdf, H)` misses the existing `(A.pdf, H)` row → full LLM re-digest
- `supersedePrevSuccess(B.pdf)` does not touch `(A.pdf, H)` → two `success` rows
- Two wiki pages (`sources/a.md`, `sources/b.md`) with identical content appear in search results
- `sources/a.md` becomes an orphan whose source no longer exists on disk

The design spec (`docs/specs/2026-07-22-refound-capture-pipeline-design.md:130`) claims "hash-based dedup including renamed files" as a test target, but this was never implemented and contradicts the `(source_path, content_hash)` PK defined at line 126 of the same document.

Current state: 0 duplicate `content_hash` rows across different `source_path` values in the production ledger (101 total rows, verified 2026-08-03 — DB readable, query returned empty result set). The mechanism is needed for correctness, not to fix an active incident.

## Decision: Orphan Sweep + Archive (Option C)

Sweep ledger `success` rows whose source files no longer exist on disk. Archive (move, not delete) their wiki pages to `sources/_archived/`. The existing `exclude` + `CheckConsistency` infrastructure handles deindexing automatically.

Rejected alternatives:

- **Option A (accept + document only)**: leaves orphan pages searchable until manually cleaned up. Acceptable today (0 duplicates), but provides no automated path.
- **Option B (content-hash-first lookup)**: prevents re-digest but silently suppresses legitimate duplicates — the same PDF in two `sources[]` dirs would produce only one page. Worse than the problem it solves.
- **Option D (rename detection + migration)**: full fix (no re-digest, no orphan, history tracked) but requires page rename (write + delete tension with SPEC policy) and programmatic frontmatter rewriting. Complexity disproportionate to P3 priority.

## Mechanism

### Sweep phase (ingest pre-scan)

After acquiring the single-writer lock and before the main file loop, `Runner.Run` calls a new `sweepOrphans` method:

1. **Source-dir availability guard**: before checking individual files, `os.Stat` each configured `sources[].path`. If a source dir is missing, skip all ledger rows whose `source_dir` matches it. This prevents mass-archiving when a source directory is temporarily unavailable (e.g. external volume unmounted, user reorganizing Downloads).
2. Query ledger: `SELECT source_path, content_hash, source_dir, wiki_page FROM ingest_ledger WHERE status = 'success'`
3. For each row where `source_dir` is available (per step 1), check `os.Stat(source_path)`:
   - File exists → skip
   - `ErrNotExist` → candidate for archive
   - Other error → log warning, skip (do not archive on transient fs errors)
4. For each candidate:
   a. Archive `wiki_page` to `sources/_archived/<basename>-<hash8>.md` via `storage.Move` (hash8 = first 8 hex of content_hash, prevents archive collisions)
   b. If move succeeds: update ledger row to `status = 'superseded'`
   c. If move fails and the wiki page is already missing (manually deleted): still update ledger to `superseded`
   d. If move fails for another reason: log warning, skip
5. Report archived count in the run report

### Archive directory

`sources/_archived/` under `wiki_dir`. Created on first archive operation via `os.MkdirAll` (inside `storage.Move`).

### Storage.Move

Add `Move(src, dst string) error` to the `Storage` interface and implement in `FSStorage`. The method reuses `resolvePath` for both paths (symlink, traversal, abs-path rejection), acquires `fs.mu`, and calls `os.Rename`. This keeps all wiki_dir writes inside the storage boundary per DESIGN.md and decision 0006.

### Exclude list

Add `sources/_archived` to the default `exclude` list in `config.go:applyDefaults`. The `IsExcluded` function (`internal/adapter/pathutil.go:53`) uses `HasPathPrefix(rel, ex)` where `rel` is the relative path from wiki root. Since `_archived` alone would not match `sources/_archived` (no path-prefix relationship), the full relative path `sources/_archived` is required.

This makes `CheckConsistency` (triggered by `wiki_list`/`wiki_search`) skip the `sources/_archived/` subtree during Scan → archived pages fall into the `toRemove` set → deindexed from FTS/`file_meta`.

Users with an explicit `exclude:` in config.yaml would need to add `sources/_archived` manually if they want the default behavior. This matches the existing pattern for `.obsidian` and `.trash`.

### Report field

Add `Archived int` to the `Report` struct. The per-file list gets a new action `archived`.

### Dry-run behavior

When `--dry-run` is active, `sweepOrphans` identifies candidates and reports them as `would-archive` but does not move files or update the ledger.

## Doc fixes (bundled)

1. `docs/specs/2026-07-22-refound-capture-pipeline-design.md:130` — remove "including renamed files" (never implemented, contradicts line 126)
2. `SPEC.md` §10.2 — add: "A renamed or deleted source file's page and ledger row survive the immediate change. On the next ingest run, a pre-scan sweep archives the orphaned page to `sources/_archived/` and marks the ledger row `superseded`."
3. `docs/research/v2-follow-ups.md` F4 → Done with rationale

## Affected files

| File | Change |
|------|--------|
| `internal/storage/fs.go` | Add `Move(src, dst string) error` — resolves both paths, acquires mutex, calls `os.Rename` |
| `internal/storage/storage.go` | Add `Move` to `Storage` interface (if interface is defined here) |
| `internal/ingest/ingest.go` | Add `sweepOrphans` method, call from `Run` before main loop, add `Archived` to `Report`, add `actionArchived`/`actionWouldArchive` |
| `internal/ingest/ledger.go` | Add `successRows() ([]ledgerRow, error)` query |
| `internal/config/config.go` | Add `sources/_archived` to default `exclude` list |
| `SPEC.md` | §10.2 rename/archive behavior, §10.4 report `archived` count |
| `DESIGN.md` | §2.3 ingest flow update (sweep step), §2.1 storage interface (Move) |
| `docs/specs/2026-07-22-...design.md` | Remove "including renamed files" from Testing bullet |
| `docs/research/v2-follow-ups.md` | F4 → Done |

## Testing

- Unit: `sweepOrphans` with a mock filesystem — source exists (no-op), source missing (archived + superseded), source stat error (skipped), wiki page already missing (ledger updated, no move error), source dir missing (entire dir skipped)
- Unit: archive collision avoidance — two files with same slug but different content_hash produce distinct archive names
- Unit: `storage.Move` — path validation (traversal, symlink rejection), successful rename, cross-path error
- Unit: dry-run mode reports `would-archive` without side effects
- Unit: `sources/_archived` in default exclude list
- Integration: create a source file, ingest it, delete the source, run ingest again → page moved to `_archived/`, ledger row `superseded`, page absent from search results (use `CheckConsistency(force=true)` or zero consistency interval to avoid timing-dependent skip)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| **Source dir temporarily unavailable** (external volume unmounted, user reorganizing Downloads) — scheduled launchd job archives the entire wiki in one unattended pass | Medium | High | Source-dir availability guard: skip sweep for rows whose `source_dir` fails `os.Stat`. The guard runs before individual file checks |
| **iCloud eviction false positives** — an iCloud-evicted source file returns `ErrNotExist` | Low (source dirs are local `~/Downloads/_Articles`) | Medium | Archive is reversible (move back from `_archived/`). Document in SPEC that iCloud-synced source dirs may trigger false archiving |
| **Archive collision** — same slug archived twice with different content | Low | Low | Archive filename includes `-<hash8>` suffix from content_hash |

## Assumptions and Preconditions

| Claim | Command | Observed | Result | Source |
|-------|---------|----------|--------|--------|
| No existing duplicate content_hash rows | `sqlite3 ... "SELECT COUNT(*) FROM ingest_ledger"` then `"SELECT ... GROUP BY content_hash HAVING COUNT(DISTINCT source_path) > 1"` | 2026-08-03 | 101 total rows; 0 duplicate rows (empty result, not suppressed) | production ledger |
| `CheckConsistency` deindexes files not found by Scan | Read `internal/index/sqlite.go:397-401` | 2026-08-03 | `toRemove` = indexed keys remaining after Scan; each removed via `removeTx` | source code |
| `IsExcluded` uses `HasPathPrefix(rel, ex)` — requires full relative path for nested dirs | Read `internal/adapter/pathutil.go:49-59` | 2026-08-03 | `HasPathPrefix("sources/_archived", "sources/_archived")` matches; `HasPathPrefix("sources/_archived", "_archived")` does not | source code |
| Source dirs are local, not iCloud | User config: `sources[].path` | 2026-08-03 | `~/Downloads/_Articles` (local) | `~/.config/cogvault/config.yaml` |
| Storage has no Move method | Read `internal/storage/fs.go` | 2026-08-03 | Only Read/Write/List/Exists/Stat/WriteSchema; no move/rename | source code |

## Success Criteria

| # | Criterion | Proving command |
|---|-----------|-----------------|
| SC1 | Orphaned source → page archived + ledger superseded | Integration test: ingest, delete source, re-ingest, assert page in `sources/_archived/` and ledger row `superseded` |
| SC2 | Archived page absent from search results | Integration test: `CheckConsistency(force=true)` after archive, then search → no hit for archived page |
| SC3 | Dry-run reports `would-archive` without moving files | Unit test: verify report contains candidate, verify file unmoved |
| SC4 | Existing (non-orphaned) pages unaffected | Integration test: ingest with all sources present, verify 0 archived |
| SC5 | Missing source dir → skip sweep for that dir | Unit test: remove source dir, run sweep, verify 0 archived and warning logged |
| SC6 | `sources/_archived` in default exclude, SPEC/DESIGN updated | `grep sources/_archived internal/config/config.go SPEC.md DESIGN.md` |
