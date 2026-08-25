---
module: release-loop
date: "2026-08-17"
last_updated: "2026-08-25"
problem_type: workflow_issue
component: external_review_merge_gate
severity: high
applies_when:
  - "an external reviewer or review bot is required before merge"
  - "a CI or GitHub status context represents an external review integration"
  - "a review may be skipped, deferred, rate-limited, or require a manual trigger"
  - "merge approval is about to be presented or consumed"
related_components:
  - shipping
  - github_pull_requests
  - external_review_bots
tags:
  - external-review
  - merge-gate
  - review-artifacts
  - status-context
  - coderabbit
  - github
---

# External review status does not prove review completion

## Context

PR #29 showed why a green status is not proof of a completed review. CI and
GitGuardian passed, and the CodeRabbit status context reported success, while
GitHub still contained zero submitted reviews and zero review threads.
CodeRabbit had skipped automatic review. A manual `@coderabbitai review`
request made after merge returned `Pull request is closed`.

The merge completed at `2026-08-17T08:33:14Z`; the manual review request arrived
at `08:37:18Z`. By then the evidence the merge gate was supposed to require
could no longer be produced on the open PR.

## Guidance

Treat checks and status contexts as execution signals, not review artifacts.
A green reviewer status can mean only that an integration finished handling an
event.

Before merge, record one explicit external-review decision:

- **Required**: wait for the expected submitted review, comments, and review
  threads; disposition every blocking finding.
- **Waived**: record who waived the review, why, and the accepted risk. Silence,
  timeout, skipped review, or a green status is not a waiver.

Fetch each evidence class independently:

1. check runs and status contexts;
2. submitted reviews and review decisions;
3. issue-level review comments; and
4. review threads, including unresolved state.

Re-fetch remote state immediately before presenting or consuming merge
approval. Reject `skipped`, `manual review required`, `in progress`,
rate-limited, unavailable, or artifact-free success as review completion. Zero
reviews or threads proves no findings only after proving that the reviewer ran
to completion.

A merge command accepted by GitHub is a remote side effect. Interrupting the
local session or polling loop does not cancel it. Re-query the PR; if it has
merged, use a revert or follow-up PR rather than assuming the interruption
stopped the merge.

## Why This Matters

Checks, statuses, reviews, comments, and threads are separate objects with
different meanings. Collapsing them into one green/red signal can authorize a
merge without substantive review.

Timing matters too. Once a PR closes through merge, a reviewer may refuse a
manual review, eliminating the artifact the gate was intended to require.

## When to Apply

Apply this gate when:

- an automated reviewer reports through a status context or check;
- automatic review can be skipped, paused, rate-limited, or manually invoked;
- an agent or script performs the merge;
- approval arrives while review work may still be running;
- the controlling session can be interrupted after sending a remote merge; or
- policy requires review evidence rather than merely passing CI.

## Examples

**Incorrect:** “CI and the review bot are green, so review is complete.” PR #29
had green checks and a green CodeRabbit context but no submitted review or
review thread.

**Incorrect:** “There are no threads, so there are no findings.” Zero threads
can mean the reviewer never ran.

**Correct, required:** CI passes, but the reviewer says automatic review was
skipped. Merge remains blocked until manual review reaches a completed state
and reviews, comments, and threads are fetched.

**Correct, waived:** The reviewer is unavailable. An authorized maintainer
records an explicit waiver and rationale, verifies the remaining required
evidence, and then merges.

**Correct after interruption:** Re-query the PR. If it already merged, do not
wait for nonexistent pre-merge threads; assess a revert or follow-up review PR.

## Additional verified run

PR #33 exercised this gate outside the feature that introduced it. CodeRabbit
first reported an artifact-free skipped status. A manual review request created
one submitted review and three review threads. Each finding was fixed, replied
to, and resolved before the user approved squash merge.

The same PR also confirmed a related rule: a resolved thread does not change a
historical `CHANGES_REQUESTED` review object. Re-check thread resolution and
merge state instead of treating that historical review state as a new finding.

After replying to a finding, fetch the thread again before closing the gate.
CodeRabbit can append an acknowledgment after the reply. The completion check
is that every root thread is resolved and every comment is accounted for. It is
not that the historical review decision changed.
