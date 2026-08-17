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

1. Classification eligibility and diagnostic-persistence eligibility are
   separate. The final result is refusal-classifiable when
   `terminal_reason == "api_error"` even if `is_error` is false; this preserves
   the observed exit-zero AUP refusal. If the anchored predicate does not
   match, a generic final `api_error` is transient.
2. A structured result is diagnostic-persistable only when `is_error` is true
   **and** either `terminal_reason == "api_error"` or
   `subtype == "error_during_execution"`. Its `result` is diagnostic-shaped
   only when, after
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
4. Policy refusal is anchored after canonicalization. It matches a line
   beginning `policy refusal:`, or this case-insensitive Go/RE2 grammar for the
   known provider envelope:
   `^api error:\s*(?:refused\b|(?:[\p{L}\p{N} ._-]+(?:'s|’s)\s+)?safeguards flagged\b)`.
   The optional provider possessive admits the observed
   `Fable 5's safeguards flagged` form without accepting negated, quoted,
   embedded, suffix-only, or generic `connection refused` text.
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

- Classification eligibility and diagnostic-persistence eligibility get
  separate one-axis tests, including the existing `is_error:false` exit-zero
  AUP refusal and a generic `api_error` transient.
- A page-like/multiline eligible result carrying a secret marker must appear in
  neither report nor ledger.
- Mixed-case accepted signatures and formatted (whitespace/ANSI) known
  envelopes must classify terminal after canonicalization.
- Explicit negatives include `API Error: not safeguards flagged`,
  `API Error: response contained "safeguards flagged"`, prefix/suffix-only
  phrases, quoted/negated/embedded `policy refusal`, `connection refused`, and
  completed generated content.
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
