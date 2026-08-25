---
module: internal/mcp
date: "2026-08-25"
problem_type: best_practice
component: git_commit_regression_test
severity: medium
applies_when:
  - "a test discriminates two code paths by wall-clock elapsed time"
  - "a timing budget or sleep duration in a test was chosen and validated only by repeated passing runs"
  - "a regression test for a race/starvation bug uses a fake subprocess with configurable sleep durations"
tags:
  - timing-tests
  - flaky-tests
  - mutation-testing
  - regression-tests
  - contention
---

# Measure timing-margin overhead directly; do not validate margins by counting passing runs

## Context

`internal/mcp/tools.go`'s `gitAutoCommit` and `cmd/cogvault/ingest.go`'s
`postIngestGitCommit` originally shared one `context.WithTimeout` across their
`git add` and `git commit` subprocesses — a slow-but-not-wedged `add` could
consume most of the shared budget and kill `commit`. The fix split the two
into independent per-command timeout contexts (PR #34).

The regression test proving this used a fake `git` binary with configurable
`GIT_FAKE_ADD_SLEEP`/`GIT_FAKE_COMMIT_SLEEP` env vars and asserted on elapsed
wall-clock time. The first margin (250ms budget, 200ms sleep, 50ms margin)
was validated by running the full test suite 5 times back-to-back and
observing no failures — then flaked on the very next unrelated run, under
real `go test -race -count=1 ./...` full-suite contention (PR #35).

The second margin (500ms budget, 300ms sleep, 200ms margin) was again
validated only by repeated-run counts and mutation testing under the same
kind of load — until directly measuring the fake binary's fork/exec overhead
under synthetic 16-core CPU-spin contention showed 68–206ms of overhead,
within noise of the 200ms margin. The third margin (1500ms budget, 900ms
sleep, ~600ms margin — 3x the measured 206ms worst case) was the first one
derived from an actual measurement rather than a repeat-count.

## Guidance

1. When a test discriminates behavior by wall-clock elapsed time against a
   timeout budget, first measure the underlying mechanism's real overhead
   under worst-case contention — do not guess a number and validate it by
   running the test repeatedly.
2. "N back-to-back passing runs" answers "has this specific run of the
   scheduler happened to cooperate N times," not "is this margin wide enough
   under contention this machine hasn't yet produced." They are different
   questions; only the second one is the actual safety property.
3. To measure worst-case overhead: launch synthetic CPU-saturating load
   (e.g. `yes > /dev/null &` on every core) and time the exact subprocess
   call the test depends on, several times, under that load. Compare against
   overhead with no load and under the real contention the test normally
   runs in (e.g. the actual `go test -race ./...` full suite) — the
   synthetic worst case is usually much larger than either.
4. Size the margin as a multiple (3x or more) of the measured worst case,
   not an arbitrary round number one step above the last flake.
5. Cap the fix path explicitly in the test's own comment: name the number of
   widenings already tried and state that another flake means redesigning
   away from wall-clock discrimination (e.g. a fake subprocess writing a
   marker file the test asserts on for presence/order, instead of elapsed
   duration) — not a further widening. Without this cap, each flake invites
   another guess-and-check cycle.

## Why This Matters

A wall-clock-discriminated test can never be made unconditionally safe
against unbounded scheduler contention — there is always a load level that
breaks any finite margin. The goal is not eliminating that possibility; it
is choosing a margin backed by a measurement of the actual failure mode
(subprocess fork/exec overhead, in this case), so the next flake is a
genuine anomaly worth investigating rather than a predictable consequence of
an unmeasured guess.

Repeated-run validation without a measured bound produces false confidence:
it passed 5 times, but 5 passes under one load profile says nothing about
the load profile that will actually occur later (a different CI runner, a
noisy neighbor, more parallel test packages).

## When to Apply

Use this before accepting any test change that:

- asserts `elapsed < timeout` or `elapsed >= duration` against a
  configurable sleep or timeout value;
- was widened once already because of an observed flake; or
- discriminates two code paths (e.g. shared vs. independent timeout
  contexts) purely by how long the whole operation took.

Do not apply it to tests whose timing bound is a hard product requirement
(e.g. "response must complete within 100ms") — those need a different
conversation about the requirement itself, not a wider test margin.

## Examples

**Incorrect**: "The test passed 5 times in a row with a 200ms margin, so
200ms is safe." — PR #35's margin was within noise of the measured 206ms
worst-case fork/exec overhead and flaked on the next run under real
contention.

**Correct**: Measure the fake `git` binary's fork/exec overhead under
synthetic 16-core CPU-spin load directly (`68–206ms` observed), then choose
a margin (`600ms`, ~3x the worst case) derived from that measurement — PR
#36. Confirm via mutation testing (bug fails 10/10, fix passes 10/10) under
the same synthetic load used to take the measurement, not just under the
test's normal run conditions.

**Correct, capping the fix path**: After the second widening still required
external pushback to catch as insufficient, document the full widening
history inline in the test and state explicitly that a fourth widening is
not the fix for the next flake — a design change (marker-file assertion
instead of wall-clock discrimination) is (PR #37).
