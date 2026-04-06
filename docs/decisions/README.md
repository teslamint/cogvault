# Decisions

This directory stores project-level decisions that should be shared across agents and sessions.

Use this directory when:

- a choice has been made and should be treated as canonical,
- an item is intentionally deferred but must be revisited later,
- the rationale matters and should not live only in chat history.

Do not use this directory for:

- temporary investigation notes,
- raw review transcripts,
- implementation plans that have not yet become a project decision.

Recommended template:

```md
# 000X-title

Status: accepted | deferred | superseded
Date: YYYY-MM-DD

## Context

## Decision

## Why

## Alternatives Considered

## Revisit Triggers

## Related Files
```

Rules:

- `SPEC.md` remains the canonical source for behavior and public contract.
- `DESIGN.md` remains the canonical source for architecture and component boundaries.
- Decision records explain why the current contract or design looks the way it does.
