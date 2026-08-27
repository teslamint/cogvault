---
title: Stable code identity for scheduled ingest
status: draft
date: 2026-08-27
schema: spec/v1
---

# Stable code identity for scheduled ingest Design

_Created 2026-08-27._

## Overview

Scheduled `cogvault ingest` runs under launchd repeatedly raise macOS TCC consent
prompts, including the "wants to access data from other apps" prompt. The cause is
the ad-hoc code signature: macOS stores each TCC grant for the binary as a
`cdhash` requirement, and every rebuild produces a new `cdhash`, so every grant
stops matching. This spec makes the signing identity and the code-signing
identifier configurable so a maintainer can sign with a stable identity, documents
the one-time grant and the stale-grant cleanup, and makes a permission-denied
source read visible in the ingest report instead of hiding it in `skipped`.

## User Scenarios

### S1: Maintainer rebuilds a signed binary and the schedule stays silent

A maintainer holds an Apple Developer ID Application identity. They set
`CODESIGN_IDENTITY` once, run `make install`, and rebuild many times afterwards.
The TCC grants already given to `~/bin/cogvault` keep matching, so the hourly
launchd job runs with no new prompt.

```bash
make install CODESIGN_IDENTITY="Developer ID Application: <name> (<team>)"
```

### S2: Contributor without any signing certificate still builds

A contributor cloning the public repository has no Developer ID certificate.
`make build` keeps its current ad-hoc behavior and exits 0. The build prints one
line naming the identity in use so the contributor knows why prompts may recur.

```bash
make build          # CODESIGN_IDENTITY defaults to "-" (ad-hoc)
./cogvault --help   # exits 0
```

### S3: First-time operator performs the one-time grant

An operator setting up the launchd schedule follows README steps: install the
signed binary, load the launchd job, then force one immediate run of that job and
answer the prompts it raises.

```bash
make install CODESIGN_IDENTITY="Developer ID Application: <name> (<team>)"
launchctl kickstart -k gui/$(id -u)/com.teslamint.cogvault.ingest
```

The manual run must go through launchd, not through the terminal. TCC attributes
a terminal-spawned process to the terminal application, which is the responsible
process; only under launchd is `cogvault` itself the client. A grant obtained
from a terminal run therefore lands on the terminal's identity and leaves the
scheduled job still prompting. This asymmetry also explains the reported
symptom: prompts appear under the schedule and never during manual ingest.

The README states which grant covers which access and that a `go build` outside
the Makefile silently reverts the identity.

### S4: Operator removes grants left behind by earlier binaries

Grants recorded against superseded `cdhash` values accumulate under the same
client path and never match again. The operator follows a documented cleanup
procedure that names the exact inspection command and warns that `tccutil reset`
operates far more broadly than the single stale row.

### S5: A denied source directory is visible in the ingest report

The scheduled job cannot read a source directory or file because consent was not
granted. The report line names the denial as a permission problem and points at
the grant procedure, instead of appearing as a generic `skipped` or a bare
`source-error` with an untyped OS string.

```
$ cogvault ingest --config ~/.config/cogvault/config.yaml
scanned=0 digested=0 ... source-errors=1
  source-error  ~/Downloads/_Articles  permission denied: cannot read source; macOS consent required, see README "launchd automation"
```

## Scope

### In

- `Makefile`: `CODESIGN_IDENTITY` and `CODESIGN_IDENTIFIER` variables, applied to
  both the build artifact and the install destination; an echo line naming the
  identity actually used.
- `README.md`: build section and launchd section updates covering the stable
  identity requirement, the one-time grant ceremony driven through
  `launchctl kickstart` (never a terminal ingest run) with the responsible-process
  reason stated, and the `go build` caveat.
- `docs/solutions/build-errors/macos-sigkill-rebuilt-go-binary.md`: correction of
  the prevention guidance, which currently presents ad-hoc re-signing as
  sufficient.
- `internal/ingest`: permission-denied classification for source directory and
  source file reads, with an actionable diagnostic string.
