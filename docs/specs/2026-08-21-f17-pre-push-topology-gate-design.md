---
schema: spec/v1
title: "F17: Pre-push base-branch topology gate for shipping"
type: process
status: approved
date: 2026-08-21
---

# F17: Pre-push base-branch topology gate for shipping

## Problem

PR #29 started from a local `main` two commits ahead of `origin/main`. The PR scope silently included those two unrelated commits. After squash merge, `origin/main` held a single squashed commit at `df5da99` while local `main` still pointed at `afe2048` (two commits ahead). Fast-forward was impossible; recovery required an authorized `backup-and-reset` reconciliation.

Root cause: shipping's Step 4 pushes the feature branch without checking whether its base branch diverges from the remote-tracking branch. A clean local base is not a clean PR base.

Source: `docs/retros/2026-08-17-llm-diagnostic-message-retro.md` T-03, F17 registration.

## Stop condition

Shipping detects and resolves or explicitly accepts base divergence before push.

## Design

### Where: shipping SKILL.md Step 4, before push

Insert a **base-branch topology gate** at the beginning of Step 4, after Step 3 (Commit) completes and before the push command. The gate applies only to **full** mode (push + PR create). Description-only and description-update modes do not push, so the gate has nothing to protect.

### Detection

```bash
git fetch origin <base_branch> --quiet
git rev-list --left-right --count origin/<base_branch>...<base_branch>
```

Parse the two-column output as `<remote_ahead>\t<local_ahead>`.

If `git fetch` fails, stop — topology verification is unavailable. Record `fetch-failed` and the error in the durable record. Note: fetch failure does not guarantee push failure (`remote.pushurl` may differ from `remote.url`).

| remote_ahead | local_ahead | Interpretation | Action |
|---|---|---|---|
| 0 | 0 | In sync | Continue |
| N | 0 | Remote has new commits | Continue — normal; PR merge will include them |
| 0 | N | **Local base ahead** — PR will include N unintended commits | Gate |
| N | M | Both diverged | Gate |

### Gate behavior

When `local_ahead > 0`:

1. List the unexpected local-only commits: `git log --oneline origin/<base_branch>..<base_branch>`
2. Present options via blocking question:
   - **Rebase feature onto remote base** (recommended): `git rebase --onto origin/<base_branch> <base_branch> <feature_branch>` — transplants only feature-unique commits onto the remote base tip. Strips the inherited local-ahead commits from the feature branch without touching the local base ref.
   - **Accept divergence** — proceed with the push, acknowledging: (a) the PR scope includes the listed local-ahead commits, and (b) Step 8 merged-result verification will fail its fast-forward check, requiring manual base reconciliation after merge.
   - **Stop** — abort shipping, user resolves manually.

**Why not `git branch -f`**: when shipping runs from a worktree (the release-loop normal path), the base branch is checked out in the main working tree. `git branch -f <base>` is rejected by git (`fatal: cannot force update the branch used by worktree at ...`). Even outside a worktree, force-moving the base ref is a destructive action on potentially intentional unpushed work.

**`--auto` mode**: escalate to blocked, never auto-resolve. The local-ahead commits may be intentional unpushed work. Automated rewriting of history or acceptance of topology divergence contradicts the PR #29 precedent, which required typed authorization plus a backup branch for the same class of base ref rewrite. Log `blocked_reason` and surface to the user.

### Rebase conflict handling

If `git rebase --onto` encounters conflicts, abort the rebase (`git rebase --abort`), stop, and report the conflicts. The user resolves manually. Never auto-resolve rebase conflicts in shipping.

### Post-resolution verification

After a successful rebase, verify the resolution:

```bash
git log --oneline origin/<base_branch>..<feature_branch>
```

The output must contain only feature-unique commits. If any of the previously listed local-ahead commits appear, the rebase did not strip them — stop and report.

### Preparation-only path

Step 0's preparation-only mode (no network) skips the topology gate entirely — the push will not happen, so the gate has nothing to protect. The composed manual-steps file includes a note: "Before pushing, check base-branch sync: `git fetch origin <base> --quiet && git rev-list --left-right --count origin/<base>...<base>` — stop if fetch fails."

### Worktree consideration

When shipping runs from a worktree, `<base_branch>` is checked out in the main working tree. `git rebase --onto` operates on the feature branch (the current checkout in the worktree), not on the base branch ref or its working tree. This is safe.

### Durable record

Log the topology check result in the shipping state sink:
- `release-loop` path: `.release-loop/progress.md` Log line: `<timestamp> ship: base-topology — origin/<base> left=N right=M; action=<clean|rebase-onto|accepted|blocked|stopped|fetch-failed|rebase-conflict>; reason=<...>`
- Standalone path: `shipping-final-action.md` in git-dir
- Accept evidence must include: the local-ahead commit list, acknowledgment that Step 8 ff will require manual reconciliation, and the timestamp.

## Rejected alternatives

1. **Check at branch creation time (worktree-isolation)**: Too early — commits can land on local base between branch creation and push. The push is the action that crystallizes the PR scope.
2. **Check at merge time (Step 7)**: Too late — the PR already exists with the wrong scope. Fixing at merge means closing and recreating, which loses review history.
3. **Always force-sync without asking**: Unsafe — the local-ahead commits may be intentional (user committed directly to main for a reason). Also fails in worktree setups (`git branch -f` rejected).
4. **`--auto` silently syncs**: Contradicts PR #29 precedent requiring typed authorization for base ref rewrites. Local-ahead commits are user's unpushed work; shipping cannot judge intent.
5. **Use `git cherry` for patch-equivalent detection**: Useful diagnostic information but adds complexity. The commit list from `git log --oneline` already gives the user enough context to decide.

## Delivery

Cross-repo change:
- **compound-loop** (`teslamint/compound-loop`): modify `skills/shipping/SKILL.md` Step 4 to add the base-topology gate
- **cogvault** (`teslamint/cogvault`): update F17 tracker entry in `docs/research/v2-follow-ups.md`, commit spec and retro docs

## Success criteria

| # | Criterion | Verification |
|---|---|---|
| 1 | Shipping detects local_ahead > 0 before push | The Step 4 text specifies the gate check and fires when local base has commits ahead of remote |
| 2 | Rebase-onto resolves the PR scope | After rebase, `git log origin/<base>..<feature> --oneline` contains only feature-unique commits |
| 3 | Accept action logs explicit evidence including Step 8 ff consequence | The durable record template contains the acceptance with commit list and ff-failure acknowledgment |
| 4 | `--auto` mode escalates to blocked | The Step 4 text specifies escalation, never silent resolution |
| 5 | Preparation-only path skips the gate with a note | Step 0's manual-steps template includes the topology check command |
| 6 | No `git branch -f` on checked-out branches | The design uses only `git rebase --onto` which operates on the current feature checkout |
