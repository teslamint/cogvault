---
schema: plan/v1
title: "F4: Orphan sweep + archive"
type: feat
status: approved
body_seal: 1a92b68f4df0b381f5c60b651e5d532309b49ac61d92ed582e3dd4eb5f4116ee
date: 2026-08-03
execution: code
origin: docs/specs/2026-08-03-orphan-sweep-archive-design.md
---

## Goal

Ingest pre-scan detects ledger `success` rows whose source file no longer exists, moves their wiki pages to `sources/_archived/`, and marks them `superseded`. Existing `exclude` + `CheckConsistency` handles deindexing.

## Architecture notes

**Decision: `Storage.Move` respects the storage boundary.** `os.Rename` called directly from ingest bypasses `resolvePath` (symlink/traversal rejection) and `fs.mu` (write mutex, decision 0006). Add `Move(src, dst string) error` to the `Storage` interface. Implementation in `FSStorage`: resolve both paths, acquire mutex, `os.MkdirAll` for destination parent, `os.Rename`. Both paths are relative to wiki root — same filesystem, atomic.

**Decision: exclude path is `sources/_archived`, not `_archived`.** `IsExcluded` (`internal/adapter/pathutil.go:53`) uses `HasPathPrefix(rel, ex)`. The Scan callback receives paths relative to root (e.g. `sources/_archived/foo.md`). `_archived` alone does not prefix-match `sources/_archived`. Verified against `HasPathPrefix` implementation at `pathutil.go:49`.

**Decision: source-dir availability guard prevents mass-archive.** Before checking individual files, `os.Stat` each configured `sources[].path`. Skip all ledger rows whose `source_dir` matches a missing dir. Prevents the scheduled launchd job from archiving everything when a source directory is temporarily unavailable.

**Decision: archive filenames include `-<hash8>` to prevent collisions.** A source archived twice (different content versions) would overwrite. Use `<basename>-<hash8>.md` where hash8 is first 8 hex of `content_hash` from the ledger row.

**Known pattern: harness test setup.** `internal/ingest/ingest_test.go` uses a `harness` struct with `newHarness(t, types, mockLLM)` that creates temp dirs, config, real FSStorage + SQLiteIndex, and a mock LLM. New sweep tests follow this pattern.

## Assumption Recheck

Origin spec: `docs/specs/2026-08-03-orphan-sweep-archive-design.md`

| Claim | Recheck | Outcome |
|-------|---------|---------|
| 0 duplicate content_hash rows | `SELECT COUNT(*)` = 101, duplicate query = empty | match |
| Storage has no Move method | `grep -c Move storage.go` = 0 | match |
| `IsExcluded` uses `HasPathPrefix` | `pathutil.go:49-59` unchanged | match |
| Source dirs are local | config unchanged | match |

## File structure

| File | Action | Responsibility |
|------|--------|----------------|
| `internal/storage/storage.go` | Modify | Add `Move(src, dst string) error` to interface |
| `internal/storage/fs.go` | Modify | Implement `Move` — resolvePath both, mutex, MkdirAll, os.Rename |
| `internal/ingest/report.go` | Modify | Add `Archived int`, `actionArchived`, `actionWouldArchive`, update `String()` |
| `internal/ingest/ledger.go` | Modify | Add `successRows() ([]ledgerRow, error)` |
| `internal/ingest/ingest.go` | Modify | Add `sweepOrphans`, call from `Run` before main loop |
| `internal/config/config.go` | Modify | Add `sources/_archived` to default exclude |
| `SPEC.md` | Modify | §10.2 rename/archive behavior, §10.4 archived count |
| `DESIGN.md` | Modify | §2.3 storage Move, §2.7 ingest sweep step |
| `docs/specs/2026-07-22-refound-capture-pipeline-design.md` | Modify | Remove "including renamed files" |
| `docs/research/v2-follow-ups.md` | Modify | F4 → Done |

## Carry-forward trigger audit

Tracker: `docs/research/v2-follow-ups.md`

| Row | Trigger class | Fired? | Disposition |
|-----|---------------|--------|-------------|
| F2 (deferred review minors) | event-based (batch cleanup) | No | Not relevant to this plan |
| F3 (FTS BUSY_SNAPSHOT) | drift-based (collision in logs) | No | Not relevant |
| F4 (spec self-contradiction) | edit-based (SPEC/design spec touch) | Yes — this plan edits SPEC.md §10.2 and the design spec | Folded in as U4 |
| F5 (dead code cleanup) | edit-based (contentHash, fixtures, MCP) | No — files not in this plan's edit list | Not relevant |

Attestation: all 4 open tracker rows examined against file structure above. F4 is the only fired trigger and is the plan's primary deliverable.

## Scenario coverage map

No User Scenarios section in origin spec. Coverage verified through unit + integration tests per SC1-SC6.

## Implementation Units

