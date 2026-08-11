# Deviation: an explicit `0` in the `auth:` numeric knobs takes the default rather than erroring

Date: 2026-08-11
Area: config validation (`internal/config`; SPEC §3.1 `auth:` block)

## Original contract

`docs/specs/2026-08-11-remote-mcp-server-design.md`, Interface → Config
validation, stated:

> - `auth.max_body_mb`, `auth.max_stream_seconds`, or
>   `auth.oauth.jwks_ttl_seconds` zero or negative → error, matching the
>   existing `max_file_size_mb` rejection style in `internal/config`.

`docs/plans/2026-08-11-001-feat-remote-mcp-server-plan.md` U1 repeated it:

> error: … zero or negative `max_body_mb`, `max_stream_seconds`, or
> `jwks_ttl_seconds` are each rejected

Both require an explicit `0` to be a startup error.

## Discovered contradiction

The same spec sentence also requires defaults for an omitted `auth:` block —
`max_body_mb: 4`, `max_stream_seconds: 3600`, `jwks_ttl_seconds: 900` — and
cites `max_file_size_mb` as the style to match. The three requirements cannot
all hold at once.

The fields are plain Go `int`s, so an omitted YAML key and an explicit `0`
decode to the identical value. `Load` applies defaults before validating:
`applyDefaults` substitutes on `== 0` (`internal/config/config.go:138` for the
pre-existing `MaxFileSizeMB`), and `validate` then checks `< 0`
(`internal/config/config.go:228`). By the time `validate` runs, no literal zero
survives to reject.

Rejecting zero would therefore reject every config that omits the `auth:`
block — the exact case the defaults exist to serve. The spec's own cited
authority, `max_file_size_mb`, rejects only negatives for this reason.

## Why documentation alone cannot fix it

The contract is unimplementable as written, not merely undocumented. Honoring
it would require changing the fields to `*int` so absent and zero become
distinguishable, which changes the config type surface, the defaults mechanism,
and the established convention every other numeric knob in this file follows.
That is a deliberate design change, not a note.

## New observable behavior

| Config input | Behavior |
|---|---|
| key omitted | default applied (`4` / `3600` / `900`) |
| explicit `0` | default applied — **indistinguishable from omitted** |
| negative | startup error, e.g. `auth.max_body_mb: must be positive; expected a value in megabytes` |

The spec's stated intent — no server ever runs with a nonsensical zero body
cap, stream cap, or JWKS TTL — still holds. Zero never reaches the running
server; it is replaced by the safe default rather than refused.

## Safety and consent boundaries

Unchanged. No transport, authorization mode, credential handling, or startup
guard is affected. The three knobs bound resource usage; substituting the
documented default for `0` is strictly more conservative than treating `0` as
"unlimited", which no code path does. No consent or permission boundary moves.

## Verification changes

U1's test suite covers the behavior in two places instead of one:

- the defaults case asserts an omitted `auth:` block, and therefore a zero
  value, yields `4` / `3600` / `900`;
- the error cases assert only negatives are rejected.

The plan's "zero … rejected" assertion is not written, because it cannot pass
without the `*int` redesign.

## Traceability

- Approved spec: `docs/specs/2026-08-11-remote-mcp-server-design.md`
  (Interface → Config validation)
- Approved plan: `docs/plans/2026-08-11-001-feat-remote-mcp-server-plan.md`
  (U1 test scenarios, error row)
- Code: `internal/config/config.go` — `applyDefaults`, `validate`
- Tests: `internal/config/config_test.go::TestAuthConfigValidation`
- Pre-existing precedent: `internal/config/config.go:138` and `:228`
  (`MaxFileSizeMB`)
- Surfaced by: the U1 implementer's report,
  `.release-loop/reports/U1-report.md`, and confirmed against source before
  acceptance
- Follow-up: whether these knobs should become `*int` so an explicit `0` is
  distinguishable is a config-wide question affecting `max_file_size_mb` and
  `consistency_interval` too. Out of scope for this feature.
