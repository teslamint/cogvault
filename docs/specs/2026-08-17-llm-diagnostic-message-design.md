---
title: Actionable Claude CLI Diagnostics
status: draft
date: 2026-08-17
schema: spec/v1
---

# Actionable Claude CLI Diagnostics Design

_Created 2026-08-17._

## Overview

When Claude CLI exits nonzero, cogvault currently reads only stderr. Claude can
instead put the actionable reason in the final stdout JSON result while leaving
stderr empty, so ingest reports only `exit status 1`. Preserve the structured
reason without leaking generated content and refine the overloaded refusal rule
without changing retry budgets, ledger schema, or backend selection.

## User Scenarios

### S1: Recognize a subscription limit

An operator runs `cogvault ingest` after exhausting a Claude subscription
limit. The failed line and ledger error include the reset message, distinguishing
capacity from a PDF, schema, or alternative-backend problem.

### S2: Diagnose an ordinary CLI failure

Claude exits nonzero without an eligible structured error result but writes an
explanation to stderr or plain stdout. Cogvault keeps the best safe explanation
and falls back to the process error only when neither stream contains one.

### S3: Preserve policy-refusal and retry semantics

A provider policy refusal remains `refused` and terminal under the same model.
Quota, login, transport, and generic API failures remain transient with zero
permanent attempts consumed, even when they carry `terminal_reason: "api_error"`
or begin with a generic `API Error:` label.

## Scope

### In

- Extract a bounded diagnostic from Claude CLI's final stdout result event.
- Refine refusal detection so overloaded API-error metadata does not make
  transient failures terminal.
- Keep diagnostics single-line and safe for the ingest report and ledger.
- Update canonical LLM/ingest contract text and regression coverage.

### Out

- Adding an automatic backend or model fallback.
- Changing `llm.backend`, `llm.model`, retry budgets, or ledger schema.
- Printing raw event arrays, prompts, source contents, or complete stdout.

## Assumptions and Preconditions

| Claim | Command | Observed at | Observed result | Evidence source |
|---|---|---|---|---|
| Claude CLI can exit 1 for a weekly limit while placing the only actionable message in the final stdout result event. | `printf 'Reply with OK only.\n' \| claude --print --output-format json --allowedTools Read --model opus >"$OUT" 2>"$ERR"; CODE=$?; jq -r '[.[] \| select(.type=="result") \| {subtype,is_error,terminal_reason,result}] \| last' "$OUT"; wc -c <"$ERR"` | `2026-08-17T04:23:57Z` | Exit 1; final result was `is_error: true`, `terminal_reason: "api_error"`, `result: "You've hit your weekly limit · resets 12pm (Asia/Seoul)"`; stderr was empty. | `docs/research/2026-08-17-claude-cli-weekly-limit-diagnostic-evidence.json` |
| Current nonzero-exit handling discards stdout unless it matches refusal text. | `date -u +observed_at=%Y-%m-%dT%H:%M:%SZ; sed -n '35,90p' internal/llm/claudecode.go \| sha256sum` | `2026-08-17T04:25:14Z` | Hash `ff5021d7…eaaaa2`; the inspected branch checks broad refusal text, then stderr, then `runErr.Error()`. | Working tree at `afe2048` |

## Architecture

The change stays inside `internal/llm/claudecode.go`. Structured stdout is
decoded before classification. Only the last `type: "result"` event
participates; non-final events can never classify the call.

### Refusal rule

The July F6 design treated `terminal_reason: "api_error"` and any `API Error:`
prefix as refusal evidence. The live probe proves both are overloaded. This
design replaces that historical implementation assumption with an exact,
case-insensitive policy predicate. It returns true only when the trimmed text
contains `safeguards flagged`, contains `policy refusal`, or begins with
`API Error:` whose trimmed payload begins with `refused`. A generic
`connection refused`, `api_error`, `API Error: rate limit`, authentication
error, or other API failure is not refusal evidence.

For a structured result, compute error eligibility first: `is_error` is true,
`subtype` is `error_during_execution`, or `terminal_reason` is `api_error`.
Only an eligible final result may participate in refusal classification or
diagnostic extraction. A completed non-error result containing refusal-like
prose is ordinary generated content.

Classify policy refusal across every authoritative stream before choosing a
diagnostic message: the eligible final structured result, stderr, and
non-JSON-looking plain stdout. This makes an explicit refusal terminal even
when another stream carries a generic error. Valid JSON is never scanned as a
raw whole string, so stale non-final text cannot override the final event.

Plain stdout means its first non-whitespace rune is neither `{` nor `[`. Any
JSON-looking stdout that is malformed, has the wrong top-level shape, or lacks
a final result event is ineligible as plain text and falls through to stderr or
the process error. This prevents truncated event arrays from leaking generated
content.

### Nonzero-exit diagnostic precedence

After the refusal-classification pass above:

1. Use the eligible final result's non-empty `result`.
2. Otherwise use non-empty stderr.
3. If stderr is empty, use non-JSON-looking plain stdout.
4. Fall back to the process error.

