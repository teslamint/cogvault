---
schema: plan/v1
title: Scheduled configured-path access check
type: feat
status: approved
date: 2026-08-29
execution: code
origin: docs/specs/2026-08-29-scheduled-access-check-design.md
deepened: true
body_seal: b11d30828a89ec54a443b21c1e84299cc286767670fd2dc808ebfa62855aad85
---

## Goal

Add a safe preflight for the filesystem paths that scheduled ingest uses. Run that
preflight twice through one temporary LaunchAgent so an operator can observe access
grant reuse without reading or modifying the TCC database.

## Architecture notes

`access-check` is a config-only Cobra command. It does not call `bootstrap`, open the
SQLite database, initialize storage, invoke Git, run the LLM, or send a notification.
This boundary makes a prompt during the preflight distinct from a full-ingest-only
prompt.

The command uses a private `accessCheckOps` function bundle. Production binds it to
`os` functions. Tests replace only the failing operation. This seam preserves both a
primary probe error and a cleanup error without adding a package-level abstraction.

Write probes create an exclusive `0600` sentinel in `wiki_dir` and the `db_path`
parent. Each probe writes, closes, reads, compares, and removes the sentinel. Source
probes mirror ingest's top-level traversal and type and size filters. After open, the
command opens with `unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC`. It compares
descriptor `Stat` with the prior `Lstat` through `os.SameFile` and requires a regular
file before reading one byte. Empty-file EOF is success.

The shell harness uses `plutil` operations to build a plist. It never interpolates
raw XML. The selected CogVault binary remains `ProgramArguments[0]`. The harness
uses one label for two `launchctl kickstart -p` calls. It polls each returned PID for
termination and then requires the command-owned stdout marker. It never parses
`launchctl print` output, which macOS does not define as an API.

The harness sets `umask 077`. It creates its plist and logs inside one verified
`0700` temporary directory. It rejects a symlinked or wrongly owned directory. Before
each run it verifies an absolute, regular, executable binary with `codesign`. It
records the binary file identity, SHA-256, and designated requirement. These values
must remain equal before and after both runs.

The harness is a stateful ceremony because it registers a job. Its trap removes only
its unique job. It removes the private directory on success. On failure it retains
the `0600` plist and logs and prints an exact deletion command. A failed `bootout`
retains all artifacts, reports a cleanup failure, and prints a recovery command. No
step alters the production ingest job.

`docs/deviations/2026-08-29-scheduled-access-launchd-lifecycle.md` authorizes this
PID-based lifecycle. It preserves the approved spec as the original decision record
and explains why unsupported `launchctl print` parsing cannot remain authoritative.

Known Pattern: `cmd/cogvault/status.go` loads config without `bootstrap`.
`deploy/com.teslamint.cogvault.ingest.plist` defines the production launchd argument
shape. `docs/solutions/build-errors/macos-sigkill-rebuilt-go-binary.md` defines the
stable code-identity and responsible-process constraints.

## Assumption Recheck

Rerun time: `2026-08-28T23:26:38Z`.

| Approved claim | Fresh evidence | Outcome |
|---|---|---|
| `wiki_dir` may use iCloud Drive. | `rg -n 'wiki_dir.*iCloud Drive' SPEC.md` still finds the allowed layout. | match |
| The root command owns the persistent `--config` flag. | `sed -n '8,34p' cmd/cogvault/main.go` shows the flag and command registration. | match |
| The active config names no Documents or Pictures source. | The sanitized config shape contains one Downloads source. | match |
| Active configured paths use local storage. | `df -P` resolves all three surfaces to the Data volume; the network-mount filter returns no row. | match |
| The scheduled job uses the installed signed binary. | The plist names `$HOME/bin/cogvault`; its requirement has identifier `dev.tmint.cogvault` and an Apple Development certificate constraint. | match |
| A logged-in GUI launchd domain is available. | `launchctl print gui/$(id -u)` exits zero. | match |

These results describe the planning snapshot. The runtime command always uses the
config supplied for that invocation.

