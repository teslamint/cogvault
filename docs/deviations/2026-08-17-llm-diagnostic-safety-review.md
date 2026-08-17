# Deviation: provider diagnostics require safety shaping before persistence

Date: 2026-08-17
Area: Claude CLI diagnostics and ingest error classification (`internal/llm`, `internal/ingest`)

## Original contract

The approved `docs/specs/2026-08-17-llm-diagnostic-message-design.md` permits a
final result event to supply a diagnostic when `is_error` is true, its subtype
is `error_during_execution`, or its terminal reason is `api_error`. It then
normalizes and stores up to 2,000 runes in the ingest report and ledger.

The same spec uses a policy-refusal predicate that contains `safeguards flagged`
or `policy refusal`, or recognizes an `API Error:` payload beginning with
`refused`. Classification occurs before display normalization.

## Discovered contradiction

Independent plan review found that error eligibility is not a content-safety
guarantee. A failed result can contain a partial generated page, prompt excerpt,
or source-derived text. Persisting it would contradict the approved scope's
prohibition on printing generated/source content.

The predicate also operates before whitespace/control canonicalization and uses
unanchored containment. Formatting can therefore make a real refusal transient,
while quoted or negated text such as `not a policy refusal` can make a transient
failure terminal. A false `refused` ledger row is skipped indefinitely under
the same source hash and model.

## Why documentation alone cannot fix it

The contradiction is in executable classification and persistence behavior.
Clarifying prose without changing the adapter would still allow page-like
content into `last_error` and would retain both refusal evasion and false
terminal classification. Tests must discriminate the revised behavior through
the actual adapter, report, and ledger paths.

## New observable behavior

1. A structured result is error-eligible only when `is_error` is true **and**
   either `terminal_reason == "api_error"` or
   `subtype == "error_during_execution"`.
2. An eligible structured `result` is diagnostic-shaped only when, after
   trimming, it is non-empty, contains no CR/LF, and does not begin with a YAML
   frontmatter delimiter, Markdown heading, or code fence. Otherwise stderr or
   the process error is used. This blocks page-like/multiline output; because
   provider text is not typed beyond `result`, a short single-line diagnostic
   may still contain source-derived wording. That bounded local persistence is
   explicitly accepted to keep unknown operational failures actionable.
3. Each authoritative candidate is canonicalized before both classification
   and display: recognized ANSI SGR/OSC sequences are removed; Unicode
   whitespace collapses to one ASCII space; remaining Unicode Control and
   Format (`Cf`) runes become `U+FFFD`.
4. Policy refusal is anchored after canonicalization. It matches only an
   `API Error:` envelope whose payload begins with `refused` or contains
   `safeguards flagged`, or a line beginning with `policy refusal:`. Quoted,
   negated, embedded, suffix-only, and generic `connection refused` text is
   transient.
5. Classification still inspects every authoritative stream before diagnostic
   selection. The approved message precedence and 2,000-rune inclusive bound
   remain unchanged.
6. Existing ledger rows are not migrated. The operator recovery path for a
   known false-refused row remains changing `llm.model` or changing the source
   content, which causes the existing model/content-hash gate to re-attempt.

## Safety and consent boundaries

The feature remains local and does not add an external trust boundary. It now
persists less provider-controlled content and removes terminal-display controls.
The accepted residual is a bounded single-line provider diagnostic in a local
report/SQLite ledger visible to the same operator who owns the source.

The review suggestion to classify exit code `2` as permanent is rejected.
`docs/decisions/0021-v2-refounding.md` D4 reserves permanent attempts for
file-specific malformed/schema-invalid outcomes. A global CLI usage, model, or
configuration error must not consume every file's attempt budget; it remains
transient but becomes actionable through the improved diagnostic.

The review's output-capture memory concern predates this feature: current
success and failure paths already buffer complete stdout/stderr. Adding a byte
ceiling would also bound legitimate generated pages and is a separate product
contract. It is registered as F16 rather than hidden in this remediation.

## Verification changes

- Error eligibility gets one-axis tests for `is_error`, subtype, and terminal
  reason rather than fixtures setting several at once.
- A page-like/multiline eligible result carrying a secret marker must appear in
  neither report nor ledger.
- Mixed-case accepted signatures and formatted (whitespace/ANSI) refusals must
  classify terminal after canonicalization.
- Quoted, negated, embedded, suffix-only, `connection refused`, and completed
  generated-content cases must remain non-refused.
- Actual report/ledger tests cover format controls (`Cf`), ANSI, control runes,
  and the 2,001-rune truncation boundary.
- Plan acceptance must prove every new named test exists; broad `go test -run`
  alternations alone are not sufficient.

## Traceability

- Approved spec: `docs/specs/2026-08-17-llm-diagnostic-message-design.md`
- Draft plan under review: `docs/plans/2026-08-17-001-fix-llm-diagnostic-message-plan.md`
- Review artifact: `.release-loop/reviews/plan-independent-review.md`
- Current adapter: `internal/llm/claudecode.go`
- Persistence path: `internal/ingest/ingest.go`, `internal/ingest/report.go`
- Existing retry authority: `docs/decisions/0021-v2-refounding.md` D4
- Historical refusal feature: `docs/research/v2-follow-ups.md` F6 and
  `docs/specs/2026-07-23-aup-error-class-and-model-option-design.md`
