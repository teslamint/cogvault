---
title: Scheduled Access Check
status: approved
date: 2026-08-29
schema: spec/v1
---

# Scheduled Access Check Design

_Created 2026-08-29._

## Overview

Add a bounded preflight that verifies CogVault's configured filesystem surfaces:
read-write access to `wiki_dir` and the `db_path` parent, plus directory and file
read access at every `sources[]` root. Add a temporary LaunchAgent harness that
runs the preflight twice so the user can verify that macOS reuses those grants. This
check does not verify unconfigured Documents or Pictures folders or every AppData
access made by subprocesses during a full ingest run. Source reads are bounded
access probes, not full-file readability or integrity checks. A configured path on
a mounted network volume uses the same probe; the command never scans network mounts.

## User Scenarios

### S1: Verify configured ingest paths

An operator runs `cogvault access-check --config <path>`. CogVault creates a unique
sentinel in `wiki_dir` and beside `db_path`, reads each back, and removes each. It
enumerates every configured source root and opens one byte from each regular file
whose extension the source accepts and whose size does not exceed the configured
limit. A filesystem or macOS access denial returns a non-zero exit status that names
the configured surface.

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
- Probe the configured `wiki_dir` and `db_path` parent with unique, exclusive
  sentinel files.
- Enumerate every configured `sources[]` root without descending into directories or
  following symlinks, matching the current ingest scanner's top-level traversal. Open
  each accepted regular source file within `max_file_size_mb` for a one-byte read.
- Read back the exact sentinel contents before removing each file.
- Remove the sentinel after success and after recoverable partial failures.
- Add a macOS script that verifies two invocations through one temporary LaunchAgent.
- Add the command's config-only bootstrap exception to `SPEC.md` and `DESIGN.md`.
- Document that the second prompt remains a user observation, not a programmatic fact.
- Explain how to distinguish a configured-path prompt from a prompt that appears only
  during full ingest subprocess or notification execution.

### Out

- Querying, modifying, or resetting the macOS TCC database.
- Detecting whether a macOS consent dialog appeared.
- Claiming to verify AppData access by `claude`, notification delivery, or the
  complete ingest path.
- Probing unconfigured Documents, Pictures, Photos Library, or other arbitrary paths.
- Enumerating `/Volumes` or requesting access to an unconfigured network share.
- Changing arguments or state for the existing scheduled ingest LaunchAgent.
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
| The active config does not name Documents or Pictures as a source. | `sed -n '/^sources:/,/^[^ -]/p' "$HOME/.config/cogvault/config.yaml"` | 2026-08-28T23:10:39Z | The only configured source is a directory under Downloads. | Local config; path category only, no personal value retained. |
| Active configured paths are not on a mounted network volume. | `df -P <wiki_dir> <db_parent> <source>; mount \| rg 'smbfs\|afpfs\|nfs\|webdav\|osxfuse\|macfuse'` | 2026-08-28T23:16:06Z | All paths resolve to the local Data volume; no matching network mount is present. | Local mount table; sanitized summary only. |

## Architecture

`cmd/cogvault/access_check.go` owns the CLI command and the bounded filesystem
preflight. It loads the validated configuration through the existing config path
flow. Write probes use `os.OpenFile` with `O_CREATE|O_EXCL|O_WRONLY` and mode `0600`,
read the file with `os.ReadFile`, compare exact bytes, and remove it with `os.Remove`.
The database probe targets `filepath.Dir(cfg.DBPath)` and never opens or changes the
database file.

The source probe calls `os.ReadDir` once for each configured root, matching the
current non-recursive ingest scanner. For each entry it uses `os.Lstat`, skips
directories and symlinks, applies the source's configured type filter, and opens
each accepted regular file, reading at most one byte. This exercises lazy file-level
consent without hashing or sending content to an LLM. Empty source roots still
require a successful enumeration. End-of-file on an empty file is success.

