---
schema: spec/v1
title: "F18: External review verification gate for shipping"
type: process
status: approved
date: 2026-08-21
---

# F18: External review verification gate for shipping

## Problem

PR #29's CodeRabbit status context reported success while GitHub contained zero submitted reviews and zero review threads. CodeRabbit had skipped automatic review. The merge completed; a post-merge manual `@coderabbitai review` request returned `Pull request is closed`.

Root cause: shipping's Step 7 (Merge Gate) presents merge approval based on CI status without verifying that external review artifacts actually exist. A green reviewer status context proves only that the integration finished handling an event, not that a review ran to completion.

Source: `docs/retros/2026-08-17-llm-diagnostic-message-retro.md` T-04, F18 registration. Guidance: `docs/solutions/workflow-issues/external-review-status-does-not-prove-review-completion.md`.

## Stop condition

Shipping records an explicit external-review decision (required with artifacts, or waived with rationale) before presenting merge approval. Artifact-free reviewer success never silently satisfies the gate.

## Design

### Where: shipping SKILL.md Step 7, before the merge approval question

Insert an **external review verification gate** at the beginning of Step 7, after the merge command is persisted and before the blocking merge-approval question or `--auto` condition evaluation. The gate runs on every path that reaches Step 7 (interactive, `--auto`, dispatched worker). Preparation-only never reaches Step 7 (Step 0 terminates earlier), so the gate has nothing to protect on that path.

### Evidence fetch

Fetch four evidence classes independently, never from a summarized or combined view. Each class answers a different question; collapsing them loses the distinction that caused the PR #29 failure.

```bash
PR_NUMBER=<number>
OWNER=<owner>
REPO=<repo>

# 1. Check runs and status contexts
gh pr checks "$PR_NUMBER"

# 2. Submitted reviews (authored review objects)
gh api "repos/{owner}/{repo}/pulls/$PR_NUMBER/reviews" --jq 'length'

# 3. Issue-level review comments
gh api "repos/{owner}/{repo}/pulls/$PR_NUMBER/comments" --jq 'length'

# 4. Review threads with resolution state (GraphQL — gh pr view has no reviewThreads field)
gh api graphql -F number="$PR_NUMBER" -F owner="$OWNER" -F name="$REPO" -f query='
  query($number:Int!,$owner:String!,$name:String!){
    repository(owner:$owner,name:$name){
      pullRequest(number:$number){
        reviewThreads(first:100){ totalCount nodes { isResolved } }
      }}}'
```

The gate checks for existence (count > 0), not exact totals, so first-page results (up to 30 items) suffice for the satisfied/artifact-free distinction. PRs with >30 reviews or comments are already satisfied on the first page.

**Fail-closed on query failure.** If any of the four queries fails (authentication error, network timeout, permission denied, GraphQL error, or rate limit), block merge approval, log the error and the failing query in the durable record, and present retry or stop options. Never treat a failed query as zero evidence — a query that did not run cannot prove absence.

Re-fetch immediately before the gate evaluates, not from cached Step 5/6 results. Remote state can change between steps.

### Review-bot detection

Identify external review integrations from the check runs and status contexts fetched in evidence class 1. A check run qualifies as a review-bot context when its name matches a known pattern (case-insensitive):

| Pattern | Integration | Observed name |
|---|---|---|
| `coderabbit` | CodeRabbit | `CodeRabbit` (check run) |
| `codeclimate` | Code Climate | — |
| `sonarcloud`, `sonarqube` | SonarQube/SonarCloud | — |
| `codacy` | Codacy | — |

This list is not exhaustive. An unrecognized integration produces no false positive (the gate does not fire), but may produce a false negative (a review bot runs without the gate catching it). The pattern list is extensible without changing the gate logic.

When no review-bot context is detected among checks/statuses, the gate records `not-applicable` and continues. Detection of a review-bot context triggers the verification decision tree below.

### Decision tree

When a review-bot context is detected, evaluate the artifacts produced by any reviewer (human or bot). The gate verifies that review work happened, not who performed it — a human review that covers the same ground satisfies the gate.