- `SPEC.md` and `DESIGN.md`: update wherever they describe report actions,
  ingest skip semantics, or the Makefile's responsibility.

### Out

- Notarization, stapling, and signed release distribution.
- Wrapping the CLI in an application bundle or a helper tool.
- Changing `wiki_dir`, `sources[]`, or `db_path` locations to avoid protected
  directories.
- Any attempt to grant, pre-seed, or script TCC consent — macOS requires a human
  action by design, and automating it is out of bounds.
- Non-macOS platforms; the signing and consent behavior is macOS-only.
- Hardened runtime (`--options runtime`), which notarization would require but
  local grants do not.

## Assumptions and Preconditions

Live assumptions retained from the design investigation. Evidence sources are the
local machine's TCC database and the working tree; no raw database output,
certificate material, or personal path is committed.

| Claim | Command | Observed at | Observed result | Evidence source |
|---|---|---|---|---|
| TCC grants for the installed binary are pinned to `cdhash`, so a rebuild invalidates them | `sqlite3 "$HOME/Library/Application Support/com.apple.TCC/TCC.db" "select service, length(csreq), hex(csreq) from access where client like '%cogvault%';"` | `2026-08-27T14:33:00+09:00` | 9 grant rows; 8 carry a 40-byte requirement decoding to a single `cdhash` condition, across 3 distinct `cdhash` values dated 2026-07-23, 2026-08-15/17 and 2026-08-25/26/27; the newest equals the installed binary's current `cdhash` | Local user TCC database (not committed) |
| A Developer ID signed CLI receives an identifier-and-team requirement instead, which survives rebuilds | select the row's `csreq` with `sqlite3`, decode it through `xxd -r -p`, then print it with `csreq -r- -t`, on a Developer ID signed CLI row | `2026-08-27T14:35:00+09:00` | Requirement decodes to `identifier "<id>" and anchor apple generic and ... certificate leaf[subject.OU] = <team>`; no `cdhash` term | Local user TCC database (not committed) |
| The installed binary carries the Go linker's ad-hoc signature and a generic identifier | `codesign -dv --verbose=4 ~/bin/cogvault` | `2026-08-27T14:20:00+09:00` | `flags=0x20002(adhoc,linker-signed)`, `Identifier=a.out` — the Makefile's `codesign --force --sign -` step was not applied to the installed copy | Local build machine (not committed) |
| The build machine holds a usable Developer ID Application identity | `security find-identity -v -p codesigning` | `2026-08-27T14:36:00+09:00` | 2 valid identities, of which 1 is a Developer ID Application identity | Local login keychain (not committed) |
| The SQLite driver is pure Go, so signing options cannot break a native dependency | `rg -n "sqlite" go.mod` | `2026-08-27T14:38:00+09:00` | `modernc.org/sqlite v1.48.1`; no cgo SQLite driver | Working tree at `58d8a00` |
| A denied source read is currently indistinguishable from an ordinary skip | `rg -n "hashFile\(" internal/ingest/ingest.go` and reading `internal/ingest/ingest.go:233-283` | `2026-08-27T14:39:00+09:00` | `os.ReadDir` failure becomes `actionSourceError` with a raw OS string; `hashFile` failure becomes `actionSkipped` with `read: <os error>` | Working tree at `58d8a00` |
| The test suite is green before any change | `go test -race ./...` | `2026-08-27T14:20:00+09:00` | 14 packages `ok`, 0 failures | Feature worktree at `58d8a00` |

Environment invariants that still apply:

- The launchd template runs the binary as a top-level job, which makes cogvault the
  responsible process for its `claude` child and attributes the child's protected
  accesses to cogvault.
- On the maintainer's machine the same binary path also backs a second launchd job
  running `cogvault serve` (`launchctl list | rg cogvault`, observed
  `2026-08-27T14:41:00+09:00`: `com.teslamint.cogvault` running,
  `com.teslamint.cogvault.ingest` scheduled). Both jobs share one code identity.

