---
schema: plan/v1
title: "F18: External review verification gate for shipping"
type: docs
status: draft
date: 2026-08-21
execution: non-code
origin: docs/specs/2026-08-21-f18-external-review-verification-gate-design.md
---

## Goal

Add an external review verification gate to shipping's Step 7 so that artifact-free reviewer success never silently satisfies the merge gate, and update the cogvault tracker to record completion.

## Architecture notes

The change is entirely textual: a new subsection in the shipping SKILL.md and a tracker entry update. No code, no schema, no runtime change.

The gate subsection goes inside Step 7 (Merge Gate), after the persist-before-gate paragraph and before the merge-approval question. This follows the F17 pattern (topology gate subsection inside Step 4).

Cross-repo delivery: compound-loop for the skill change, cogvault for the tracker.

## Assumption Recheck

All origin spec assumptions are immutable historical observations against PR #29 (a closed, merged PR). No recheck command would return a different result.

| Claim | Outcome |
|---|---|
| PR #29 has zero submitted reviews | match — immutable historical state |
| PR #29 has zero review comments | match — immutable historical state |
| PR #29 has zero review threads | match — immutable historical state |
| CodeRabbit appears as check run named `CodeRabbit` | match — immutable historical state |
| `gh pr view --json reviews` returns no thread data | match — gh CLI field set is stable |
| Review threads require GraphQL | match — GitHub API contract |

## File structure

| File | Repo | Action |
|---|---|---|
| `skills/shipping/SKILL.md` | compound-loop | Modify — add external review verification gate subsection to Step 7 |
| `docs/research/v2-follow-ups.md` | cogvault | Modify — update F18 row from Open to Done |

## Scenario coverage map

The origin spec has no User Scenarios section (process spec, not feature spec). Verification is by textual inspection: the shipping SKILL.md Step 7 text matches the spec's design sections.

## U1: Add external review verification gate to shipping Step 7

Files:
  Modify: `skills/shipping/SKILL.md` (compound-loop repo at `/Users/teslamint/workspace/compound-loop/skills/shipping/SKILL.md`)
Steps:
  1. Read the current Step 7 section of shipping SKILL.md
  2. Write a new subsection "External review verification gate" inside Step 7, after the persist-before-gate paragraph and before "Default: present the PR". Content covers: evidence fetch (4 classes with exact commands including GraphQL for threads), review-bot detection (case-insensitive pattern table with `CodeRabbit` observed name), decision tree, gate behavior (3 options with 60s wait and cap 2 re-fetch), `--auto` behavior, durable record format, interaction with Step 6, timing consideration
  3. Self-review: verify the subsection's decision tree, commands, and record format match the approved spec exactly
  4. Commit to the compound-loop repo: `docs(shipping): add F18 external review verification gate to Step 7`
Acceptance: The shipping SKILL.md Step 7 contains the external review verification gate subsection with all spec-mandated elements: 4 evidence fetch commands, detection table, decision tree, 3-option gate behavior, `--auto` escalation, durable record format.

## U2: Update F18 tracker entry

Files:
  Modify: `docs/research/v2-follow-ups.md` (cogvault repo)
Steps:
  1. Update F18 row: change status from `Open` to `Done`, append completion summary following the F17 pattern
  2. Commit: `docs(process): mark F18 external review verification gate Done`
Acceptance: F18 row shows `Done` with a summary referencing the spec path and the shipping SKILL.md change.

## Carry-forward trigger audit

Audited `docs/research/v2-follow-ups.md` at 10abccb: 1 open row (F18), 1 fired (F18 is the current work), 0 unobservable.

| Row | Trigger class | What fired it | Disposition |
|---|---|---|---|
| F18 | edit-based — fires when shipping's merge gate is touched | This plan's U1 modifies shipping Step 7 | Folded as U1 + U2 |

No stateful ceremony in the deliverable; no mutation/failure-state matrix required.

## Deferred to Follow-Up Work

None. The spec's pattern list (CodeRabbit, Code Climate, SonarQube, Codacy) is extensible without a plan — adding a row to the detection table is a mechanical edit.

## Open unknowns

**Planning-time**: none.

**Implementation-time**: none — all content is specified in the approved spec.
