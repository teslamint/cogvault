# F2 Deferred Review Minors — Implementation Plan

Spec: docs/specs/2026-08-08-f2-deferred-review-minors-design.md (approved)
Date: 2026-08-08

## Implementation units

Ordered by dependency: foundational changes first, then dependents.

### Unit 1: config cleanup (U2-m1, U2-m2, U2-m3, U2-m4, U2-m5)

Files: `internal/config/config.go`, `internal/config/config_test.go`

1. Remove `allowDot` param from `validatePath`; update 2 callers
2. `Save()`: add `os.MkdirAll(filepath.Dir(configPath), 0o755)` before WriteFile; wrap error
3. `db_path` error message wording improvement
4. `expandTilde`: change signature to `(string, error)`; update `applyDefaults` and all callers to propagate error
5. Add tests: `~x/` not expanded; `db_path == wiki_dir` rejected

Verify: `go test ./internal/config/...`

### Unit 2: storage test (U3-m1, U3-m2)

Files: `internal/storage/fs_test.go`

1. Remove `_ = size` dead assignment in `TestStatDirectory` (line 169)
2. Add test: `Stat` on a symlink returns `ErrSymlink`

Verify: `go test ./internal/storage/...`

### Unit 3: index mtime gate (U4-m1, U4-m2, U4-m3)

Files: `internal/index/sqlite.go`, `internal/index/sqlite_test.go`

1. `Add()`: pass `int64(len(content))` instead of `0` for size; add comment for mtime="" accepted limitation
2. Add code comment about TZ offset in mtime (U4-m2)
3. Add negative boundary test: same-size+same-mtime+changed-content triggers re-index

Verify: `go test ./internal/index/...`

### Unit 4: LLM adapter (U5-m1, U5-m2, U5-m3, U5-m4, U5-m5)

Files: `internal/llm/claudecode.go`, `internal/llm/claudecode_test.go`

1. Add `context.Canceled` check alongside existing `DeadlineExceeded` (line 58) — return "context cancelled" text, keep ErrTransient
2. U5-m3: keep `is_error:true` as ErrTransient (current behavior is correct for known subtypes — overloaded_error, rate limits). Add log warning for unknown subtypes. No classification change needed
3. Tighten argv test: exact-equality instead of Contains
4. Assert stdin includes PageSlug and instruction shape
5. Add test: empty-success-result classified as permanent (line 116-117 already returns plain error, not ErrTransient — verify test)

Verify: `go test ./internal/llm/...`

### Unit 5: ingest core (U6-m1, U6-m3, U6-m4, U6-m5, U6-m7)

Files: `internal/ingest/ingest.go`, `internal/ingest/ingest_test.go`

1. `recordFailure`: replace `_ =` with `log.Printf` for upsert error
2. Success row: `attempts: 0` instead of `attemptsOf(prev)` (line 311)
3. U6-m7: add code comment documenting self-heal path (accept option a — index-fail is classInfra, attempt spared, next run overwrites)
4. Add code comment: TOCTOU hash-vs-LLM-read window accepted (U6-m3)
5. Add test: symlink entry silently skipped during scan
6. U6-m1 renamed-file-dedup: close as superseded by F4 orphan-sweep tests

Verify: `go test ./internal/ingest/...`

### Unit 6: CLI + dry-run (U7-m1, U7-m2)

Files: `cmd/cogvault/ingest.go`, `cmd/cogvault/cli_test.go`

1. Print partial report before returning error (lines 62-66)
2. `--dry-run`: skip LLM adapter creation/validation (nil adapter acceptable when DryRun=true)

Verify: `go test ./cmd/cogvault/...`

### Unit 7: MCP hardening (U7-m5)

Files: `internal/mcp/*.go`

1. Add `AdditionalProperties: false` to tool JSON schemas where applicable

Verify: `go test ./internal/mcp/...`

### Unit 8: integration test refactoring (U9-m1, U9-m3, U9-m4)

Files: `cmd/cogvault/ingest_integration_test.go`, `cmd/cogvault/cli_test.go`

1. Consolidate `setupIngestVault` + `newIngestVault` into one shared helper
2. Extract `testDSN(dbPath)` helper; replace hardcoded DSN string
3. Supersede assertion: bind hash→status check

Verify: `go test ./cmd/cogvault/... -run Integration`

## Execution order

Units 1-5 are independent (different packages). Unit 6 depends on Unit 5 (ingest changes). Unit 7 is independent. Unit 8 is independent.

Parallel-safe: Units 1, 2, 3, 4, 5, 7 can run concurrently. Then Unit 6. Then Unit 8.

Sequential implementation order: 1 → 2 → 3 → 4 → 5 → 6 → 7 → 8, commit per unit.

## Verification

After all units: `go test ./...` full suite, `go vet ./...`, `go build ./...`
