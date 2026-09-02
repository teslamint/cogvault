---
schema: plan/v1
title: "Stable code identity for scheduled ingest"
type: feat
status: done
body_seal: 7a262f9c748c876584ac4a80733dfd97d34ff320a6991b19dd50821a28af0e23
date: 2026-08-27
completed_by: 5f64f49ead9e3da4c6a24692f57b61e1819586a6
execution: code
origin: docs/specs/2026-08-27-ingest-tcc-prompts-design.md
---

## Goal

Stop the macOS TCC consent prompts that reappear on every scheduled ingest run
after a rebuild, and make a denied source directory visible in the ingest
report instead of silently indistinguishable from an ordinary skip.

The prompts recur because the installed binary carries an ad-hoc signature
whose designated requirement is a bare `cdhash`. A rebuild that changes that
hash stops matching the prior requirement and macOS asks again.
Signing with a stable identity under a fixed identifier makes the designated
requirement certificate-based with no `cdhash` term, so a rebuilt binary
satisfies the requirement recorded for its predecessor.

Two independent surfaces deliver this: build identity in the `Makefile` plus
the operator documentation around it, and denial visibility in
`internal/ingest`. Neither depends on the other.

## Architecture notes

No package boundary changes. No new package, no new exported symbol in
`internal/ingest`.

**Build identity.** `make build` and `make install` gain two variables:
`CODESIGN_IDENTITY` (default `-`, the current ad-hoc behavior) and
`CODESIGN_IDENTIFIER` (default `dev.tmint.cogvault`). Both are declared with
`?=`, not `=`, so an exported environment variable reaches the recipe. A
command-line assignment overrides either form, but `=` would silently discard
an exported `CODESIGN_IDENTITY` and build ad-hoc — the exact failure this plan
exists to remove. Both signing invocations — the build artifact and the install
destination — use the same pair, because TCC matches the code at the granted
path, not the build path. The default keeps a contributor without any
certificate building successfully: cogvault is a public repository and a
certificate must never be a build prerequisite.

**Denial visibility.** `internal/ingest` reports a permission-denied source
read with a fixed diagnostic instead of the raw OS string. Three sites emit it:
`scan`'s `os.ReadDir` on a source directory (`ingest.go:239`), `scan`'s
`hashFile` on a source file (`ingest.go:277`), and `reportSweepSourceError`
(`ingest.go:508`), which the orphan sweep reaches from two call sites. All
three route their error text through one unexported helper in the package.
`scan`'s `os.Lstat` failure (`ingest.go:252`, `skipped` with prefix `stat: `)
is deliberately left alone — it is not a read of source content.

The macOS half of the diagnostic is gated on `runtime.GOOS == "darwin"` at
runtime rather than a build tag, because `errors.Is(err, fs.ErrPermission)`
also matches Linux `EACCES` and one shared helper cannot be split by file
without duplicating it. The package's existing `notify_darwin.go` build-tag
split stays untouched.

Counters and actions do not change, so `Report.SumCheck` is untouched and
`reportSweepSourceError` keeps passing the original `err` to `slog.Warn` — the
fixed string is a report-line concern, not a log concern.

**Transition-family declaration.** This plan declares neither release-loop
transition family. There is no generation to produce and no outward
publication: `final_action` is `merge-to-base`, local. The classification is
recorded here so a reviewer reads a decision rather than an omission; see the
Mutation/failure-state matrix section for the ceremony classification and its
reasoning.

**No personal identifier in any committed file.** cogvault is a public
repository. No file this plan creates or modifies may contain a team ID, a
certificate hash, an email address, or a machine-specific absolute path. Every
identity example uses the placeholder form
`Developer ID Application: <your name> (<team>)`. Verification steps check this
by shape, never by naming the concrete value they are searching for.

**Known Pattern** (`docs/solutions/build-errors/macos-sigkill-rebuilt-go-binary.md`):
the repository already documents ad-hoc re-signing as the remedy for the
adjacent SIGKILL failure. That document's Prevention section is now wrong for
the recurring-prompt case — "expect to re-sign after every rebuild" is exactly
the cost this plan removes — and U4 corrects it.

## Assumption Recheck

Origin spec: `docs/specs/2026-08-27-ingest-tcc-prompts-design.md`, ten retained
live assumptions. Every retained command was rerun at 2026-08-27T10:00:00Z.
All ten outcomes are **match**.

