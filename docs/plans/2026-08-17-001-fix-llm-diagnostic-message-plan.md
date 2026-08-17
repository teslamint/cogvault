---
schema: plan/v1
title: Surface actionable Claude CLI diagnostics without poisoning retry classification
type: fix
status: done
completed_by: df5da99232c19910a6a86b068c76545210346212
date: 2026-08-17
execution: code
origin: docs/specs/2026-08-17-llm-diagnostic-message-design.md
body_seal: 092a994b840300ad8e12901706df94a3fe62199335e87989def8d45fe396d40a
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
  event may participate. Refusal classification may inspect a final
  `terminal_reason: api_error` even when `is_error` is false, preserving the
  observed exit-zero AUP refusal. Diagnostic persistence is stricter: it
  requires `is_error` plus either `error_during_execution` or `api_error`.
- Persist a structured result as a diagnostic only when it is non-empty,
  single-line, and not page-shaped (no frontmatter, heading, or code-fence
  prefix). Otherwise use stderr or the process error. This keeps partial
  generated pages and prompt/source excerpts out of the report and ledger.
- Separate classification precedence from message precedence. First inspect
  policy-specific refusal evidence across the eligible final result, stderr,
  and non-JSON-looking plain stdout. Only after classification remains
  transient choose final result, stderr, plain stdout, then process error.
- Canonicalize every candidate before classification and display: remove
  recognized ANSI SGR/OSC sequences, collapse Unicode whitespace, and replace
  remaining Unicode Control and Format (`Cf`) runes with `U+FFFD`.
- Use the deviation's anchored, case-insensitive grammar: a line beginning
  `policy refusal:`, or `API Error:` followed by `refused`, bare `safeguards
  flagged`, or the observed `<provider>'s safeguards flagged` envelope. Quoted,
  negated, embedded, suffix-only, `connection refused`, and generic API
  messages remain transient.
- Treat stdout beginning with `{` or `[` after trimming as JSON-looking. If it
  is malformed, wrong-shaped, or lacks a final result, never emit it as plain
  text; use stderr or the process error.
- Current canon supersedes the July F6 design's broad `api_error`/`API Error:`
  refusal assumption. Historical ledger rows are not migrated; the ingest
  model/content-hash gate stays unchanged.
- F16 records the pre-existing unbounded stdout/stderr capture. This plan fires
  that edit-based trigger by touching `claudecode.go` but defers a byte ceiling:
  it would also change legitimate success-page output and needs its own design.
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
| Anchored refusal predicate | Mixed-case `Api Error: ReFuSeD` and observed `API Error: Fable 5's safeguards flagged this message` each evaluate `true` on repeat. | Change only the payload to `not safeguards flagged` or `response contained "safeguards flagged"` → `false`; embedded `not a policy refusal` also stays `false`. | Policy-refusal boolean after canonicalization. |
| JSON-looking stdout guard | `[broken` evaluated twice → `true`, `true`. | Change only the first non-space rune to plain `rate limit` → `true`, `false`. | Eligibility for plain-stdout fallback. |
| Diagnostic-shape guard | The same one-line weekly message evaluated twice → eligible, eligible. | Add only a leading `---` or one CR/LF → eligible, ineligible. | Structured-result persistence eligibility. |
| Rune truncation | The same 2,000-rune Korean string normalized twice → equal, length `2000`, no marker. | Add one Korean rune → outputs differ; result length remains `2000` and ends in `…`. | Final rune count and truncation marker. |

## Independent review disposition

- Accepted into this draft: exact test-name presence gates, the real terminal
  test (`TestE2ERefusalNotRetried`), same-hash failed rerun evidence, full
  wrapped-error oracles, full U2 package regression, exact DESIGN sections,
  closed F6 tracker repair, mixed-case/one-axis/control mutation cases, safe
  diagnostic shaping, canonicalization-before-classification, and Unicode
  Format control handling.
