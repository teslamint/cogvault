---
schema: plan/v1
title: "Ingest attention report: status command and push notification"
type: feat
status: draft
date: 2026-08-25
execution: code
origin: docs/specs/2026-08-24-ingest-attention-report-design.md
---

## Goal

Add two channels for surfacing attention-worthy ingest failures: a `cogvault
status` CLI command (pull) and macOS notifications on newly terminal files
during scheduled runs (push). Users currently have no signal when a source file
permanently fails to digest.

## Architecture notes

**Config-only bootstrap for `status`**: the existing `bootstrap()` opens both
the index SQLite DB and the wiki storage. `status` needs only the config (for
`DBPath` and `LLM.Model`) and the ledger. A dedicated config-only load path
avoids creating index files as a side effect. Pattern: `resolveConfigPath` →
`config.Load` → check `os.Stat(cfg.DBPath)` → `openLedger` (or exit clean
when DB absent).

**Package boundary**: `ledger` and `ledgerRow` are unexported types in
`internal/ingest`. The `status` command lives in `cmd/cogvault` (package
main) and cannot access them. Solution: export an `AttentionRow` struct with
JSON tags and an `AttentionRows(dbPath, model string) ([]AttentionRow, error)`
function from `internal/ingest`. This function encapsulates the DB-absent
guard (`os.Stat`), ledger open, query, and type conversion — U4 calls one
function and receives exported, JSON-serializable data. The unexported
`ledger.attentionRows` remains internal and returns `[]ledgerRow`; the
exported wrapper converts to `[]AttentionRow`.

**Display status mapping**: the ledger stores exhausted files as
`status='failed'` with `attempts >= maxAttempts`. The `status` command and
notification must display this as `exhausted`, not `failed`. The
`AttentionRows` function performs this mapping: `failed + attempts >= 3` →
`Status: "exhausted"` in `AttentionRow`.

**Timestamp display**: ledger stores `digested_at` as UTC RFC3339Nano. The
`status` command's human output displays local time (`2006-01-02 15:04`).

**Latest-row CTE**: the attention query must use `MAX(digested_at)` per
`source_path` to exclude files whose latest ledger row is `success` or
`superseded`. Since `digested_at` is RFC3339Nano text with potentially
variable-length fractional seconds, the CTE uses `MAX(rowid)` as a tiebreak
to guarantee determinism. Test fixtures use second-precision timestamps.

**NewAttention population**: `recordFailure` populates `Report.NewAttention`
when the result is terminal and was not already terminal for the current model.
The prior-state checks (`wasAlreadyExhausted` / `wasAlreadyRefused`) are
defense-in-depth — the skip branches in `Run` already prevent terminal rows
from reaching `recordFailure` — so tests exercise them via direct
`recordFailure` calls, not end-to-end.

**Notify ownership**: `Runner` holds an injectable `notifyFunc` field
(defaults to `osascriptNotify` on darwin, no-op otherwise). The caller gates
on `--scheduled` before calling `runner.Notify(report)`.

**Error-prefix derivation**: notification body uses the substring after the
first colon in `last_error` (trimmed, max 60 chars). Stage prefixes like
`validate:` or `digest:` are uninformative alone.

## Assumption Recheck

Origin spec retains five live assumptions. Recheck results:

| Claim | Command | Outcome | Fresh result |
|---|---|---|---|
| `actionExhausted` set only in skip branch | `rg actionExhausted internal/ingest/ingest.go` | match | Line 157 only |
| `actionRefused` set in both skip and `recordFailure` | `rg actionRefused internal/ingest/ingest.go` | match | Lines 149, 354 |
| No `cogvault status` command exists | `rg 'StatusCmd\|statusCmd\|newStatusCmd' --glob '*.go'` | match | No matches |
| `openLedger` executes DDL | Code inspection `ledger.go:50-64` | match | CREATE TABLE + ALTER on every open |
| `supersedePrevSuccess` targets only `status='success'` | `rg supersedePrevSuccess internal/ingest/ledger.go` | match | `WHERE status = 'success'` only |

No contradictions; no unavailable evidence.

## File structure

### New files

| File | Responsibility |
|---|---|
| `cmd/cogvault/status.go` | `cogvault status` subcommand: config load, call `ingest.AttentionRows`, format output |
| `cmd/cogvault/status_test.go` | Status command tests (human + JSON output, missing DB) |
| `internal/ingest/attention.go` | Exported `AttentionRow` struct + `AttentionRows` wrapper (DB-absent guard, status mapping) |
| `internal/ingest/attention_test.go` | Test exported `AttentionRows` including DB-absent and status mapping |
| `internal/ingest/notify_darwin.go` | `osascriptNotify` function (build tag `//go:build darwin`) |
| `internal/ingest/notify_other.go` | No-op notifier (build tag `//go:build !darwin`) |
| `internal/ingest/notify_test.go` | Notification format and gating tests |