| # | Claim | Outcome | Note |
|---|---|---|---|
| 1 | TCC grants for `~/bin/cogvault` are pinned to a `cdhash` requirement | match | Fresh query returns `40\|9`: 9 rows, all carrying a 40-byte requirement. The spec recorded 8 of 9; the fresh count supports the pinning claim more strongly, not less. |
| 2 | Ad-hoc signing yields a designated requirement that is `cdhash` alone | match | Re-measured directly on a throwaway `/tmp` build. |
| 3 | A rebuild fails the previous ad-hoc requirement (`codesign --verify -R=`, exit 3) | match | Re-measured on throwaway builds. |
| 4 | Developer ID signing with `--identifier` yields an identifier + anchor + certificate requirement with no `cdhash` term | match | Re-measured on throwaway builds. |
| 5 | A rebuild under that stable identity satisfies the previous build's requirement | match | `explicit requirement satisfied`. |
| 6 | The installed `~/bin/cogvault` is `flags=0x20002(adhoc,linker-signed)`, `Identifier=a.out` | match | Still `Identifier=a.out`, `Signature=adhoc`, `CDHash` prefix `3468847a`. Not modified. |
| 7 | 2 valid codesigning identities exist, 1 of them Developer ID | match | Unchanged. |
| 8 | `modernc.org/sqlite v1.48.1` — pure Go, no cgo | match | Unchanged. |
| 9 | A denied source read is currently indistinguishable from an ordinary skip; the sweep emits `err.Error()` bare | match | All three `internal/ingest` code-site greps unchanged. |
| 10 | `go test -race ./...` green | match | 13 ok + 1 no-test-files = the same 14 packages the baseline counted, 0 failures. |

All six throwaway `/tmp` builds used for rows 2–5 were deleted afterward.
`~/bin/cogvault` was not touched.

**One contradiction was surfaced and discharged before this plan.** The spec's
environment-invariant bullet recorded `dev.tmint.cogvault` as the running
launchd job where `launchctl list` reports `com.teslamint.cogvault`.
`git diff 6ca1d51 7052f49` proves the Open Decision 3 identifier substitution
at the approval commit hit that recorded observation as collateral damage; the
user-reviewed text at `6ca1d51` was correct, and the gate answer itself stated
that the launchd job labels keep their `com.teslamint.cogvault` prefix as a
separate namespace. Restoring the line is compliance with the approval, not a
deviation from it, so no `docs/deviations/` addendum applies — that authority
is scoped to observable-behavior changes and its required "why documentation
alone cannot fix it" item cannot be answered truthfully here. Fixed in
`4c5c6235fe2b1e923679c80e029d0c2254b53cb6`; `git show --stat 7052f49` confirms
the blast radius was the spec file alone.

## File structure

### New files

None. The permission helper is roughly ten lines with one caller pattern; a
dedicated file would fragment `internal/ingest` against its existing
convention of keeping scan-path helpers (`hashFile`, `snapshotDir`) in
`ingest.go`.

### Modified files

| File | Change | Unit |
|---|---|---|
| `internal/ingest/ingest.go` | add `io/fs` and `runtime` imports; add unexported `sourceErrorText`; route three error-text sites through it | U1 |
| `internal/ingest/ingest_test.go` | add `TestRunSourcePermissionDenied` | U1 |
| `Makefile` | add `CODESIGN_IDENTITY` / `CODESIGN_IDENTIFIER`, apply both to build artifact and install destination, echo the identity used | U2 |
| `README.md` | build section: signing variables, the one-time switching cost, and the plain-`go build` caveat; `### 5. Schedule zero-touch ingest (launchd)`: grant ceremony, what each grant covers, stale-grant cleanup, `serve`-job restart note | U3 |
| `docs/solutions/build-errors/macos-sigkill-rebuilt-go-binary.md` | correct the Prevention section; explain certificate rotation and future-signing requirements | U4 |
| `SPEC.md` | §10.4 Report: document the permission-denied line class | U5 |
| `DESIGN.md` | line 664: update the `Makefile` row | U5 |

The README heading `Schedule zero-touch ingest` must survive verbatim: the
runtime diagnostic in U1 quotes it.

## Scenario coverage map

Only S5 is machine-testable. The origin spec states directly that signing
behavior cannot be asserted automatically — a test cannot install to `~/bin`,
answer a GUI consent dialog, or read the SIP-protected system TCC database.
S1–S4 are therefore bound to their measured-by procedure from the spec's
Success Criteria, which is the observable evidence those rows walk.