### U1: Storage.Move + tests
Execution note: test-first
Files:
  Modify: `internal/storage/storage.go`, `internal/storage/fs.go`
  Test: `internal/storage/fs_test.go`
Interfaces:
  Produces: `Storage.Move(src, dst string) error`
  Reuses: `resolvePath`, `fs.mu`, `os.MkdirAll`, `os.Rename`
Test scenarios:
  happy: move `sources/a.md` to `sources/_archived/a-abcd1234.md` — file at dst, gone from src
  edge: destination parent dir does not exist — created automatically; src does not exist — returns error wrapping `ErrNotFound`
  error: src path traversal (`../escape`) — rejected `ErrTraversal`; dst path traversal — rejected; src is symlink — rejected `ErrSymlink`
Steps:
  1. Add `Move(src, dst string) error` to `Storage` interface in `storage.go`
  2. In `fs.go`, implement `FSStorage.Move`: call `resolvePath(src)` and `resolvePath(dst)`, acquire `fs.mu`, `os.MkdirAll(filepath.Dir(absDst), 0o755)`, `os.Rename(absSrc, absDst)`. Map `os.ErrNotExist` to `cverr.ErrNotFound`. Block moves to/from `_schema.md` path
  3. Add `mockStorage.Move` stub to `internal/mcp/tools_test.go` mock (interface compliance)
  4. Write tests in `internal/storage/fs_test.go`: happy path, missing src, traversal rejection, symlink rejection, auto-mkdir
  5. Run `go test ./internal/storage/ ./internal/mcp/`
Acceptance: all tests pass, `go vet ./...` clean

### U2: Ledger successRows + report fields + config exclude
Execution note: test-first
Files:
  Modify: `internal/ingest/ledger.go`, `internal/ingest/report.go`, `internal/config/config.go`
  Test: `internal/ingest/ledger_test.go` (if exists, else inline in ingest_test.go), `internal/config/config_test.go`
Interfaces:
  Produces: `ledger.successRows() ([]ledgerRow, error)`, `Report.Archived`, `actionArchived`, `actionWouldArchive`
Test scenarios:
  happy: `successRows` returns rows with status=success; `sources/_archived` in `Config.AllExcluded()` output
  edge: no success rows → empty slice; explicit user exclude list without `sources/_archived` — only user entries returned (user override)
Steps:
  1. In `ledger.go`, add `successRows`:
     ```go
     func (l *ledger) successRows() ([]ledgerRow, error) {
         rows, err := l.db.Query(
             `SELECT source_path, content_hash, source_dir, digested_at, wiki_page, status, attempts, last_error, run_origin, llm_model
              FROM ingest_ledger WHERE status = 'success'`)
         // scan into []ledgerRow, return
     }
     ```
  2. In `report.go`, add `Archived int` to `Report`, add `actionArchived = "archived"` and `actionWouldArchive = "would-archive"` constants, update `String()` format line to include `archived=%d`
  3. In `config.go:applyDefaults`, add `"sources/_archived"` to the default `Exclude` slice alongside `.obsidian` and `.trash`
  4. Run `go test ./internal/ingest/ ./internal/config/`
Acceptance: `successRows` returns expected rows; `Report.String()` includes archived count; `AllExcluded()` contains `sources/_archived`

### U3: sweepOrphans + integration tests
Execution note: test-first
Files:
  Modify: `internal/ingest/ingest.go`
  Test: `internal/ingest/ingest_test.go`
Interfaces:
  Consumes: `ledger.successRows()`, `storage.Move()`, `ledger.upsert()` (existing)
  Produces: `Runner.sweepOrphans(report *Report, dryRun bool)`
Test scenarios:
  happy: source deleted → page moved to `_archived/<slug>-<hash8>.md`, ledger row `superseded`, report.Archived == 1 (Covers SC1)
  happy: all sources present → 0 archived, no side effects (Covers SC4)
  happy: dry-run → report shows `would-archive`, no file moved, ledger unchanged (Covers SC3)
  edge: source dir missing → skip all rows for that dir, log warning (Covers SC5)
  edge: wiki page already deleted manually → ledger still updated to `superseded`, no move error
  edge: source stat returns non-ErrNotExist error → skip, log warning
  integration: ingest file, delete source, re-ingest → archived page not in search results (Covers SC2 — use `CheckConsistency(force:true)` or zero consistency interval)
