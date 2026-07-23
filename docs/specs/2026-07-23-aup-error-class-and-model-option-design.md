---
title: Ingest AUP-refusal error class + LLM model option
status: draft
date: 2026-07-23
schema: spec/v1
---

# Ingest AUP-refusal error class + LLM model option Design

_Created 2026-07-23. Follow-up F6 from the v2 Phase 1 retro. Revision 2: independent review round 1 (C1 ledger migration, M1 detection order + is_error, M2 model comparison, m1-m6) applied._

## Overview

Two coupled fixes to `cogvault ingest`, driven by the 2026-07-23 real backlog run where 2 of 65 PDFs (an eLife genetics paper, an mRNA-vaccine article) were refused by Fable-5's AUP safeguards and — misclassified as transient — retried on every scheduled run forever. (1) Detect provider policy/AUP refusals and classify them as a new terminal `refused` class that does not waste retries. (2) Add a config `llm.model` knob so the claudecode backend can route ingest to a model without those safeguards (e.g. opus/sonnet), and auto-retry refused files when the configured model changes.

## User Scenarios

### S1: AUP refusal stops retrying
A PDF trips the digestion model's content safeguards. Today the failure looks transient and re-runs hourly forever. Instead, ingest records it once as `refused` and skips it on subsequent runs (same model), so no scheduled run wastes an LLM call on it.

### S2: Route ingest to an AUP-free model
The user edits `~/.config/cogvault/config.yaml` to set `llm.model: opus`. The next `cogvault ingest` sends `--model opus` to the Claude CLI; content Fable-5 refused now digests successfully.

### S3: Switching models auto-recovers refused files
After setting `llm.model: opus`, the user does nothing else. The next scheduled run notices the previously-`refused` rows were produced under a different model, re-attempts them under opus, and they succeed — zero manual DB surgery.

### S4: Refusals are visible in the report and ledger
`cogvault ingest` prints a `refused=N` count distinct from `failed=N`, and each refused file's `PerFile` line and ledger row carry a `refused` status with the CLI's refusal message, so the user can see *why* a file has no page.

## Scope

### In
- Update `CONCEPTS.md` "Error classes (ingest)" entry from three-way to four-way (add `refused`: provider policy/AUP refusal, terminal under the same model, re-attempted on model change).
- New backend sentinel `ErrRefused` returned by the claudecode backend when it detects a provider policy/AUP refusal, in both CLI manifestations observed: (a) exit 0 with a result event whose `terminal_reason` is `api_error` (and/or a result body matching the refusal signature); (b) non-zero exit whose captured output matches the refusal signature.
- New ingest failure class `refused`: terminal after one occurrence (never retried under the same model), recorded with ledger status `refused`; distinct `refused` counter in the report.
- Config `llm.model` (string, optional, default empty). When non-empty, the claudecode backend adds `--model <value>` to its argv; empty preserves today's behavior (CLI default model). The value is passed through to the CLI unvalidated (typos surface as a CLI/transient failure).
- Ledger records the model used per row (new `llm_model` column) via an explicit additive migration: `openLedger` checks `PRAGMA table_info(ingest_ledger)` and runs `ALTER TABLE ingest_ledger ADD COLUMN llm_model TEXT NOT NULL DEFAULT ''` when the column is absent (SQLite has no `ADD COLUMN IF NOT EXISTS`, and `CREATE TABLE IF NOT EXISTS` is a no-op on the existing 63-row table, so the column will not appear without this). The ledger keeps its own DDL and must NOT be folded into the index layer's `user_version` drop-and-recreate (which would erase ingest history).
- Skip-already-processed logic for terminal rows (`refused`, or `failed` with attempts exhausted) re-attempts the file when the currently-configured model differs from the row's recorded `llm_model` by exact string comparison; otherwise skips it. `success` and `superseded` rows are never re-attempted on model change. Model comparison is raw-string: an empty configured model ("CLI default") and a recorded `"opus"` differ, and vice versa — so clearing a previously-set model also triggers one bounded re-attempt of terminal rows. This is intentional and bounded to one occurrence per row per model change.