| S-ID | Unit chain | Evidence |
|---|---|---|
| S1 — maintainer rebuilds a signed binary and the schedule stays silent | U2 → U6 | SC2's five-step sequence: no prompt appears, and the `last_modified >= $REBUILD_EPOCH` row count is 0 |
| S2 — contributor with no certificate still builds | U2 | SC3: `make clean && make build CODESIGN_IDENTITY=- && ./cogvault --help` exits 0 |
| S3 — first-time operator performs the one-time grant | U3 → U6 | SC1's Allow observation and AppData-row query, driven by the README ceremony U3 writes |
| S4 — operator removes grants left by earlier binaries | U3 | SC6 reviewer rubric against the README cleanup subsection |
| S5 — a denied source directory is visible in the ingest report | U1 | `TestRunSourcePermissionDenied` (Covers S5) |

## U1: Permission-denied classification in ingest

Execution note: test-first

Files:
  Modify: `internal/ingest/ingest.go`
  Test: `internal/ingest/ingest_test.go`

Interfaces:
  Consumes: `errors.Is`, `io/fs.ErrPermission`, `runtime.GOOS`
  Produces:
    - unexported: `func sourceErrorText(err error) string`
  Report line text (platform-independent): `permission denied: cannot read source`
  Report line text (darwin): `permission denied: cannot read source; macOS consent required, see README "Schedule zero-touch ingest"`

Test scenarios:
  happy: n/a — this unit adds no success path
  edge: a source directory that was readable on a prior run and is chmod `0` on the next run produces two `source-error` lines for that path (the sweep runs before the scan, and `Run` sweeps first) and `source-errors=2`
  edge: a source directory with no ledger rows produces one `source-error` line and `source-errors=1`
  error: a single source file chmod `0` produces a `skipped` line whose error keeps the `read: ` prefix ahead of the fixed diagnostic
  error: a non-permission read failure keeps `err.Error()` unchanged — `TestRunSourceDirReadError` must keep passing with no edit
  integration: `TestRunSourcePermissionDenied` drives `Runner.Run` end to end through the existing harness (Covers S5)

Steps:
  1. Write failing test `ingest_test.go::TestRunSourcePermissionDenied`, a
     table test shaped after `TestRunSourceDirReadError`
     (`internal/ingest/ingest_test.go:1123`) and driving `Runner.Run` through
     the existing `newHarness` — `newHarness` already places `wikiDir` and
     `dbPath` outside `srcDir`, so chmodding `srcDir` does not break the wiki
     or the ledger.
     - Case `dir`: run once normally so the ledger holds a row for a file in
       that directory, then `os.Chmod(dir, 0)` and run again. Assert exactly
       two `source-error` lines for that path and `report.SourceErrors == 2`,
       and assert over **every** `source-error` line rather than the first.
     - Case `dir-unswept`: on a fresh harness, `os.Chmod(dir, 0)` before any
       run and run once. Assert exactly **one** `source-error` line for that
       path and `report.SourceErrors == 1`. This is the discriminating case
       for the two-line claim in case `dir`: `sweepOrphans` groups ledger rows
       by directory and skips a directory whose group is empty
       (`ingest.go:436-438`, `if len(dirRows) == 0 { continue }`), so with no
       ledger row only `scan` reports. Without this case the test suite cannot
       distinguish "the sweep runs first" from "every denial reports twice".
     - Case `file`: `os.Chmod(file, 0)` and run once. Assert one `skipped`
       line for that path whose error starts with `read: `.
     - Every case: register `t.Cleanup(func() { os.Chmod(path, 0o755) })`
       after the `t.TempDir` call that created the path. Go runs cleanups in
       LIFO order, so the later-registered chmod restore executes before the
       temp-dir removal and the removal succeeds.
     - Every case: `if os.Geteuid() == 0 { t.Skip("root bypasses mode bits") }`.
     - Every case asserts the substring `permission denied: cannot read source`
       unconditionally, and asserts the `; macOS consent required` suffix only
       inside `if runtime.GOOS == "darwin"`.
  2. Run `go test ./internal/ingest -run TestRunSourcePermissionDenied`;
     confirm it fails because the report still carries the raw OS string.
  3. Add `io/fs` and `runtime` to the import block of `ingest.go`. Both are
     absent today; `errors` is already imported at line 6.
  4. Add the helper to `ingest.go`, next to `hashFile`:
     ```go
     func sourceErrorText(err error) string {
         if !errors.Is(err, fs.ErrPermission) {
             return err.Error()
         }
         msg := "permission denied: cannot read source"
         if runtime.GOOS == "darwin" {
             msg += `; macOS consent required, see README "Schedule zero-touch ingest"`
         }
         return msg
     }
     ```
  5. Route the three sites through it, changing only the `Error:` value:
     - `ingest.go:242` (`scan`, after `os.ReadDir` at line 239):
       `Error: err.Error()` becomes `Error: sourceErrorText(err)`.
     - `ingest.go:280` (`scan`, after `hashFile` at line 277):
       `Error: "read: " + err.Error()` becomes
       `Error: "read: " + sourceErrorText(err)`.
     - `ingest.go:514` (`reportSweepSourceError`, whose signature is at line
       508; line 513 is the `Action:` line and stays unchanged):
       `Error: err.Error()` becomes `Error: sourceErrorText(err)`.
     Leave `ingest.go:252` (the `os.Lstat` `stat: ` line) unchanged. Leave the
     `slog.Warn` call in `reportSweepSourceError` unchanged — it keeps
     receiving the original `err`.
  6. Run `go test ./internal/ingest -run 'TestRunSourcePermissionDenied|TestRunSourceDirReadError'`;
     confirm both pass.
  7. Run `go test -race ./...`; confirm 0 failures across the same 14 packages
     the baseline counted.
  8. Commit: `feat(ingest): report permission-denied source reads distinctly`

