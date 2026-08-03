---
module: release-loop
date: "2026-08-03"
problem_type: workflow_issue
component: plan_change_control
severity: high
applies_when:
  - "review finds observable behavior missing from an approved sealed plan"
  - "a safety fix contradicts an approved spec or plan"
  - "the original approval record must remain byte-identical"
  - "implementation needs a new user approval gate"
tags:
  - release-loop
  - sealed-plan
  - deviation
  - approval-gate
  - review-remediation
---

# Review-introduced behavior needs a deviation

## Context

Review can find a safety requirement after a user approves a spec or plan.
The required fix can change observable behavior.
Editing the approved artifact would erase the original decision record.
Implementing the fix directly would also bypass the approval gate.

The orphan-sweep release hit this condition twice.
Review round 1 found archive safety contradictions.
Review round 2 found a silent run-level ledger failure prescribed by the original plan.

## Guidance

1. Stop direct implementation when review proves a contract contradiction.
2. Commit a deviation addendum before you finalize a remediation plan.
3. Preserve the approved spec and plan byte-for-byte.
4. Draft a separate remediation plan that cites the deviation.
5. Review the new plan and obtain explicit user approval.
6. Seal the approved plan before implementation.
7. Implement the behavior test-first.
8. Re-run the full artifact set and branch review.

The deviation addendum records seven items:

- the original contract;
- the discovered contradiction;
- why documentation alone cannot fix it;
- the new observable behavior;
- safety and consent boundaries;
- verification changes; and
- traceability to the review, artifacts, code, and tests.

## Why This Matters

This sequence preserves decision history.
It shows what the user approved and what later evidence disproved.
It also keeps behavior changes behind a visible approval gate.

A passing test does not authorize behavior absent from the approved artifact set.
The committed deviation and sealed successor plan provide that authority.

## When to Apply

Use this workflow for changes to observable behavior, including:

- state transitions;
- persisted data;
- permission or consent boundaries;
- terminal outcomes;
- run-level failure behavior; and
- required verification evidence.

Do not use it for an internal refactor with unchanged behavior.
Normal cleanup can stay inside the current plan or remain deferred.

## Examples

### Orphan-sweep safety

Review found upgrade, source-availability, permission, cancellation, and overwrite gaps.
The team committed `docs/deviations/2026-08-03-orphan-sweep-review-remediation.md`.
Plan 002 then defined and sealed the test-first remediation.

### Ledger-query failure

The original plan told `successRows()` failures to log and continue.
Review proved that this hid a run-level failure.
The team committed `docs/deviations/2026-08-03-orphan-sweep-ledger-query-failure.md`.
Plan 003 authorized error propagation and next-run recovery.

### Internal cleanup

The remediation plans deferred unrelated dead-code and fixture cleanup.
Those edits did not change the approved behavior.
They did not require a deviation addendum.