- Accepted as separate scope: unbounded process-output capture is registered as
  F16 and deliberately deferred because it changes success-page output too.
- Rejected: making exit code `2` permanent. Decision 0021 D4 permits permanent
  attempt consumption only for file-specific malformed/schema-invalid output;
  a global CLI usage/model/config error remains transient so it cannot exhaust
  every source file.

## File structure

| Path | Responsibility |
|---|---|
| `internal/llm/claudecode.go` | Final-event inspection, safe diagnostic shaping, anchored refusal classification, diagnostic precedence, ANSI/control canonicalization, and exact rune bound. |
| `internal/llm/claudecode_test.go` | One-axis eligibility, mixed streams, page/secret rejection, false-refusal negatives, fallbacks, ANSI/Control/Format cases, and 1,999/2,000/2,001-rune boundaries. |
| `internal/llm/testdata/bin/claude` | Deterministic fake modes for structured quota/auth/API/refusal and competing-stream cases. |
| `cmd/cogvault/ingest_integration_test.go` | User-visible report plus persisted `last_error`/status/attempt evidence. |
| `SPEC.md` | Current public classification, diagnostic precedence, normalization, and historical-row behavior. |
| `DESIGN.md` | Internal LLM adapter/error-class ownership in §§2.1, 2.6, 2.7, data flow, and file table. |
| `docs/research/v2-follow-ups.md` | Repair closed F6's superseded broad refusal summary without reopening it; preserve F15/F16 tracking. |

## Scenario coverage map

| Scenario | Ordered unit chain | Scenario evidence |
|---|---|---|
| S1 — recognize a subscription limit | U1 → U2 | `TestIngestNonzeroExitDiagnostic/weekly-limit` verifies the exact report and ledger message. Covers S1. |
| S2 — diagnose an ordinary CLI failure | U1 → U2 | `TestIngestNonzeroExitDiagnostic/stderr-fallback` verifies an ordinary non-structured failure remains actionable through the CLI. Covers S2. |
| S3 — preserve refusal and retry semantics | U1 → U2 | `TestDigestRefusal*`, `TestDigestGenericAPIErrorNotRefused`, `TestE2EMixedFailureIsolation`, `TestE2ERefusalTerminal`, and `TestE2ERefusalNotRetried` verify terminal/refused versus retryable/failed ledger outcomes. Covers S3. |

## U1: Final-event diagnostics and policy-specific refusal classification

Execution note: test-first
Files:
  Create: none
  Modify: `internal/llm/claudecode.go`, `internal/llm/claudecode_test.go`, `internal/llm/testdata/bin/claude`
  Test: `internal/llm/claudecode_test.go`
Interfaces:
  Consumes: `(*ClaudeCode).digest(context.Context, DigestRequest)`, `resultEvent`, `lastResultEvent([]resultEvent)` and the existing stdout/stderr buffers.
  Produces: unchanged exported `Adapter.Digest` interface and unchanged `ErrTransient`/`ErrRefused` sentinels; intentionally refined generic-`api_error` classification plus private inspection/canonicalization helpers with no cross-package API.
Test scenarios:
  happy: Observed weekly-limit JSON on exit 1 returns an error containing the reset message, satisfies `errors.Is(err, ErrTransient)`, and does not satisfy `ErrRefused`. Covers S1.
  edge: Separate fixtures prove refusal-classification versus diagnostic-persistence eligibility, including the existing `is_error:false` exit-zero AUP refusal; mixed-case/ANSI/whitespace-formatted known envelopes remain refused after canonicalization; explicit `not safeguards flagged`, quoted `"safeguards flagged"`, stale/embedded/suffix-only phrases and `connection refused` remain transient; completed or error-marked page/multiline content carrying a secret marker is not persisted. Covers S3.
  error: JSON-looking malformed/truncated/wrong-shape/no-result stdout is never emitted raw; stderr, non-JSON stdout, and process-error fallbacks follow the approved order; Format controls (`Cf`) and other terminal controls cannot survive display normalization. Covers S2.
  integration: n/a — leaf adapter unit; U2 walks the CLI/ledger boundary.