Acceptance: `go test ./internal/ingest -run 'TestRunSourcePermissionDenied|TestRunSourceDirReadError'` passes, and `go test -race ./...` reports 0 failures.

## U2: Makefile signing variables

Execution note: skip-test-first

Files:
  Modify: `Makefile`

Interfaces:
  Consumes: `codesign(1)`
  Produces:
    - `CODESIGN_IDENTITY` — default `-` (ad-hoc, current behavior)
    - `CODESIGN_IDENTIFIER` — default `dev.tmint.cogvault`
    - one echoed line per build naming the identity and identifier used

Test scenarios:
  happy: `make build` with no override signs ad-hoc and exits 0
  edge: an exported `CODESIGN_IDENTITY` in the environment reaches the recipe, because both variables use `?=`
  edge: a command-line assignment `make build CODESIGN_IDENTITY=-` overrides both the default and any exported value
  error: n/a — a bad identity fails in `codesign(1)`, which already exits non-zero and fails the target; this unit adds no error path of its own
  integration: n/a — build-system unit, exercised by the S2 command in the Scenario coverage map rather than by a Go test

Steps:
  1. Add both variables after `INSTALL_DIR`, using `?=` so an exported
     environment variable reaches the recipe:
     ```makefile
     CODESIGN_IDENTITY   ?= -
     CODESIGN_IDENTIFIER ?= dev.tmint.cogvault
     ```
     A command-line assignment (`make build CODESIGN_IDENTITY=-`) overrides
     either form. `=` would additionally discard an exported value and build
     ad-hoc without saying so, which is the failure mode this plan removes.
  2. In `build`, replace `codesign --force --sign - $(BINARY)` with an echo
     line followed by the parameterized invocation:
     ```makefile
     <TAB>@echo "codesign: identity=$(CODESIGN_IDENTITY) identifier=$(CODESIGN_IDENTIFIER)"
     <TAB>codesign --force --sign "$(CODESIGN_IDENTITY)" --identifier "$(CODESIGN_IDENTIFIER)" $(BINARY)
     ```
  3. In `install`, replace `codesign --force --sign - $(INSTALL_DIR)/$(BINARY)`
     with the same quoted flags applied to `$(INSTALL_DIR)/$(BINARY)`. Both artifacts
     must carry the same identity and identifier: TCC matches the code at the
     granted path, not the build path.
  4. Verify the contributor default:
     `make clean && make build CODESIGN_IDENTITY=- && ./cogvault --help`;
     confirm exit 0 and one echoed identity line.
  5. Verify the identifier actually lands:
     `codesign -dv ./cogvault 2>&1 | grep '^Identifier='`; confirm
     `Identifier=dev.tmint.cogvault` rather than the Go linker's default
     `Identifier=a.out`. This is the changed-axis check for step 4's default —
     the ad-hoc identity is unchanged, but the identifier is not.
  6. Verify the `?=` behavior that step 1 exists for:
     `CODESIGN_IDENTIFIER=probe.invariance make build` then
     `codesign -dv ./cogvault 2>&1 | grep '^Identifier='` must report
     `Identifier=probe.invariance`. Under `=` it would report
     `Identifier=dev.tmint.cogvault`, so this distinguishes the two forms.
     Re-run plain `make build` afterwards to restore the default identifier.
  7. Commit: `build: parameterize codesign identity and identifier`

