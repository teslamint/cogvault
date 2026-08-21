# Retro: F16 — Bound CLI stdout/stderr capture

**PR**: #30 (merged 57f9457, 2026-08-21)
**Follow-up**: F16 in `docs/research/v2-follow-ups.md`
**Scope**: `internal/llm/claudecode.go`, `DESIGN.md` §2.6

## What shipped

Replaced unbounded `bytes.Buffer` with `cappedWriter` (stdout 4 MiB, stderr 1 MiB) in the Claude CLI adapter. Stdout overflow returns a permanent error. Check order: context cancellation → refusal → overflow → deadline exceeded.

## Success criteria evaluation

| Criterion | Met? | Evidence |
|-----------|------|----------|
| Process memory bounded during CLI capture | Yes | `cappedWriter` silently discards bytes past the cap |
| Legitimate wiki pages unaffected | Yes | `TestDigestHappy`, `TestDigestFencedStripped` pass unchanged; 4 MiB is ~40x any realistic page |
| Deterministic oversized output does not retry forever | Yes | Overflow is permanent (not transient); matches `TestDigestGarbagePermanent` pattern and F6 precedent |
| Refusal classification still outranks overflow | Yes | `TestDigestStdoutOverflowRefusalPrecedence` pins this |

## What went well

- **F6 precedent guided the error classification.** The review caught the initial ErrTransient design — invariant 3 (only permanent failures consume bounded attempts) would have created the exact infinite-retry mode F6 documented. The advisor identified this before implementation.
- **Review caught the check ordering gap.** DeadlineExceeded before overflow would have allowed a runaway CLI (>4 MiB then hang) to retry forever as transient. Moving DeadlineExceeded after overflow closed this.
- **Small, focused change.** 134 lines added, 12 removed. Three new tests, zero regressions across the full suite.

## What could improve

- **Initial design had two classification errors** (ErrTransient, check ordering). Both were caught by review, but a single-pass implementation would have shipped them. The F6 precedent was available in the same file's follow-ups tracker — checking existing follow-up entries for analogous failure modes before designing would have caught the ErrTransient issue inline.

## Carry-forward

| Item | Type | Priority | Tracker |
|------|------|----------|---------|
| `cappedWriter` drops the entire crossing chunk, not just excess bytes — actual captured amount is up to one os/exec copy chunk (32 KiB) less than the cap | known-limitation | P4 | This retro |
| No ingest-level test pins overflow → `classPermanent`; permanence rides on iota default fallthrough | test-gap | P4 | This retro |