Exit-zero parsing uses the same policy-specific predicate. A final `api_error`
without policy-refusal text is `ErrTransient`; malformed JSON and ordinary
schema-invalid success output keep their permanent behavior.

### Normalization bound

Normalize the selected message in rune order. Runs of Unicode whitespace,
including CR/LF, tabs, and line/paragraph separators, become one ASCII space.
Other Unicode control characters, including ESC, NUL, and C1 controls, become
`U+FFFD`. If the normalized message exceeds 2,000 runes, retain the first 1,999
and append the one-rune marker `…`; the final diagnostic is at most 2,000 runes
including the marker. The cap is a code constant, not configuration.

Transient errors still wrap `ErrTransient`, so ingest attempt accounting stays
unchanged.

## Interface and Contract

There is no new flag, config field, exported API, or ledger field. Nonzero
transient failures gain a more specific normalized error detail in the report
and ledger `last_error`. There is also one intentional classification change:
an exit-zero generic `api_error` without policy-specific refusal text becomes
`ErrTransient` instead of `ErrRefused`, changing its report bucket, ledger
status, and future retry behavior.

No migration rewrites existing ledger rows. A generic API failure already
stored as `refused` remains terminal under the same model until the configured
model or source content changes; this feature prevents new false refusals but
does not infer which historical rows were wrong.

`SPEC.md` §4.2/§10 and `DESIGN.md`'s LLM component will state final-event
eligibility, normalization, and the policy-specific refusal rule. The
historical F6 design remains an archaeology record; current canon explicitly
supersedes its broad `api_error`/`API Error:` assumption.

## Testing

- Extend the fake CLI with nonzero JSON modes for weekly limit, generic API
  rate limit, authentication failure, explicit refusal, stale non-final
  safeguards text followed by quota, and completed non-error output plus
  actionable stderr.
- Add mixed-stream cases (structured quota plus stderr refusal; stdout refusal
  plus generic stderr), JSON-looking malformed/truncated output, wrong JSON
  shapes, and completed content containing the exact refusal phrase.
- Assert only the final eligible result participates, generic API errors remain
  transient, and policy refusal remains refused on both exit paths.
- Cover stderr/plain-stdout/process fallbacks; ESC, NUL, CR/LF, tabs, Unicode
  separators; multibyte text; and exact 1,999/2,000/2,001-rune bounds.
- Re-run timeout, malformed JSON, missing-binary, and attempt-accounting tests.
- In CLI integration, assert exact diagnostic equality in the report and ledger
  `last_error`, status `failed`, and attempts `0`.

## Risks and Mitigations

- **Generic API failures become terminal.** Use only policy-specific phrases and
  test quota/auth negatives on exit-zero and nonzero paths.
- **Stale events or generated content mask stderr.** Parse first, require final
  error eligibility, reject malformed JSON-looking stdout as plain text, and
  test each negative case.
- **A generic diagnostic masks policy evidence in another stream.** Classify
  refusal across eligible structured output, stderr, and permitted plain
  stdout before applying diagnostic-message precedence.
- **Terminal control or oversized output reaches report/ledger.** Replace
  controls, collapse whitespace, and enforce the exact inclusive bound.
- **Retry behavior changes accidentally.** Require sentinel, ledger status, and
  attempts assertions alongside message assertions.

## Success Criteria

1. The observed weekly-limit JSON reports its reset message while remaining transient and not refused; generic API, rate-limit, and authentication errors follow the same class.
   - **Measured by**: `go test ./internal/llm/ -run 'TestDigestStructuredTransientDiagnostic|TestDigestGenericAPIErrorNotRefused' -v`
2. Only the final error-eligible result can supply or classify structured output; stale non-final text, completed content containing refusal phrases, and malformed JSON-looking stdout cannot override the final result or stderr.
   - **Measured by**: `go test ./internal/llm/ -run TestDigestDiagnosticEventEligibility -v`
3. Plain and structured policy-specific refusals remain `ErrRefused` on exit-zero, nonzero, and mixed-stream paths, while `connection refused` remains transient.
   - **Measured by**: `go test ./internal/llm/ -run TestDigestRefusal -v`
4. Fallbacks preserve stderr, then non-JSON stdout, then the process error; normalization replaces terminal controls and enforces the exact 2,000-rune inclusive bound.
   - **Measured by**: `go test ./internal/llm/ -run 'TestDigestNonzeroExitDiagnosticFallbacks|TestNormalizeCLIDiagnostic' -v`
5. The ingest report and ledger `last_error` contain the same normalized weekly-limit reason, with status `failed` and attempts `0`.
   - **Measured by**: `go test ./cmd/cogvault/ -run TestIngestNonzeroExitDiagnostic -v`
6. The complete repository remains regression-free.
   - **Measured by**: `go test -race ./...`

## Open Decisions

No product decisions remain open. Planning may choose private helper names and
test-fixture organization, but it may not change extraction precedence,
policy-specific refusal semantics, event eligibility, control normalization, or
the 2,000-rune inclusive bound.
