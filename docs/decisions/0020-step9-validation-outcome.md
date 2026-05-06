# 0020-step9-validation-outcome

Status: accepted
Date: 2026-05-05

## Context

Step 9 (SPEC Section 11) prescribed a 1-week real-world validation. A 5-day execution produced a partial success: technical functionality confirmed, but workflow friction prevented daily-use habit formation.

The primary failure signal is "friction high" per the SPEC pivot table. The prescribed response is CLI shortcut and workflow simplification.

## Decision

### D1: Accept "friction high" as the dominant failure signal and apply SPEC-prescribed pivot

The ingest workflow (scan-parse-reason-search-write) requires 3-6 explicit MCP calls per note with manual user orchestration each time. This friction exceeded the value threshold after Day 3.

Pivot: introduce a single-step ingest CLI command (e.g. `cogvault ingest <path>`) that collapses the multi-step workflow into one operation.

This is a workflow convenience layer. It does not change the MCP tool interface, passthrough architecture, or engine responsibilities.

### D2: Passthrough mode is retained

Per 0014-roadmap-adoption-boundaries D1, the system remains passthrough: the engine provides tools, agents orchestrate. The CLI shortcut is additive and does not introduce an engine-level compiler or orchestrator.

The v0.2 compiler remains a separate trigger (see Revisit Triggers below).

## Why

SPEC Section 11 explicitly prescribes this pivot for the "friction high" signal. The validation confirmed that technical components work correctly — the problem is workflow ergonomics, not capability. A CLI shortcut addresses the exact friction point (multi-step manual orchestration) without architectural disruption.

## Alternatives Considered

### A1: Proceed directly to v0.2 compiler

Rejected. SPEC pivot table assigns the compiler to "quality low," not "friction high." The friction is in orchestration overhead, not in output quality. A lighter intervention (CLI shortcut) should be attempted first.

### A2: Declare validation complete (success)

Rejected. The success criterion ("1 week daily use, wiki was useful") was not met. Day 7 habit formation was not reached. Honest assessment prevents premature advancement.

### A3: Redesign MCP tool granularity (merge tools)

Rejected. Tool granularity serves the passthrough model — agents need fine-grained control. The friction is in the human-to-agent instruction path, not in tool design.

## Revisit Triggers

- CLI shortcut alone does not resolve friction after 1 week of use: escalate to v0.2 compiler (2-pass automation).
- Search becomes the bottleneck (chicken-and-egg unresolved even with reduced friction): consider batch-ingest or bootstrap-import command.
- Schema non-compliance emerges as friction decreases and page volume increases: add `wiki_write` validation warnings.

## Related Files

- `SPEC.md` Section 11
- `docs/research/step9-validation-report.md`
- `docs/decisions/0014-roadmap-adoption-boundaries.md`
- `docs/research/step9-mcp-unblock-plan.md`