Acceptance: `make clean && make build CODESIGN_IDENTITY=- && ./cogvault --help` exits 0; `codesign -dv ./cogvault` reports `Identifier=dev.tmint.cogvault`; and an exported `CODESIGN_IDENTIFIER` changes that reported identifier.

## U3: README build and launchd grant documentation

Execution note: skip-test-first

Files:
  Modify: `README.md`

Interfaces:
  Consumes: U2's variable names and defaults
  Produces: build-section signing paragraph, one-time switching cost, and `go build` caveat; launchd-section grant ceremony, per-grant coverage statement, stale-grant cleanup, and `serve`-job restart note

Test scenarios:
  happy: n/a — documentation unit with no executable success path
  edge: n/a — documentation unit
  error: n/a — documentation unit
  integration: the SC6 reviewer rubric in step 8 walks S3 and S4 against the written text; the commands the section prints are the ones U6 hands to the maintainer

Steps:
  1. In `### 1. Build`, replace the existing bash block. The first line's
     comment currently reads `# build + adhoc codesign (macOS FDA safe)`,
     which is now misleading — an ad-hoc signature is exactly what loses the
     grant on rebuild. Write:
     ```bash
     make build                         # build + adhoc codesign (default, no certificate needed)
     make install CODESIGN_IDENTITY="Developer ID Application: <your name> (<team>)"
     ```
     State in one sentence that the default `-` identity produces an ad-hoc
     signature whose TCC grants die on every rebuild, and that a stable
     identity is what makes grants survive. Use the placeholder form above;
     never write a concrete team identifier or certificate name.
  2. Add the one-time switching cost next to those commands, which the spec's
     Risks table requires the README to carry: changing the code-signing
     identifier resets the binary's TCC identity once, so the first signed
     install costs one final round of prompts. Recurrence stops after that
     round, not before it.
  3. Correct the existing "Or manually (without codesign …)" caveat: a plain
     `go build -o cogvault ./cmd/cogvault` silently restores the linker's
     ad-hoc signature and `Identifier=a.out`, discarding a stable identity
     already applied. Say that re-running `make build` or `make install` is
     what restores it.
  4. In `### 5. Schedule zero-touch ingest (launchd)`, keep the heading text
     exactly as it is — U1's runtime diagnostic quotes it — and replace the
     two-bullet "One-time grants the scheduled binary needs:" list with a
     numbered ceremony:
     1. `make install CODESIGN_IDENTITY="Developer ID Application: <your name> (<team>)"`
     2. `cp deploy/com.teslamint.cogvault.ingest.plist ~/Library/LaunchAgents/`
        and `launchctl load ~/Library/LaunchAgents/com.teslamint.cogvault.ingest.plist`
     3. `launchctl kickstart -k gui/$(id -u)/com.teslamint.cogvault.ingest`
     4. Answer each consent prompt once.
     State why step 3 must be a `kickstart` and not a manual
     `cogvault ingest` in a terminal: macOS attributes a terminal-spawned
     process to the terminal, so a manual run grants the terminal, not
     cogvault, and the scheduled job keeps prompting.
  5. Directly under the ceremony, state what each grant covers: a folder
     prompt covers reads of that one source directory, and the "data from
     other apps" prompt creates an AppData service row for cogvault. The exact
     triggering access remains unmeasured. Write the observed scope plainly. Mark the
     Full Disk Access question as unresolved — whether one Full Disk Access
     grant supersedes the individual prompts is Open Decision 2. U6 records the
     result when observed. U6 step 6 reconciles this paragraph
     with what the ceremony observes; do not assert a supersedes relationship
     before that.
  6. Keep the existing `claude` non-interactive auth bullet and the harmless
     `node: command not found` paragraph as they are.
  7. Add a "Removing stale grants" subsection: the same binary path can
     retain TCC grant rows bound to earlier code requirements. Give the
     inspection command and warn that `tccutil reset <service>` clears that
     service for **every** application on the machine, not only cogvault, so
     the System Settings per-application view is the narrower instrument.
  8. Add a note that `~/bin/cogvault` also backs the `com.teslamint.cogvault`
     `serve` job. A running `serve` process keeps executing the pre-install
     image until it is restarted, so restart that job after an install if the
     new identity is meant to apply to it too.
  9. Self-review against the SC6 rubric: every command copy-pasteable, no
     machine-specific absolute path, no personal identifier, each grant's
     coverage stated, the `go build` caveat present, and the `tccutil` blast
     radius stated.
  10. Commit: `docs(readme): document stable signing identity and the launchd grant ceremony`