| Submitted reviews | Review threads | Review-bot status | Decision |
|---|---|---|---|
| > 0 | any | any | **Satisfied** — review artifacts exist |
| 0 | > 0 | any | **Satisfied** — review threads prove a reviewer ran |
| 0 | 0 | `pass` (gh bucket: `pass`) | **Artifact-free success** — gate fires |
| 0 | 0 | `pending` (gh bucket: `pending`) | **In progress** — block, poll for completion |
| 0 | 0 | `skipping` (gh bucket: `skipping`) | **Skipped** — gate fires |
| 0 | 0 | `fail` (gh bucket: `fail`) | **Failed** — gate fires |
| 0 | 0 | `cancel` (gh bucket: `cancel`) | **Cancelled** — gate fires |
| 0 | 0 | unknown bucket value | **Unknown** — gate fires (fail-closed) |

"Satisfied" means the review-bot context's green status is backed by actual review artifacts. Continue to the merge approval question.

### Gate behavior

When the gate fires (artifact-free success, skipped, failed, cancelled, or unknown reviewer status):

Present a blocking question with three options:

When the decision tree evaluates to **In progress** (reviewer status `pending`): do not present the merge-approval question. Apply the same 60-second wait and re-fetch cycle as "Required — request review" (cap 2 attempts). If the status changes to satisfied or a gate-fires state, proceed accordingly. On cap exhaustion, present the gate question with the three options below.

- **Required — request review** (recommended): the reviewer must produce review artifacts before merge proceeds. If the reviewer supports manual invocation (e.g., `@coderabbitai review`), present the invocation command. After invocation, wait 60 seconds, then re-fetch all four evidence classes and re-evaluate the decision tree. Cap 2 re-fetch attempts with 60-second waits between them. On cap exhaustion, re-present the gate question with two remaining options: waive or stop.
- **Waived**: proceed without external review. Record who waived (always `user`), the rationale, and the accepted risk. Silence, timeout, or a green status without artifacts is not a waiver — the user must explicitly choose this option.
- **Stop**: abort shipping; user resolves manually.

### `--auto` mode

Escalate to blocked when the gate fires. Never auto-waive a required external review — the decision to proceed without review artifacts is a risk acceptance that requires human judgment. Log a state-specific `blocked_reason` in the durable record and surface to the user: `external-review-artifact-free` for artifact-free success, `external-review-skipped` for skipped, `external-review-failed` for failed/cancelled/unknown, `external-review-pending` for pending after cap exhaustion, `external-review-query-failed` for query failures. This is consistent with F17's `--auto` behavior (escalate to blocked, never auto-resolve).

### `--auto` when satisfied

When the gate evaluates to satisfied (review artifacts exist) or not-applicable (no review-bot detected), `--auto` mode continues without user interaction. The presence of review artifacts is objective evidence, not a judgment call.

### Durable record

Log the gate result in the shipping state sink:

- `release-loop` path: `.release-loop/progress.md` Log line: `<timestamp> ship: external-review — reviewer=<name|none>; reviews=<N>; comments=<N>; threads=<N>; status=<value>; decision=<satisfied|waived|required|not-applicable|blocked|stopped>; waived_by=<user|none>; accepted_risk=<...|none>; reason=<...>`
- Standalone path: `shipping-final-action.md` in git-dir

Waiver evidence must include: the user's stated rationale (`accepted_risk`), who waived (`waived_by`, always `user`), the review-bot name, and the timestamp. A waiver without rationale is a schema violation. Non-waiver decisions set `waived_by=none` and `accepted_risk=none`.

### Interaction with Step 6

Step 6 (Review Feedback) processes review comments and threads that already exist. The external review verification gate checks whether those artifacts exist at all. The two steps are complementary:

- If the reviewer ran and produced comments, Step 6 processes them, and the gate finds artifacts — satisfied.
- If the reviewer skipped, Step 6 has nothing to process (zero threads), and the gate catches the gap.
- Step 6 runs before Step 7. The gate re-fetches independently because new reviews can arrive between steps.

### Timing consideration

