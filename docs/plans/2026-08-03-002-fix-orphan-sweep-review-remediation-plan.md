---
schema: plan/v1
title: "F4 review remediation"
type: fix
status: draft
date: 2026-08-03
execution: code
origin: docs/specs/2026-08-03-orphan-sweep-archive-design.md
---

## Goal

Resolve every actionable finding from orphan-sweep review round 1.
Preserve the approved artifacts through the committed deviation addendum.
Return review round 2 with no P0-P2 findings.

## Architecture notes

**Decision: the archive exclusion is an internal invariant.**
`Config.AllExcluded` always includes `sources/_archived` exactly once.
The user `exclude` list remains unchanged.
This repairs existing configs without a config migration.

**Decision: a surviving tracked source proves directory availability.**
The sweep reads an exact directory snapshot before it selects candidates.
It archives only when one tracked source remains and exactly one is missing.
An all-missing or multi-missing directory is ambiguous and remains unchanged.
This favors safety over immediate cleanup.

The sweep reads the directory again immediately before each page move.
An exact restored entry cancels that candidate.
The implementation must compare actual entry names.
It must not use case-folded `Stat` results for this check.
`Runner.readDir func(string) ([]os.DirEntry, error)` provides both snapshots.
`New` sets the function to `os.ReadDir`.

Persisted device identifiers and sentinel files are rejected.
They add schema or source-directory state.
The v2 boundary keeps source directories read-only.

**Decision: storage enforces permissions and no-overwrite behavior.**
`Storage.Move` rejects either path when `exclude_read` matches.
It also rejects an existing destination with an error that wraps `os.ErrExist`.
The check runs under the existing storage mutex.
Permission checks run before filesystem existence checks.
For permitted paths, a missing source takes precedence over an occupied destination.
This lets a completed move resume through the existing `ErrNotFound` branch.

Platform-specific rename flags are rejected.
They would add operating-system branches for one local workflow.
The accepted external filesystem race in decision 0004 remains unchanged.

**Decision: success rows require a live wiki page.**
Ingest calls `Storage.Stat` before it reports a source as unchanged.
A missing page makes the source eligible for re-digestion.
Any other storage error records a per-file infra failure without changing the row.
This repairs move-then-ledger-failure states without a new ledger status.

**Decision: cancellation applies to the sweep.**
`Runner.Run` checks the context before the sweep.
The sweep checks the context before each candidate.
`sweepOrphans(ctx, report, dryRun) error` returns `ctx.Err()`.
`Run` returns the wrapped cancellation error immediately.
After a page move starts, the row finishes its ledger transition.

**Decision: archive exclusion does not deny direct reads.**
An archived page remains readable through its exact wiki path.
The user removes retained archive pages manually.

Authoritative deviation:
`docs/deviations/2026-08-03-orphan-sweep-review-remediation.md`.

## Global constraints

- Never write to a configured source directory.
- Keep the approved spec and approved plan byte-identical.
- Preserve the ingest single-writer lock.
- Preserve the storage global write mutex.
- Dry-run must not mutate storage or the ledger.
- Keep archived pages readable through exact paths.
- Use no new dependencies.
- Use test-first execution for every behavior change.

## Assumption Recheck

Origin spec: `docs/specs/2026-08-03-orphan-sweep-archive-design.md`.

The committed deviation addendum authorizes each contradiction below.
Addendum commits: `89389c7` and `6745cce`.

| Approved claim | Fresh evidence | Outcome |
|---|---|---|
| Explicit user excludes require manual archive exclusion | `git show main:internal/config/config.go` shows old generated configs contain an explicit default list | contradiction — existing configs violate canonical deindexing behavior; addendum item 1 applies |
| One source-directory `Stat` prevents mass archive | Review validation reproduced an empty mountpoint and check-use race at `internal/ingest/ingest.go:319` | contradiction — addendum items 2 and 3 apply |
| `Storage.Move` respects the storage boundary | `internal/storage/fs.go:110-139` omits `exclude_read` and destination collision checks | contradiction — addendum items 5 and 6 apply |
| Dry-run has no side effects | `internal/ingest/ingest.go:343-349` returns before move and ledger update | match — strengthen the test proof |

