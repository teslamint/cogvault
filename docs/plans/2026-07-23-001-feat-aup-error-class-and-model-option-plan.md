---
schema: plan/v1
title: F6 — AUP-refusal error class + llm.model option
type: feat
status: draft
date: 2026-07-23
execution: code
origin: docs/specs/2026-07-23-aup-error-class-and-model-option-design.md
---

# F6 Plan — AUP-refusal error class + llm.model option

## Goal

Stop Claude CLI AUP/policy refusals from being retried forever (classify them as a new terminal `refused` ingest class) and add a `llm.model` config knob passed to `claude --model`, with a per-row recorded model so switching models auto-recovers refused files on the next run.

## Architecture notes

- **Refusal detection (internal/llm)**: `resultEvent` gains `TerminalReason string \`json:"terminal_reason"\``. New exported `var ErrRefused = errors.New("claude policy refusal")`. In `parseResult`, refusal detection runs **before** the existing `is_error`/`error_during_execution` transient branch (claudecode.go:90-92): a final result event whose `TerminalReason == "api_error"` OR whose `Result` matches the refusal signature returns `ErrRefused` regardless of `subtype`/`is_error` (the observed refusal has `is_error:false`, so it would otherwise slip into the success path). Signature helper `isRefusalText(s)`: true when `s` (trimmed) has prefix `"API Error:"` OR contains `"safeguards flagged"`. In the non-zero-exit branch (claudecode.go:56-64), check `isRefusalText` against **both** stdout and stderr before returning; a match → `ErrRefused`, else the current `ErrTransient`.
- **Model passthrough (internal/llm)**: `ClaudeCode` gains an unexported `model string`; a functional option `WithModel(string)` or a second constructor param sets it (choose `NewClaudeCode(binPath, model string)` — single call site at cmd/cogvault/ingest.go:41, so a signature change is clean and avoids an options package). `digest` builds argv as `--print --output-format json --allowedTools Read` and appends `--model <model>` when `model != ""`.
- **Ingest class + retry (internal/ingest)**: new `classRefused` in the failureClass enum. `digestOne`'s error branch maps `errors.Is(err, llm.ErrRefused)` → `classRefused` (checked before the generic `classPermanent` default, alongside the existing `ErrTransient` check). `recordFailure` writes ledger status `refused` for `classRefused` (and does NOT increment attempts — refused is terminal in one occurrence). `Report` gains `Refused int` and a new `actionRefused` action; `String()` gains `refused=%d`.
- **Model-gated terminal retry (internal/ingest + ledger)**: `ledgerRow` gains `llmModel string`; `lookup` SELECT and `upsert` INSERT include `llm_model`; both success and failure `upsert` calls set it from the configured model. `Run`'s found-row branch (ingest.go:121-135) changes: `success`/`superseded` → always skip (Unchanged); `refused` or (`failed` with attempts ≥ maxAttempts) → skip ONLY when `prev.llmModel == configuredModel` (raw string ==), else fall through to re-digest. The configured model is read from `r.cfg.LLM.Model` (Runner already holds cfg).
- **Ledger migration (internal/ingest/ledger.go)**: `CREATE TABLE IF NOT EXISTS` is a no-op on the existing 63-row DB, so `openLedger` must, after the CREATE, run `PRAGMA table_info(ingest_ledger)`, and if no `llm_model` column is present, execute `ALTER TABLE ingest_ledger ADD COLUMN llm_model TEXT NOT NULL DEFAULT ''`. This runs before any `lookup` reads the column. The ledger's DDL stays independent of the index layer's `user_version` drop-and-recreate (internal/index/sqlite.go), which only touches `wiki_fts`/`file_meta`.
- **Config (internal/config)**: `LLMConfig` gains `Model string \`yaml:"model"\``. No validation (passthrough); `applyDefaults` leaves it empty. KnownFields(true) already admits it once the struct field exists.
- **Known Patterns** (docs/solutions, CONCEPTS.md): error classes are canonical vocabulary — CONCEPTS.md "Error classes (ingest)" goes 3-way → 4-way. Error wrapping stays `fmt.Errorf("pkg.Fn %s: %w", ...)`. Ledger owns its own DB connection with DSN busy_timeout (docs/solutions/database-issues/sqlite-pool-pragma-and-busy-snapshot.md) — the ALTER runs on that same handle.