The preflight skips files over `max_file_size_mb`, matching ingest. It intentionally
reads files inside the two-minute settle window because they are future configured
ingest candidates. It skips every non-regular entry. Current ingest skips only
directories and symlinks, so a specially named FIFO or device remains an existing
scanner gap that this safe preflight does not claim to reproduce.

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
success it prints one `passed` line for `wiki_dir`, `db_path parent`, and every
`source`, followed by `configured ingest access check passed`, then exits zero. It
returns an error that names the configured surface, path, and failed operation.

The sentinel basename starts with `.cogvault-access-check-` and contains a random
suffix. Exclusive creation prevents overwriting any existing file. The command
attempts cleanup on every path after creation. When a probe operation and cleanup
both fail, the returned error preserves both operation names with `errors.Join`.
The command stops after the first failed surface so the error remains actionable.

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
is no longer running, has `last exit code = 0`, and stdout contains
`configured ingest access check passed`. A non-zero exit, missing marker, or timeout fails the run and prints
the stdout and stderr paths. The script then states that absence of a second dialog
shows access reuse only for the selected binary, temporary job, and configured
filesystem surfaces.

The README interpretation is diagnostic. A folder or network-volume prompt during
`access-check` belongs to its execution path. The operator first checks the selected
binary and config path, then the configured filesystem surfaces. A prompt that
appears only during full `ingest` is outside this preflight. The next candidates
include the database file and its sidecars, wiki contents, Git operations, `claude`,
and `osascript`. The documentation keeps these candidates separate and never
instructs the operator to approve unrelated broad access.

## Testing

- CLI tests use temporary wiki, database-parent, and source directories to verify
  success output, sentinel cleanup, root enumeration, one-byte reads, type filters,
  size limits, empty-file EOF success, non-regular entry skipping, symlink
  non-following, and rejection of positional arguments.
- CLI tests use a non-directory surface to verify a stable non-zero failure without
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
  subprocess access in ingest. Mitigation: state the result as observed reuse for
  configured filesystem surfaces only.
- Checking every accepted source file can be slow for a very large source root.
  Mitigation: read at most one byte per file and perform no hashing or content parsing.
- A one-byte source read does not prove that ingest can hash the complete file.
  Mitigation: describe it as an access preflight and retain normal ingest error reporting.
- A consent dialog can outlive an automated timeout. Mitigation: retain logs and
  unload the temporary job; do not alter the production ingest job.

## Success Criteria

1. The CLI proves create, read, and delete access to `wiki_dir` and the database parent without leaving a sentinel.
   - **Measured by**: `go test ./cmd/cogvault -run 'TestAccessCheck'`
2. The CLI enumerates every configured source root and performs a bounded read of each accepted, size-eligible top-level regular file without following symlinks.
   - **Measured by**: `go test ./cmd/cogvault -run 'TestAccessCheck'`
3. The launchd harness invokes one temporary job twice and fails on a non-zero or timed-out run.
   - **Measured by**: `bash scripts/check-scheduled-access_test.sh`
4. Existing behavior remains green.
   - **Measured by**: `go test -race ./...`
5. The user can distinguish configured-path checks from a complete ingest or AppData check.
   - **Measured by**: reviewer confirms the CLI help, script output, and README limit the result to configured filesystem surfaces and never claim to query TCC persistence or unconfigured folders.
6. A configured network-volume path uses the normal surface probe without mount discovery.
   - **Measured by**: a recording filesystem seam confirms that source enumeration receives exactly the configured source roots; a static check confirms the implementation contains no `/Volumes` mount-discovery path.

## Open Decisions

The cause of prompts for unconfigured Documents or Pictures paths remains unresolved.
Candidates include direct database and wiki content access, Git operations, the
spawned `claude` CLI, `osascript`, and AppData access under iCloud Drive. A
network-volume prompt is also unresolved when the binary, config, and configured
surfaces use local storage. This implementation does not request broad folder or
network access to hide that diagnostic gap. The operator decides whether absence of
a second dialog is sufficient for the configured filesystem surfaces.