Planning review found one approved-contract contradiction after the assumption
recheck. The original launchd completion and failure-cleanup state machine is unsafe.
The committed deviation addendum discharges that contradiction before plan approval.

## File structure

| File | Responsibility | Unit |
|---|---|---|
| `cmd/cogvault/access_check.go` | config-only command, write probes, source probes, error preservation | U1 |
| `cmd/cogvault/access_check_test.go` | command contract and filesystem boundary regression tests | U1 |
| `cmd/cogvault/main.go` | register `access-check` | U1 |
| `scripts/check-scheduled-access.sh` | temporary LaunchAgent ceremony | U2 |
| `scripts/check-scheduled-access_test.sh` | fake-launchctl ceremony and plist regression tests | U2 |
| `SPEC.md` | CLI contract and config-only exception; deviation governs the corrected harness lifecycle | U3 |
| `DESIGN.md` | command bootstrap boundary and file ownership | U3 |
| `README.md` | operator procedure and diagnostic interpretation | U3 |
| `.release-loop/runs/scheduled-access-check/evidence/U4/` | disposable real-macOS ceremony transcript; not committed | U4 |

## Scenario coverage map

| Scenario | Unit chain | Evidence |
|---|---|---|
| S1 — verify configured ingest paths | U1 → U3 | `TestAccessCheckConfiguredSurfaces` uses real roots and accepted-file read outcomes, including a new settle-window file, and proves no runtime bootstrap. Covers S1. |
| S2 — complete the launchd approval ceremony | U1 → U2 → U3 → U4 | The shell test proves the state machine; U4 records two real runs through the GUI launchd domain. Covers S2. |
| S3 — diagnose a failed scheduled access check | U2 → U3 | Successful bootout leaves the job absent and private failure artifacts retained; bootout failure retains artifacts and prints recovery commands. Covers S3. |

## Test discrimination checks

The tests must fail when the protected behavior changes. A passing happy-path fixture
alone does not satisfy this plan.

| Boundary | Invariance fixture | Changed-axis fixture | Effect-bearing signal |
|---|---|---|---|
| Configured source set | Run the same two-root config twice against real temporary roots. Inject a read failure only for a rejected-extension file; both runs still succeed. | Inject the same sentinel read failure only for one accepted file in the third real root; the command fails with that source, path, and read operation. | Accepted-file error and exit status; root output and recorded enumeration are secondary evidence. |
| Config-only command | Run the same Cobra invocation twice with absent DB, WAL, SHM, and `_schema.md`; both post-run trees equal the pre-run tree. | In an ephemeral worktree, mutate the access-check handler to call `bootstrap`; the unchanged no-bootstrap test must fail on the forbidden artifact set. | Forbidden runtime artifacts in database and wiki paths. |
| Sentinel cleanup | A normal probe returns to the exact pre-run directory snapshot. | Inject remove failure after successful comparison; exit becomes nonzero and the residual sentinel path is reported. | Exit status, reported cleanup path, and directory snapshot. |
| LaunchAgent ceremony | The real harness runs against a stateful fake environment with one binary identity and label, produces two markers, and ends with bootout. | Stateful fake commands change the second digest, fail bootout, or remove a required failure artifact during execution. Ephemeral harness-copy mutations remove the second kickstart or change its label. The unchanged shell test must fail each case. | Harness exit status and job/filesystem state are primary; transcript fields are secondary evidence. |
| Documentation claims | An independent reviewer confirms that committed help and README make none of three prohibited propositions. | Three fixtures paraphrase, respectively, TCC-persistence proof, complete-ingest proof, and unconfigured-path proof; the reviewer rejects each proposition. | Proposition-level reviewer verdict; exact-phrase inventory is only a narrow regression guard. |

The `/Volumes` scan, exact-phrase inventory, and recorded calls are supporting
evidence. Real filesystem outcomes, harness state, and independent proposition review
are the primary evidence.

## U1: Configured filesystem preflight command

