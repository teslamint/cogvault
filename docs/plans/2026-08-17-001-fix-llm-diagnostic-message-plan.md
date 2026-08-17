---
schema: plan/v1
title: Surface actionable Claude CLI diagnostics without poisoning retry classification
type: fix
status: draft
date: 2026-08-17
execution: code
origin: docs/specs/2026-08-17-llm-diagnostic-message-design.md
---

# Surface actionable Claude CLI diagnostics without poisoning retry classification

## Goal

Replace opaque `claude cli: exit status 1` details with the final eligible
Claude result diagnostic while keeping policy refusals terminal and quota,
authentication, transport, and generic API failures transient. Prove the exact
message reaches both the per-file report and ledger without changing the
adapter API, retry budget, configuration, or database schema.

## Architecture notes

- Keep the change inside the existing `internal/llm` adapter boundary. The
  public `Adapter.Digest` signature and `ErrTransient`/`ErrRefused` sentinels do
  not change.
- Parse stdout as an event array before classification. Only the final result
  event may participate, and only when `is_error`, `error_during_execution`, or
  `api_error` makes it error-eligible.
- Separate classification precedence from message precedence. First inspect
  policy-specific refusal evidence across the eligible final result, stderr,
  and non-JSON-looking plain stdout. Only after classification remains
  transient choose final result, stderr, plain stdout, then process error.
- Use the approved finite predicate: case-insensitive `safeguards flagged`,
  `policy refusal`, or an `API Error:` payload beginning with `refused`.
  `connection refused` and generic `API Error:` messages remain transient.
- Treat stdout beginning with `{` or `[` after trimming as JSON-looking. If it
  is malformed, wrong-shaped, or lacks a final result, never emit it as plain
  text; use stderr or the process error.
- Normalize diagnostics by collapsing Unicode whitespace, replacing other
  Unicode controls with `U+FFFD`, and bounding the final string to 2,000 runes
  including `…` (1,999 retained runes plus the marker when truncated).
- Current canon supersedes the July F6 design's broad `api_error`/`API Error:`
  refusal assumption. Historical ledger rows are not migrated; the ingest
  model/content-hash gate stays unchanged.
- Known Pattern: no existing `docs/solutions/` entry covers LLM diagnostics or
  ingest error classification. Follow the established fake-Claude modes in
  `internal/llm/testdata/bin/claude` and the mixed failure/refusal integration
  patterns in `cmd/cogvault/ingest_integration_test.go`.

## Assumption Recheck

| Approved claim | Fresh command evidence | Outcome |
|---|---|---|
| Claude CLI exits 1 for the weekly limit, leaves stderr empty, and puts the reset message in the final `api_error` result. | At `2026-08-17T05:08:40Z`, reran the approved `claude --print --output-format json --allowedTools Read --model opus` probe: exit `1`; final result `is_error:true`, `terminal_reason:"api_error"`, `result:"You've hit your weekly limit · resets 12pm (Asia/Seoul)"`; stderr bytes `0`. | match |
| The current nonzero branch discards structured stdout and has the approved baseline hash. | At `2026-08-17T05:09:00Z`, `sed -n '35,90p' internal/llm/claudecode.go \| sha256sum` returned `ff5021d74141bbe3c0a88d415a40a11003deecfee70e7818a0a413c7e2eaaaa2`. | match |

No contradiction or unavailable assumption remains.

## Planning discrimination evidence

The plan's three load-bearing comparisons were exercised with the same
computations the units will implement:

| Guard or comparison | Invariance fixture | Changed-axis fixture | Effect-bearing signal |
|---|---|---|---|
| Finite refusal predicate | `API Error: refused` evaluated twice → `true`, `true`. | Change only the payload to `API Error: rate limit` → `true`, `false`. | Policy-refusal boolean. |
| JSON-looking stdout guard | `[broken` evaluated twice → `true`, `true`. | Change only the first non-space rune to plain `rate limit` → `true`, `false`. | Eligibility for plain-stdout fallback. |
| Rune truncation | The same 2,000-rune Korean string normalized twice → equal, length `2000`, no marker. | Add one Korean rune → outputs differ; result length remains `2000` and ends in `…`. | Final rune count and truncation marker. |

