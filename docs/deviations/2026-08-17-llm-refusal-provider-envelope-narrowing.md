# Deviation: refusal classification accepts only observed provider envelopes

Date: 2026-08-17
Area: Claude CLI policy-refusal classification (`internal/llm`)

## Original contract

`docs/deviations/2026-08-17-llm-diagnostic-safety-review.md` authorizes this
case-insensitive provider grammar after diagnostic canonicalization:

`^api error:\s*(?:refused\b|(?:[\p{L}\p{N} ._-]+(?:'s|’s)\s+)?safeguards flagged\b)`

The approved plan simultaneously requires quoted, negated, embedded, and
suffix-only safeguards phrases to remain transient.

## Discovered contradiction

Final branch review proved the provider-name branch is greedy enough to accept
arbitrary leading prose. Both examples below match and become terminal even
though the approved plan requires them to remain transient:

- `API Error: not Fable 5's safeguards flagged`
- `API Error: response says Fable's safeguards flagged`

A false `ErrRefused` row is skipped indefinitely for the same model and source
hash, recreating the poisoned-ledger failure this feature is intended to remove.

## Why documentation alone cannot fix it

The shipped predicate in `internal/llm/claudecode.go` executes the broad regex.
Tests omit the possessive-envelope negative variants, so prose changes alone
would leave both false-terminal paths live.

## New observable behavior

After canonicalization, refusal classification accepts only these anchored,
case-insensitive forms:

1. a line beginning `policy refusal:`;
2. `API Error:` whose payload begins `refused`;
3. `API Error:` whose payload begins `safeguards flagged`; or
4. the observed provider envelope `API Error: Fable 5's safeguards flagged`
   (ASCII or curly apostrophe).

Unknown provider-name safeguard envelopes remain transient and visible rather
than terminal until an observed, reviewed fixture justifies adding them. The two
negated/embedded examples above, quoted phrases, suffix-only phrases, and
`connection refused` remain transient.

## Safety and consent boundaries

This narrows terminal classification and cannot make a transient failure consume
permanent attempts. The conservative failure direction is retry plus actionable
visibility, not silently skipping a file forever. Existing ledger rows are not
migrated; model/content change remains the recovery path for a known historical
false refusal.

## Verification changes

- Adapter tests assert both negative examples are `ErrTransient`, not
  `ErrRefused`, and retain their diagnostic text.
- CLI/ingest integration runs both examples through report and ledger, proves
  status `failed`, attempts `0`, then re-runs the same hash successfully to
  demonstrate recovery rather than same-model terminal skip.
- Existing positive fixtures for `API Error: refused`, bare
  `safeguards flagged`, `policy refusal:`, and the exact Fable 5 possessive
  envelope remain `ErrRefused`.
- The sealed scenario-map names are realized literally by subtests
  `weekly-limit` and `stderr-fallback`; the scheduled companion remains a
  separate subtest.

## Traceability

- Approved spec: `docs/specs/2026-08-17-llm-diagnostic-message-design.md`
- Approved sealed plan: `docs/plans/2026-08-17-001-fix-llm-diagnostic-message-plan.md`
- Prior deviation: `docs/deviations/2026-08-17-llm-diagnostic-safety-review.md`
- Final branch review: `.release-loop/reviews/branch-diff.txt` review finding on
  `internal/llm/claudecode.go` refusal regex and scenario-map subtest names
- Code: `internal/llm/claudecode.go`
- Tests: `internal/llm/claudecode_test.go`,
  `cmd/cogvault/ingest_integration_test.go`