## Assumption Recheck

Origin spec retains four live assumptions; rechecked at planning time 2026-07-23.

| Approved claim | Fresh evidence | Outcome |
|---|---|---|
| `claude --model <alias>` supported | `claude --help` shows `--model <model>` | match |
| claudecode.go:56-64 nonzero-exit → transient; 90-92 is_error/error_during_execution → transient | re-read at HEAD: lines confirmed (digest() 52-64, parseResult() 90-92) | match |
| `LLMConfig` has only `Backend` today (Model addition is additive) | config.go: `type LLMConfig struct { Backend string }` | match |
| A Fable-5 AUP refusal surfaces as subtype success + is_error false + terminal_reason api_error + "API Error:" body; also as exit-1 in the backlog | not re-runnable without spending an LLM call and reproducing an AUP trip (the field `terminal_reason` exists — confirmed value "completed" on success; the `api_error` value relies on the 2026-07-23 validation observation) | unavailable — dual-signal detection (terminal_reason OR body) makes a miss degrade to transient (retried), bounding the risk; carried as an implementation-time note |

## File structure

- `internal/llm/claudecode.go` (+claudecode_test.go, testdata/bin/claude) — refusal detection, ErrRefused, model arg. Modify.
- `internal/llm/llm.go` — `ErrRefused` sentinel (sibling to ErrTransient). Modify.
- `internal/config/config.go` (+config_test.go) — `LLMConfig.Model`. Modify.
- `internal/ingest/ledger.go` (+ledger_test.go) — `llm_model` column, ALTER migration, lookup/upsert. Modify.
- `internal/ingest/ingest.go`, `report.go` (+ingest_test.go) — classRefused, model-gated retry, Report.Refused. Modify.
- `cmd/cogvault/ingest.go` (+cli_test.go / ingest_integration_test.go) — pass `cfg.LLM.Model` to `NewClaudeCode`. Modify.
- `CONCEPTS.md` — 4-way error classes. Modify.

## Scenario coverage map

| S-ID | Unit chain | Scenario evidence |
|---|---|---|
| S1 AUP refusal stops retrying | U1→U3→U4 | U4 integration "refusal terminal, second run zero LLM calls" (Covers S1) |
| S2 route to AUP-free model | U2→U3→U5 | U5 integration "--model reaches CLI, refusal-mode-off file digests" (Covers S2); U1 unit asserts `--model` argv |
| S3 switching models auto-recovers | U3→U5 | U5 integration "refused row re-attempted after configured model changes" (Covers S3) |
| S4 refusals visible in report/ledger | U1→U3 | U4 integration asserts `refused=1 failed=0` report line + ledger status (Covers S4) |

## Implementation Units

## U1: llm — refusal detection + ErrRefused
Execution note: test-first
Files:
  Modify: internal/llm/llm.go, internal/llm/claudecode.go, internal/llm/testdata/bin/claude
  Test: internal/llm/claudecode_test.go
Interfaces:
  Consumes: existing `resultEvent`, `parseResult`, `digest` error branches
  Produces: `var ErrRefused error`; `resultEvent.TerminalReason string`; `isRefusalText(string) bool`; refusal detection ordered before the is_error/error_during_execution branch in `parseResult`; non-zero-exit branch returns `ErrRefused` on a stdout-or-stderr signature match