Steps:
  1. Add fake modes and failing tests named `TestDigestStructuredTransientDiagnostic`, `TestDigestGenericAPIErrorNotRefused`, `TestDigestDiagnosticEventEligibility`, `TestDigestDiagnosticShape`, expanded `TestDigestRefusal*`, `TestDigestNonzeroExitDiagnosticFallbacks`, and `TestNormalizeCLIDiagnostic`. Before interpreting a green focused run, execute `set -e; for name in TestDigestStructuredTransientDiagnostic TestDigestGenericAPIErrorNotRefused TestDigestDiagnosticEventEligibility TestDigestDiagnosticShape TestDigestNonzeroExitDiagnosticFallbacks TestNormalizeCLIDiagnostic; do go test ./internal/llm/ -list "^${name}$" | grep -qx "$name"; done`; every name must exist and any missing earlier name must fail the loop. Then run the focused tests and confirm they fail for the intended opaque-detail/classification/safety reasons.
  2. Refactor stdout inspection so valid event JSON exposes only its last result, classification eligibility preserves final `api_error` AUP inspection regardless of `is_error`, diagnostic-persistence eligibility applies the deviation's stricter conjunction, diagnostic shape rejects multiline/page-like content, and JSON-looking decode failures cannot become plain stdout.
  3. Canonicalize authoritative candidates before classification/display: remove recognized ANSI SGR/OSC sequences, collapse Unicode whitespace, replace remaining Control and Format (`Cf`) runes with `U+FFFD`, and truncate only messages over 2,000 runes to 1,999 plus `…`.
  4. Replace broad refusal matching with the deviation's exact anchored provider-envelope grammar and perform the refusal pass across classification-eligible final result, stderr, and permitted plain stdout before choosing a diagnostic message. Use separate eligibility axes, mixed-case accepted signatures, the observed Fable possessive envelope, ANSI/whitespace formatting, and explicit negated/quoted/embedded/suffix negatives so representative mutations fail.
  5. Run `go test ./internal/llm/ -run 'TestDigestStructuredTransientDiagnostic|TestDigestGenericAPIErrorNotRefused|TestDigestDiagnosticEventEligibility|TestDigestDiagnosticShape|TestDigestRefusal|TestDigestNonzeroExitDiagnosticFallbacks|TestNormalizeCLIDiagnostic' -v` plus `go test ./internal/llm/ -v`; confirm all new and existing timeout, malformed JSON, missing-binary, rate-limit, cancellation, and refusal cases pass. Commit: `fix(llm): surface actionable Claude CLI diagnostics`.
Acceptance: the explicit `go test -list` loop proves all six new named tests exist, and both focused/full `internal/llm` commands exit 0.

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
  edge: Stderr-only transient output appears identically in report/ledger; ANSI, Control/Format runes, and a 2,001-rune stderr diagnostic are canonicalized/truncated through the actual path; a page-like eligible result with a secret marker falls back without persisting the marker. Covers S2.
  error: JSON-looking truncated output with empty stderr persists only the process-error fallback; a repeated same-hash transient failure updates the single row's `last_error`, keeps attempts `0`, and creates no duplicate; explicit refusal remains terminal and un-retried. Covers S3.
  integration: Run the new subcases alongside `TestE2EMixedFailureIsolation`, `TestE2ERefusalTerminal`, `TestE2ERefusalNotRetried`, and `TestE2EModelChangeRecoversRefused`; together they walk S1, S2, and S3 plus historical-row recovery.
