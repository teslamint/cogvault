# Scheduled access-check U4 operational record

Consolidated: 2026-09-09
Observation date: 2026-08-29
Status: records consolidated; same-execution prompt and cleanup proof unresolved

Follow-up: a [new ceremony on 2026-09-09](2026-09-09-access-check-live-evidence.md) provides complete same-label evidence and closes the operational follow-up.
The historical evidence gap described here remains unchanged.

## Scope

This is a historical evidence consolidation, not a new runtime test.
The [access-check contract](../../SPEC.md#914-access-check) bounds the operations and claims.
The [retrospective](../retros/2026-08-29-scheduled-access-check-retro.md) requested one record joining execution, prompts, and cleanup.

The source artifacts identify different temporary jobs. No inspected artifact links those jobs as one execution.
Keep their observations separate even though both records report two successful runs.

## Observation inventory

| Source | Time (UTC) | Exact label | Supported observation | Missing evidence |
|---|---|---|---|---|
| A: real-ceremony.txt | 2026-08-29T06:25:00Z | `com.teslamint.cogvault.access-check.501.26775.19894` | Aggregate exit 0, two configured-path runs, unchanged signed binary, temporary job absent, success directory absent | Prompt field still awaits operator confirmation; individual run logs are not embedded |
| B: access-check-prompt-observation.txt | 2026-08-29T07:22:11Z | `com.teslamint.cogvault.access-check.501.32593.6052` | Operator reported no popup during either of two successful access-check launches | Binary identity and cleanup are not recorded for this label |
| C: prompt-observation.txt | 2026-08-29T07:09:39Z | `com.teslamint.cogvault.ingest` | Operator reported no popup during production ingest kickstart and no new stderr | Separate production context; cannot fill A or B's missing fields |

## Source A: execution and cleanup

Source revision: `5c6ddb565b5ee6798444cae93b95e49a43e3ee8b`.
The recorded command is `COGVAULT_BIN=<installed> COGVAULT_CONFIG=<active> scripts/check-scheduled-access.sh`.
Its recorded output is `scheduled configured-path access check passed twice`, with exit status 0 and run count 2.

The installed binary path is `/Users/teslamint/bin/cogvault`.
Its signing identifier is `dev.tmint.cogvault`, with an Apple Development leaf identity present.
The artifact omits account and team details.
The designated requirement includes the identifier, Apple generic anchor, Apple Development leaf constraint, and Apple intermediate constraint.
Recorded binary SHA-256: `e3b7581e3f6021302b4266c1ae7589e189a9e6746563185ba2941e7102f11202`.
The identity remained unchanged during the recorded ceremony.

The configuration covered an iCloud File Provider wiki, a local database parent, and one Downloads source.
Exact configuration paths were omitted by the source artifact.
After success, `launchctl print` returned service-not-found status 113 and the success private directory was absent.
The artifact attributes directory removal to the harness success path.

An earlier attempt used `com.teslamint.cogvault.access-check.501.27494.10295`.
The process exited 0, but its success marker appeared after the harness checked stdout, causing a false-negative.
That temporary job was absent afterward; failure plist and initial logs were retained for diagnosis.
The recorded fix polls the marker within the existing timeout after PID completion.
The artifact reports a failing-before/passing-after delayed-marker regression. This consolidation did not rerun that test.

## Reconciliation and claim limits

The retrospective's T5 associated the later no-popup observation with the ceremony.
The exact labels in A and B do not support that association.
B therefore does not replace A's `prompt_observation: awaiting operator confirmation` field.
The inspected archive contains no other occurrence of either successful temporary label.

Source A proves the recorded two-run execution and cleanup, subject to its aggregate evidence limits.
Source B records a two-run no-popup observation for another label.
Source C records a production-ingest observation only.
None proves persistent TCC grants, full ingest coverage from access-check, or access to unconfigured paths.

The consolidation is complete. A combined same-label proof remains open.
Close that gap only with contemporaneous evidence for the missing fields or a separately dated new ceremony.
A new ceremony cannot retroactively confirm the historical run.

## Source provenance

All three originals remain unchanged under `.release-loop/archive/2026-08-29-scheduled-access-check/evidence/U4/`.
They are local ignored artifacts, not tracked Git files. This document preserves their substantive observations for repository readers.
The hashes below identify the exact source bytes inspected; they do not independently verify the runtime claims.

| Source | File | SHA-256 |
|---|---|---|
| A | `real-ceremony.txt` | `ed612abe7c2efb48082405a6249930557c4a3b15b26567cdc286d6de27ffb1b8` |
| B | `access-check-prompt-observation.txt` | `f9cc25d52b45fe1746581d86d8476e2813ad02a7dd58ed3ed92b85b67ff5a469` |
| C | `prompt-observation.txt` | `3b6206284d30216c4b89e1d352f629effa8eb688ea24608fec190ff8050804b2` |
