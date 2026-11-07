# 0028: No-backup public-PDF collector trial

Status: accepted for the trial only (explicit user approval, 2026-11-07). It does not
change or satisfy ADR 0027 item 14; the production gate stays in force.
Date: 2026-11-07

## Context

ADR 0027 item 14 keeps production capture off until an off-NAS backup, a deletion-log
replica, and a passed restore test exist, and it limits any prototype before that to
temporary roots with no live wiki, NAS, config, or credentials. The cost of an
off-NAS backup is undecided, so that gate cannot open soon. The operator still wants
to learn whether the collector works on the real NAS (an x86_64 Synology host with
Portainer): that the image runs, a worker restarts and keeps its job history, and a
public PDF can be fetched and stored.

The operator has approved, explicitly and for this trial only, that:

- there is **no backup** of the trial, and its originals and job history are lost
  if the NAS fails;
- the trial collects **public material only** and is **not connected** to existing
  data, the wiki, or automatic ingest;
- the existing stack, config, archive, wiki, credentials, and ports are **not
  changed**, and no credential is created.

## Decision

Add a separate, narrower way to run the HTTP collector, and keep item 14 as it is.

1. **Separate program and policy.** `cogvault-trial` (`cmd/cogvault-trial`,
   `internal/trial`) is its own binary and Dockerfile target (`trial`). It never calls
   the item 14 activation check, and no flag or setting makes `cogvault archive job
   run`, `serve`, or `cogvault-kci` skip it. Item 14 is not amended, weakened, or
   marked satisfied by this ADR. Trial evidence (a fetched PDF, a restart that kept
   a job) is **not** evidence for item 14.
2. **Own root and registry, never the production ones.** The trial root is a new,
   dedicated path (default `/trial/data` in a Docker named volume, whose data sits
   under the Docker root directory, apart from the production install directory
   `/volume1/docker/cogvault`). It must not overlap, contain, or sit inside a
   protected path, and must be empty or carry a trial marker; otherwise the program
   refuses to start and every command refuses before it writes. Nothing the trial
   writes is read by the MCP server, the ingest pipeline, or the wiki, and the binary
   links none of them (a test checks the dependency list).
3. **Explicit acceptance.** The program starts only when `COGVAULT_TRIAL_ACK` equals the
   acceptance sentence exactly and `COGVAULT_TRIAL_ACCEPTED_BY` names who accepted it
   and when. The acceptance is recorded in the root's marker.