### Modified files

| File | Change |
|---|---|
| `cmd/cogvault/main.go` | Register `newStatusCmd()` |
| `cmd/cogvault/ingest.go` | Call `runner.Notify(report)` when `--scheduled` and `err == nil` |
| `internal/ingest/ledger.go` | Add unexported `attentionRows(model string) ([]ledgerRow, error)` with latest-row CTE |
| `internal/ingest/ledger_test.go` | Test `attentionRows` with seeded rows |
| `internal/ingest/report.go` | Add `NewAttention []FileResult` field |
| `internal/ingest/ingest.go` | Populate `NewAttention` in `recordFailure`; add `Notify` method + injectable `notifyFunc` + `NewAttention` population in `Runner` |
| `internal/ingest/ingest_test.go` | Test `NewAttention` population paths |
| `SPEC.md` | Add §9.13 `status` command contract |

## Scenario coverage map

| S-ID | Unit chain | Evidence |
|---|---|---|
| S1 — Scheduled run, new failure, push notification | U1 → U2 → U3 → U4 → U5 | `TestNewlyExhaustedNotifies` (Covers S1) |
| S2 — Known failure, no repeat notification | U2 → U3 | `TestAlreadyExhaustedNoNotify` (Covers S2) |
| S3 — Manual status check | U1 → U4 | `TestStatusHumanOutput` (Covers S3) |
| S4 — Problem resolved | U1 | `TestAttentionExcludesResolvedPath` (Covers S4) |
| S5 — Clean state | U4 | `TestStatusCleanOutput` (Covers S5) |
| S6 — Machine-readable output | U4 | `TestStatusJSONOutput` (Covers S6) |
| S7 — Source file deleted while exhausted | U1 | `TestAttentionIncludesDeletedSource` (Covers S7) |
| S8 — Interactive run, no push | U3 | `TestInteractiveNoNotify` (Covers S8) |

## U1: Ledger attention query

Execution note: test-first

Files:
  Modify: `internal/ingest/ledger.go`
  Test: `internal/ingest/ledger_test.go`

Interfaces:
  Consumes: `ledger.db` (existing `*sql.DB`)
  Produces:
    - unexported: `func (l *ledger) attentionRows(model string) ([]ledgerRow, error)`
    - exported: `type AttentionRow struct { Path, Status, Error, LastAttempt, Model string; Attempts int }` (JSON-tagged)
    - exported: `func AttentionRows(dbPath, model string) ([]AttentionRow, error)` — encapsulates DB-absent guard, ledger open, query, `failed→exhausted` mapping, UTC→local timestamp conversion

Test scenarios:
  happy: seeded ledger with 1 exhausted + 1 refused row for current model → returns both (Covers S3)
  edge: same source_path has old exhausted row + newer success row → excluded (Covers S4)
  edge: same source_path has two failed hashes → only the latest-rowid row returned
  edge: deleted source file with exhausted row → still returned (Covers S7)
  edge: exhausted row for different model → excluded
  error: empty ledger → returns empty slice
  integration: n/a — leaf unit

Steps:
  1. Write failing test `ledger_test.go::TestAttentionRows` with seeded rows covering happy + 4 edge + error cases. Use second-precision RFC3339 timestamps for deterministic ordering.
  2. Run tests; confirm failure (method does not exist).
  3. Implement unexported `attentionRows` in `ledger.go`:
     ```sql
     WITH latest AS (
       SELECT source_path, MAX(rowid) AS max_id
       FROM ingest_ledger
       WHERE (source_path, digested_at) IN (
         SELECT source_path, MAX(digested_at) FROM ingest_ledger GROUP BY source_path
       )
       GROUP BY source_path
     )
     SELECT l.source_path, l.content_hash, l.digested_at, l.last_error,
            l.attempts, l.llm_model, l.status
     FROM ingest_ledger l
     JOIN latest lt ON l.rowid = lt.max_id
     WHERE l.llm_model = ?
       AND ((l.status = 'failed' AND l.attempts >= 3)
            OR l.status = 'refused');
     ```
  4. Run tests; confirm pass, no regressions.
  5. Create `attention.go` with exported `AttentionRow` struct (JSON tags: `path`, `status`, `error`, `last_attempt`, `llm_model`, `attempts`) and `AttentionRows(dbPath, model string) ([]AttentionRow, error)`:
     - `os.Stat(dbPath)`: if absent, return `nil, nil`.
     - `openLedger(dbPath)` → `defer l.close()`.
     - Call `l.attentionRows(model)`.
     - Convert: `failed + attempts >= 3` → Status `"exhausted"`; `refused` → Status `"refused"`. Parse `digested_at` to local time string.
  6. Write `attention_test.go::TestAttentionRowsExported` covering DB-absent → nil, exported fields, status mapping.
  7. Run tests; confirm pass.
  8. Commit: `feat(ingest): add ledger attention query with latest-row semantics`

