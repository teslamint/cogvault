# Source collector: NAS worker deployment design

**Status: review draft. Nothing here has been deployed, and no command below was run against the NAS.** ADR 0027 is still *proposed*; its production activation gate (item 14) is not met. This document describes a safe way to add the collection worker (`cogvault archive job run`, SPEC §9.16 "Collection jobs") after the gate is proven, and how to roll it back. It does not enable production capture.

## What runs where

```
chat clients ──MCP (archive_job_*)──► cogvault serve ──┐
                                                       ├── archive root (NAS local disk)
operator ──docker exec ... job submit──────────────────┤     registry.db, objects/, outbox/, ...
                                                       │
                         cogvault-collector (worker) ──┘──► public internet (outbound only)
```

- **`cogvault serve`** (existing stack, `compose.yaml`) exposes the `archive_job_*` MCP tools only while `archive.capture_enabled` is true and the item 14 check passes.
- **`cogvault-collector`** (new stack, `compose.collector.yaml`) is the only process that downloads. It dials out, has no listener, no wiki mount, and no credentials.
- Both stacks open the **same** archive root on the NAS **local** disk. The registry is SQLite; operations are ordered by `archive.lock` (flock) on that host. Never put the root on SMB, and never open it from another host. The Mac reaches archive data only through the hand-off files (`outbox/`, `receipts/`), as ADR 0027 item 9 says.

## Files in this change (templates, not applied)

| File | Purpose |
|---|---|
| `deploy/nas/compose.collector.yaml` | Worker stack. Post-gate only. Read-only root filesystem, all capabilities dropped, no ports, memory and pid limits. |
| `deploy/nas/env.collector.example` | Variables for that stack. `ARCHIVE_DIR` is deliberately empty. |
| `deploy/nas/config.archive.example.yaml` | The `archive:` block to merge into the NAS `config.yaml`, in two stages. |

`deploy/nas/compose.yaml` and `deploy/nas/config.yaml` are **not changed**. The live config has no `archive:` block, so capture is off today.

## The KCI browser worker (`cogvault-kci`): implemented, no image yet

Jobs with `--executor browser` are KCI article pages. They are run by a **separate binary**, `cogvault-kci`, so the browser library stays out of `cogvault`, the MCP server image. It has the same gate as `job run` (explicit `--config`, matching `archive.root`, `capture_enabled: true`, the item 14 check, a pause while closed, no bypass flag) and the same archive mounts and evidence mounts described below. It opens the article page in headless Chrome, clicks the portal's own "KCI 원문 내려받기" button, and stores the PDF as a held capture. It never logs in or accepts a dialog, and a missing button, login, consent step, challenge, or off-site link ends the job as `human_action_required` with a fixed code (SPEC §9.16 "Executors").

What is **not** here, and is required before it can run on the NAS:

- **An image with a browser.** `cogvault-kci` needs Chrome or Chromium and fonts. The repository's Dockerfile has neither and is unchanged, so there is no compose template for it. A non-root container that cannot use Chrome's sandbox needs `--no-sandbox`, which weakens the browser; prefer a runtime that supports the sandbox.
- **Network scope.** The worker confines the browser to `www.kci.go.kr` itself. A host rule that lets this container reach only that host is an extra layer and is not part of these templates.
- The open decisions in ADR 0027 ("Missing decisions for MCP fetch scope", "KCI official-browser executor"), including a terms review for automating the portal.

To try it on a development host with a Chrome installed (this contacts the real portal once, and is not a NAS procedure):

```
COGVAULT_LIVE_KCI=1 go test ./internal/kcibrowser -run TestLiveKCI -v
```

## The gate

`job run` has one start condition. It needs an explicit `--config`, and it has **no option that skips the gate**. Before it opens the archive and before every claim it re-reads that config and requires all of:

1. `archive.root` equals the worker's `--root`;
2. `archive.capture_enabled: true`;
3. the same item 14 activation check `serve` uses (`archive.deployment` complete, a passing restore-test report for a backup under `backup_target`, and an off-NAS deletion-log replica that covers the live log).

While any is false the process stays up and logs `paused: <code>: <keys>` once per change. It claims nothing and creates nothing under the root. When the config changes it logs `resumed` within `--poll` (30 s here), with no restart. A download already in progress when the switch is turned off finishes; no new job is claimed.

This is a presence check on operator attestations, as ADR 0027 item 14 states. A passing check is not a deployment approval, and it cannot verify that the backup target is off the NAS, that backups run daily, or that the replica stays current.

## Rehearsal before the gate: off the NAS only

ADR 0027 item 14 allows pre-gate work only with temporary roots and no live wiki, NAS, config, or credentials. So there is **no NAS trial stack**, and the worker command has no bypass flag. Rehearse locally:

```
go test ./internal/archive ./cmd/cogvault -run 'Job|Worker' -count=1
```

