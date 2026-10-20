# Scheduled access-check live evidence

Date: 2026-09-09
Post-run checks completed before: 2026-09-09T05:27:47Z
Status: complete; execution, operator prompt observation, and cleanup verified for one label

## Execution identity

Exact label: `com.teslamint.cogvault.access-check.501.22868.1798`.
GUI launchd domain: `gui/501`.
Harness revision: `33ee955eb140ca1ccb3c5c8b8295c9f676c851d6`.
Command: `bash -x scripts/check-scheduled-access.sh` with default installed binary and active configuration.
The harness directly launched the binary with `access-check --config <active-config>`.

Installed binary: `/Users/teslamint/bin/cogvault`.
Binary SHA-256: `7039d5fbc4b6289add5eeb687b84076eb2450e16d81049e11a42001c3919fd97`.
Strict signature verification passed. The identifier is `dev.tmint.cogvault`, with an Apple Development identity.
The harness checked the same file identity, digest, and designated requirement before and after each run.
The installed binary digest identifies the tested artifact; the harness revision does not establish its build revision.

## Two-run transcript

| Run | PID returned by launchd | Observed completion |
|---|---|---|
| 1 | `477` | PID polling finished; exact success marker matched; run logs copied; identity check passed |
| 2 | `5524` | PID polling finished; exact success marker matched; run logs copied; identity check passed |

Both runs used the exact label above.
Each passed the marker check for `configured ingest access check passed`.
The harness reached its final output: `scheduled configured-path access check passed twice`.
Individual process exit codes were not collected by this harness; completion evidence consists of PID termination and exact markers.

## Cleanup

The trace records successful `launchctl bootout gui/501/com.teslamint.cogvault.access-check.501.22868.1798` before directory removal.
An independent post-run `launchctl print` for that exact job returned status `113`.
The harness private directory was `cogvault-access-check.7RvWBr` under the system temporary directory.
An independent absence check on its exact absolute path returned status `0`.
The harness removed its temporary plist and run logs with that directory.
The separate evidence directory remains available for audit.

## Prompt observation

After completion, the operator confirmed that both launches were observed and neither showed a macOS permission popup.
The question explicitly identified label suffix `22868.1798`, linking the response to this ceremony.
Confirmation date: 2026-09-09. Source: the operator's response in this session.
The earlier `OK` response acknowledged readiness only; the later explicit observation supplies the prompt evidence.
This is human observation, not a deduction from command output or a claim about the TCC database.

## Provenance and limits

Local trace: `/tmp/cogvault-access-evidence.vUuQyk/ceremony.log`.
Trace SHA-256: `0e04ff45554c6fbcc4bb47f504ff51d0fa70931554d1e5d146db43191313a82f`.
The trace contains machine-specific signing and path details. This document records a sanitized summary.
The trace has no per-event timestamps. The UTC value above bounds the post-run inspection, not the exact start time.

The existing harness regression suite returned `PASS: scheduled access harness` during this collection.
This run checks configured filesystem operations only; it does not establish persistent TCC grants or full-ingest prompt behavior.
It does not repair the historical label mismatch in the [U4 record](2026-09-09-access-check-u4-operational-record.md).
