# Scheduled access LaunchAgent lifecycle deviation

## Original contract

The approved design creates a temporary plist under `~/Library/LaunchAgents`.
It polls `launchctl print` for the job state and last exit code. On failure, it
removes the job and plist but retains stdout and stderr logs.

Source: `docs/specs/2026-08-29-scheduled-access-check-design.md`.

## Discovered contradiction

The local `launchctl` help exposes `kickstart -p`, which returns the launched PID.
Apple does not define the human-readable `launchctl print` body as a stable parsing
API. A parser can therefore misclassify completion after a macOS update.

The approved cleanup contract also removes the plist on failure. That discards the
exact job definition needed to diagnose an argument, path, or identity error. A
missing `~/Library/LaunchAgents` parent adds an unrelated setup failure even though
`launchctl bootstrap` accepts a plist from a private temporary directory.

## Why documentation alone cannot fix it

The script must change its completion and cleanup state machine. Documentation cannot
make unsupported output parsing stable or restore a plist that the script deleted.

## New observable behavior

The harness stores its plist and logs in one private temporary directory. It starts
each run with `launchctl kickstart -p`, polls the returned PID, and requires the
command-owned success marker after the PID exits.

On success, it boots out the job and removes the private directory. On a run failure,
it boots out the job and retains the `0600` plist and logs. On a bootout failure, it
retains all artifacts and prints exact bootout and deletion recovery commands.

## Safety and consent boundaries

The harness sets `umask 077` and verifies that its temporary directory is private,
owned by the current user, and not a symlink. It never changes the production ingest
job. It runs the selected signed CogVault binary directly as `ProgramArguments[0]`.

The configured-path boundary is unchanged. The command does not query TCC, discover
mounts, or request access to an unconfigured folder or network share.

## Verification changes

Shell tests use fake process IDs to prove completion, timeout, signal, failure, and
cleanup outcomes. They verify private modes and retained recovery artifacts. A final
machine-local ceremony runs the signed installed binary twice through the real GUI
launchd domain and verifies that the temporary job is absent afterward.

## Traceability

- Approved contract: `docs/specs/2026-08-29-scheduled-access-check-design.md`
- Successor plan: `docs/plans/2026-08-29-001-feat-scheduled-access-check-plan.md`
- Review evidence: `.release-loop/runs/scheduled-access-check/reviews/plan-deepening-review.md`
- Finding disposition: all architecture, feasibility, and security findings in that
  artifact are fixed by the successor plan
- Implementation owners: `scripts/check-scheduled-access.sh` and `scripts/check-scheduled-access_test.sh`
