# 0022-repository-working-conventions

Status: accepted
Date: 2026-07-23

## Context

`CLAUDE.md` section 7 had become the only repository-local place where several active implementation conventions were collected. Decision [0012](0012-agent-documentation-governance.md) D1 says durable project rules must live in neutral decision records rather than only in agent-facing documents. This record relocates the active conventions without changing their meaning, qualifies legacy references against current canon, and preserves stale wording for audit. [0023](0023-stale-agent-convention-reconciliation.md) separately resolves the two statements whose literal promotion conflicted with current governance or architecture.

The same cleanup also relocates one maintenance obligation from `CLAUDE.md` section 0: after a completed Step, README progress status and the coding-convention decision reference list must be updated as part of completion work.

## Decision

### D1. Documentation maintenance is part of implementation completion

The pre-cleanup source required README progress status and the coding-convention decision-reference list to be updated after every completed Step.

Required updates:

- update `README.md` progress status to match the completed Step outcome.
- update the specialized-decision index in D3 so the coding-convention references match the completed Step outcome.

This keeps the old `CLAUDE.md` section 0 obligation auditable instead of leaving it as an agent-only reminder. [0023](0023-stale-agent-convention-reconciliation.md) D1 supersedes its unconditional normative meaning with an impact-based rule.

### D2. Core repository working conventions

The active repository-wide implementation conventions formerly listed in `CLAUDE.md` section 7 are:

1. Use the standard Go project layout centered on `cmd/` and `internal/`.
2. Storage, Index, and Adapter are interfaces. Unit tests use mocks.
3. Wrap errors with operation-specific context using `%w`, and rely on `errors.Is()` or equivalent caller-side inspection at the boundary that handles them.
4. Use structured logging via `log/slog`; debug-only detail belongs behind debug-level logging.
5. Verify changes with `go test -race ./...`. Tests use interface mocks plus `testdata/fixtures`.
6. Pin dependency versions explicitly in `go.mod`, which is the current version authority, and commit `go.sum` as integrity metadata.

The pre-cleanup source also said: "Propagate `context.Context` through all I/O and LLM-facing calls." That sentence is preserved here for lossless migration. [0023](0023-stale-agent-convention-reconciliation.md) D2 resolves its current normative meaning against the implemented interface boundaries.

### D3. Specialized convention owners remain canonical for their boundaries

The section 7 links are preserved here as a qualified index rather than repeated as prose inside an agent-facing file. A linked legacy record is not valid in full when current `SPEC.md`, `DESIGN.md`, or a later accepted decision supersedes part of it:

- [0001-config-validation](0001-config-validation.md) remains useful for config/storage responsibility, validation order, and YAML-field strictness. Its v1 `wiki_dir` and `db_path` relative-path rules are superseded by [0021](0021-v2-refounding.md) D1 and current `SPEC.md`; `exclude` and `exclude_read` remain wiki-root-relative.
- [0006-storage-write-serialization](0006-storage-write-serialization.md) owns the MVP single global storage write mutex rule.
- [0007-storage-error-mapping](0007-storage-error-mapping.md) owns which storage failures become sentinel errors and which stay wrapped raw errors.
- [0008-step3-adapter-decisions](0008-step3-adapter-decisions.md) owns the accepted adapter-layer implementation decisions recorded during Step 3.
- [0009-step3-deferred-items](0009-step3-deferred-items.md) is a historical deferred-item tracker; revalidate each open item against current canon before acting on it.
- [0015-step6-cmd-decisions](0015-step6-cmd-decisions.md) preserves accepted v1 CLI rationale where current code and canon still retain it. In D6, `WikiDir` and `DBPath` are now absolute under [0021](0021-v2-refounding.md) D1 and current `SPEC.md`; `SchemaPath`, `Exclude`, and `ExcludeRead` remain wiki-root-relative.
- [0016-step6-deferred-items](0016-step6-deferred-items.md) is a historical Step 6 tracker whose resolved markers remain useful; revalidate any unstruck item against current code and canon.
- [0023-stale-agent-convention-reconciliation](0023-stale-agent-convention-reconciliation.md) owns the current impact-based documentation-maintenance rule and context-propagation boundary.

This decision does not supersede their still-applicable clauses. It records which legacy clauses need current-canon qualification and gives the old section 7 list a neutral top-level owner.

## Why

- The active conventions already existed; relocating them and explicitly isolating the stale all-I/O statement satisfies [0012](0012-agent-documentation-governance.md) without inventing a new architecture policy.
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
- `docs/decisions/0021-v2-refounding.md`
- `docs/decisions/0023-stale-agent-convention-reconciliation.md`
