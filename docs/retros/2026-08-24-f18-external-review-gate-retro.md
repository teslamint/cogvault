---
feature: f18
pr: 32
merged: 762b86c60101aef7410fd04fc8728d6aeb9a6499
date: 2026-08-24
---

# F18: External review verification gate — Retro

## What shipped

Added an external review verification gate to the shipping skill's Step 7 (Merge Gate). The gate fetches 4 evidence classes independently (check runs, submitted reviews, review comments, review threads via GraphQL), detects review-bot contexts by case-insensitive pattern match, and fires when zero reviews and zero threads exist despite a green reviewer status. Three options when gate fires: request review (60s wait, cap 2 re-fetches), waive with recorded rationale, or stop. `--auto` escalates to blocked with state-specific reasons.

Cross-repo: compound-loop `skills/shipping/SKILL.md` Step 7 (f2efda9, fbff02f), cogvault spec + plan + tracker (PR #32).

## Success criteria evaluation

| # | Criterion | Met? | Evidence |
|---|---|---|---|
| 1 | Shipping fetches 4 evidence classes independently | Yes | SKILL.md Step 7 specifies 4 commands with GraphQL for threads |
| 2 | Artifact-free reviewer success fires gate | Yes | Decision tree maps (reviews=0, threads=0, status=pass) to gate-fires |
| 3 | Waiver requires explicit user choice with rationale | Yes | 3 options specified; waiver records waived_by, accepted_risk |
| 4 | `--auto` escalates to blocked | Yes | State-specific blocked_reason values defined |
| 5 | Satisfied state continues without blocking | Yes | reviews>0 or threads>0 maps to satisfied |
| 6 | No review-bot = not-applicable | Yes | Detection section specifies not-applicable |
| 7 | Durable record logs every outcome | Yes | Format includes reviewer, reviews, comments, threads, status, decision, waived_by, accepted_risk |

## What went well

- **F18 gate self-tested during its own shipping run**: PR #32's CodeRabbit initially skipped review (status=skipped, reviews=0, threads=0). The gate logic identified this as artifact-free success and fired. After requesting review via `@coderabbitai review` and re-fetching, the gate confirmed satisfaction (reviews=1). This is the exact PR #29 failure mode the spec was designed to catch.
- **Empirical grounding prevented a blocking defect**: Advisor caught that `gh pr view --json reviewDecision,reviews` lacks thread data before the spec was presented. Dry-running against PR #29 confirmed the gap and revealed the correct CodeRabbit check run name (`CodeRabbit`, not `coderabbitai`).
- **CodeRabbit review improved the spec**: 7 of 9 findings were valid and addressed (fail-closed behavior, gh pr checks vocabulary alignment, pending state handling, state-specific blocked_reason, record format completeness, GraphQL variable usage).

## Lessons

- **Lesson 1 — Process specs benefit from live execution as validation.**
  **Why**: The F18 gate spec's first real execution was its own PR. CodeRabbit's "Review skipped" status triggered the gate, proving the decision tree works. No unit test can substitute for a real artifact-free shipping run.
  **How to apply**: When a process spec defines a gate or decision tree, prefer shipping the spec change through the gate it defines.

- **Lesson 2 — Review-bot check run names are display names, not API slugs.**
  **Why**: The spec initially listed `coderabbitai` as the detection pattern. Empirical verification against PR #29 showed the actual check run name is `CodeRabbit` (mixed case). Case-insensitive matching was already specified, but the pattern table would have been misleading.
  **How to apply**: Always verify detection patterns against the actual API response, not documentation or assumed naming conventions.

## Carry-forward

| Item | Type | Priority | Tracker |
|---|---|---|---|
| Operational verification: no real diverged-base shipping run yet confirms F17 gate fires | process | P3 | `docs/research/v2-follow-ups.md` F17 |
| Operational verification: no real artifact-free shipping run outside F18's own PR yet confirms F18 gate fires independently | process | P3 | `docs/research/v2-follow-ups.md` F18 |
