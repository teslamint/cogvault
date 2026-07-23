# 0023-stale-agent-convention-reconciliation

Status: accepted
Date: 2026-07-23

## Context

The agent-documentation cleanup moved durable guidance out of `CLAUDE.md` under [0012](0012-agent-documentation-governance.md). Review found two agent-only statements whose literal promotion would conflict with current repository reality:

1. every completed Step must update README progress and the convention-reference list, even when neither changed; and
2. every I/O and LLM call must carry `context.Context`, although current storage, index, and source-adapter interfaces are context-free.

[0022](0022-repository-working-conventions.md) preserves both source statements for lossless migration. This separate decision resolves their current normative meaning instead of silently changing them inside the relocation decision. The user approved this reconciliation path on 2026-07-23.

## Decision

### D1. Completion documentation updates are impact-based

After a completed Step:

- update `README.md` when user-visible project progress or status changed;
- update [0022](0022-repository-working-conventions.md) D3 when convention ownership or applicability changed; and
- update derivative entrypoint pointers only when an owner or path changed.

Do not create no-op documentation edits when none of those conditions applies. This aligns completion work with [0012](0012-agent-documentation-governance.md) D5 rather than treating every Step as a documentation-status change. D1 only qualifies the old unconditional README/index reminder; it does not replace D5's requirement to update affected canonical documents.

### D2. Context propagation follows current cancellable boundaries

Current cancellable application APIs—specifically ingest orchestration and LLM calls—MUST propagate `context.Context`. Existing storage, index, and source-adapter interfaces remain context-free. MCP callback contexts are framework-supplied but currently terminate at those context-free interfaces, so D2 does not require artificial propagation through them.

Adding context to those context-free interfaces is an architecture/API change that requires its own design justification, compatibility review, and tests; it is not implied by the historical all-I/O sentence.

## Supersedes

- [0022](0022-repository-working-conventions.md) D1's literal unconditional update rule is superseded by D1 above.
- The historical all-I/O context sentence preserved in [0022](0022-repository-working-conventions.md) D2 is resolved by D2 above and is not an active requirement for context-free storage, index, or source-adapter interfaces.

## Why

- Conditional documentation maintenance prevents fabricated status changes and keeps entrypoint diffs traceable to actual ownership changes.
- Context belongs on operations that currently support cancellation and deadlines; changing stable interfaces only to satisfy stale agent guidance would be an unplanned cross-layer refactor.
- Separating this decision from 0022 preserves the cleanup plan's distinction between lossless relocation and semantic policy reconciliation.

## Alternatives Considered

- Preserve both statements literally as current policy
  Rejected because one requires no-op documentation churn and the other contradicts current interfaces and canonical design.
- Add `context.Context` to every I/O interface during the documentation cleanup
  Rejected because it would expand a documentation task into a broad API and implementation migration.
- Narrow the statements silently inside 0022
  Rejected because the approved cleanup plan required semantic changes to be separated.

## Revisit Triggers

- Storage, index, or source-adapter operations gain cancellation, deadlines, remote I/O, or other concrete reasons to accept context.
- Repository governance replaces impact-based documentation maintenance with a different completion protocol.
- A convention owner or entrypoint path changes and the 0022 D3 index or derivative pointers need revision.

## Related Files

- `CLAUDE.md`
- `AGENTS.md`
- `README.md`
- `DESIGN.md`
- `docs/decisions/0012-agent-documentation-governance.md`
- `docs/decisions/0022-repository-working-conventions.md`