Execution note: test-first
Files:
  Create: `cmd/cogvault/access_check.go`, `cmd/cogvault/access_check_test.go`
  Modify: `cmd/cogvault/main.go`
  Test: `cmd/cogvault/access_check_test.go`
Interfaces:
  Consumes: `resolveConfigPath(*cobra.Command)`, `config.Load(string)`, config path fields, `unix.Open`, `os.NewFile`, `os.SameFile`
  Produces: `newAccessCheckCmd() *cobra.Command`, `runAccessCheck(*cobra.Command, []string) error`, private `accessCheckOps`, stdout lines `passed: <surface>: <path>`, final marker `configured ingest access check passed`
Test scenarios:
  happy: temporary wiki and database-parent probes succeed; two real configured roots contain distinct accepted and rejected files; a read failure attached only to a rejected file has no effect; sentinels are absent afterward
  happy: `TestAccessCheckDoesNotBootstrap` starts without a database, WAL, SHM, or `_schema.md` and proves none exists after success
  edge: a newly created settle-window file and an empty accepted file are opened; EOF succeeds; directory, symlink, wrong extension, oversized file, and non-regular entry receive no content read
  edge: an injected path replacement makes opened-file `Stat` differ from the prior `Lstat`; the command rejects it before reading content
  edge: replacement with a FIFO returns without blocking because source open uses nonblocking and no-follow flags
  error: a non-directory surface fails with its surface and path; injected readback mismatch still cleans up; cleanup-only failure returns nonzero; primary plus remove failure preserves both operation names
  error: table-driven `ReadDir`, `Lstat`, open, and read permission failures name the source surface, exact path, and failed operation
  error: a sentinel read failure attached to an accepted file returns nonzero and names that exact source, path, and read operation, proving the real traversal reached content read
  integration: `TestAccessCheckConfiguredSurfaces` invokes the Cobra command with a temporary config and verifies all surface lines and the final marker. Covers S1
Steps:
  1. Add failing command tests with real directories and files for two-root behavior, config-only behavior, settle-window access, success output, positional-argument rejection, cleanup, filters, descriptor identity, exact readback, permission diagnostics, combined errors, and the U1 discrimination fixtures. Use injection only for one otherwise unreachable failure per case.
  2. Run `go test ./cmd/cogvault -run TestAccessCheck`; confirm failures show that `access-check` is unregistered.
  3. Implement the private operation bundle, write probe, source probe, command handler, and root registration. Open sources with nonblocking, no-follow, close-on-exec flags. Compare pre-open `Lstat` with descriptor `Stat` through `os.SameFile` before reading. Keep config loading separate from `bootstrap`.
  4. Run `gofmt` and the targeted tests. In a disposable worktree, introduce only the bootstrap-calling handler mutation and prove `TestAccessCheckDoesNotBootstrap` fails on forbidden artifacts. Remove the worktree. Run the static mount guard as supporting evidence, then `go test -race ./...`.
  5. Commit: `feat(cli): verify configured ingest filesystem access`
Acceptance: the targeted tests pass; every U1 invariance fixture compares equal; every changed-axis or guard-failure fixture is rejected for its named signal; the supporting static guard and full race suite pass.

## U2: Temporary LaunchAgent verification ceremony

Execution note: test-first
Files:
  Create: `scripts/check-scheduled-access.sh`, `scripts/check-scheduled-access_test.sh`
  Modify: none
  Test: `scripts/check-scheduled-access_test.sh`
Interfaces:
  Consumes: Darwin platform and GUI-domain preflight; `launchctl bootstrap|kickstart -p|bootout`, `plutil`, `codesign`, `shasum`, `stat`, `id -u`, `mktemp -d`, `COGVAULT_BIN`, `COGVAULT_CONFIG`
  Produces: one unique `com.teslamint.cogvault.access-check.<random>` job, two direct `access-check` invocations, 120-second per-run bound, private failure logs, exact recovery commands, manual no-second-dialog instruction