## File structure

| Path | Responsibility |
|---|---|
| `internal/llm/claudecode.go` | Final-event inspection, finite refusal classification, diagnostic precedence, control normalization, and exact rune bound. |
| `internal/llm/claudecode_test.go` | Unit discrimination for eligibility, mixed streams, false-refusal negatives, fallbacks, controls, and 1,999/2,000/2,001-rune boundaries. |
| `internal/llm/testdata/bin/claude` | Deterministic fake modes for structured quota/auth/API/refusal and competing-stream cases. |
| `cmd/cogvault/ingest_integration_test.go` | User-visible report plus persisted `last_error`/status/attempt evidence. |
| `SPEC.md` | Current public classification, diagnostic precedence, normalization, and historical-row behavior. |
| `DESIGN.md` | Internal LLM adapter data flow and ownership of the diagnostic/refusal rule. |

## Scenario coverage map

| Scenario | Ordered unit chain | Scenario evidence |
|---|---|---|
| S1 — recognize a subscription limit | U1 → U2 | `TestIngestNonzeroExitDiagnostic/weekly-limit` verifies the exact report and ledger message. Covers S1. |
| S2 — diagnose an ordinary CLI failure | U1 → U2 | `TestIngestNonzeroExitDiagnostic/stderr-fallback` verifies an ordinary non-structured failure remains actionable through the CLI. Covers S2. |
| S3 — preserve refusal and retry semantics | U1 → U2 | `TestDigestRefusal*`, `TestDigestGenericAPIErrorNotRefused`, `TestE2EMixedFailureIsolation`, and `TestE2ERefusalTerminal` verify terminal/refused versus retryable/failed ledger outcomes. Covers S3. |

## U1: Final-event diagnostics and policy-specific refusal classification

Execution note: test-first
Files:
  Create: none
  Modify: `internal/llm/claudecode.go`, `internal/llm/claudecode_test.go`, `internal/llm/testdata/bin/claude`
  Test: `internal/llm/claudecode_test.go`
Interfaces:
  Consumes: `(*ClaudeCode).digest(context.Context, DigestRequest)`, `resultEvent`, `lastResultEvent([]resultEvent)` and the existing stdout/stderr buffers.
  Produces: unchanged `Adapter.Digest` behavior and unchanged `ErrTransient`/`ErrRefused` sentinels; private event-inspection and normalization helpers with no cross-package API.
Test scenarios:
  happy: Observed weekly-limit JSON on exit 1 returns an error containing the reset message, satisfies `errors.Is(err, ErrTransient)`, and does not satisfy `ErrRefused`. Covers S1.
  edge: Final eligible structured refusal remains refused; stale non-final safeguards text is ignored; completed content containing `safeguards flagged` is ineligible; `connection refused` remains transient; structured quota plus stderr refusal and stdout refusal plus generic stderr both classify refused. Covers S3.
  error: JSON-looking malformed/truncated/wrong-shape/no-result stdout is never emitted raw; stderr, non-JSON stdout, and process-error fallbacks follow the approved order. Covers S2.
  integration: n/a — leaf adapter unit; U2 walks the CLI/ledger boundary.
Steps:
  1. Add fake modes and failing tests named `TestDigestStructuredTransientDiagnostic`, `TestDigestGenericAPIErrorNotRefused`, `TestDigestDiagnosticEventEligibility`, expanded `TestDigestRefusal*`, `TestDigestNonzeroExitDiagnosticFallbacks`, and `TestNormalizeCLIDiagnostic`. Run `go test ./internal/llm/ -run 'TestDigestStructuredTransientDiagnostic|TestDigestGenericAPIErrorNotRefused|TestDigestDiagnosticEventEligibility|TestDigestRefusal|TestDigestNonzeroExitDiagnosticFallbacks|TestNormalizeCLIDiagnostic' -v`; confirm failure shows the current opaque detail, broad false-refusal paths, or missing helpers rather than fixture setup errors.
  2. Refactor stdout inspection so valid event JSON exposes only its last result, error eligibility is computed before refusal/diagnostic use, and JSON-looking decode failures cannot become plain stdout.
  3. Replace broad refusal matching with the approved finite predicate and perform the refusal pass across eligible final result, stderr, and permitted plain stdout before choosing a diagnostic message.
  4. Add diagnostic normalization: collapse Unicode whitespace, replace other controls with `U+FFFD`, and truncate only messages over 2,000 runes to 1,999 plus `…`. Use table cases at 1,999, 2,000, and 2,001 runes with multibyte Korean text to prove rune rather than byte accounting.
  5. Run the focused command from step 1 plus `go test ./internal/llm/ -v`; confirm all new and existing timeout, malformed JSON, missing-binary, rate-limit, and refusal cases pass. Commit: `fix(llm): surface actionable Claude CLI diagnostics`.