Test scenarios:
  happy: existing `ok`/`okfenced` modes still return the page (regression — no false refusal)
  edge: fake mode `refusal_exit0` (result event: subtype success, is_error false, terminal_reason api_error, body "API Error: ... safeguards flagged") → `errors.Is(err, ErrRefused)`; a success event with terminal_reason "completed" and a body that merely contains the words but not the signature is NOT refused
  error: fake mode `refusal_exitN` (exit 1, "API Error:" on stdout) → ErrRefused; existing `ratelimit` (exit 1, generic stderr) and `execerr` (error_during_execution) still → ErrTransient (regression); a refusal whose signature is only in stderr on a nonzero exit → ErrRefused
  integration: n/a — leaf unit
Steps:
  1. Add `refusal_exit0` and `refusal_exitN` modes to internal/llm/testdata/bin/claude (guard behind CLAUDE_FAKE_MODE / CLAUDE_FAKE_MODE_MATCH so existing modes are unchanged).
  2. Write failing tests in claudecode_test.go for the scenarios above.
  3. Add `ErrRefused` to llm.go; add `TerminalReason` to `resultEvent`; add `isRefusalText`; insert refusal detection at the top of `parseResult`'s post-parse logic (before the is_error/subtype checks) and in the non-zero-exit branch of `digest` (check stdout+stderr).
  4. Run `go test -race ./internal/llm/`; confirm pass.
  5. Commit: "feat(llm): detect AUP/policy refusals as ErrRefused"
Acceptance: `go test -race ./internal/llm/` passes; refusal modes yield ErrRefused, transient modes unchanged.

## U2: llm + config — model passthrough
Execution note: test-first
Files:
  Modify: internal/llm/claudecode.go, internal/config/config.go
  Test: internal/llm/claudecode_test.go, internal/config/config_test.go
Interfaces:
  Consumes: `NewClaudeCode`
  Produces: `NewClaudeCode(binPath, model string) *ClaudeCode`; `digest` appends `--model <model>` when non-empty; `config.LLMConfig` gains `Model string \`yaml:"model"\``