## File structure

| File | Action | Responsibility |
|---|---|---|
| `internal/config/config.go` | Modify | Add the invariant archive exclusion to effective exclusions |
| `internal/config/config_test.go` | Modify | Cover upgraded explicit lists and exact deduplication |
| `internal/index/sqlite_test.go` | Modify | Prove old explicit configs exclude archive pages from consistency scans |
| `internal/storage/fs.go` | Modify | Enforce `exclude_read` and preserve existing destinations |
| `internal/storage/fs_test.go` | Modify | Cover both permission directions and destination collisions |
| `internal/ingest/ingest.go` | Modify | Add survivor proof, exact recheck, cancellation, and missing-page recovery |
| `internal/ingest/ingest_test.go` | Modify | Cover every review finding and recovery branch |
| `SPEC.md` | Modify | Record the effective exclusion and conservative sweep behavior |
| `DESIGN.md` | Modify | Record the safety algorithm and Move semantics |
| `.release-loop/evidence/U3/` | Create locally | Record T1 and T2 transition evidence from disposable fixtures |
| `.release-loop/evidence/U4/` | Create locally | Record T2 recovery and T3 rebuild evidence from disposable fixtures |

## Scenario coverage map

The origin spec has no User Scenarios section.
The units cover success criteria SC1-SC6 and every review-round finding.

| Contract scenario | Unit chain | Walking evidence |
|---|---|---|
| SC1 orphan archive and supersede | U1 -> U2 -> U3 | `TestSweepOrphansSourceDeleted` plus exact destination and ledger assertions |
| SC2 archived page absent from search | U1 -> U3 | upgraded-config consistency test plus `TestSweepOrphansSearchExclusion` |
| SC3 dry-run has no side effects | U4 | dry-run storage and ledger assertions |
| SC4 present sources remain unchanged | U3 | survivor snapshot test with a retained source |
| SC5 unavailable source skips sweep | U3 | empty and unreadable directory tests |
| SC6 archive exclusion and canonical docs | U1 -> U5 | config tests and canonical document checks |
| Review recovery branches | U2 -> U3 -> U4 | cancellation, restore race, collision, and ledger recovery tests |

## Implementation Units

### U1: Effective archive exclusion

Execution note: test-first
Files:
  Modify: `internal/config/config.go`
  Test: `internal/config/config_test.go`, `internal/index/sqlite_test.go`
Interfaces:
  Consumes: `(*Config).AllExcluded() []string`
  Produces: an effective exclusion list that contains `sources/_archived` exactly once
Test scenarios:
  happy: a pre-feature explicit list returns its entries plus `sources/_archived`
  edge: an explicit list that already contains the archive path does not duplicate it
  error: n/a — this pure list operation returns no error
  integration: `internal/index` loads the old generated default and excludes an archived path, Covers SC2 and SC6
Steps:
  1. Write failing config tests for the old explicit default and duplicate prevention.
  2. Run the targeted tests and confirm the archive path is absent or duplicated.
  3. Add the internal archive exclusion to `AllExcluded` without mutating `Config.Exclude`.
  4. Add the consistency integration test in `internal/index/sqlite_test.go`.
  5. Run the config and index tests.
  6. Commit: `fix(config): Preserve archive exclusion across upgrades`.
Acceptance: `go test ./internal/config ./internal/index` passes.

### U2: Safe storage move

Execution note: test-first
Files:
  Modify: `internal/storage/fs.go`
  Test: `internal/storage/fs_test.go`
Interfaces:
  Consumes: `(*FSStorage).Move(src, dst string) error`, `(*FSStorage).isExcludeRead(path string) bool`
  Produces: `ErrPermission` for protected paths and `os.ErrExist` for an occupied destination
Test scenarios:
  happy: a normal page moves to a new archive destination
  edge: an existing destination remains byte-identical and the source remains present
  error: both `exclude_read` directions return `ErrPermission`, including a missing protected source
  integration: a missing source with an occupied destination returns `ErrNotFound` for sweep recovery