Steps:
  1. Extend `ledgerSnapshot` and its SELECT/Scan order with `last_error`; run `go test ./cmd/cogvault/` immediately so every existing helper caller is verified before adding new cases.
  2. Add `TestIngestNonzeroExitDiagnostic` subtests for structured weekly limit in interactive and `--scheduled` origins, stderr fallback, formatted/2,001-rune stderr, page-like eligible output with a secret marker, JSON-looking truncated output, and repeated same-hash transient failure. Use a fresh disposable DB except the rerun subtest, which deliberately keeps one DB across two failed runs and changes only the fake diagnostic.
  3. Build the complete expected ledger value as `digest: llm.Digest <source-path>: claude cli: <normalized-diagnostic>: transient llm failure`; assert `row.lastError` equals it exactly and stdout contains the exact `<per-file path>  <complete last_error>` sequence. For rejected page-like output, assert the secret marker occurs in neither surface. Also assert status `failed`, attempts `0`, row count `1` on rerun, and scheduled `run_origin` without substring-only diagnostic checks.
  4. Prove the new test exists with `go test ./cmd/cogvault/ -list '^TestIngestNonzeroExitDiagnostic$' | grep -qx TestIngestNonzeroExitDiagnostic`. Run `go test ./cmd/cogvault/ -run 'TestIngestNonzeroExitDiagnostic|TestE2EMixedFailureIsolation|TestE2ERefusalTerminal|TestE2ERefusalNotRetried|TestE2EModelChangeRecoversRefused' -v`, `go test ./internal/ingest/ -run 'TestRunTransientErrorNoAttemptIncrement|TestRunCancelAfterFirstFile|TestRunContextCanceled' -v`, and full `go test ./cmd/cogvault/`; confirm persistence, rerun, historical recovery, scheduled-origin, and cancellation paths pass. Commit: `test(ingest): pin Claude CLI diagnostics in report and ledger`.
Acceptance: the exact-name gate and all three focused/full commands exit 0; report and ledger carry the complete same wrapped error, and the repeated failure leaves one row with attempts `0`.

## U3: Synchronize canonical diagnostic and error-class contracts

Execution note: skip-test-first — canonical prose documents behavior already discriminated by U1 and U2.
Files:
  Create: none
  Modify: `SPEC.md`, `DESIGN.md`, `docs/research/v2-follow-ups.md`
  Test: none
Interfaces:
  Consumes: approved spec, committed safety deviation `docs/deviations/2026-08-17-llm-diagnostic-safety-review.md`, U1 behavior, and U2 report/ledger evidence.
  Produces: current canonical behavior text for future implementation and review; no runtime API.
Test scenarios:
  happy: `SPEC.md` states that a nonzero eligible final result supplies the diagnostic before stderr and remains transient unless the finite policy predicate matches. Covers S1 and S2.
  edge: Canon separates final-`api_error` refusal-classification eligibility (independent of `is_error`) from stricter diagnostic-persistence eligibility (`is_error` plus failure channel and safe shape), then states mixed-stream refusal precedence, JSON-looking raw-output rejection, ANSI/Control/Format canonicalization, exact 2,000-rune normalization, and historical-row non-migration. Covers S3.
  error: Reviewer compares every finite predicate phrase and fallback step against `internal/llm/claudecode.go`; any extra broad `api_error`/`API Error:` refusal claim fails acceptance.
  integration: n/a — documentation unit; U2 owns executable scenario evidence.