The package tests run the real queue, lease, retry, SSRF refusal, and worker loop on temporary roots (the loop takes an injected gate in tests only). The browser executor is tested the same way: its contract with a fake driver, and its driver against a real headless Chrome and a local copy of the portal markup (`go test ./internal/kcibrowser`, skipped when no Chrome is installed). What needs the real NAS cannot be rehearsed before the gate: volume ownership can be checked in stage 1 (capture still off), and the NAS outbound path is first exercised by the single operator-submitted job in stage 2, after sign-off.

## Choosing the archive root

`ARCHIVE_DIR` must be a directory that cogvault may treat as an archive root. `Open` creates `objects/`, `outbox/`, `receipts/`, `quarantine/`, `deletions/`, `registry.db`, and `archive.lock` in it.

**Do not assume `/volume1/docker/cogvault/archive` is such a directory.** It already exists and holds manual bundles. It is not known to be empty or to be a cogvault registry root. Before choosing, the operator inventories it read-only, for example:

```
ls -la /volume1/docker/cogvault/archive
test -f /volume1/docker/cogvault/archive/registry.db && echo "has a registry" || echo "no registry"
```

Then decide, and record the decision:

- **A new, dedicated, empty directory** on the NAS local disk, owned by `NAS_UID:NAS_GID`. The manual bundles stay where they are. This is the default recommendation; the directory name is the operator's choice and is not decided here.
- **The existing directory**, only if the operator has confirmed what is in it and wants those files beside the registry. If it contains a `registry.db`, it is an older prototype registry: `Open` upgrades it **in place and one way** to schema 6, so take a backup with the binary that wrote it first.

Importing manual bundles into a registry is a separate task (`archive capture` / `import-report`); this design does not do it.

## Preconditions (all must hold; none is met today)

1. ADR 0027 item 14 settings are real and proven: the archive volume; an off-NAS daily backup with 30-day retention; an off-NAS deletion-log replica; a passing `archive restore-test` that used that replica; a named owner for permissions, credential, and scope; the hand-off mount and paths. A human has reviewed them.
2. The policy decisions for MCP fetch scope are made (ADR 0027, "Missing decisions for MCP fetch scope").
3. A released image contains `archive job` (ADR 0025 release flow). No such release exists yet. Pin the image by digest in the stack once it does.
4. The archive root is chosen and inventoried (above).
5. The evidence directory (`EVIDENCE_DIR`) exists on the NAS host with `backups/`, `reports/`, and `replica/` already in it, and the restore test has been run **inside a container that sees the evidence at the container paths below** (see "The restore test").

## Rollout (after the preconditions)

Each step has an exit check. Stop at the first failed check and use the rollback section.

**0. Back up.** With the current binary, run `archive backup --to <new dir>` on the archive root (if one already exists), and copy the NAS `config.yaml` and both compose files aside.

**Mounts: one set, in both serve and the collector.** The activation check runs in both processes and reads files, so both need the evidence at the **same container paths** that `config.yaml` names:

| Container path | Host source | Mode | Read by |
|---|---|---|---|
| `/config/config.yaml` | `${COGVAULT_DIR}/config.yaml` | ro | both (the existing serve mount already does this) |
| `/data/archive` | `${ARCHIVE_DIR}` | rw | both |
| `/evidence/reports` | `${EVIDENCE_DIR}/reports` | **ro** | `archive.deployment.restore_test_report` |
| `/evidence/replica` | `${EVIDENCE_DIR}/replica` | **ro** | `archive.deployment.deletion_log_replica` |

`backups/` is **not** mounted into the long-running containers: `backup_target` (`/evidence/backups`) is only compared as a path against the path recorded in the report. The collector stack already has these mounts. For serve, add these lines to the `volumes:` of the `cogvault` service in `compose.yaml` (a documented change, not applied by this PR):

```
      - ${ARCHIVE_DIR}:/data/archive
      - ${EVIDENCE_DIR}/reports:/evidence/reports:ro
      - ${EVIDENCE_DIR}/replica:/evidence/replica:ro
```

Use `/evidence/...` in `config.yaml` exactly as in `config.archive.example.yaml`. A different path, a typo, or a missing mount makes the check fail with a coded error, never a silent pass; the tests `TestWorkerGateReadsEvidenceFromConfiguredMountPaths` and the reload cases pin this.

**The restore test.** The restore-test report records the backup path and the replica path exactly as the process that ran it saw them, and the activation check compares them to `config.yaml`. Running it on the host with host paths therefore produces a report the containers reject. Run it once, in a throwaway container with the evidence at the container paths (the archive root is opened read-write because `Open` takes the archive lock):

```
docker run --rm --user ${NAS_UID}:${NAS_GID} --read-only --tmpfs /tmp \
  -v ${ARCHIVE_DIR}:/data/archive \
  -v ${EVIDENCE_DIR}/backups:/evidence/backups:ro \
  -v ${EVIDENCE_DIR}/replica:/evidence/replica:ro \
  -v ${EVIDENCE_DIR}/reports:/evidence/reports \
  -v <scratch dir on the NAS local disk>:/scratch \
  ghcr.io/teslamint/cogvault:<version> \
  archive --root /data/archive restore-test \
    --backup /evidence/backups/<backup name> \
    --report /evidence/reports/restore-test.json \
    --deletion-log /evidence/replica/deletions-log.jsonl \
    --scratch /scratch
```