Steps:
  1. In `ingest.go`, add `sweepOrphans(report *Report, dryRun bool)`:
     ```go
     func (r *Runner) sweepOrphans(report *Report, dryRun bool) {
         // 1. Check source-dir availability
         availableDirs := map[string]bool{}
         for _, src := range r.cfg.Sources {
             dir := filepath.Clean(src.Path)
             if _, err := os.Stat(dir); err == nil {
                 availableDirs[dir] = true
             } else {
                 slog.Warn("sweep: source dir unavailable, skipping", "dir", dir, "error", err)
             }
         }

         // 2. Query success rows
         rows, err := r.ledger.successRows()
         if err != nil {
             slog.Warn("sweep: ledger query failed", "error", err)
             return
         }

         // 3. Check each row
         for _, row := range rows {
             if !availableDirs[row.sourceDir] {
                 continue
             }
             if _, err := os.Stat(row.sourcePath); err == nil {
                 continue // source exists
             } else if !errors.Is(err, os.ErrNotExist) {
                 slog.Warn("sweep: stat error, skipping", "path", row.sourcePath, "error", err)
                 continue
             }

             // Source is gone — archive candidate
             if dryRun {
                 report.Archived++
                 report.PerFile = append(report.PerFile, FileResult{
                     Path: row.sourcePath, Action: actionWouldArchive,
                 })
                 continue
             }

             // Move wiki page to _archived
             base := filepath.Base(row.wikiPage)
             ext := filepath.Ext(base)
             name := strings.TrimSuffix(base, ext)
             dst := "sources/_archived/" + name + "-" + row.contentHash[:8] + ext

             moveErr := r.store.Move(row.wikiPage, dst)
             if moveErr != nil && !errors.Is(moveErr, cverr.ErrNotFound) {
                 slog.Warn("sweep: move failed", "src", row.wikiPage, "dst", dst, "error", moveErr)
                 continue
             }

             // Update ledger → superseded
             row.status = "superseded"
             if err := r.ledger.upsert(row); err != nil {
                 slog.Warn("sweep: ledger update failed", "path", row.sourcePath, "error", err)
                 continue
             }

             report.Archived++
             report.PerFile = append(report.PerFile, FileResult{
                 Path: row.sourcePath, Action: actionArchived,
             })
         }
     }
     ```
  2. In `Runner.Run`, call `r.sweepOrphans(report, opts.DryRun)` after lock acquisition and before the schema read / main file loop
  3. Add `import "log/slog"` if not already imported
  4. Write unit tests using the existing harness pattern: `TestSweepOrphansSourceDeleted`, `TestSweepOrphansAllPresent`, `TestSweepOrphansDryRun`, `TestSweepOrphansSourceDirMissing`, `TestSweepOrphansWikiPageAlreadyGone`
  5. Write integration test `TestSweepOrphansSearchExclusion`: ingest a file, delete source, re-run ingest, search for the page title → 0 results
  6. Run `go test -race ./internal/ingest/`
Acceptance: all tests pass; `go test -race ./...` clean

### U4: Doc updates
Execution note: skip-test-first
Files:
  Modify: `SPEC.md`, `DESIGN.md`, `docs/specs/2026-07-22-refound-capture-pipeline-design.md`, `docs/research/v2-follow-ups.md`
Steps:
  1. `SPEC.md` §10.2 — after the existing page-identity paragraph, add: "A renamed or deleted source file's page and ledger row survive the immediate change. On the next ingest run, a pre-scan sweep archives the orphaned page to `sources/_archived/<slug>-<hash8>.md` and marks the ledger row `superseded`. The `sources/_archived` directory is excluded from indexing."
  2. `SPEC.md` §10.4 — add `archived` to the report counts list
  3. `DESIGN.md` §2.3 — add `Move(src, dst string) error` to the storage interface description; note: resolves both paths, acquires mutex, MkdirAll + os.Rename
  4. `DESIGN.md` §2.7 — add sweep step: "Before the main file loop, `sweepOrphans` queries success rows and archives pages whose source files no longer exist. Source-dir availability guard prevents mass-archive when a directory is temporarily unavailable."
  5. `docs/specs/2026-07-22-refound-capture-pipeline-design.md:130` — change "hash-based dedup including renamed files" to "hash-based dedup"
  6. `docs/research/v2-follow-ups.md` F4 — update to Done with rationale: "Resolved via orphan sweep + archive (Option C). Pages for missing sources archived to `sources/_archived/`, ledger rows superseded. SPEC §10.2 documents rename behavior. Design spec's 'including renamed files' removed (never implemented)."
  7. Commit all doc changes together
Acceptance: `grep sources/_archived SPEC.md DESIGN.md` returns matches; design spec no longer mentions "renamed files" in testing bullet

## Mutation/failure-state matrix

No stateful ceremony in the deliverable; no mutation/failure-state matrix required.

## Open unknowns

### Implementation-time
- Exact `slog` import path — `log/slog` (Go 1.21+; verify go.mod min version)
- Whether `ledger_test.go` exists as a separate file or tests are inline in `ingest_test.go` — adapt placement accordingly

## Deferred to Follow-Up Work

- Orphan page problem for **deleted sources** (not renamed) has the same symptoms. This plan's sweep handles both cases identically — no separate follow-up needed.
- The stale `contentHash()` function in ingest (F5) is not touched by this plan. Remains in F5.