Acceptance: `rg 'Schedule zero-touch ingest' README.md` still matches the heading; the launchd section contains a `launchctl kickstart` step and a `tccutil` blast-radius warning; `rg -n '\([A-Z0-9]{10}\)' README.md` returns nothing (the placeholder form `(<team>)` does not match this shape, a real Apple team identifier does); and `rg -n '[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}' README.md` returns no address belonging to a person.

## U4: Correct the solutions-doc prevention guidance

Execution note: skip-test-first

Files:
  Modify: `docs/solutions/build-errors/macos-sigkill-rebuilt-go-binary.md`

Interfaces:
  Consumes: U2's variable names
  Produces: a Prevention section that no longer prescribes ad-hoc re-signing as the steady state, plus certificate rotation and future-signing requirements

Test scenarios:
  happy: n/a — documentation unit with no executable success path
  edge: n/a — documentation unit
  error: n/a — documentation unit
  integration: n/a — leaf documentation unit; the step 6 acceptance greps are its only mechanical check

Steps:
  1. Rewrite the Prevention section. The first three bullets stay true in
     substance — sign after `go build`, re-sign at the install destination,
     keep the step in the Makefile target — but restate them against
     `CODESIGN_IDENTITY` / `CODESIGN_IDENTIFIER` rather than a hard-coded `-`.
  2. Replace the last bullet, "When granting FDA to a Go binary, expect to
     re-sign after every rebuild." Re-signing is still required after every
     rebuild; what is no longer required is re-granting. Say that an ad-hoc
     identity produces a `cdhash`-only designated requirement, so a rebuild
     that changes the `cdhash` invalidates matching grants. Say that a stable identity under a
     fixed identifier produces a certificate-based requirement with no `cdhash`
     term, so grants survive rebuilds. Link `README.md`'s
     `Schedule zero-touch ingest` section for the ceremony.
  3. Add a "Certificate rotation" paragraph, which the spec's Risks table
     assigns to this document: securely timestamped Developer ID code signed
     while the certificate is valid can remain valid after that certificate
     expires. A valid identity is still required for future rebuilds. Moving to
     a new identity changes the code requirement, so recovery is the one-time
     ceremony — install under the new identity, kickstart the launchd job, and
     answer the prompts once — followed by the README's stale-grant inspection
     procedure. State that the local effect of certificate revocation on TCC
     matching is unverified. Name no certificate, no team identifier, and no
     expiry date.
  4. Update the embedded Makefile snippet in the Solution section so it matches
     the Makefile U2 actually produces, including the `?=` assignments; a stale
     snippet is what a reader will copy.
  5. Leave the frontmatter `root_cause` unchanged: it describes the SIGKILL
     failure this document was written for, which is still accurate.
  6. Commit: `docs(solutions): correct the codesign prevention guidance for stable identities`

Acceptance: `rg 'expect to re-sign after every rebuild' docs/solutions/build-errors/macos-sigkill-rebuilt-go-binary.md` returns nothing; the file references `CODESIGN_IDENTITY`; and the Certificate rotation paragraph names future rebuilds and the unverified local TCC effect of revocation.

## U5: Canon updates

Execution note: skip-test-first

Files:
  Modify: `SPEC.md`, `DESIGN.md`

Interfaces:
  Consumes: U1's fixed diagnostic strings, U2's Makefile variables
  Produces: SPEC.md §10.4 permission-denied line class; DESIGN.md's `Makefile` row

Test scenarios:
  happy: n/a — documentation unit with no executable success path
  edge: n/a — documentation unit
  error: n/a — documentation unit
  integration: step 4 diffs the written strings against the strings U1 emits, which is the only cross-unit consistency check this unit needs