Acceptance: `go test ./internal/ingest/ -run TestAttention -v` passes all cases.

## U2: Report NewAttention population

Execution note: test-first

Files:
  Modify: `internal/ingest/report.go`, `internal/ingest/ingest.go`
  Test: `internal/ingest/ingest_test.go`

Interfaces:
  Consumes: `recordFailure` internal method (existing), `maxAttempts` const, `failureClass` type
  Produces: `Report.NewAttention []FileResult` (new field)

Test scenarios:
  happy: permanent failure reaching maxAttempts → in NewAttention (Covers S1)
  edge: permanent failure with prev already exhausted for current model → NOT in NewAttention (direct recordFailure call; unreachable through Run — defense-in-depth)
  edge: refused with no prior refused row → in NewAttention
  edge: refused with prior refused row for current model → NOT in NewAttention (direct recordFailure call)
  edge: permanent failure with attempts > maxAttempts after model change (carryover 3→4) → in NewAttention
  error: transient/infra failure → NOT in NewAttention regardless of attempts
  integration: n/a — leaf unit

Steps:
  1. Add `NewAttention []FileResult` to `Report` in `report.go`.
  2. Write failing tests in `ingest_test.go::TestNewAttention*` covering all 6 scenarios. Tests call `recordFailure` directly with constructed `prev` and `Report`.
  3. Run tests; confirm failure (NewAttention not populated).
  4. In `recordFailure`, after the existing ledger upsert:
     - If `class == classPermanent` and `attempts >= maxAttempts`: check `wasAlreadyExhausted` (prev non-nil, prev.llmModel matches, prev.attempts >= maxAttempts). If not already exhausted, append to `report.NewAttention`.
     - If `class == classRefused`: check `wasAlreadyRefused` (prev non-nil, prev.status == "refused", prev.llmModel matches). If not already refused, append to `report.NewAttention`.
  5. Run tests; confirm pass.
  6. Commit: `feat(ingest): populate NewAttention in report for newly terminal files`

Acceptance: `go test ./internal/ingest/ -run TestNewAttention -v` passes all cases.

## U3: Notification dispatch (darwin + no-op)

Execution note: test-first

Files:
  Create: `internal/ingest/notify_darwin.go`, `internal/ingest/notify_other.go`, `internal/ingest/notify_test.go`
  Modify: `internal/ingest/ingest.go`, `cmd/cogvault/ingest.go`

Interfaces:
  Consumes: `Report.NewAttention []FileResult`
  Produces: `func (r *Runner) Notify(report *Report)` method; `Runner.notifyFunc` injectable field

Test scenarios:
  happy: NewAttention has 1 entry → notifyFunc called with correct title and body (Covers S1)
  happy: NewAttention has 3 entries → body shows first file + `외 2건`
  edge: NewAttention is empty → notifyFunc not called (Covers S2)
  edge: error-prefix extraction — `"digest: llm.Digest ...: claude policy refusal"` → `"llm.Digest ...: claude policy refusal"` (truncated to 60 chars)
  error: notifyFunc returns error → logged as warning, no propagation
  integration: `runIngest` calls `Notify` only when `--scheduled` and `err == nil` (Covers S8)

Steps:
  1. Add `notifyFunc func(title, body string) error` field to `Runner` in `ingest.go`. In `New`, set default via a package-level `defaultNotify` variable.
  2. Create `notify_darwin.go` with build tag `//go:build darwin`:
     ```go
     func osascriptNotify(title, body string) error {
         return exec.Command("osascript", "-e",
             fmt.Sprintf(`display notification %q with title %q`, body, title)).Run()
     }
     func init() { defaultNotify = osascriptNotify }
     ```
  3. Create `notify_other.go` with build tag `//go:build !darwin`:
     ```go
     func init() { defaultNotify = func(_, _ string) error { return nil } }
     ```
  4. Add `Notify(report *Report)` method on `Runner`: extract `NewAttention`, format title (`cogvault ingest`) and body (error-prefix after first colon, max 60 chars; `외 N건` for multiple), call `notifyFunc`.
  5. Write tests in `notify_test.go` covering all 6 scenarios. Tests inject a capturing `notifyFunc`.
  6. In `cmd/cogvault/ingest.go`, after `runner.Run` returns: if `scheduled && err == nil && report != nil`, call `runner.Notify(report)`.
  7. Run all tests; confirm pass, no regressions.
  8. Commit: `feat(ingest): add macOS notification for newly terminal ingest files`