Steps:
  1. Update `SPEC.md` §§4.2 and 10 with the deviation's two eligibility rules: a final `api_error` is refusal-classifiable regardless of `is_error`, while persisting its structured diagnostic requires `is_error`, an approved failure channel, and safe diagnostic shape. Include the anchored policy-refusal grammar, classification-before-message precedence, safe fallbacks, ANSI/Control/Format canonicalization, exact normalization bound, exit-zero generic-API reclassification, and historical-row non-migration/recovery path.
  2. Update `DESIGN.md` §§2.1, 2.6, 2.7, the ingest/LLM data flow, and the file table so all current error-class and `internal/llm` ownership descriptions include refused/model-gated behavior and the diagnostic-safety ordering.
  3. Repair closed F6 in `docs/research/v2-follow-ups.md` without reopening it: retain its delivered refusal/model feature history, then record that the 2026-08-17 live weekly-limit evidence superseded terminal-reason/generic-prefix classification with the anchored current-canon rule. Preserve F15 and F16 verbatim.
  4. Run `rg -n 'api_error|API Error:|safeguards flagged|policy refusal|2,000|last_error|ErrRefused|refused' SPEC.md DESIGN.md docs/research/v2-follow-ups.md internal/llm/claudecode.go`; compare every current-canon/tracker match against the approved spec plus deviation and remove contradictory broad claims. Run `git diff --check -- SPEC.md DESIGN.md docs/research/v2-follow-ups.md` and `go test -race ./...`. Commit: `docs(llm): specify actionable CLI failure diagnostics`.
Acceptance: exact DESIGN sections and F6 are converged, F15/F16 remain intact, the grep comparison finds no contradictory current claim, `git diff --check` is clean, and `go test -race ./...` exits 0.

## Mutation/failure-state matrix

| Transition | Pre-state | Action | Expected post-state | Success | Forced failure | Rerun | Rollback or compensation | Headless | Cancellation or abort | Owning unit | Evidence owner |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| T1 — persist a transient Claude diagnostic | Disposable source has no current-hash ledger row. | Ingest receives a safe structured or fallback transient diagnostic and calls the existing failure upsert. | One `failed` row with canonical `last_error`, attempts `0`; report carries the same complete wrapped error. | U2 weekly/stderr fixtures prove exact report/ledger equality. | U2 page-like secret fixture proves the marker is absent; JSON-looking truncated output proves process-error fallback is persisted without raw JSON leakage. | U2 same-hash failed→failed fixture changes only the diagnostic and proves one replaced row, updated `last_error`, attempts `0`, no duplicate. | Delete the disposable temp DB/wiki fixture; no production migration or irreversible state exists. | U2 runs the weekly fixture with `--scheduled` and proves only `run_origin` differs. | Pre-run cancellation (`TestRunContextCanceled`) invokes no LLM; adapter cancellation (`TestDigestContextCancelledTransient`) wraps `ErrTransient`; generic transient persistence (`TestRunTransientErrorNoAttemptIncrement`) records attempts `0`. No stronger combined cancellation/rerun claim is made. | U2 | `.release-loop/evidence/U2/` focused/full command output and disposable ledger query |

## Carry-forward trigger audit

| Fired tracker row | Trigger class | What fired it | Disposition |
|---|---|---|---|
| F16 | edit-based | This plan modifies `internal/llm/claudecode.go` adjacent to the existing stdout/stderr `bytes.Buffer` capture. | Deferred below: a byte ceiling also changes valid success-page output and requires a separate design rather than an incidental diagnostic fix. |

Audited `docs/research/v2-follow-ups.md` at `7db28ef`: 2 open rows, 1 fired, 0 unobservable.

F15 is event-based and has not fired. Its exact stop condition remains
artifact-backed successful MCP `initialize` plus `tools/call` round trips from
both hosted clients through the same chosen IdP/tunnel deployment; no such
artifact exists in this local diagnostic work.

## Deferred to Follow-Up Work

- F15 remains in `docs/research/v2-follow-ups.md`. It requires real hosted-client
  deployment evidence and is not folded into a local ingest diagnostic fix.
- F16 remains Open after its edit trigger fired. A capture-time byte ceiling
  must define how legitimate generated page JSON is bounded; this plan changes
  only which bounded failure detail is persisted after the pre-existing capture.
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
  spec plus committed deviation ordering and tests remain unchanged.
- U2 may use one table-driven test or two named tests, but the public success
  criterion command must continue to select `TestIngestNonzeroExitDiagnostic`.