Steps:
  1. In `SPEC.md` §10.4 Report, next to the existing sentence "Files whose
     `Lstat` fails appear as `skipped` with error `"stat: <err>"`."
     (`SPEC.md:1001`), add the new class: a source directory or file whose read
     fails with a permission error reports
     `permission denied: cannot read source`, and on macOS that text carries
     the suffix `; macOS consent required, see README "Schedule zero-touch ingest"`.
     State that a source directory emits this as `source-error` (once from the
     orphan sweep and once from the scan, because `Run` sweeps before it
     scans) and a source file emits it as `skipped` behind the `read: ` prefix.
  2. Confirm the Sum invariant sentence needs no edit: `source-errors` is
     already excluded from it and no counter or action changed.
  3. In `DESIGN.md:664`, replace the `Makefile` row's
     "build/install (with adhoc codesign at destination)" with a description
     naming `CODESIGN_IDENTITY` (default `-`, ad-hoc) and `CODESIGN_IDENTIFIER`
     (default `dev.tmint.cogvault`), applied at both the build artifact and the
     install destination. `DESIGN.md:664` is the only `Makefile` mention in
     either canon file — `SPEC.md` mentions the Makefile nowhere, and this plan
     adds no such mention.
  4. Self-review the §10.4 wording against the strings U1 actually emits,
     character for character including the inner double quotes around the
     README heading.
  5. Commit: `docs(spec): document the permission-denied source report class`

Acceptance: `rg -n 'permission denied: cannot read source' SPEC.md` matches, and `rg -n 'CODESIGN_IDENTITY' DESIGN.md` matches.

## U6: Maintainer-machine verification packet

Execution note: skip-test-first

Files:
  Modify: `README.md` (step 6 only, reconciling the grant-coverage paragraph U3 wrote with what the ceremony observed)

Interfaces:
  Consumes: U2's Makefile, U3's README ceremony
  Produces: the SC1 and SC2 measurements, the resolution of both Open Decisions, and the reconciled README grant-coverage wording

Test scenarios:
  happy: n/a — no unit-testable success path; the maintainer executes and reports
  edge: the maintainer is unavailable, so step 7 records SC1 and SC2 as unmeasured rather than as passing
  error: SC1 returns zero rows, which fails the criterion rather than passing it vacuously
  integration: this unit closes S1 and S3 in the Scenario coverage map, which no Go test can walk

This unit is prepare-and-hand-off, not execute. Every packet below mutates the
maintainer's live machine: it overwrites `~/bin/cogvault`, which backs two
running launchd jobs (`com.teslamint.cogvault` serve and
`com.teslamint.cogvault.ingest`), and it raises GUI consent prompts that only
a human at the keyboard can answer. The implementer prepares the exact
commands and hands them to the maintainer; the maintainer executes them and
reports the observed values back. The implementer runs none of them.

Steps:
  1. Prepare the SC1 packet:
     ```
     TCC_DB="$HOME/Library/Application Support/com.apple.TCC/TCC.db"
     make install CODESIGN_IDENTITY="Developer ID Application: <your name> (<team>)"
     launchctl kickstart -k gui/$(id -u)/com.teslamint.cogvault.ingest
     sqlite3 "$TCC_DB" "select service, auth_value, length(csreq) from access where client='$HOME/bin/cogvault' and service='kTCCServiceSystemPolicyAppData';"
     ```
     The maintainer answers the displayed prompt before running the query. Pass
     requires the maintainer to report Allow and the query to return the AppData
     row. Record `length(csreq)` as an observation only: the observed AppData
     service stores an empty `csreq`, so it cannot prove the code requirement.
     Zero rows fails the criterion; it does not pass it vacuously.
  2. Prepare the SC2 packet as an ordered five-step sequence, using the same
     `TCC_DB` assignment as step 1: signed install → kickstart and answer the
     prompts → record the installed `CDHash` from `codesign -dv` for
     diagnostic context and set `REBUILD_EPOCH=$(date +%s)` → `make install`
     again → kickstart again. An unchanged-source reproducible build can retain
     its `CDHash`, so it is not a pass criterion. Pass requires that no prompt
     appears at the second kickstart and that
     `sqlite3 "$TCC_DB" "select count(*) from access where client='$HOME/bin/cogvault' and last_modified >= $REBUILD_EPOCH;"`
     returns 0.
  3. Hand both packets to the maintainer with the note that step 1 costs one
     final round of prompts: switching from `Identifier=a.out` to
     `Identifier=dev.tmint.cogvault` is itself an identity change, so the
     existing grants do not carry over. This is the same one-time cost U3
     step 2 documents in the README.
  4. Record the reported values in the run ledger's Log — the SC1 AppData row
     and `csreq` length plus both counts for SC2 — with every personal
     identifier redacted as `<team>` / `<devid>`. Record no concrete
     certificate values.
  5. Resolve both Open Decisions from what the ceremony observes: which access
     raises `kTCCServiceSystemPolicyAppData`, and whether one Full Disk Access
     grant supersedes the individual folder prompts. Record each observed answer
     in the ledger. If the ceremony did not test Full Disk Access, record that
     decision as unmeasured and leave the README marker unchanged. Neither
     gates the merge.
  6. Reconcile the README with step 5's answers. If one Full Disk Access grant
     supersedes the individual prompts, rewrite U3 step 5's paragraph to
     document the single grant and delete the unresolved marker. If it does
     not, replace the marker with the observed answer. If step 7 applies and
     the ceremony never ran, leave the paragraph as U3 wrote it — the
     unresolved marker is then accurate — and say so in the completion report.
     Commit any change here as
     `docs(readme): reconcile grant coverage with the verified ceremony`.
  7. If the maintainer is unavailable, record SC1 and SC2 as unmeasured in the
     ledger and say so in the completion report. Do not report them as passing.

