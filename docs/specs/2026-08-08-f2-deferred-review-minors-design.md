# F2 Deferred Review Minors — Design Spec

Status: draft
Date: 2026-08-08
Owner: v2-follow-ups.md F2

## Context

The v2 capture pipeline review (PR #3, 2026-07-22) triaged 27 minor findings as
follow-ups (F2 in v2-follow-ups.md). One (U6-m6) was fixed by orphan sweep
(PR #14). This spec covers the remaining 26 items as a single batch.

Design phase was authored directly (not via `compound-loop:designing` skill)
due to the batch nature of the work — each item was already defined and scoped
in the original review.

## Scope

### Group A — Dead code / unused params (2 items)

| ID | Fix |
|----|-----|
| U2-m1 | Remove `allowDot` param from `validatePath` — both callers pass `false` |
| U3-m2 | Remove `_ = size` dead assignment in `TestStatDirectory` (`storage/fs_test.go:169`) |

### Group B — Error handling (5 items)

| ID | Fix |
|----|-----|
| U2-m2 | `Save()`: add `os.MkdirAll` for parent dir; wrap `WriteFile` error |
| U2-m4 | `expandTilde`: return `(string, error)` so callers surface `UserHomeDir` failure instead of silently using unexpanded path. Callers that ignore the error will fail at compile time — desired forcing function |
| U6-m4 | `recordFailure`: log upsert error via `log.Printf` instead of discarding with `_ =` |
| U6-m7 | After write-success/index-fail: **options for gate** — (a) leave file, rely on next-run overwrite + document (index-fail is classInfra → attempt spared → self-heals); (b) reorder to ledger-before-index; (c) delete orphan page. Recommend (a) — deletion contradicts repo precedent (no `wiki_delete`, orphan sweep archives instead of deleting) |
| U7-m1 | CLI `ingest.go`: print partial report before returning error from `runner.Run` |

### Group C — Test coverage (9 items)

| ID | Fix |
|----|-----|
| U2-m5 | Add config tests: `~x/` literal prefix not expanded; `db_path == wiki_dir` rejected |
| U3-m1 | Add `storage/fs_test.go` test: `Stat` rejects symlink targets (returns `ErrSymlink`). This is the storage layer, not ingest |
| U4-m3 | Add index test: same-size+same-mtime+changed-content triggers re-index (negative boundary test for mtime gate) |
| U5-m1 | Tighten argv assertion to exact-equality (not `Contains`) |
| U5-m2 | Assert stdin includes `PageSlug` and instruction shape |
| U5-m4 | Add test: empty-success-result from LLM classified as permanent failure |
| U6-m1 | Add ingest test: (1) symlink entry silently skipped (current behavior — no PerFile entry, assert silent-skip); (2) renamed-file-dedup — close as superseded by F4 orphan-sweep tests (PR #14 covers the archive+supersede path) |
| U9-m1 | Supersede assertion: bind hash→status, not just count statuses |

### Group D — Behavioral edge cases (5 items)

| ID | Fix |
|----|-----|
| U4-m1 | Partial fix: pass `int64(len(content))` as size in `Add()` instead of 0. Mtime requires interface changes — accept one extra read on mtime mismatch and add code comment |
| U5-m3 | Per-subtype classification instead of blanket flip. Current code: `is_error:true` → all `ErrTransient` (line 107). Fix: keep transient for subtypes that indicate server pressure (`overloaded_error`, rate limits); classify unknown/unrecognized subtypes as transient with a warning log (safe default). Do NOT blanket-flip to permanent — that would burn retry cap on genuinely transient API pressure |
| U5-m5 | Fix misleading error **text**: when parent context is cancelled, `cmd.Run()` returns "signal: killed" which reaches the user as the error message. Detect `ctx.Err() == context.Canceled` (same pattern as existing `DeadlineExceeded` check at line 58) and return "context cancelled" instead. Classification stays transient — already correct |
| U6-m5 | Success row: reset `attempts` to 0 instead of inheriting prior failed count. Attempt count is informational beyond max-attempts gating; resetting on success is semantically correct |
| U7-m2 | `--dry-run`: skip LLM adapter validation so claude binary isn't required on PATH. Affects CLI init path, not adapter interface |

### Group E — Documentation of accepted limitations (2 items)

| ID | Fix |
|----|-----|
| U4-m2 | Code comment: mtime `RFC3339Nano` includes TZ offset — cross-TZ processes may produce different strings for the same moment, disabling the mtime gate (safe direction: extra re-index, not missed updates) |
| U6-m3 | Code comment: TOCTOU window between hash and LLM read is accepted; symlink `Lstat`/read window in `resolvePath` also accepted. Note: storage layer already has a comment at `fs.go:65-67` for the `resolvePath` TOCTOU; this adds the ingest-layer hash window |

### Group F — Test refactoring (2 items)

| ID | Fix |
|----|-----|
| U9-m3 | Consolidate `setupIngestVault` (integration) and `newIngestVault` (CLI) into one shared helper |
| U9-m4 | Extract production DSN construction into a shared `testDSN(dbPath)` helper, remove hardcoded string in busy-timeout test |

### Group G — Optional hardening (1 item)

| ID | Fix |
|----|-----|
| U7-m5 | Add `AdditionalProperties: false` to MCP tool JSON schemas where applicable |

### Group H — Style (1 item)

| ID | Fix |
|----|-----|
| U2-m3 | Improve `db_path` error message readability (minor wording) |

### Closed (1 item, no action)

| ID | Reason |
|----|--------|
| U6-m6 | Fixed by orphan sweep PR #14 — deleted-source ledger rows superseded |

## Success criteria

1. All 26 open items addressed (code change, test, or documented decision)
2. Full test suite passes with no regressions
3. Behavioral changes limited to those explicitly listed in Groups D and B (U7-m1). SPEC §4.2 reviewed if U5-m3 changes failure-class semantics; update if needed

## Risks

- **U2-m4** (`expandTilde` signature change): compile-time breakage at all call sites — desired forcing function, but verify no unchecked call sites remain
- **U5-m3**: must NOT blanket-flip `is_error:true` to permanent. Per-subtype table required. `overloaded_error` and rate-limit subtypes must stay transient
- **U6-m5** (attempts reset): changes ledger row content — safe because attempt count is informational beyond max-attempts gating
- **U4-m1** (partial fix): size=`len(content)` eliminates one axis of mismatch; mtime mismatch causes one extra read per MCP-written page per reindex cycle — accepted

## Out of scope

- F3 (SQLITE_BUSY_SNAPSHOT) — separate follow-up
- New feature work or contract changes beyond those listed