Acceptance: `go test ./internal/llm/ -run 'TestDigestStructuredTransientDiagnostic|TestDigestGenericAPIErrorNotRefused|TestDigestDiagnosticEventEligibility|TestDigestRefusal|TestDigestNonzeroExitDiagnosticFallbacks|TestNormalizeCLIDiagnostic' -v` and `go test ./internal/llm/ -v` both exit 0.

## U2: Prove report and ledger persistence through ingest

Execution note: skip-test-first — U1 already implements the behavior; this unit adds a cross-package regression gate rather than another production change.
Files:
  Create: none
  Modify: `cmd/cogvault/ingest_integration_test.go`
  Test: `cmd/cogvault/ingest_integration_test.go`
Interfaces:
  Consumes: fake Claude modes from U1, `executeCommand("ingest", "--config", path)`, and the existing `ingest_ledger` query helper.
  Produces: integration-only `ledgerSnapshot.lastError` coverage and `TestIngestNonzeroExitDiagnostic`; no production API.
Test scenarios:
  happy: Weekly-limit structured JSON yields `failed=1`, embeds the exact normalized reset message in the per-file line and `last_error`, records status `failed`, and records attempts `0` in both interactive and scheduled origins. Covers S1.
  edge: Stderr-only transient output appears in the per-file line and identical ledger `last_error` without changing the summary bucket. Covers S2.
  error: JSON-looking truncated output with empty stderr persists only the process-error fallback, never raw JSON; existing explicit policy-refusal integration still records `refused`, while existing mixed transient failure still retries. Covers S3.
  integration: Run the two new subcases alongside `TestE2EMixedFailureIsolation` and `TestE2ERefusalTerminal`; together they walk S1, S2, and S3.
Steps:
  1. Extend `ledgerSnapshot` and its SELECT/Scan order with `last_error`; rerun existing ingest integration tests to prove the helper change is observational only.
  2. Add table-driven `TestIngestNonzeroExitDiagnostic` subtests for structured weekly limit in interactive and `--scheduled` origins, stderr-only fallback, and JSON-looking truncated output with empty stderr, using a fresh disposable config/database per subtest.
  3. Assert exact diagnostic equality after the existing `digest: llm.Digest <path>: claude cli:` prefix in stdout and `last_error`, plus status `failed` and attempts `0`; avoid substring-only ledger assertions that could miss control/truncation drift.
  4. Run `go test ./cmd/cogvault/ -run 'TestIngestNonzeroExitDiagnostic|TestE2EMixedFailureIsolation|TestE2ERefusalTerminal' -v` plus `go test ./internal/ingest/ -run 'TestRunCancelAfterFirstFile|TestRunContextCanceled' -v`; confirm persistence, retry, scheduled-origin, and cancellation paths pass. Commit: `test(ingest): pin Claude CLI diagnostics in report and ledger`.
Acceptance: the focused integration command exits 0 and proves the report and ledger carry identical normalized details with unchanged status/attempt semantics.

## U3: Synchronize canonical diagnostic and error-class contracts

Execution note: skip-test-first — canonical prose documents behavior already discriminated by U1 and U2.
Files:
  Create: none
  Modify: `SPEC.md`, `DESIGN.md`
  Test: none
Interfaces:
  Consumes: approved spec, U1 classification/normalization behavior, and U2 report/ledger evidence.
  Produces: current canonical behavior text for future implementation and review; no runtime API.