Test scenarios:
  happy: fake launchctl records one bootstrap, two PID-returning kickstarts for one label, marker verification after each PID exits, bootout, and private-directory deletion
  edge: binary and config paths with spaces and XML metacharacters remain exact plist values; temp directory mode is `0700` and plist and log modes are `0600`
  error: invalid signature or a changed binary identity blocks kickstart; missing marker and zero-timeout cases return nonzero, print private log paths, boot out the job, and retain artifacts
  error: fake bootout failure retains the plist and logs, reports cleanup failure, and prints exact bootout and deletion recovery commands
  error: non-macOS, missing GUI domain, non-executable binary, or unreadable config fails before any bootstrap call
  error: INT and TERM after bootstrap invoke ownership-scoped bootout and preserve the private plist and logs; bootout failure adds recovery commands
  integration: the script drives the fake command suite end to end for two runs with one unchanged binary and label. Covers S2 and S3
Steps:
  1. Add a failing shell test that always executes the real harness against stateful fake platform, GUI-domain, launchctl, codesign, and CogVault commands. Cover preflight rejection, signals, binary replacement, private modes, PID completion, bootout failure, recovery commands, and artifact loss.
  2. Run `bash scripts/check-scheduled-access_test.sh`; confirm it fails because the harness does not exist.
  3. Implement `umask 077`, a verified private temp directory, plist creation through `plutil`, direct binary arguments, binary identity sealing, `kickstart -p` PID polling, marker checks, and ownership-scoped cleanup.
  4. Run the syntax and shell tests. In isolated copies of the harness, remove only the second kickstart and then change only its label. Confirm the unchanged shell test fails each mutant. Confirm stateful fake digest, bootout, and artifact-loss injections fail through harness exit and filesystem/job state before checking transcripts.
  5. Commit: `feat(launchd): verify scheduled configured-path access`
Acceptance: the end-to-end fake ceremony and syntax checks pass; every one-axis fake injection or harness-copy mutation makes the unchanged shell test fail on harness exit or job/filesystem state; transcripts only explain that failure.

## U3: Canonical contract and operator guidance

Execution note: characterization-first
Files:
  Create: none
  Modify: `SPEC.md`, `DESIGN.md`, `README.md`
  Test: `cmd/cogvault/access_check_test.go`, `scripts/check-scheduled-access_test.sh`
Interfaces:
  Consumes: the U1 CLI output contract and the U2 environment-variable and cleanup contracts
  Produces: SPEC CLI section and config-only exception, DESIGN command boundary, README deviation-authorized ceremony and diagnostic decision tree
Test scenarios:
  happy: a reader can run the installed binary through the temporary job twice and identify the success marker
  edge: guidance states that configured network paths are checked without mount discovery and that iCloud File Provider can remain on local storage
  error: guidance does not claim TCC database persistence, complete ingest coverage, or access to unconfigured Documents, Pictures, Photos Library, or network shares
  integration: a reviewer traces S1 through the CLI section, S2 through the ceremony, and S3 through retained-log diagnostics. Covers S1, S2, and S3
Steps:
  1. Add the `access-check` contract and config-only exception to `SPEC.md`. Add the matching ownership and bootstrap notes to `DESIGN.md`.
  2. Add the build/install prerequisite, script command, two-run interpretation, and failure decision tree to the existing README launchd section.
  3. Use an independent bounded-claim review for three propositions: no TCC-persistence proof, no complete-ingest proof, and no unconfigured-path proof. Require rejection of one paraphrased negative fixture per proposition. Keep an exact forbidden-phrase inventory only as a narrow static guard.
  4. Run the targeted Go test, shell test, `go test -race ./...`, and `git diff --check`.
  5. Commit: `docs(access): document scheduled path verification`
Acceptance: all commands pass and the documentation reviewer confirms the bounded result claims for all three scenarios.

## U4: Real signed-binary launchd verification

Execution note: skip-test-first
Files:
  Create: `.release-loop/runs/scheduled-access-check/evidence/U4/real-ceremony.txt` as ignored disposable evidence
  Modify: none
  Test: `scripts/check-scheduled-access.sh`