Test scenarios:
  happy: `NewClaudeCode(bin, "opus")` → recorded argv (via fake's argv-capture) contains `--model opus`; config with `llm: {model: opus}` round-trips
  edge: `NewClaudeCode(bin, "")` → argv has NO `--model`; config with no `llm.model` → empty string default
  error: n/a (passthrough, no validation)
  integration: n/a — consumed by U3/U5
Steps:
  1. Write failing tests: argv assertion for model set/unset; config parse of `llm.model`.
  2. Change `NewClaudeCode` signature to take `model`; store it; append `--model` in `digest`'s exec args when non-empty. Add `Model` to `LLMConfig`.
  3. Update the sole non-test call site cmd/cogvault/ingest.go:41 to `llm.NewClaudeCode(binPath, cfg.LLM.Model)` (kept minimal here; U5 owns the cmd test).
  4. Run `go test -race ./internal/llm/ ./internal/config/`; confirm pass.
  5. Commit: "feat(llm,config): llm.model passthrough to claude --model"
Acceptance: `go test -race ./internal/llm/ ./internal/config/` passes; `--model` present iff configured.

## U3: ingest — classRefused, model column, model-gated retry
Execution note: test-first
Files:
  Modify: internal/ingest/ledger.go, internal/ingest/ingest.go, internal/ingest/report.go
  Test: internal/ingest/ledger_test.go, internal/ingest/ingest_test.go
Interfaces:
  Consumes: `llm.ErrRefused` (U1), `config.LLMConfig.Model` (U2)
  Produces: `ledgerRow.llmModel string`; `lookup`/`upsert` carry `llm_model`; `openLedger` runs the `PRAGMA table_info` + `ALTER TABLE ADD COLUMN llm_model` migration; `classRefused`; `Report.Refused int` + `actionRefused`; `Run` terminal-row skip gated on `prev.llmModel == r.cfg.LLM.Model`
Test scenarios:
  happy: `ErrRefused` from a mock adapter → ledger status `refused`, attempts unchanged (0), `Report.Refused==1`, `actionRefused` PerFile entry
  edge: a `refused` row with `llmModel=""` and configured model `"opus"` → re-attempted (differs); same row with configured model `""` → skipped (matches); a `success` row + changed model → still Unchanged (never re-attempted); an exhausted `failed` row re-attempted on model change
  error: the migration path — open a ledger DB pre-created WITHOUT `llm_model` (raw `CREATE TABLE` with the old 9 columns + a row), then `openLedger` adds the column and preserves the row (no data loss, reads back `llm_model=""`)
  integration: n/a — walked in U4/U5
Steps:
  1. Write failing ledger_test.go (migration adds column to a column-less table; lookup/upsert round-trip llm_model) and ingest_test.go (classRefused status/attempts/report; model-gated retry matrix incl. success-not-retried).
  2. In ledger.go: add `llm_model` to `ledgerRow`, the `lookup` SELECT, and the `upsert` INSERT column list + values; in `openLedger` after the DDL Exec, query `PRAGMA table_info(ingest_ledger)`, and ALTER-add the column when absent.
  3. In ingest.go: add `classRefused`; in `digestOne` map `errors.Is(err, llm.ErrRefused)` → classRefused before the classPermanent default; in `recordFailure` write status `refused` (no attempt increment) for classRefused, threading the configured model into both upserts (add `llmModel` to every `ledgerRow{...}`); change the `Run` found-row switch so terminal rows (`refused`; `failed` with attempts≥max) skip only when `prev.llmModel == r.cfg.LLM.Model`.
  4. In report.go: add `Refused int`, `actionRefused = "refused"`, and `refused=%d` in `String()`.
  5. Run `go test -race ./internal/ingest/`; confirm pass.
  6. Commit: "feat(ingest): refused class, per-row model, model-gated terminal retry"
Acceptance: `go test -race ./internal/ingest/` passes; `go vet ./...` clean.

## U4: cmd wiring + refusal integration
Execution note: test-first
Files:
  Modify: cmd/cogvault/ingest.go, cmd/cogvault/ingest_integration_test.go
Interfaces:
  Consumes: `ingest.New`, `llm.NewClaudeCode(binPath, cfg.LLM.Model)`, fake CLI refusal modes (U1)
  Produces: end-to-end refusal behavior over the cobra harness
Test scenarios:
  happy: backlog with one refusal-mode file (via CLAUDE_FAKE_MODE_MATCH targeting its filename) → report `refused=1 failed=0`, others digest, exit 0; ledger status `refused` for that file (Covers S1, S4)
  edge: second run, same (empty) model → the refused file is skipped (zero LLM calls: fake argv-record absent for it)
  error: n/a (per-file failure already isolated)
  integration: Covers S1, S4
Steps:
  1. Confirm cmd/cogvault/ingest.go passes `cfg.LLM.Model` into `NewClaudeCode` (from U2); no further cmd change expected beyond that line.
  2. Write failing integration tests using the existing harness (executeCommand + fakeClaudeOnPath + CLAUDE_FAKE_MODE_MATCH) for the refusal + second-run-skip scenarios.
  3. Fix any wiring gaps; run `go test -race ./cmd/...`; confirm pass.
  4. Commit: "test(e2e): AUP refusal is terminal and reported"
Acceptance: `go test -race ./cmd/...` passes; refusal file reported `refused`, not retried.

## U5: model-recovery integration + CONCEPTS
Execution note: test-first
Files:
  Modify: cmd/cogvault/ingest_integration_test.go, CONCEPTS.md
Interfaces:
  Consumes: full wiring (U1-U4)
  Produces: end-to-end model-change recovery; updated canonical vocabulary
Test scenarios:
  happy: file refused under model "" (default) → set configured model to "opus" and re-run with the fake in success mode for that file → it re-digests to `success` (Covers S3); with `--model` reaching the fake (argv-record shows `--model opus`) (Covers S2)
  edge: re-running WITHOUT changing the model leaves the refused row untouched (guards against the retry firing on unchanged config)
  error: n/a
  integration: Covers S2, S3
Steps:
  1. Write failing integration test: two runs with different configured `llm.model`, asserting re-attempt-on-change and no-op-on-same; assert `--model opus` in the fake's recorded argv.
  2. Update CONCEPTS.md "Error classes (ingest)" to 4-way (add `refused`: provider policy/AUP refusal; terminal under the same model; re-attempted on model change).
  3. Run full `go test -race ./...`; confirm all packages pass.
  4. Commit: "test(e2e): model-change recovery of refused files; docs(concepts): 4-way error classes"
Acceptance: `go test -race ./...` all packages pass; CONCEPTS reflects the refused class.

## Mutation/failure-state matrix

Stateful ceremony: an ingest run writing ledger rows + wiki pages + index rows. This change adds the `refused` terminal transition and the model-gated re-attempt; the T1-T4 transitions from the original v2 plan are unchanged. New/changed rows only (evidence under `.release-loop/evidence/U3/` and `.release-loop/evidence/U4/`). Worked example: `skills/planning/references/stateful-ceremony-matrix-example.md`; deviation authority: `docs/solutions/workflow-issues/review-introduced-state-machine-deviation.md`.

| Transition | Pre-state | Action | Post-state | Unit / evidence owner |
|---|---|---|---|---|
| T5 refuse | no or non-terminal row for (path,hash) | digest returns ErrRefused | `refused` row, attempts unchanged, `llm_model` = configured model | U3 / U3 tests |
| T6 refused-skip | `refused` row, `llm_model` == configured model | scan → lookup → skip | unchanged (no LLM call) | U3 / U4 tests |
| T7 model-change re-attempt | `refused` (or exhausted `failed`) row, `llm_model` != configured model | scan → lookup → re-digest | new terminal or `success` row with new `llm_model` | U3 / U5 tests |

Outcome classes (applied to the new transitions):
- **success**: as post-state; U3 unit + U4/U5 e2e.
- **forced failure**: injection = mock `llm.Adapter` returning `ErrRefused` / fake CLI refusal modes, isolated in `t.TempDir()` with no real LLM.
- **rerun**: T5→T6 is the idempotence guard (second run under same model skips); T7 is rerun-under-changed-config; both asserted.
- **rollback/compensation**: none — eventual consistency; a refused row is corrected by a model change (T7) or a content change (new hash → new row), not rollback.
- **headless**: identical code path under `--scheduled` (origin column only); the migration + retry are origin-agnostic.
- **cancellation/abort**: ctx cancellation between files unchanged from the base pipeline (existing T-row behavior); no new cancellation surface.

## Deferred to Follow-Up Work

- The old-hash `refused` row of a since-changed file persists (harmless; `supersedePrevSuccess` only supersedes `success`). Not cleaned up here.
- CLI `--fallback-model` wiring, per-source model selection, model-value validation — spec Scope Out.
- F7 (non-ASCII slug), F8 (source types), and the F2 minor batch remain separate tracker items.

## Open unknowns

Planning-time: none. The single unavailable assumption (exact live AUP JSON shape) is spec-sanctioned; the dual-signal detection degrades safely and U1 pins both manifestations via the fake CLI.

Implementation-time:
- Exact `PRAGMA table_info` scan loop shape in Go (column-name check) — U3 picks the idiom; the acceptance test (migrate a column-less table) is the gate.
- Whether to thread the configured model via a new `recordFailure`/`digestOne` param or read `r.cfg.LLM.Model` directly inside them — either satisfies the tests; Runner holds cfg so a direct read is likely simplest.
- Precise refusal-signature helper wording (prefix vs contains) — U1 test fixtures pin the accepted/rejected bodies.

## Handoff

Headless under release-loop: plan path recorded in `.release-loop/progress.md`; implementing proceeds U1→U5 after the plan-approval gate.