`--report` must name a file that does not exist, so move an old report aside first. The command exits non-zero if the restore test fails.

**1. Stage 1: archive mounted, capture off.**
- Serve: add the three mounts above and merge the stage 1 block of `config.archive.example.yaml` into `config.yaml` (`capture_enabled: false`, **no** `deployment`). Redeploy serve with the new image.
- Check: serve starts; `tools/list` has no `archive_*` tool; the existing wiki tools work as before.
- Deploy the collector stack (`compose.collector.yaml`, env from `env.collector.example`).
- Check: `docker logs` shows `paused: archive capture activation blocked: capture_disabled`; the container is healthy; `ls` of the archive root shows files owned by `NAS_UID:NAS_GID`; `docker exec <container> ls /evidence/reports /evidence/replica` lists the report and the replica (read-only mounts present).

**2a. Stage 2a: attestations, capture still off, serve restarted.** Only with a human sign-off on the item 14 evidence. `archive.deployment` is **restart-only**: a SIGHUP reload that changes it is rejected (`restart_only_changed`) and changes nothing.
- Add the `deployment` block (container paths) to `config.yaml`, keep `capture_enabled: false`, then **restart serve** (redeploy or `docker restart <serve container>`).
- Check: serve starts with the archive tools still absent; the collector logs the paused reason (`capture_disabled`). The collector re-reads the config by itself and needs no restart.

**2b. Stage 2b: capture on; reload changes only `capture_enabled`.**
- Set `archive.capture_enabled: true` and nothing else, then `docker kill --signal=HUP <serve container>`. A blocked activation is rejected and logged with a code and key names only; capture stays off, and the code says which attestation to fix.
- Check: `tools/list` now shows the `archive_*` tools including `archive_job_submit`; within the worker poll interval the worker logs `resumed`.
- Smoke with one operator-submitted job (public URL, no query): `docker exec <collector container> cogvault archive --root /data/archive job submit <url> --submitter operator`, then `... job status <job-id>`. Expect `held` (bytes stored, completeness `unknown`) and a `collections` entry on the capture.

**The replica must stay current.** The worker re-runs the activation check on every poll, and the check requires the replica to hold every record of the live deletion log. After any quarantine, restore, or purge on the NAS, update the off-NAS replica; until it is current the worker logs `paused: ...deletion_log_replica_not_current` and claims nothing. `serve` checks only at start and on reload, so its tools stay as they are. Changing which files the attestations name (not their contents) needs a serve restart; replacing the report or replica file at the same path does not.

## Switching off, and rollback

- **Kill switch (capture off):** set `archive.capture_enabled: false` in `config.yaml`, then `docker kill --signal=HUP <serve container>`. The MCP tools are removed and the worker pauses within one poll interval. Queued jobs stay queued.
- **Stop the worker:** stop or remove the collector stack in Portainer. A job being downloaded loses its lease and is resumed by the next worker; no data is lost. Removing the stack does not touch the archive root.
- **Roll back the serve image:** a schema 6 registry cannot be opened by an older binary (`unsupported registry schema version 6`), so first set `archive.capture_enabled: false` and remove the `archive:` block from `config.yaml` (serve then never opens the archive), then redeploy the previous `COGVAULT_VERSION`. The archive root is left as it is.
- **Never delete the archive root** as part of a rollback. Restore from a backup only if the registry is damaged, and use the restore procedure that re-applies the deletion log (ADR 0027 item 13).

## Network egress (defense in depth, not verified)

The worker refuses non-public destinations in-process: every DNS answer must be a public address and it connects only to the address it checked, on every redirect. A host-level rule that stops the collector container from reaching the NAS and the LAN is an extra layer, and is deployment specific (for example a firewall rule for the container's network). It is recommended and is not part of these templates.

## Not verified

- No compose file here was deployed or run. The stack YAML was parsed and rendered with sample variables only; `docker compose config` was not available on the review host.
- No run against a NAS: volume ownership, container uid, outbound path, and Portainer behavior are untested.
- **HTTP worker, live:** not exercised. The build host gives freshly built Go binaries no outbound network (the unmodified `main` binary behaves the same), so the HTTP happy path is covered by local `httptest` servers only. A public query URL such as the BOK PDF is refused by design (ADR 0027, "Worked example: the BOK PDF").
- **Browser worker, live:** one official download of the KCI fixture article ran on a development host through headless Chrome (the browser makes the requests, this process only resolves DNS and talks to the browser over loopback), and it matched the operator's earlier copy byte for byte. Not run in a container or on the NAS.
- The restore-test `docker run` command and the read-only evidence mounts were not run. What is tested is the activation gate itself: with real evidence (a backup, an off-root replica, and a real `restore-test` report) under mount-style paths, the gate passes, and it refuses a missing report, a missing replica, a `backup_target` at another path, and a replica at another path (`TestWorkerGateReadsEvidenceFromConfiguredMountPaths`). That `archive.deployment` is restart-only on reload is pinned by a reload test.
