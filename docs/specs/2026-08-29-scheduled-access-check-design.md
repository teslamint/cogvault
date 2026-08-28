---
title: Scheduled Access Check
status: draft
date: 2026-08-29
schema: spec/v1
---

# Scheduled Access Check Design

_Created 2026-08-29._

## Overview

Add a bounded write probe that verifies the configured wiki directory is usable by
the installed CogVault binary. Add a temporary LaunchAgent harness that runs the
probe twice under launchd so the user can verify that macOS reuses that wiki access
grant. This check does not verify every AppData access made by a full ingest run.

## User Scenarios

### S1: Verify the configured wiki directory

An operator runs `cogvault access-check --config <path>`. CogVault creates a unique
sentinel file directly under `wiki_dir`, reads it back, removes it, and reports
success. A filesystem or macOS access denial returns a non-zero exit status.

### S2: Complete the launchd approval ceremony

An operator runs `scripts/check-scheduled-access.sh`. The script installs a
temporary LaunchAgent that invokes the installed binary's `access-check` command.
The first run may display the macOS access prompt. The operator allows access, then
the script runs the same job again. Two successful runs and no second prompt show
that the same code identity reused the grant in the scheduled execution context.

### S3: Diagnose a failed scheduled access check

When either temporary job run fails or times out, the script prints the retained
stdout and stderr log paths. It unloads the temporary job and removes its plist, but
keeps logs needed for diagnosis.

## Scope

### In

- Add `cogvault access-check --config <path>`.
- Probe only the configured `wiki_dir` with a unique, exclusive sentinel file.
- Read back the exact sentinel contents before removing the file.
- Remove the sentinel after success and after recoverable partial failures.
- Add a macOS script that verifies two invocations through one temporary LaunchAgent.
- Add the command's config-only bootstrap exception to `SPEC.md` and `DESIGN.md`.
- Document that the second prompt remains a user observation, not a programmatic fact.

### Out

- Querying, modifying, or resetting the macOS TCC database.
- Detecting whether a macOS consent dialog appeared.
- Claiming to verify AppData access by `claude`, notification delivery, source reads,
  or the complete ingest path.
- Changing arguments or state for the existing scheduled ingest LaunchAgent.
- Probing `sources[]`, `db_path`, or arbitrary operator-supplied paths.
- Promising that a grant survives certificate, identifier, or designated-requirement changes.
- Supporting the launchd harness on non-macOS systems.

## Assumptions and Preconditions

The installed binary must use the same signing identity and identifier that the
scheduled ingest job will use. The operator must run the harness in a logged-in GUI
session because macOS may need to display a consent dialog.

| Claim | Command | Observed at | Observed result | Evidence source |
|---|---|---|---|---|
| `wiki_dir` may reside in a macOS-protected location. | `rg -n 'wiki_dir.*iCloud Drive' SPEC.md` | 2026-08-28T22:55:06Z | SPEC allows the wiki root under iCloud Drive. | `SPEC.md` |
| The current command tree loads configuration through the root `--config` flag. | `sed -n '1,120p' cmd/cogvault/main.go` | 2026-08-28T22:55:06Z | Root defines the persistent flag and registers subcommands. | `cmd/cogvault/main.go` |

## Architecture

`cmd/cogvault/access_check.go` owns the CLI command and the bounded filesystem
probe. It loads the validated configuration through the existing config path flow.
The probe uses `os.OpenFile` with `O_CREATE|O_EXCL|O_WRONLY` and mode `0600`, reads
the file with `os.ReadFile`, compares exact bytes, and removes it with `os.Remove`.

`scripts/check-scheduled-access.sh` owns the macOS ceremony. It writes a temporary
plist under the current user's LaunchAgents directory with the selected CogVault
binary as `ProgramArguments[0]`, bootstraps the job into the GUI domain, and kicks
off a second run without changing the binary or job label. It uses `launchctl print`
and the command's stdout marker to determine each run's completion. A trap removes
the job and plist.