4. **Public HTTP PDFs only, on an exact allowlist.** Only `https`, only exact allowlisted
   host names (at most 10), on the first URL and on every redirect, only a body that
   starts `%PDF-`, no login, no KCI browser, no access-restricted path (a 401, 403,
   or similar ends the job as `human_action_required`, as in the production worker;
   executor `browser` jobs are never claimed). The first smoke test uses the RFC
   Editor (`www.rfc-editor.org`), whose usage notice allows free download and
   distribution of RFCs (<https://www.rfc-editor.org/series/rfc-use/>). KCI and BOK
   terms remain undecided, so neither is allowed by default.
5. **Bounded volume.** Defaults and hard caps (the program refuses a setting above a cap):
   queued jobs 10 (cap 50); submissions per day 20 (100); requests per hour 20 (60);
   requests per day 60 (200); minimum 10 s between requests (floor 2 s); attempts 3
   (5); body 32 MiB (cap 32 MiB).
6. **The worker, not the submitter, enforces.** Jobs can also be queued by the generic
   `archive job submit` on the same root. So the worker checks the request budget before
   each claim, and checks every claimed job (scheme, host, executor, attempt cap)
   before it sends a byte; a job outside the policy ends as failed with "nothing was
   sent". Budgets are counted from the registry, so they survive a restart. A root-local cross-process lock serializes each budget check with the claim that records an attempt, including a second `run --once` process.
7. **Stop, restart, no automatic deletion.** The container may be stopped and restarted
   (a restart policy is allowed); the same job is read back from the same root. A
   `PAUSE` file pauses the worker without stopping it. There is **no delete or cleanup
   command and no automatic cleanup**; removing a trial root is a documented manual
   step. A stop abandons a download in progress without a record, and the lease expiry
   resumes it.
8. **No new inbound surface.** The trial opens no port. Submission and reads run through
   the container's console or `docker exec`.

## Why

- A separate program makes it structurally impossible for the trial to be mistaken for
  production capture, and leaves the audited gate unchanged. A flag on the production
  gate would put a bypass in the code path that item 14 protects.
- A separate root and a "marker or empty" rule turn an operator mistake (pointing the
  trial at the production archive or a wiki) into a refusal instead of damage.
- The allowlist and the budget limit what an operator, or anyone who can run a command
  in the container, can make the host fetch.
- Accepting the loss is explicit, recorded, and limited to data that can be fetched
  again from a public source.

## Consequences

- If the NAS fails, the trial's originals and job history are gone. Nothing in the trial
  depends on them: public PDFs can be fetched again.
- The trial proves things about the image, the runtime, restart behavior, and one
  fetch. It proves nothing about production durability, deletion handling, the
  restore test, or release to Cogvault's ingest; those stay with item 14.
- Moving trial data into production, if ever wanted, needs its own decision and a manual
  import; this design gives no path from the trial registry to ingest.
- An image must exist for the NAS to run. No image is published by this change. The
  amd64 image can be built (`docker build --platform linux/amd64 --target trial .`),
  imported with Portainer's Images import, or published later through the release flow
  of ADR 0025 with its own approval.
- Every setting that widens the trial (another host, higher limits, a browser or login
  path, a connection to ingest) needs a new decision, not an environment variable.

## Rejected

- **Run the production worker with the gate off or a trial flag.** It puts a bypass in the
  one code path item 14 guards, and would let a trial capture sit in the production
  archive.
- **Reuse the production archive root with a separate registry file.** One typo away from
  mixing; the marker and overlap rules exist to prevent exactly this.
- **Ship it as a feature of the MCP server.** That adds an inbound path and a link to the
  ingest-facing code.
- **A `cleanup` command.** A destructive command on a store that holds the only copy of the
  trial's history is more risk than the trial needs; cleanup is manual and explicit.

## Evidence and gaps (2026-11-07)

Verified locally (a Mac, isolated containers, a named volume, no NAS): unit and package
tests (policy, root guard, budgets, worker re-check, restart, pause, the dependency
check); a linux/arm64 container with the stack's constraints (read-only root,
`tmpfs /tmp`, all capabilities dropped, no-new-privileges, memory and pid limits, a
restart policy): one real download of `https://www.rfc-editor.org/rfc/rfc9110.pdf`
(HTTP 200, `application/pdf`, 2,858,365 bytes, SHA-256
`60b30efa1048900833d1758440247fe8ac85a3134f2327388dcb24e07d814c89`, equal to a direct
download made at the same time), job held with completeness `unknown`, the same job and
capture read after a stop and start, pause and resume through `docker exec`, a repeated
submission returned the same job with no second download, and a production-looking root
refused. A linux/amd64 image built, ran under emulation (`uname -m` x86_64), applied the
policy, and refused an overlapping root; it did not fetch.

At the time of the local test, the NAS was not verified. The NAS addendum below
records the later deployment. A published image, long-running behavior, backup,
and restore remain unverified. RFC documents can change; a later fetch that gives a different digest is
recorded with its response and time, never treated as an error.

## NAS trial addendum (2026-11-07 UTC)

The user approved this isolated, no-backup trial. Portainer stack 11 now runs on
the NAS from a locally imported linux/amd64 image, with a named volume under
`/volume1/@docker/volumes/cogvault-trial-data/_data`. The initial Compose
`cpus: 0.5` failed because this Synology Docker kernel lacks the CPU CFS quota
feature; removing that one setting allowed the stack to start. The RFC 9110 PDF
was fetched once and held with a verified SHA-256 and one attempt, and the same
record survived a stack stop/start and image redeployment. The complete run record
is in `docs/deployment/trial.md`. The production gate remains closed.
