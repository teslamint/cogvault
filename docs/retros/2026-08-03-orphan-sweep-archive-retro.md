# Retro: Orphan sweep archive

- Date: 2026-08-03
- Source: PR #14
- Spec: docs/specs/2026-08-03-orphan-sweep-archive-design.md
- Plan: docs/plans/2026-08-03-001-feat-orphan-sweep-archive-plan.md

## Release data

| Metric | Value |
|---|---|
| **Changed non-test lines** | 1,434 added and removed lines |
| Commits | 23 |
| Review rounds | 2 |
| Comments (fixed / deferred) | 0 / 0 |
| CI failures | 0 |
| Duration (first spec commit → merge) | 6h 10m 56s |
| Units planned / completed | 10 / 10 across plans 001, 002, and 003 |

## Success criteria: measured vs declared

The retrospective ran each measurement after merge commit `45ce6bb`.

| # | Declared criterion | Measurement (command / rubric) | Measured result | Verdict |
|---|---|---|---|---|
| SC1 | An orphaned source archives its page and supersedes its ledger row | `go test -race ./internal/ingest -run '^TestSweepOrphansSourceDeleted$' -count=1 -v` | verified: the test passed and asserted the exact readable archive path plus a `superseded` row | Met |
| SC2 | Search excludes an archived page | `go test -race ./internal/ingest -run '^TestSweepOrphansSearchExclusion$' -count=1 -v` | verified: the test passed after forced consistency and found no archived-page result | Met |
| SC3 | Dry-run reports `would-archive` without a move | `go test -race ./internal/ingest -run '^TestSweepOrphansDryRun$' -count=1 -v` | verified: the test passed and preserved both storage and ledger state | Met |
| SC4 | Existing pages remain unaffected | `go test -race ./internal/ingest -run '^TestSweepOrphansAllPresent$' -count=1 -v` | verified: the test passed with every source present and zero archived pages | Met |
| SC5 | A missing source directory skips its sweep | `go test -race ./internal/ingest -run '^TestSweepOrphansSourceDirMissing$' -count=1 -v` | verified: the test passed with zero archives and emitted the expected unavailable-directory warning | Met |
| SC6 | The default exclusion and canonical documents include `sources/_archived` | `grep sources/_archived internal/config/config.go SPEC.md DESIGN.md` | verified: operative config code and canonical SPEC and DESIGN lines all matched | Met |

## Carry-forward from previous retro

Previous retro: `docs/retros/2026-07-27-makefile-codesign-retro.md`.

| Item | Status | Evidence |
|---|---|---|
| Add destination codesign to the Makefile | Done | PR #11 and tracker F10 |
| Update the F9 tracker row | Done | tracker F9 names PR #10 and main `3b3659f` |
| Set `llm.model=opus` in the real config | Done | previous retro recorded the live config check; tracker F6 records the model recovery path |
| Deferred F6 minors | Not started | tracker F2 remains Not started |
| F1 SC3 and SC4 validation | Done | tracker F1 records all four criteria as Done |
| F2 through F5, F7, and F8 | In progress | F4 completed in PR #14; F7 and F8 were already Done; F2, F3, and F5 remain Not started (T1) |

- Previous doc shape: conformant

## Interview Transcript

- Independence level: same-model fresh-context
- Rounds used: 2 (max 5)

| ID | Round | Phase | Probe | Answer | Evidence | Verdict (verbatim) |
|---|---|---|---|---|---|---|
| T1 | 1→2 | 4 | Which prior carry-forward items did this release reconcile? | This release reconciled F4 only. F2, F3, and F5 stayed open. Older Done rows remained unchanged. | tracker diff for merge `45ce6bb`; PR #14; plans 001, 002, and 003 | accepted |
| T2 | 1→2 | 3 | What exact contract does the SC6 grep prove? | Runtime defaults and `AllExcluded` enforce the archive exclusion. SPEC and DESIGN own the matching canonical rules. | `internal/config/config.go:35,102,127-131`; `SPEC.md:113,143,524-525`; `DESIGN.md:169` | accepted |
| T3 | 1→2 | 5 | Which review-round-one production risk mattered most? | An empty or partially unavailable source could archive many live wiki pages. Exact survivor proof prevents that mutation. | review-round-one ledger entry; remediation deviation lines 63-66; survivor tests | accepted |
| T4 | 1→2 | 5 | What proves the final ledger-query behavior? | A failed query now returns an error before later mutations. A new connection lets the next run converge. | round-two finding 6; ledger-query deviation; plan 003 U10 tests and evidence | accepted |
| T5 | 1→2 | 5 | Do 23 commits and three plans show hardening or under-scoping? | They show both. Review gates worked, but critical availability and failure-state requirements appeared too late. | release-loop timeline; both deviations; 10/10 completed units | accepted |

## Findings

### What worked well

- **What happened**: The branch review stopped three P1 risks before merge. It later cleared six P2 findings through approved remediation.
  **Why**: The workflow required independent reviews, sealed plans, deviations, and fresh race tests.
  **How to apply**: Keep a branch-level adversarial review after every task-level review passes.
  **Cites**: T3, T4, Phase 2 release data

### What to improve

- **What happened**: Critical upgrade, source-availability, permission, cancellation, and run-level failure rules appeared after initial implementation.
  **Why**: The first design did not model every mutation and failure state across config, storage, ledger, and source boundaries.
  **How to apply**: Create the mutation matrix and failure evidence commands before implementation starts.
  **Cites**: T5, both deviation documents

### Process observations

- **What happened**: Placeholder fixture paths made the first U3 evidence records unauditable despite passing tests.
  **Why**: The tests did not emit exact fixture identity and boundary-sentinel data during the same invocation.
  **How to apply**: Make each proving test log its disposable targets before evidence capture.
  **Cites**: T5, round-two finding 4

## Carry-forward items registered

| Item | Type | Priority | Tracked at |
|---|---|---|---|
| Replace the F4 tracker placeholder with PR #14 and merge `45ce6bb` | process | P4 | `docs/research/v2-follow-ups.md` F4 |

## Lessons

- A safety fix that contradicts a sealed plan needs a committed deviation and a separately approved plan.
- Evidence must come from the same disposable test run. Placeholder fixture paths make a passing test unauditable.

## Compounding

- compound invocation: `Documentation complete — docs/solutions/workflow-issues/review-introduced-state-machine-deviation.md`

Retrospective complete — docs/retros/2026-08-03-orphan-sweep-archive-retro.md