Test scenarios:
  happy: `SPEC.md` states that a nonzero eligible final result supplies the diagnostic before stderr and remains transient unless the finite policy predicate matches. Covers S1 and S2.
  edge: Canon states final-event eligibility, mixed-stream refusal precedence, JSON-looking raw-output rejection, exact control/2,000-rune normalization, and historical-row non-migration. Covers S3.
  error: Reviewer compares every finite predicate phrase and fallback step against `internal/llm/claudecode.go`; any extra broad `api_error`/`API Error:` refusal claim fails acceptance.
  integration: n/a — documentation unit; U2 owns executable scenario evidence.
Steps:
  1. Update `SPEC.md` §4.2 and §10 LLM/ingest clauses with the finite policy-refusal rule, error eligibility, classification-before-message precedence, safe fallback rules, exact normalization bound, exit-zero generic-API reclassification, and no historical-ledger migration.
  2. Update `DESIGN.md`'s `internal/llm` component description and data flow with the same ownership and ordering, without copying test implementation details.
  3. Run `rg -n 'api_error|API Error:|safeguards flagged|policy refusal|2,000|last_error' SPEC.md DESIGN.md internal/llm/claudecode.go`; compare each match against the approved spec and remove any contradictory broad classification statement from current canon.
  4. Run `git diff --check -- SPEC.md DESIGN.md` and `go test -race ./...`; confirm documentation is clean and the full repository remains green. Commit: `docs(llm): specify actionable CLI failure diagnostics`.
Acceptance: the grep comparison has no contradictory current-canon claim, `git diff --check` is clean, and `go test -race ./...` exits 0.

## Mutation/failure-state matrix

| Transition | Pre-state | Action | Expected post-state | Success | Forced failure | Rerun | Rollback or compensation | Headless | Cancellation or abort | Owning unit | Evidence owner |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| T1 — persist a transient Claude diagnostic | Disposable source has no current-hash ledger row. | Ingest receives an eligible structured or fallback transient diagnostic and calls the existing failure upsert. | One `failed` row with normalized `last_error`, attempts `0`; report carries the same detail. | U2 weekly/stderr fixtures prove exact report/ledger equality. | U2 JSON-looking truncated-output fixture plus empty stderr proves process-error fallback is persisted without raw JSON leakage. | Re-run against the same failed current hash; existing upsert replaces the row, keeps attempts `0`, and never creates a duplicate. | Delete the disposable temp DB/wiki fixture; no production migration or irreversible state exists. | U2 runs the weekly fixture with `--scheduled` and proves only `run_origin` differs. | U1 `TestDigestContextCancelledTransient` plus existing `TestRunCancelAfterFirstFile`/`TestRunContextCanceled` prove cancellation invents no structured diagnostic or durable duplicate; fixture teardown removes partial disposable state. | U2 | `.release-loop/evidence/U2/` focused command output and disposable ledger query |

## Carry-forward trigger audit

Audited `docs/research/v2-follow-ups.md` at `318e0c9`: 1 open row, 0 fired, 0 unobservable.

F15 is event-based: it fires when the user chooses and exercises a real IdP,
stable tunnel, Claude app, and ChatGPT deployment. This plan modifies only the
local Claude CLI ingest adapter, tests, and canon; the remote interoperability
event has not occurred and is unrelated to this fix.

## Deferred to Follow-Up Work

- F15 remains in `docs/research/v2-follow-ups.md`. It requires real hosted-client
  deployment evidence and is not folded into a local ingest diagnostic fix.
- Automatic backend/model fallback remains outside the approved spec. Better
  diagnostics make that future decision observable but do not authorize it.
- Historical `refused` ledger rows receive no migration because their original
  provider message cannot be reconstructed safely; model or content change
  remains the current recovery gate.

## Open unknowns

### Planning-time

None. Both retained assumptions matched, the finite predicate and precedence are
approved, and every scenario has executable evidence.

### Implementation-time

- Private helper names and whether stdout inspection returns a small private
  struct or separate booleans may be chosen inside U1, provided the approved
  ordering and tests remain unchanged.
- U2 may use one table-driven test or two named tests, but the public success
  criterion command must continue to select `TestIngestNonzeroExitDiagnostic`.
