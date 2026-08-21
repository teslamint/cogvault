# Retro: F17 — Pre-push base-topology gate

**PRs**: cogvault #31 (merged 10abccb), compound-loop #17 (merged 9e4ebd3)
**Follow-up**: F17 in `docs/research/v2-follow-ups.md`
**Spec**: `docs/specs/2026-08-21-f17-pre-push-topology-gate-design.md`
**Scope**: compound-loop `skills/shipping/SKILL.md` Step 4

## What shipped

Added a base-branch topology gate to shipping SKILL.md Step 4 (full mode only). Before pushing, the gate fetches origin, compares `rev-list --left-right --count`, and fires when local base has commits ahead of remote. Resolution: `rebase --onto` (recommended), accept-with-ff-warning, or stop. `--auto` escalates to blocked.

## Release data

| Metric | Value |
|---|---|
| **Changed non-test lines** | 34 (SKILL.md) + 115 (spec) |
| Commits | 5 (across 2 repos) |
| Review rounds | 2 (Codex + CodeRabbit) |
| Comments (fixed / deferred) | 6 / 0 |
| CI failures | 0 |
| Duration (start → merge) | 1h 6m |
| Units planned / completed | 2 / 2 |

## Success criteria evaluation

| # | Criterion | Met? | Evidence |
|---|---|---|---|
| 1 | Shipping detects local_ahead > 0 before push | Yes | Step 4 text specifies rev-list gate |
| 2 | Rebase-onto resolves the PR scope | Yes | Scratch-repo proof: C1/C2 stripped, F1 only remains |
| 3 | Accept action logs Step 8 ff consequence | Yes | Durable record template includes ff-failure acknowledgment |
| 4 | --auto mode escalates to blocked | Yes | Step 4 text: "escalate to blocked" |
| 5 | Preparation-only path skips with note | Yes | Manual-steps template includes fetch+rev-list |
| 6 | No git branch -f on checked-out branches | Yes | Scratch-repo proof: fatal exit=128 |

Operational verification pending: no real diverged-base shipping run yet.

## What went well

- **Advisor caught 3 blockers before user gate.** Initial design used `git branch -f` (worktree-rejected), `git rebase main` (no-op), and missed Step 8 ff conflict. All fixed before presenting the design for approval.
- **Scratch-repo proofs were decisive.** Three git behaviors verified in 30 seconds each, providing concrete evidence for design decisions and review responses.
- **Codex review found the `--is-ancestor` ancestry gap.** The P1 finding (false positive on non-inherited feature branches) was caught before CodeRabbit.
- **CodeRabbit found the intermediate-commit edge case.** `--is-ancestor` checks only the tip; count comparison (`origin/base..feature - base..feature`) catches L1-branched features.

## What could improve

- **Initial design had 3 classification errors** (branch -f, rebase no-op, Step 8 conflict). The advisor caught all three, but a single-pass design would have shipped them. Checking the git man pages for worktree constraints before designing would have caught #1 and #2 inline.
- **Cross-repo delivery is awkward.** The release-loop expects a single branch in a single repo. F17 required two PRs in two repos with linked tracking. The loop accommodated it, but the state tracking was ad-hoc.
- **CodeRabbit rate limit blocked the final approval cycle.** The incremental re-review mode doesn't produce APPROVED state, only COMMENTED. Combined with rate limiting, this forced a manual waiver.

## Carry-forward

| Item | Type | Priority | Tracked at |
|---|---|---|---|
| Operational verification: run shipping with a real diverged base to confirm the gate fires | verification | P3 | F17 Done text in `docs/research/v2-follow-ups.md` |

## Lessons

- `git branch -f` is rejected on any branch checked out in another worktree — always check worktree constraints before designing ref-moving commands.
- `merge-base --is-ancestor` checks only the tip commit, not intermediate ancestors. Count comparison (`rev-list --count origin/base..feature - base..feature`) catches partial inheritance.
- `remote.pushurl` can differ from `remote.url`, so fetch failure does not guarantee push failure.