Steps:
  1. Write failing tests for both permission directions and an occupied destination.
  2. Run the targeted tests and confirm `Move` currently crosses or replaces each boundary.
  3. Resolve both paths, reject protected paths, check the source, then check the destination.
  4. Run the storage and MCP tests.
  5. Commit: `fix(storage): Preserve archive and permission boundaries`.
Acceptance: `go test ./internal/storage ./internal/mcp` passes.

### U3: Conservative orphan selection

Execution note: test-first
Files:
  Modify: `internal/ingest/ingest.go`
  Test: `internal/ingest/ingest_test.go`
Interfaces:
  Consumes: `context.Context`, `ledger.successRows()`, and `Runner.readDir func(string) ([]os.DirEntry, error)`
  Produces: `sweepOrphans(ctx context.Context, report *Report, dryRun bool) error`
Test scenarios:
  happy: one retained success source and one missing source produce one archive
  edge: zero survivors and several missing rows both skip; ordered snapshot 2 restores one candidate and cancels its move
  error: a directory read failure and a non-`ErrNotFound` move failure preserve pages and ledger rows
  integration: exact archive names, direct archive reads, and `TestSweepOrphansSearchExclusion` prove SC1, SC2, SC4, and SC5
Steps:
  1. Write failing tests for single-missing proof, multi-missing refusal, exact restore detection, cancellation, read failure, move failure, and exact archive names.
  2. Run the targeted tests and confirm each current unsafe branch.
  3. Add `Runner.readDir` with `os.ReadDir` as the production default.
  4. Return context errors and add exact initial and pre-move snapshots.
  5. Require one survivor and exactly one missing row before `Move`.
  6. Write `.release-loop/evidence/U3/T1-{success,forced-failure,rerun,headless,cancellation}.md`.
  7. Write `.release-loop/evidence/U3/T2-{success,cancellation}.md`.
  8. Run the ingest race tests and commit: `fix(ingest): Require proof before orphan archives`.
Acceptance: `go test -race ./internal/ingest` passes.

### U4: Ingest recovery and proof completion

Execution note: test-first
Files:
  Modify: `internal/ingest/ingest.go`, `internal/ingest/ingest_test.go`
Interfaces:
  Consumes: `Storage.Stat(path string) (size int64, mtime time.Time, err error)` and a found `ledgerRow`
  Produces: re-digestion only when page `Stat` returns `ErrNotFound`
Test scenarios:
  happy: a present source rebuilds its missing success page
  edge: a move-then-ledger-failure state heals after the source returns
  error: a protected or unreadable page preserves the success row, increments `Failed`, and appends `actionFailed`
  integration: dry-run preserves the success row and creates no archive destination, Covers SC3
Steps:
  1. Write failing tests for missing-page recovery and move-then-ledger-failure recovery.
  2. Strengthen the dry-run test with storage and ledger assertions.
  3. Add protected-page and non-`ErrNotFound` storage error tests.
  4. On a non-`ErrNotFound` error, increment `Failed` and append `actionFailed` with `stat wiki page: <error>`.
  5. Preserve the success row by not calling `recordFailure` for that stat error.
  6. Verify a success row with `Storage.Stat` before reporting `Unchanged`.
  7. Add a temporary `BEFORE INSERT` trigger for `status = 'superseded'` and raise `ABORT`.
  8. Drop the trigger before the recovery rerun and assert page, index, and row convergence.
  9. Write `.release-loop/evidence/U4/T1-rollback.md`.
  10. Write `.release-loop/evidence/U4/T2-{forced-failure,rerun,rollback,headless}.md`.
  11. Write `.release-loop/evidence/U4/T3-{success,forced-failure,rerun,rollback,headless,cancellation}.md`.
  12. Run ingest race tests and commit: `fix(ingest): Recover missing success pages`.
Acceptance: `go test -race ./internal/ingest` passes.

### U5: Canonical contract reconciliation

