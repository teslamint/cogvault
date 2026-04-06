# 0003-canonical-context-locations

Status: accepted
Date: 2026-04-06

## Context

Multiple agents collaborate on this repository.
Project context was at risk of fragmenting across agent-specific files and chat history, which makes later review and implementation drift more likely.

## Decision

Canonical project context is stored in neutral, repo-local locations:

- `SPEC.md` is the canonical source for public behavior and contract.
- `DESIGN.md` is the canonical source for architecture and component boundaries.
- `docs/decisions/` stores accepted or deferred project decisions that should survive across agents and sessions.
- `docs/research/` stores review notes and investigation summaries before they are promoted into decisions.
- `CLAUDE.md` is an agent-facing index and background document, not a sole source of truth.

## Why

- Shared context must be readable by Claude Code, Codex, Gemini, and future agents without depending on one agent's private conventions.
- Contracts, design, and rationale change at different rates and should not be mixed into one file.
- Decisions that exist only in chat are easy to lose and hard to search later.

## Alternatives Considered

- Keeping all context in `CLAUDE.md`
  Rejected because it makes an agent-specific document look canonical.
- Keeping decisions only in chat history or external tooling
  Rejected because repo-local context is easier to review, diff, and carry across sessions.

## Revisit Triggers

- Additional agent-specific entry documents are introduced.
- Repo-local documentation becomes too fragmented and needs a higher-level index.
- External decision tooling becomes the primary decision index and repo-local docs need to be slimmed down.

## Related Files

- `CLAUDE.md`
- `SPEC.md`
- `DESIGN.md`
- `docs/decisions/README.md`
- `docs/research/index.md`
