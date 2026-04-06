# 0001-config-validation

Status: accepted
Date: 2026-04-06

## Context

Step 1 introduces YAML config loading and validation for the MVP.
Multiple agents reviewed the design before implementation. Some decisions are implementation-level and do not belong directly in `SPEC.md`, but they still need to be shared across agents.

## Decision

The config layer validates path-string safety and minimal policy constraints only.

Accepted rules:

- `wiki_dir` must be vault-relative, must not contain `..`, and must not resolve to the vault root (`"."` or equivalent such as `"./"`).
- `db_path` must be vault-relative, must not contain `..`, must not be `"."`, and must not represent a directory-like path.
- `exclude` and `exclude_read` entries must be vault-relative, must not contain `..`, and must reject absolute paths, empty strings, and `"."`.
- YAML decoding uses `KnownFields(true)` to reject unknown keys.
- Empty config files are treated as an empty config and then defaults are applied.
- `AllExcluded()` returns `exclude` followed by `exclude_read` with no normalization or deduplication.

## Why

- The MVP prioritizes explicit failure over silent acceptance of suspicious config.
- Path-string validation belongs in config because it defines allowed configuration shape.
- Filesystem state and permissions do not belong in config validation; those are enforced later by storage.
- Rejecting unknown keys prevents typo-driven silent misconfiguration.

## Alternatives Considered

- Using `filepath.Clean()` before rejecting `..`
  Rejected because inputs like `foo/../bar` would become harder to reason about and could hide intent.
- Deduplicating `AllExcluded()`
  Rejected because the current consumers only need ordered concatenation and the extra normalization is unnecessary for MVP.
- Allowing unknown YAML fields
  Rejected because typo detection is more valuable than forward compatibility in MVP.

## Revisit Triggers

- Windows support is added.
- Config is loaded from more than one source and merge semantics become necessary.
- Storage or scanner consumers need normalized exclusion sets instead of ordered concatenation.

## Related Files

- `SPEC.md`
- `DESIGN.md`
- `internal/config/config.go`
- `internal/config/config_test.go`