## Architecture

No package boundary changes. Two independent surfaces move:

1. **Build identity (`Makefile`).** The `codesign` invocation gains two variables.
   `CODESIGN_IDENTITY` defaults to `-`, preserving today's ad-hoc behavior for
   anyone without a certificate. `CODESIGN_IDENTIFIER` defaults to
   `com.teslamint.cogvault` and replaces the linker's `a.out`, so the recorded TCC
   identity is stable and specific rather than shared with every other Go binary.
   Both the build artifact and the install destination are signed, preserving the
   destination re-sign rule established by the earlier codesign work.

2. **Denial visibility (`internal/ingest`).** `scan` already records two failure
   points: `os.ReadDir` on a source directory and `hashFile` on a source file. Both
   gain a permission check using `errors.Is(err, fs.ErrPermission)`. A permission
   failure keeps its existing counter and action so the report sum invariant
   (`Report.SumCheck`) is untouched; only the `Error` string changes, to a fixed
   diagnostic that names the denial.

   `fs.ErrPermission` also matches `EACCES` on Linux, where no TCC exists, so the
   macOS-specific half of the diagnostic is gated on `runtime.GOOS == "darwin"`
   at runtime. A runtime check is chosen over a build tag because the caller is a
   single shared `scan` path; the package's existing `notify_darwin.go` build-tag
   split stays untouched.

Data flow, ledger semantics, error classes, and attempt accounting are unchanged.
A denied read never reaches the LLM, so it cannot consume a permanent attempt.

## Interface

| Surface | Change |
|---|---|
| `make build` / `make install` | New variables `CODESIGN_IDENTITY` (default `-`) and `CODESIGN_IDENTIFIER` (default `com.teslamint.cogvault`); one echoed line naming the identity used |
| `cogvault ingest` report | `source-error` and `skipped` lines for denied reads carry a fixed permission diagnostic instead of a bare OS string |
| Config file | No change |
| MCP tools | No change |

The diagnostic text is two fixed strings so tests can assert them exactly:

- every platform: `permission denied: cannot read source`
- additionally on `runtime.GOOS == "darwin"`, the suffix
  `; macOS consent required, see README "launchd automation"`

The full darwin string is therefore
`permission denied: cannot read source; macOS consent required, see README "launchd automation"`.
The `os.ReadDir` failure emits the string bare, as it emits `err.Error()` bare
today. The `hashFile` failure keeps the `read: ` prefix it already emits, so its
line reads `read: permission denied: cannot read source…`.

## Testing

- `internal/ingest`: a table test creates a source directory, removes its
  permissions with `os.Chmod(dir, 0)`, runs `scan`, and asserts the
  `source-error` line carries the permission diagnostic. A second case chmods a
  single file to `0` and asserts the `skipped` line carries it. Both skip when
  running as root, where the permission bits do not deny access. Both assert the
  platform-independent half of the string always, and assert the macOS suffix
  only under `runtime.GOOS == "darwin"`, so the suite stays green on Linux CI.
- `internal/ingest`: an existing-behavior test asserts that a non-permission read
  error keeps its current message shape, so the change stays narrow.
- Full suite: `go test -race ./...`.
- Signing behavior cannot be asserted automatically, because TCC state is a
  machine-level side effect. The manual verification procedure is part of Success
  Criteria 1 and 2 and is documented in README.

## Risks

