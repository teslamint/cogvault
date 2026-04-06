# Step 1 Review Notes

Date: 2026-04-06
Scope: `internal/errors`, `internal/config`, related `SPEC.md` / `DESIGN.md` alignment

## Summary

Step 1 received multiple review passes from different agents.
The main themes were:

- config validation contract,
- path safety edge cases,
- document/implementation consistency,
- deferred UX and naming tradeoffs.

## Issues That Were Fixed

- `wiki_dir`, `db_path`, `exclude`, and `exclude_read` validation were tightened.
- Unknown YAML keys are rejected via `KnownFields(true)`.
- Empty config files are treated as empty config and then defaulted.
- Multiple YAML documents are rejected.
- Directory-like `db_path` inputs such as `data/.` are rejected.
- `SPEC.md` and `DESIGN.md` were updated to reflect the implemented config contract.

## Deferred Items Promoted To Decisions

See:

- `docs/decisions/0001-config-validation.md`
- `docs/decisions/0002-step1-deferred-items.md`

## Notes

- Review discussions happened across multiple agents. This file is intentionally a compact summary, not a transcript.
- If Step 2 introduces new path-handling rules, update the decision records instead of appending ad hoc notes here.