Interfaces:
  Consumes: signed installed binary, active config, GUI launchd domain, U2 harness
  Produces: sanitized evidence for code-identity shape, two marker-bearing runs, one label, prompt observation, and cleanup
Test scenarios:
  happy: the installed Apple Development-signed binary completes two runs; the second run raises no prompt; the temporary job and private directory are absent
  edge: a first-run configured-path prompt is allowed by the operator and the second run completes within the bound
  error: any unexpected Documents, Pictures, network-volume, or AppData prompt is recorded by category and is not treated as configured-path proof
  integration: the GUI-domain ceremony completes S2 and preserves S3 diagnostics on failure. Covers S2 and S3
Steps:
  1. Build and install the feature binary with the current stable Apple Development identity and identifier `dev.tmint.cogvault`. Verify its designated requirement before execution.
  2. Run `scripts/check-scheduled-access.sh` against the active config. Publish a sanitized transcript with marker, label-consistency, prompt-category, binary-identity, and cleanup evidence.
  3. Set `evidence_path=.release-loop/runs/scheduled-access-check/evidence/U4/real-ceremony.txt`. Set `job_label` from its exact `label=` line. Verify `launchctl print "gui/$(id -u)/$job_label"` exits nonzero after cleanup. Verify the harness reports no retained directory on success.
  4. If a prompt appears only during a later full ingest, record it as an unresolved full-runtime candidate. Do not broaden this ceremony or approve an unrelated folder.
  5. Commit: no commit; this unit produces ignored machine-local verification evidence.
Acceptance: the U4 transcript proves two real runs with one unchanged code identity and no temporary job afterward, or records the exact failed boundary without claiming persistence.

## Mutation/failure-state matrix

| Transition | Pre-state | Action | Expected post-state | Outcomes | Unit and evidence owner |
|---|---|---|---|---|---|
| Sentinel lifecycle | no sentinel at one configured write surface | exclusively create, write, close, read, compare, remove | sentinel absent | Success: remove succeeds after comparison. Forced failure: injected write, close, read, or compare failure triggers remove and records the exact partial state. Rerun: unique naming avoids collision with crash residue. Compensation: remove follows every successful create; cleanup-only failure returns nonzero with path. Headless: config or surface validation fails before create. Cancellation: abrupt death can leave an identifiable hidden sentinel for manual removal. | U1; `.release-loop/runs/scheduled-access-check/evidence/U1/` stores targeted output. |
| Temporary LaunchAgent lifecycle | private temp directory absent; unique job absent | create private assets, seal binary identity, bootstrap, run twice, boot out | job absent; private directory removed on success or retained on failure | Success: two marker-bearing PID-bounded runs, then cleanup. Forced failure: fake launchctl fails after bootstrap; trap boots out and retains `0600` artifacts. Rerun: a new random label and directory start absent. Compensation: successful bootout precedes directory removal; bootout failure retains evidence and prints recovery commands. Headless: no GUI domain, invalid binary, or invalid signature fails before bootstrap. Cancellation: INT or TERM runs ownership-scoped cleanup and reports cleanup failure. | U2 and U4; U2 stores fake transcripts and U4 stores the real transcript. |

The matrix follows the worked example in the planning skill. Any observable change to
this lifecycle after plan approval requires the repository's deviation-addendum flow.

## Carry-forward trigger audit

Audited `docs/research/v2-follow-ups.md` at `27c114f59f8f7bcd6755919d12759d123cbb5749`: 0 open rows, 0 fired, 0 unobservable.

## Deferred to Follow-Up Work

- Harden ingest against accepted-extension FIFO, socket, and device entries. The
  current scanner can open a non-regular entry. Changing that behavior needs a
  separate regression specification and is not required for this safe preflight.
- Isolate prompts that appear only during full ingest. Candidate paths include the
  database sidecars, wiki contents, Git subprocess, LLM subprocess, and notification
  subprocess. The configured-path preflight supplies the first diagnostic split.

## Open unknowns

### Planning-time

None.

### Implementation-time

- A disconnected configured network share can fail immediately or reach the
  120-second bound. Both results use the same failure cleanup contract.
