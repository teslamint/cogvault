# 0002-step1-deferred-items

Status: deferred
Date: 2026-04-06

## Context

Step 1 implementation and review surfaced a small set of issues that are known, understood, and intentionally not addressed yet.
They should not remain only in chat history because multiple agents may continue implementation.

## Decision

The following items are deferred:

1. Validation errors do not currently wrap the config file path.
2. The package name `internal/errors` remains unchanged even though it overlaps with the standard-library package name.

## Why

### Validation error wrapping

For the single-vault MVP, messages such as `wiki_dir: ...` and `db_path: ...` are considered sufficient.
The project does not yet have multi-vault CLI flows or log aggregation requirements that make config-path context mandatory.

### `internal/errors` naming

The alias cost is acceptable for now, and renaming the package would create churn across future packages before there is evidence that the naming overlap is actually painful in this codebase.

## Alternatives Considered

- Wrap validation errors as `config <path>: <field error>`
  Deferred, not rejected.
- Rename `internal/errors` to `internal/cverr`, `internal/errs`, or similar
  Deferred, not rejected.

## Revisit Triggers

Revisit validation error wrapping if any of the following appears:

- multi-vault CLI flows,
- log aggregation or centralized diagnostics,
- multiple `Load()` call sites with different config roots.

Revisit the package name if any of the following appears:

- aliasing is repeated across 3 or more implementation packages,
- reviewers repeatedly flag readability problems around `errors` imports,
- standard-library `errors` and project `errors` are commonly used in the same files.

## Related Files

- `CLAUDE.md`
- `internal/config/config.go`
- `internal/errors/errors.go`
