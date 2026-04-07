# 0012-agent-documentation-governance

Status: accepted
Date: 2026-04-07

## Context

The repository currently has multiple agent-facing entry documents and external plan files:

- `CLAUDE.md`
- `AGENTS.md`
- implementation plans under `~/.claude/plans/`

Review work after Step 4 showed repeated drift between these documents and the canonical project records. The problem was not just duplicated text, but unclear ownership:

- durable project rules were at risk of being recorded only in agent-facing docs,
- `AGENTS.md` had drifted into a partial copy of `CLAUDE.md`,
- plan files were sometimes treated as if they could override reviewed behavior.

The project already established that canonical context belongs in `SPEC.md`, `DESIGN.md`, and `docs/decisions/`. This document narrows how agent-facing docs and plan files must relate to that canon.

## Decision

### D1: Durable documentation-governance rules belong in `docs/decisions/`

If a rule must survive across sessions and agents, it must live in a decision record, not only in `CLAUDE.md`, `AGENTS.md`, or a plan file.

Examples:

- what counts as canonical project documentation,
- whether a document is authoritative or derivative,
- what must be updated after implementation lands.

### D2: Plan files are working notes, not canonical project state

Implementation plans such as `~/.claude/plans/*.md` may describe intended changes, but they do not become canonical merely by existing.

After review or implementation, canonical project state remains:

- `SPEC.md` for behavior and public contract,
- `DESIGN.md` for architecture and implementation boundaries,
- `docs/decisions/` for durable rationale and accepted project rules.

If a plan drifts from reviewed code or canon, the plan is stale and must be updated, replaced, or ignored.

### D3: `CLAUDE.md` is a background/index document

`CLAUDE.md` may summarize context, link to canonical documents, and provide agent-facing background, but it must not become the only place where durable project rules are defined.

When `CLAUDE.md` mentions a durable rule, it should point to the relevant decision record when one exists.

### D4: `AGENTS.md` is a pointer/delta document, not a mirrored handbook

`AGENTS.md` must not duplicate the full body of `CLAUDE.md`.

Its role is limited to:

- pointing the agent to canonical context and background documents,
- recording agent-specific operational differences when needed,
- staying intentionally short enough that synchronization is tractable.

If a rule applies project-wide rather than only to one agent workflow, it belongs in canon, not in duplicated prose across both files.

### D5: Documentation maintenance is part of implementation completion

When implementation changes land and they affect repository state or operating assumptions, the author must update the relevant canonical and derivative documents before calling the work complete.

At minimum, review these candidates:

- `README.md` for implementation progress and user-visible project state,
- `docs/decisions/` when a durable rule or rationale was established,
- `CLAUDE.md` when background/index references need to point at new canonical records,
- `AGENTS.md` when agent-specific pointers or deltas changed.

This is a review expectation, not just a documentation preference.

## Why

- The repository already has enough moving parts that duplicated prose quickly becomes stale.
- Agent-facing docs are useful, but they are a poor place to anchor durable project rules.
- Plans are necessary for execution, but they are intentionally provisional.
- Making documentation maintenance explicit reduces repeated review findings about drift and unclear ownership.

## Alternatives Considered

- Keep the rules only in `CLAUDE.md`
  Rejected because it makes an agent-facing document look canonical.
- Keep `AGENTS.md` as a full synchronized copy of `CLAUDE.md`
  Rejected because drift is structural, not accidental, when two long documents must stay mirrored.
- Treat plan files as the source of truth during active implementation
  Rejected because reviewed canon must remain stable even when plans are revised.

## Revisit Triggers

- A new shared documentation index replaces both `CLAUDE.md` and `AGENTS.md`.
- The repository adopts a different persistent decision system and repo-local ADRs become secondary.
- Agent-specific workflows diverge enough that a short pointer-style `AGENTS.md` is no longer sufficient.

## Related Files

- `docs/decisions/0003-canonical-context-locations.md`
- `docs/decisions/0011-step4-review-convergence.md`
- `CLAUDE.md`
- `AGENTS.md`
- `README.md`