A merge accepted by GitHub is a remote side effect. If the session is interrupted after sending the merge command but before the gate re-fetch, the merge may already have completed. On resume, re-query the PR state; if merged, do not wait for review artifacts that can no longer be produced on the closed PR. Assess a revert or follow-up review PR instead.

## Assumptions and Preconditions

| Claim | Command | Observation | Result | Source |
|---|---|---|---|---|
| PR #29 has zero submitted reviews | `gh api repos/teslamint/cogvault/pulls/29/reviews --jq 'length'` | 2026-08-21T13:09Z | `[]` (0 reviews) | GitHub REST API |
| PR #29 has zero review comments | `gh api repos/teslamint/cogvault/pulls/29/comments --jq 'length'` | 2026-08-21T13:09Z | `0` | GitHub REST API |
| PR #29 has zero review threads | `gh api graphql -F number=29 ...reviewThreads...` | 2026-08-21T13:09Z | `totalCount: 0, nodes: []` | GitHub GraphQL API |
| CodeRabbit appears as check run named `CodeRabbit` (mixed case), not `coderabbitai` | `gh pr checks 29` | 2026-08-21T13:09Z | `CodeRabbit pass 0 Review completed` | GitHub Checks API |
| `gh pr view --json reviews` returns review objects but no thread data | `gh pr view 29 --json reviewDecision,reviews` | 2026-08-21T13:09Z | `{decision: "", review_count: 0}` — no thread fields | gh CLI |
| Review threads require GraphQL (`reviewThreads` field) | GraphQL query with `reviewThreads(first:100)` | 2026-08-21T13:09Z | Returns `totalCount` and `nodes[].isResolved` | GitHub GraphQL API |

## Rejected alternatives

1. **Add the gate to Step 5 (CI Loop)**: Step 5 watches CI checks. Review verification is a separate concern — mixing them conflates execution signals with review artifacts, the exact conflation F18 corrects.
2. **Add the gate to Step 6 (Review Feedback)**: Step 6 processes existing review comments. The absence-of-review detection is a prerequisite to merge, not a feedback-processing concern. Adding it to Step 6 means it runs too early (before final re-fetch) and conflates two responsibilities.
3. **Always require external review**: Too strict. Many repos have no external review bot configured. The gate must be conditional on detection.
4. **Auto-detect required reviewers from branch protection**: Branch protection rules are repo-level policy, not PR-level evidence. A required-reviewer rule may be satisfied by a human reviewer while the review-bot skipped. The gate targets review-bot artifact verification, not human-reviewer policy enforcement.
5. **Trust the review-bot's own status as sufficient**: This is the exact failure mode F18 corrects. CodeRabbit's green status meant "I handled the event," not "I produced a review."
6. **Use `gh pr view --json` for thread data**: Verified empirically — `gh pr view --json reviewDecision,reviews` returns review objects but has no `reviewThreads` field. Thread count and resolution state require GraphQL.

## Delivery

Cross-repo change:
- **compound-loop** (`teslamint/compound-loop`): modify `skills/shipping/SKILL.md` Step 7 to add the external review verification gate
- **cogvault** (`teslamint/cogvault`): update F18 tracker entry in `docs/research/v2-follow-ups.md`, commit spec and retro docs

## Success criteria

| # | Criterion | Verification |
|---|---|---|
| 1 | Shipping fetches 4 evidence classes independently before merge approval | Step 7 text specifies the four commands (REST for reviews/comments, GraphQL for threads) and mandates re-fetch |
| 2 | Artifact-free reviewer success fires the gate | Decision tree maps (reviews=0, threads=0, status=success) to gate-fires |
| 3 | Waiver requires explicit user choice with recorded rationale | Gate behavior specifies three options; waiver records who, rationale, and risk |
| 4 | `--auto` mode escalates to blocked when gate fires | `--auto` section specifies escalation, never auto-waiver |
| 5 | Satisfied state (artifacts exist) continues without blocking | Decision tree maps (reviews>0) and (threads>0) to satisfied |
| 6 | No review-bot detected produces not-applicable, not a false alarm | Detection section specifies not-applicable when no pattern matches |
| 7 | Durable record logs every gate outcome with evidence | Record format includes reviewer, counts, status, decision, and reason |