| Risk | Mitigation |
|---|---|
| A contributor without a certificate hits a broken build | `CODESIGN_IDENTITY` defaults to `-`; S2 proves `make build` still exits 0 |
| Changing the code-signing identifier resets the binary's TCC identity once, producing one final round of prompts | Documented in README as an expected one-time cost of the switch; Success Criterion 2 measures recurrence after that point, not the first run |
| A plain `go build` silently restores the linker ad-hoc signature and breaks the grants again | README states the caveat next to the build commands; `make install` remains the documented path |
| The same binary path backs a second, long-running launchd job (`cogvault serve`), which keeps executing the pre-install image until it is restarted | README documents restarting the server job after `make install`, next to the grant procedure |
| Certificate expiry or revocation invalidates the requirement later | Documented in the solutions doc as the known lifetime limit of this approach |
| `tccutil reset` affects far more than the stale rows | The cleanup procedure documents inspection first and warns about the blast radius; no command in the repository executes it |
| The exact access that triggers the "data from other apps" prompt is unidentified | The fix is independent of which access triggers it, because a stable identity preserves every grant; the open question is tracked in Open Decisions rather than assumed away |

## Success Criteria

1. Every TCC grant recorded after the signed install is identity-based, not
   `cdhash`-based. Re-signing does not rewrite rows that already exist, so the
   claim is scoped to rows whose `last_modified` is at or after the signed
   install; pre-existing `cdhash` rows are expected to remain until the
   documented stale-grant cleanup (S4) removes them.
   - **Measured by**: record `INSTALL_EPOCH=$(date +%s)` immediately before the signed `make install`. After the re-grant round, for **every** row returned by `sqlite3 "$HOME/Library/Application Support/com.apple.TCC/TCC.db" "select service, hex(csreq) from access where client='$HOME/bin/cogvault' and last_modified >= $INSTALL_EPOCH;"`, decoding its `csreq` through `xxd -r -p` and `csreq -r- -t` prints a requirement containing `identifier "com.teslamint.cogvault"` and a `certificate leaf[subject.OU]` term, and containing no `cdhash` term. At least one row must be returned; zero rows fails the criterion rather than passing it vacuously.
2. A rebuild after the grants are re-established does not invalidate them. The
   measurement runs in this exact order, because the first signed install still
   costs one final round of prompts:
   1. `make install CODESIGN_IDENTITY=<identity>` — signed install.
   2. `launchctl kickstart -k gui/$(id -u)/com.teslamint.cogvault.ingest`, then answer every prompt it raises. This is the one-time re-grant round.
   3. Record `codesign -dv --verbose=4 ~/bin/cogvault` CDHash and `REBUILD_EPOCH=$(date +%s)`.
   4. `make install CODESIGN_IDENTITY=<identity>` again; confirm the CDHash changed.
   5. `launchctl kickstart -k gui/$(id -u)/com.teslamint.cogvault.ingest`.
   - **Measured by**: after step 5, no consent prompt appears, and `select count(*) from access where client='$HOME/bin/cogvault' and last_modified >= $REBUILD_EPOCH;` returns 0.
3. A build without any signing certificate still succeeds.
   - **Measured by**: `make clean && make build CODESIGN_IDENTITY=- && ./cogvault --help` exits 0.
4. A denied source read is reported as a permission problem.
   - **Measured by**: `go test ./internal/ingest -run TestScanPermissionDenied` passes.
5. The full test suite stays green.
   - **Measured by**: `go test -race ./...` reports 0 failures.
6. The documentation lets an operator perform the grant and the cleanup without asking a question.
   - **Measured by**: reviewer rubric — the README launchd section and the solutions doc contain copy-pasteable commands, contain no machine-specific absolute path or personal identifier, state what each grant covers, and state the `go build` caveat and the `tccutil` blast radius. Pass requires all five.

## Open Decisions

| Question | Owner |
|---|---|
| Which access raises `kTCCServiceSystemPolicyAppData` for cogvault — the spawned `claude` CLI reading another application's support directory, the `osascript` notification path, or `wiki_dir` under `~/Library/Mobile Documents` | `implementing`, as a bounded observation during manual verification; it does not gate the fix |
| Whether a single Full Disk Access grant supersedes the individual folder prompts and the app-data prompt, allowing the README to document one grant instead of several | `implementing`, verified on the maintainer's machine before the README wording is finalized |
| Whether the identifier should be `com.teslamint.cogvault` or a repository-neutral value for contributors who sign with their own certificate | User, at spec approval |