Acceptance: the run ledger's Log contains either the SC1 and SC2 measurements as reported by the maintainer, or an explicit unmeasured record naming why; and the README grant-coverage paragraph either carries the observed answer or still carries the unresolved marker with the unmeasured record explaining it.

## Mutation/failure-state matrix

No stateful ceremony in the deliverable; no mutation/failure-state matrix required.

Classification reasoning, recorded so this reads as a decision rather than an
omission: the deliverable is repository content — Go code, `Makefile`, README,
solutions doc, and canon — merged to the base branch locally. No unit pushes to
a remote, creates a remote repository, publishes to a registry, creates a
platform release, or changes repository visibility, so no unit crosses an
outward-publication boundary.

The TCC grant ceremony is the near miss and it stays out for two reasons.
First, no unit performs it: U6 is prepare-and-hand-off, and the machine
mutations belong to the maintainer executing with first-hand consent, not to
plan execution. Documenting a ceremony an operator performs does not place that
ceremony in the deliverable. Second, the matrix could not be filled honestly
if it were: the headless outcome is impossible by the spec's own Scope Out
(macOS requires a human action by design), there is no safe injection boundary
for a SIP-protected machine-global TCC database, and no disposable fixture can
exist for it. The operational content that would have lived in those rows —
`serve`-job restart, the `go build` caveat, stale-grant persistence, and the
certificate rotation and future-signing requirements — is delivered as U3 and U4 documentation instead,
where an operator will actually read it.

## Carry-forward trigger audit

Audited `docs/research/v2-follow-ups.md` at `4c5c623`: 0 open rows, 0 fired, 0 unobservable.

## Deferred to Follow-Up Work

- **Deduplicating the scan+sweep source-error pair.** A denied source
  directory emits two identical `source-error` lines because `Run` sweeps
  before it scans and both read the directory. Explicitly scoped Out in the
  origin spec; changing it touches report line structure and counters.
- **A gitignored `local.mk` include.** Would let a maintainer keep
  `CODESIGN_IDENTITY` in a file rather than repeating it on the command line.
  With `?=` an exported environment variable already covers the common case,
  so this is convenience, not capability. Not in the spec's Scope In; noted at
  the draft gate rather than folded into U2.
- **Widening the README wording beyond Developer ID.** Any stable signing
  identity (Developer ID, Apple Development, self-signed) may have a different
  designated requirement. Distribution and notarization require Developer ID.
  Deferred because this plan measures only the observed Developer ID ceremony
  and does not generalize TCC row storage across identity types.
- **Notarization, stapling, hardened runtime, signed release distribution.**
  Scoped Out in the origin spec.

## Open unknowns

### Planning-time (resolved)

None. The one contradiction the assumption recheck surfaced was discharged
before this plan was written; see the Assumption Recheck section.

### Implementation-time (deferred)

- Which access raises `kTCCServiceSystemPolicyAppData` (the "data from other
  apps" prompt). U6 observed the service row but did not isolate the access,
  so it remains unmeasured and does not gate any unit.
- Whether a single Full Disk Access grant supersedes the individual folder
  prompts. U6 did not test this, so it remains unmeasured and the README
  paragraph U3 step 5 writes retains its unresolved marker.