Execution note: skip-test-first
Files:
  Modify: `SPEC.md`, `DESIGN.md`
Interfaces:
  Consumes: the committed deviation addendum and implemented behavior
  Produces: canonical behavior and architecture documentation
Test scenarios:
  happy: SPEC describes the invariant archive exclusion and survivor proof
  edge: DESIGN describes all-missing, multi-missing, direct-read, and no-overwrite behavior
  error: n/a — documentation changes have no runtime error path
  integration: canonical claims match tests and code, Covers SC6
Steps:
  1. Update the config default and archive exclusion contract in `SPEC.md`.
  2. State that exact-path reads can access retained archive pages until manual removal.
  3. Update sweep safety, cancellation, recovery, and Move semantics in `DESIGN.md`.
  4. Verify every new canonical claim against the implemented tests.
  5. Run `git diff --check`.
  6. Commit: `docs: Reconcile orphan sweep safety contracts`.
Acceptance: `rg -n "sources/_archived|survivor|os.ErrExist|exclude_read" SPEC.md DESIGN.md` returns the new contracts.

## Mutation/failure-state matrix

Evidence records use disposable `t.TempDir` fixtures.
No record may reference the user's configured source or wiki directories.

| Transition | Pre-state | Action | Success | Forced failure | Rerun | Rollback or compensation | Headless | Cancellation or abort | Unit / evidence owner |
|---|---|---|---|---|---|---|---|---|---|
| T1 archive page | one tracked source is present; exactly one is missing; live page and success row exist | move the live page to its unused archive destination | archive page exists; live page is absent; row can advance | pre-create the destination or deny it through `exclude_read`; both files and the success row remain | a prior successful move returns `ErrNotFound` before destination occupancy is checked | a restored source rebuilds the live page through U4 recovery | scheduled and interactive origins use the same non-interactive branch | cancellation before a candidate returns an error; cancellation after move starts completes that row | U2/U3/U4; transition evidence in U3 and compensation evidence in U4 |
| T2 supersede ledger row | page move completed; ledger row is `success` | persist `superseded` | row becomes `superseded` | a temporary `BEFORE INSERT` trigger rejects `status = 'superseded'`; the page remains archived and the row remains `success` | drop the trigger; absent source retries supersede; restored source rebuilds its missing live page | next-run recovery is the compensation; no source write occurs | scheduled origin logs and continues without a prompt | the candidate completes its ledger step after a move starts | U3/U4; transition evidence in U3 and recovery evidence in U4 |
| T3 rebuild missing live page | source exists; success row exists; live wiki page is missing | digest and write the page again | page, index, and success row agree | inject a digest or storage failure; the file remains retryable and is not unchanged | the next run retries through existing failure classification | no prior live page exists to roll back; a later retry compensates | scheduled origin uses the same retry path | cancellation before the file prevents digest; adapter cancellation uses the existing context | U4; `.release-loop/evidence/U4/T3-*` |

### Evidence production

Each record names the plan, matrix row, source commit, fixture root, and configured targets.
Each record includes the command, exit status, pre-state, post-state, and next-run result.
All commands use `-count=1 -v` and disposable test fixtures.