The command does not use `internal/storage.FSStorage`: storage path policy is not
needed for a fixed root sentinel, while the probe must test direct root access before
other runtime setup opens the database or creates wiki assets. `SPEC.md` lists
`access-check` with the config-only CLI exceptions. `DESIGN.md` records the matching
bootstrap boundary.

## CLI Contract

`cogvault access-check [--config <path>]` accepts no positional arguments. On
success it prints `wiki access check passed: <wiki_dir>` and exits zero. It returns
an error that names the failed operation and `wiki_dir` when create, write, close,
read, content verification, or cleanup fails.

The sentinel basename starts with `.cogvault-access-check-` and contains a random
suffix. Exclusive creation prevents overwriting any existing file. The command
attempts cleanup on every path after creation. When a probe operation and cleanup
both fail, the returned error preserves both operation names with `errors.Join`.

## LaunchAgent Harness Contract

The script requires macOS, an executable CogVault binary, and a readable config
path. Optional environment variables select the installed binary and config path;
their defaults are `$HOME/bin/cogvault` and
`$HOME/.config/cogvault/config.yaml`. The script does not accept or persist a signing
identity. Plist generation XML-escapes every dynamic string so spaces and XML
metacharacters in paths remain one unchanged `ProgramArguments` value.

The script uses one unique label for both runs and stores bounded diagnostic files
under a temporary directory. It waits at most 120 seconds per run. Before each
kickstart it clears the stdout marker and then polls `launchctl print` until the job
is no longer running, has `last exit code = 0`, and stdout contains the command's
success marker. A non-zero exit, missing marker, or timeout fails the run and prints
the stdout and stderr paths. The script then states that absence of a second dialog
shows access reuse only for the selected binary, temporary job, and `wiki_dir`.

## Testing

- CLI tests use a temporary config and wiki directory to verify success, exact
  output, sentinel cleanup, and rejection of positional arguments.
- CLI tests use a non-directory wiki root to verify a stable non-zero failure without
  leaving a sentinel. Permission-denied behavior uses an injected filesystem seam or
  a conditional macOS test that states its skip conditions.
- Unit-level injection around file removal verifies that cleanup failures are not
  hidden by an earlier probe failure.
- Shell tests run the harness with fake `launchctl` and a fake binary to verify two
  kickstarts, stable job identity, timeout/error reporting, cleanup, and paths that
  contain spaces and XML metacharacters.
- The full Go test suite and shell tests run before release.

## Risks

- macOS attributes consent to a responsible code identity and execution context,
  not only a pathname. Mitigation: both runs use the same installed binary and one
  LaunchAgent definition.
- A crash can leave a sentinel. Mitigation: use a unique hidden filename so a later
  run never overwrites it and the residual artifact remains identifiable.
- A successful filesystem probe cannot prove the internal TCC database contents or
  other AppData access in ingest. Mitigation: state the result as observed wiki access
  reuse by the selected binary only.
- A consent dialog can outlive an automated timeout. Mitigation: retain logs and
  unload the temporary job; do not alter the production ingest job.

## Success Criteria

1. The CLI proves create, read, and delete access to the configured `wiki_dir` without leaving a sentinel.
   - **Measured by**: `go test ./cmd/cogvault -run 'TestAccessCheck'`
2. The launchd harness invokes one temporary job twice and fails on a non-zero or timed-out run.
   - **Measured by**: `bash scripts/check-scheduled-access_test.sh`
3. Existing behavior remains green.
   - **Measured by**: `go test -race ./...`
4. The user can distinguish the wiki probe from a complete ingest or AppData check.
   - **Measured by**: reviewer confirms the CLI help, script output, and README limit the result to the selected binary and `wiki_dir` and never claim to query TCC persistence.

## Open Decisions

No open decisions remain. The operator decides whether the observed absence of a
second consent dialog is sufficient for the local machine.