Acceptance: `go test ./internal/ingest/ -run TestNotify -v` passes; `go build ./cmd/cogvault/` succeeds on darwin and (cross-compile) linux.

## U4: `cogvault status` command

Execution note: test-first

Files:
  Create: `cmd/cogvault/status.go`, `cmd/cogvault/status_test.go`
  Modify: `cmd/cogvault/main.go`

Interfaces:
  Consumes: `config.Load` (existing), `ingest.AttentionRows` (U1 exported wrapper)
  Produces: `cogvault status [--config path] [--json]` CLI command

Test scenarios:
  happy: seeded ledger with attention rows → human-readable table with `exhausted`/`refused` labels and local timestamps (Covers S3)
  happy: same rows + `--json` → valid JSON with `attention` array, all fields present (Covers S6)
  edge: DB file absent → `주의 필요 항목 없음.` without creating DB
  edge: empty attention → clean output (Covers S5)
  error: invalid config path → error
  integration: registered in root command (Covers S3)

Steps:
  1. Write failing tests in `status_test.go` covering all 6 scenarios. Tests seed a temp DB via `openLedger` (same-package test or test helper), then call `runStatus`.
  2. Implement `status.go`:
     - `newStatusCmd()` with `--json` flag.
     - `runStatus`: load config via `resolveConfigPath` + `config.Load` (not `bootstrap` — no index/storage side effects). Call `ingest.AttentionRows(cfg.DBPath, cfg.LLM.Model)` — this handles DB-absent guard internally and returns `[]AttentionRow` with exported, JSON-tagged fields. Format output:
     - Human format: `주의 필요: <N>건\n  <status>  <filename>  <error>  (<timestamp>)\n` or `주의 필요 항목 없음.\n`. Status is already mapped (`exhausted`/`refused`), timestamp already local.
     - JSON format: `json.NewEncoder(cmd.OutOrStdout()).Encode(...)` with `{"attention": [...], "model": "<model>"}`.
  3. Register `newStatusCmd()` in `main.go`.
  4. Run tests; confirm pass.
  5. Commit: `feat(cli): add cogvault status command for attention-worthy ingest items`

Acceptance: `go test ./cmd/cogvault/ -run TestStatus -v` passes; `cogvault status --help` shows usage.

## U5: SPEC.md documentation

Execution note: skip-test-first

Files:
  Modify: `SPEC.md`

Interfaces:
  Consumes: n/a
  Produces: §9.13 `status` command contract

Steps:
  1. Update §9 preamble: change "Every command except `fetch` opens the index" to "Every command except `fetch` and `status` opens the index" — `status` loads config only, not `bootstrap()`.
  2. Add §9.13 to SPEC.md after the last CLI command section. Document: usage (`cogvault status [--config path] [--json]`), what it queries (ledger attention rows for current model with latest-row-per-source-path semantics), output formats (human + JSON), DB-absent behavior (clean output, no DB creation), and that it does not acquire the ingest lock or open the index.
  3. Self-review against the implemented behavior in U1 and U4.
  4. Commit: `docs(spec): add §9.13 cogvault status contract`

Acceptance: `rg 'cogvault status' SPEC.md` matches the new section; §9 preamble mentions `status` in the exception list.

## Carry-forward trigger audit

Audited `docs/research/v2-follow-ups.md` at commit `3be3992`: 18 open rows (F1–F18), all status Done. 0 fired, 0 unobservable.

No durable carry-forward tracker row has an edit-based trigger matching any file in this plan's file structure. No event-based trigger is relevant to this feature.

## Mutation/failure-state matrix

No stateful ceremony in the deliverable; no mutation/failure-state matrix required.

## Deferred to Follow-Up Work

- **Stale rows for deleted source files**: `sweepOrphans` handles only success rows. Exhausted/refused rows for deleted source files persist in `cogvault status`. Future enhancement: `--dismiss <path>` flag or automatic source-existence check.
- **Transient/infra silent exhaustion after model change**: push notification misses this edge case. Pull (`cogvault status`) catches it. See spec Architecture section.
- **Notification click action**: plain notification, no Terminal deep link.

## Open unknowns

### Planning-time (resolved)

None — all architectural decisions resolved in spec.

### Implementation-time (deferred)

- Exact `attentionRows` SQL may need tuning after testing with real ledger data (tiebreak behavior with rowid).
- `defaultNotify` package variable initialization pattern — the `init()` approach in build-tagged files may need adjustment if test isolation requires per-test overrides (injectable `notifyFunc` on Runner handles this, but the default-setting mechanism may vary).