| Record | Proving test command | Required observations |
|---|---|---|
| `U3/T1-success.md` | `go test -race ./internal/ingest -run '^TestSweepOrphansSourceDeleted$' -count=1 -v` | one live page moves; the exact archive page is readable; the source row advances |
| `U3/T1-forced-failure.md` | `go test -race ./internal/ingest -run '^TestSweepOrphansMoveFailure$' -count=1 -v` | the move fails; both the live page and success row remain |
| `U3/T1-rerun.md` | `go test -race ./internal/ingest -run '^TestSweepOrphansWikiPageAlreadyGone$' -count=1 -v` | the missing live page does not block the supersede rerun |
| `U4/T1-rollback.md` | `go test -race ./internal/ingest -run '^TestSweepOrphansLedgerFailureRestoredSource$' -count=1 -v` | a restored source rebuilds the live path without a source write |
| `U3/T1-headless.md` | `go test -race ./internal/ingest -run '^TestSweepOrphansScheduled$' -count=1 -v` | scheduled origin completes without a prompt |
| `U3/T1-cancellation.md` | `go test -race ./internal/ingest -run '^TestSweepOrphansCancellationBetweenCandidates$' -count=1 -v` | candidate 1 completes; candidate 2 remains unchanged; `Run` returns cancellation |
| `U3/T2-success.md` | `go test -race ./internal/ingest -run '^TestSweepOrphansSourceDeleted$' -count=1 -v` | the exact bound row becomes `superseded` |
| `U3/T2-cancellation.md` | `go test -race ./internal/ingest -run '^TestSweepOrphansCancellationBetweenCandidates$' -count=1 -v` | the moved row completes its ledger transition before cancellation returns |
| `U4/T2-forced-failure.md` | `go test -race ./internal/ingest -run '^TestSweepOrphansLedgerSupersedeFailure$' -count=1 -v` | the trigger aborts only the supersede insert; the row remains `success` |
| `U4/T2-rerun.md` | `go test -race ./internal/ingest -run '^TestSweepOrphansLedgerFailureMissingSourceRerun$' -count=1 -v` | removing the trigger lets the next absent-source run supersede the row |
| `U4/T2-rollback.md` | `go test -race ./internal/ingest -run '^TestSweepOrphansLedgerFailureRestoredSource$' -count=1 -v` | a restored source rebuilds its missing live page and preserves row binding |
| `U4/T2-headless.md` | `go test -race ./internal/ingest -run '^TestSweepOrphansLedgerFailureScheduledRecovery$' -count=1 -v` | scheduled recovery converges without a prompt |
| `U4/T3-success.md` | `go test -race ./internal/ingest -run '^TestRunRebuildsMissingSuccessPage$' -count=1 -v` | page, index, and success row converge |
| `U4/T3-forced-failure.md` | `go test -race ./internal/ingest -run '^TestRunMissingSuccessPageDigestFailure$' -count=1 -v` | failure is not reported as unchanged and remains retryable |
| `U4/T3-rerun.md` | `go test -race ./internal/ingest -run '^TestRunMissingSuccessPageRetry$' -count=1 -v` | the next run rebuilds the page |
| `U4/T3-rollback.md` | `go test -race ./internal/ingest -run '^TestRunMissingSuccessPageWriteFailure$' -count=1 -v` | no partial live page remains; the next run can compensate |
| `U4/T3-headless.md` | `go test -race ./internal/ingest -run '^TestRunMissingSuccessPageScheduled$' -count=1 -v` | scheduled origin rebuilds without a prompt |
| `U4/T3-cancellation.md` | `go test -race ./internal/ingest -run '^TestRunMissingSuccessPageCanceled$' -count=1 -v` | cancellation prevents a new digest mutation |

## Carry-forward trigger audit

| Tracker row | Trigger class | What fired it | Disposition |
|---|---|---|---|
| F5 dead `contentHash()` cleanup | edit-based | this plan modifies `internal/ingest/ingest.go` | defer; deletion is unrelated to review remediation and would widen the diff |

F2 is event-based and did not fire.
The F5 fixture and MCP targets are edit-based and did not fire.

Unobservable drift-based row:

| Tracker row | Named record | Why unobservable |
|---|---|---|
| F3 FTS `BUSY_SNAPSHOT` | real scheduled-ingest and serve collision logs | this local remediation does not inspect production logs |

Audited `docs/research/v2-follow-ups.md` at `6745cce`: 3 open rows, 1 fired, 1 unobservable.

## Deferred to Follow-Up Work

- F5 dead code, fixtures, and MCP fallback remain in `docs/research/v2-follow-ups.md`.
- Atomic platform-specific no-replace rename remains unnecessary for the local single-user threat model.
- Ledger corruption validation remains out of scope because all repository writers generate bound SHA-256 rows.

## Open unknowns

### Planning-time

No planning-time unknowns remain.

### Implementation-time

No implementation-time unknowns remain.
