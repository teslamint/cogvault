# 0014-roadmap-adoption-boundaries

Status: accepted
Date: 2026-04-07

## Context

The project evaluated ideas from the external `mempalace` project and added a small set of them to the README roadmap as future candidates.

That README change records what is currently considered interesting, but not why certain ideas fit `cogvault` while others do not.

Without a durable decision record, future readers would have to reconstruct the rationale from chat history:

- why some navigation and memory-bootstrapping ideas were accepted as possible `v0.2+` work,
- why other `mempalace` features were explicitly not adopted,
- why those exclusions do not mean the project permanently rejects all future retrieval or graph work.

## Decision

### D1: `cogvault` may adopt lightweight workflow ideas, not the `mempalace` product model

Ideas are eligible for roadmap adoption when they strengthen the current `cogvault` product shape:

- local vault-centered workflow,
- passthrough MCP tooling,
- Markdown- and JSON-visible state,
- SQLite FTS as the MVP retrieval core.

This includes the following `v0.2+` candidate directions:

- taxonomy/status tools for structure-first navigation,
- duplicate-aware write workflow,
- a wake-up summary separate from `_schema.md`,
- stronger `_wiki/` browsing taxonomy.

These are treated as possible future enhancements, not committed features.

### D2: `mempalace` features that would redefine the core abstraction are not adopted

The following are not planned as direct adoptions:

- AAAK-style compressed representation formats,
- vector/semantic search as a standalone replacement for SQLite FTS,
- temporal knowledge graph as a required core abstraction,
- conversation-mining as the primary product mode.

This is a boundary decision about product shape, not a permanent ban on every related technique.

### D3: Future retrieval upgrades must complement or deliberately supersede current canon

The project may still explore future retrieval expansion such as vector search or hybrid retrieval, but only if that work is introduced as a later-stage extension that does not silently rewrite the MVP identity.

In practice:

- MVP keeps SQLite FTS as the retrieval core,
- future hybrid search remains possible,
- direct imports from external projects must be filtered through `SPEC.md`, `DESIGN.md`, and repo-local decisions rather than adopted whole.

## Why

### Why the accepted ideas fit

The accepted items improve agent workflow without changing the nature of the system:

- taxonomy/status tools improve navigation,
- duplicate checks improve write quality,
- wake-up summaries improve low-token bootstrapping,
- stronger `_wiki/` taxonomy improves browsing and structure discovery.

All four can be expressed within the current vault/Markdown/MCP model.

### Why the rejected ideas do not fit directly

The rejected items belong to a different center of gravity:

- compressed dialects optimize memory density over human-readable Markdown,
- vector-first replacement would move retrieval identity away from the current FTS-based MVP,
- temporal knowledge graphs introduce a heavier reasoning model than the current wiki builder needs,
- conversation-mining changes the product from “vault wiki tooling” into a different primary input mode.

### Why this belongs in a decision record

The README is a roadmap summary, not the place to preserve the full rationale behind adoption boundaries. This decision reduces future review churn when someone asks why a specific `mempalace` idea was listed, excluded, or partially deferred.

## Alternatives Considered

### A1: Keep the rationale only in README prose

Rejected because README should stay concise and user-facing.

### A2: Treat all `mempalace` features as equally valid future candidates

Rejected because it would blur the current product boundary and make the roadmap look much broader than the canon supports.

### A3: Reject all external inspiration categorically

Rejected because some workflow-level ideas are useful and compatible with the current architecture.

## Revisit Triggers

- The project moves beyond passthrough tooling and introduces a higher-level engine that changes the product center of gravity.
- Future specs introduce vector or hybrid retrieval as a planned core capability.
- Real-world usage shows that structure-first navigation or duplicate-aware writing is not actually valuable.
- The project begins to ingest conversation archives as a first-class input source.

## Related Files

- `README.md`
- `SPEC.md`
- `DESIGN.md`
- `CLAUDE.md`
- `docs/decisions/0012-agent-documentation-governance.md`
