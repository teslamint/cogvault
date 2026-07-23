# 0022-repository-working-conventions

Status: accepted
Date: 2026-07-23

## Context

`CLAUDE.md` section 7 had become the only repository-local place where several active implementation conventions were collected. Decision [0012](0012-agent-documentation-governance.md) D1 says durable project rules must live in neutral decision records rather than only in agent-facing documents. This record relocates the active conventions without changing their meaning.

The same cleanup also relocates one maintenance obligation from `CLAUDE.md` section 0: after a completed Step, README progress status and the coding-convention decision reference list must be updated as part of completion work.

## Decision

### D1. Documentation maintenance is part of implementation completion

After a completed Step, README progress status and the coding-convention decision reference list MUST be updated before calling the work complete.

Required updates:

- update `README.md` progress status to match the completed Step outcome.
- update the coding-convention decision reference list carried by the repository's derivative guidance documents.

This keeps the old `CLAUDE.md` section 0 obligation in neutral canon instead of leaving it as an agent-only reminder.

### D2. Core repository working conventions

The repository-wide implementation conventions formerly listed in `CLAUDE.md` section 7 are:

1. Use the standard Go project layout centered on `cmd/` and `internal/`.
2. Storage, Index, and Adapter are interfaces. Unit tests use mocks.
3. Wrap errors with operation-specific context using `%w`, and rely on `errors.Is()` or equivalent caller-side inspection at the boundary that handles them.
4. Propagate `context.Context` through I/O and LLM-facing calls.
5. Use structured logging via `log/slog`; debug-only detail belongs behind debug-level logging.
6. Verify changes with `go test -race ./...`. Tests use interface mocks plus `testdata/fixtures`.
7. Pin dependency versions explicitly in `go.mod`, commit `go.sum`, and keep the historical `mcp-go` v0.46.0 note as reference context from `sage-wiki`; the actual implementation-time version is fixed when the code lands.

### D3. Specialized convention owners remain canonical for their boundaries

The section 7 links to decision records remain valid and are now incorporated by reference instead of being repeated as prose inside an agent-facing file:

- [0001-config-validation](0001-config-validation.md) owns config validation boundaries, validation order, and YAML-field strictness.
- [0006-storage-write-serialization](0006-storage-write-serialization.md) owns the MVP single global storage write mutex rule.
- [0007-storage-error-mapping](0007-storage-error-mapping.md) owns which storage failures become sentinel errors and which stay wrapped raw errors.
- [0008-step3-adapter-decisions](0008-step3-adapter-decisions.md) owns the accepted adapter-layer implementation decisions recorded during Step 3.
- [0009-step3-deferred-items](0009-step3-deferred-items.md) owns the still-deferred Step 3 follow-up obligations.
- [0015-step6-cmd-decisions](0015-step6-cmd-decisions.md) owns the accepted CLI implementation decisions recorded during Step 6.
- [0016-step6-deferred-items](0016-step6-deferred-items.md) owns the still-deferred Step 6 follow-up obligations.

This decision does not supersede those records. It exists to give section 7 a neutral top-level owner and a stable place to point future agents and reviewers.

## Why

- The conventions already existed and were active; relocating them to a neutral decision record satisfies [0012](0012-agent-documentation-governance.md) without inventing a new policy.
- The repository needed one neutral owner for the "update README progress and convention references after a completed step" obligation.
- Linking specialized decisions keeps this file short and avoids duplicating implementation-specific rationale already reviewed elsewhere.

## Alternatives Considered

- Keep the conventions only in `CLAUDE.md`
  Rejected because that leaves active repository-wide rules in an agent-facing document.
- Copy the full specialized decision text into this record
  Rejected because duplication would increase drift without adding new policy value.
- Expand this cleanup into a new coding standard beyond the current section 7 text
  Rejected because U1 is a relocation task, not a policy-design task.

## Revisit Triggers

- A future cleanup changes repository-wide verification or dependency rules rather than merely relocating them.
- The repository stops using `CLAUDE.md` and `AGENTS.md` as derivative entrypoints and needs a different top-level convention index.
- One of the specialized linked decisions is superseded and this index must be updated to point at the new owner.

## Related Files

- `CLAUDE.md`
- `AGENTS.md`
- `README.md`
- `docs/context/project-background.md`
- `docs/decisions/0001-config-validation.md`
- `docs/decisions/0006-storage-write-serialization.md`
- `docs/decisions/0007-storage-error-mapping.md`
- `docs/decisions/0008-step3-adapter-decisions.md`
- `docs/decisions/0009-step3-deferred-items.md`
- `docs/decisions/0012-agent-documentation-governance.md`
- `docs/decisions/0015-step6-cmd-decisions.md`
- `docs/decisions/0016-step6-deferred-items.md`