### Out
- Automatic model fallback / retry-with-different-model within a single run (the CLI's own `--fallback-model` is not wired; one model per run).
- Per-source or per-file model selection (single global `llm.model`).
- Validating the model value against a known model list.
- Refusal-signature breadth beyond the signals named above — the exact detection rule (api_error terminal vs. body signature) is settled in Open Decisions O1, not here.
- The old-hash `refused` row of a since-changed file persists (a new hash creates a new row; `supersedePrevSuccess` only supersedes `success`). Harmless, noted.
- Retroactively reclassifying the 2 already-removed backlog failures (their files were moved out of the source dir during validation).

## Assumptions and Preconditions

| Claim | Command | Observed at | Observed result | Evidence source |
|---|---|---|---|---|
| A Fable-5 AUP refusal surfaces as a result event with `subtype: success`, `is_error: false`, `terminal_reason: api_error`, body starting `API Error:` / "safeguards flagged" | `echo "Read the PDF file at path: <eLife pdf> ..." \| claude --print --output-format json --allowedTools Read` then inspect final event | 2026-07-23 (F6 validation) | `subtype: success, is_error: false, terminal_reason: api_error, result: "API Error: Fable 5's safeguards flagged this message..."` — because `is_error` is false, today this passes the success branch and the refusal text becomes the page body, failing frontmatter validation as classPermanent (the exit-1 manifestation is what the 2/65 backlog files hit, classified transient) | Live CLI run during backlog validation |
| The same content can also surface as a non-zero exit (`exit status 1`) classified transient today | 2026-07-23 backlog run | 2026-07-23 | `claude cli: exit status 1: transient llm failure` on 2/65 files | Backlog run report, docs/research/v2-follow-ups.md F6 |
| `claude --model <alias>` is supported and accepts an alias or model id | `claude --help \| grep -A2 -- --model` | 2026-07-23 | `--model <model>  Model for the current session. Provide an alias for the latest model...` | Live CLI help |
| Current backend hard-classifies nonzero exit and `error_during_execution`/`is_error` as `ErrTransient` | read internal/llm/claudecode.go:56-95 | 2026-07-23 | lines 56-64 (nonzero exit → transient), 90-92 (error_during_execution/is_error → transient) | Working tree |

## Architecture

- **internal/llm** — `resultEvent` gains a `terminal_reason` field. `parseResult` gains refusal detection that runs **before** the existing `is_error`/`error_during_execution` transient branch (claudecode.go:90-92): a final result event with `terminal_reason == "api_error"` OR a body matching the refusal signature returns `ErrRefused` regardless of `subtype`/`is_error` (the observed refusal has `is_error: false`, so ordering after the transient branch would misfire; ordering before it is required). The non-zero-exit branch (claudecode.go:56-64) inspects captured output — **both stdout and stderr** — for the refusal signature and returns `ErrRefused` instead of `ErrTransient` on a match. `ClaudeCode` gains an optional model field; `Digest`'s argv includes `--model <model>` when set. A constructor option or exported field carries the model from config.
- **internal/ingest** — a new `classRefused` in the failure-class enum maps `errors.Is(err, llm.ErrRefused)` to a `refused` ledger status that is terminal (no attempt budget, not retried under the same model). The `Run` ledger-lookup branch (ingest.go:117-134, not `scan()`) already fetches the full row before deciding to skip; extend its SELECT to include `llm_model` and re-queue terminal rows (`refused`, exhausted `failed`) when it differs from the configured model. The `Report` gains a `Refused` counter. The runner passes `cfg.LLM.Model` when constructing the backend and records it on every ledger write.
- **internal/config** — `LLMConfig` gains `Model string \`yaml:"model"\``; no validation beyond the existing backend check.

Data flow unchanged except: model flows config → runner → backend argv; refusal flows backend `ErrRefused` → ingest `classRefused` → ledger `refused` + report counter; recorded model gates terminal-row retry.

## Interface

Config (additive):

```yaml
llm:
  backend: claudecode
  model: opus          # optional; empty → CLI default model
```

CLI: no new flags. `cogvault ingest` report line gains a field:

```
digested=N failed=N refused=N skipped=N deferred=N unchanged=N source-errors=N
```

Backend: new exported `var ErrRefused = errors.New(...)`, sibling to `ErrTransient`.

## Data Model

`ingest_ledger` gains a `llm_model TEXT NOT NULL DEFAULT ''` column (the model used for the attempt) and admits a new `status` value `refused` (alongside `success | failed | superseded`). Migration is **explicit, not implicit**: `openLedger` reads `PRAGMA table_info(ingest_ledger)` and, if `llm_model` is absent, runs `ALTER TABLE ingest_ledger ADD COLUMN llm_model TEXT NOT NULL DEFAULT ''`. `CREATE TABLE IF NOT EXISTS` alone does not add the column to the existing 63-row DB, and reading `llm_model` in `lookup`'s SELECT before the ALTER runs would raise "no such column". The ledger's DDL stays independent of the index layer's `user_version` drop-and-recreate (internal/index/sqlite.go), which only touches `wiki_fts`/`file_meta` and must never drop `ingest_ledger`. Existing rows read back with `llm_model = ''`.

## Testing

- Unit (internal/llm): fake `claude` script gains modes — `refusal_exit0` (result event: subtype success, terminal_reason api_error, "API Error:" body) → `ErrRefused`; `refusal_exitN` (nonzero exit, refusal signature in output) → `ErrRefused`; existing `execerr`/`ratelimit` still → `ErrTransient` (regression); model argv assertion (`--model opus` present when configured, absent when empty).
- Unit (internal/ingest): `ErrRefused` → `refused` status, one occurrence, no second attempt under same model; a `refused` row re-attempted when configured model differs from the row's `llm_model`; a `refused` row skipped when model matches; a `success` row NOT re-attempted on model change (stays `unchanged`, zero LLM calls); `Report.Refused` counted; exhausted `failed` also re-queued on model change; the `PRAGMA table_info` migration path adds the column to a pre-existing column-less ledger without data loss.
- Unit (internal/config): `llm.model` parses; empty default; round-trips.
- Integration (cmd, fake CLI): backlog with one refusal-mode file → report `refused=1`, others digest, exit 0; second run same model → that file untouched (skipped); a run after changing configured model → the refused file re-attempted.
- All: `go test -race ./...`.

## Risks

- **Over-broad refusal detection** — treating every `api_error` terminal as `refused` could mask a genuinely transient API error, stranding a file that would have succeeded on retry. Mitigation: `refused` is re-attempted on model change, and the user can force a re-run; O1 records the choice. A too-narrow signature is the safer failure (falls back to transient = retried), so prefer matching `terminal_reason == api_error` broadly but keep it evidence-anchored.
- **CLI output shape drift** — `terminal_reason` is an undocumented field; a CLI update could rename it. Mitigation: dual signal (terminal_reason OR body signature); a miss degrades to transient (retried), not a crash.
- **Model column vs. existing ledger migration** — adding a column to a table that may already hold 63 rows. Mitigation: `DEFAULT ''`, additive; verify existing rows read back with empty model and are treated as "model differs" only when a non-empty model is configured (so a configured model triggers one re-attempt of old terminal rows — acceptable, and desired for the refused ones).

## Success Criteria

1. AUP refusals are terminal, not infinitely retried.
   - **Measured by**: unit test — a refusal-mode fake CLI yields ledger status `refused`; a second run under the same model performs zero LLM calls for that file (argv-record file absent/unchanged).
2. `llm.model` reaches the CLI.
   - **Measured by**: unit test asserting `--model opus` in the recorded argv when configured, and absent when empty.
3. Switching models re-attempts refused files.
   - **Measured by**: integration test — a `refused` row under model A is re-attempted (and, with a success-mode fake, succeeds) after the configured model changes to B.
4. Report distinguishes refused from failed.
   - **Measured by**: integration test asserting the report line contains `refused=1` and `failed=0` for a refusal-only run.
5. No regression.
   - **Measured by**: `go test -race ./...` all packages pass; existing transient/permanent/infra classifications unchanged (their tests still green).

## Open Decisions

- **O1 — Refusal signature breadth**: treat any `terminal_reason == "api_error"` as `refused` (broad, simple; risks masking a transient API error), or require the body signature ("API Error:" / "safeguards flagged") in addition (narrow, risks missing a reworded refusal). Proposal: `api_error` terminal OR body signature → refused; the model-change re-attempt and manual re-run bound the downside. Owner: user, at approval.
- **O2 — Old terminal rows on first configured-model run**: once `llm.model` is set, every pre-existing terminal row (empty recorded model ≠ configured model) is re-attempted once. For `refused` rows this is desired; for genuinely-`failed` (malformed) rows it costs one extra call each. Proposal: accept (one-time, bounded, and a fresh attempt may well succeed). Owner: user, at approval.
