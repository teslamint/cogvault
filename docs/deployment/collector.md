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

The package tests run the real queue, lease, retry, SSRF refusal, and worker loop on temporary roots (the loop takes an injected gate in tests only). What needs the real NAS cannot be rehearsed before the gate: volume ownership can be checked in stage 1 (capture still off), and the NAS outbound path is first exercised by the single operator-submitted job in stage 2, after sign-off.

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

## Rollout (after the preconditions)

Each step has an exit check. Stop at the first failed check and use the rollback section.

**0. Back up.** With the current binary, run `archive backup --to <new dir>` on the archive root (if one already exists), and copy the NAS `config.yaml` and both compose files aside.

**1. Stage 1: archive mounted, capture off.**
- In the serve stack add the bind mount `${ARCHIVE_DIR}:/data/archive`, and merge the stage 1 block of `config.archive.example.yaml` into `config.yaml` (`capture_enabled: false`, no `deployment`). Redeploy serve with the new image.
- Check: serve starts; `tools/list` has no `archive_*` tool; the existing wiki tools work as before.
- Deploy the collector stack (`compose.collector.yaml`, env from `env.collector.example`).
- Check: `docker logs` shows `paused: archive capture activation blocked: capture_disabled`; the container is healthy; `ls` of the archive root shows files owned by `NAS_UID:NAS_GID`.

**2. Stage 2: capture on.** Only with a human sign-off on the item 14 evidence.
- Add the `deployment` attestations to `config.yaml` and set `capture_enabled: true`.
- Signal serve to reload: `docker kill --signal=HUP <serve container>`. A blocked activation is rejected and logged with a code and key names only.
- Check: `tools/list` now shows the `archive_*` tools including `archive_job_submit`; within the worker poll interval the worker logs `resumed`.
- Smoke with one operator-submitted job (public URL, no query): `docker exec <collector container> cogvault archive --root /data/archive job submit <url> --submitter operator`, then `... job status <job-id>`. Expect `held` (bytes stored, completeness `unknown`) and a `collections` entry on the capture.

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
- A live public download was not exercised from the build host (no outbound access for Go binaries there); the happy path is covered by local `httptest` servers.
