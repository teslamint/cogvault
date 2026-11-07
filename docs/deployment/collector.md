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
| `Dockerfile` target `kci` | The browser worker image: headless Chromium (exact apk version, digest-pinned base) plus `cogvault-kci`. The default target is unchanged. |
| `deploy/nas/compose.collector-kci.yaml` | The browser worker stack. Post-gate only; **not to be applied** before ADR 0027 item 14 and a terms review. |
| `COGVAULT_KCI_IMAGE` in `env.collector.example` | The published `kci` image, pinned by digest. Empty: nothing publishes it yet. |

`deploy/nas/compose.yaml` and `deploy/nas/config.yaml` are **not changed**. The live config has no `archive:` block, so capture is off today.

## The KCI browser worker (`cogvault-kci`)

Jobs with `--executor browser` are KCI article pages. They are run by a **separate binary**, `cogvault-kci`, so the browser library stays out of `cogvault`, the MCP server image. It has the same gate as `job run` (explicit `--config`, matching `archive.root`, `capture_enabled: true`, the item 14 check, a pause while closed, no bypass flag) and the same archive and evidence mounts. It opens the article page in headless Chromium, clicks the portal's own "KCI 원문 내려받기" button, and stores the PDF as a held capture. It never logs in or accepts a dialog, and a missing button, login, consent step, challenge, or off-site link ends the job as `human_action_required` with a fixed code (SPEC §9.16 "Executors"). A PDF is attributed to the official control only when its document request has a user gesture and a `fncDown` initiator; absent evidence is canceled, and this has been tested locally in Chrome, not on the live KCI portal.

**Do not apply `compose.collector-kci.yaml`, and do not enable capture, until all three hold:** (1) ADR 0027 item 14 is proven and reviewed; (2) a **terms and permission review** of automating the KCI portal is recorded (see below: free full text being offered is not permission to collect or redistribute it); and (3) the permissions owner has **approved running Chromium with `--no-sandbox`**, or a runtime where its sandbox works is used. The gate alone keeps the worker paused, but the template is not to be deployed before then.

### The image (Dockerfile target `kci`)

```
docker build --target kci --build-arg VERSION=<id> -t cogvault-kci:<tag> .
```

- **Default target unchanged.** The `kci` stages sit before the final stage, so `docker build .` (and the `container` workflow) still builds the `cogvault` image: checked, 37.7 MB, no Chromium, no `cogvault-kci`.
- **Pins.** Base `alpine:3.22@sha256:bfe7ceb9…c66b034`; Chromium by exact apk version `chromium=142.0.7444.59-r0` (build arg `CHROMIUM_VERSION`). Alpine drops a superseded chromium from its index, so a stale pin makes the build **fail** instead of silently upgrading. Bump the version deliberately, re-run the checks below, and update the digest when the base moves. A pin also means Chromium security updates arrive only when someone bumps it.
- **Runs as uid 1000 `cogvault`** by default; the compose file overrides it with `NAS_UID:NAS_GID`. `ENTRYPOINT` is `cogvault-kci`, so the image has no shell-based start path. It has no `cogvault` binary: submit jobs through the serve or HTTP-collector container.
- **Not published.** No workflow builds or pushes this target. Publishing it (tags, a multi-platform build, recording the digest) is a release decision this PR does not make; `COGVAULT_KCI_IMAGE` must be a digest reference.

### The stack (`compose.collector-kci.yaml`)

| Setting | Value | Why |
|---|---|---|
| `image` | `${COGVAULT_KCI_IMAGE}` (digest) | no floating tag |
| `user` | `${NAS_UID}:${NAS_GID}` | the account that owns the archive root |
| `read_only`, `tmpfs` | read-only root; `/tmp:size=1g` | the profile and the download live in `/tmp` and are removed after each fetch |
| `cap_drop`, `security_opt` | `ALL`; `no-new-privileges` | no extra privilege |
| `mem_limit`, `cpus`, `pids_limit` | `1g`, `1.0`, `256` | bounded; passed the checks below, not tuned for the NAS |
| ports, `network_mode`, `privileged` | none | no listener; the worker and browser only dial out |
| mounts | config `:ro`; archive rw; `/evidence/reports`, `/evidence/replica` `:ro` | same paths as the serve stack and `config.yaml` |
| command | `--root`, `--config`, `--chrome=/usr/bin/chromium`, `--no-sandbox`, no bypass flag | gate unconditional |

**`--no-sandbox` needs an operational approval; it is a real weakening.** Chromium's own documentation (`docs/security/apparmor-userns-restrictions.md`) says `--no-sandbox` disables critical security features and must not be used to browse the open web. In this container (non-root, all capabilities dropped, default seccomp) Chromium's sandbox does not start on the engine used here (exit 139 during zygote startup), and a Synology kernel may lack the user namespaces it needs, so the template passes the flag. This stack visits only `www.kci.go.kr`, but that page's scripts and the downloaded file are third-party content and the article ID can come from a job a client submitted. The compensating controls are the container limits, a browser confined to that host by the worker itself (pinned resolver plus a request guard), a fresh profile per fetch, no credentials, and no ports. They reduce exposure; they do not replace the sandbox. Whether to accept the risk is **not decided here**; it is for the permissions owner, and the alternative is a runtime that supports the sandbox.

### Validation record (what was run, exactly)

Engine: a Podman 6.0.2 (buildah 1.44.1) engine behind the `docker` CLI on macOS arm64. `docker buildx` and the `docker compose` plugin were not available. Everything below was local, on temporary roots; nothing touched the NAS.

**linux/arm64, native in the engine VM**
- `docker build --target kci` succeeded; 744 MB; `chromium --version` is `Chromium 142.0.7444.59`.
- Under the compose constraints (uid 1026:100, read-only root, `/tmp` tmpfs 1g, all capabilities dropped, `no-new-privileges`, 1g memory, 1 CPU, 256 pids): the worker refused to start with no arguments, with a missing config, and with a bypass flag; Chromium started and rendered with `--no-sandbox` and exited 139 without it.
- The driver tests ran **inside the image** with its Chromium and the same constraints (`COGVAULT_BROWSER_TESTS=1`): all passed, including the byte-for-byte download, the eight non-download classifications, the off-site block, and the queue-to-capture chain.
- A live run inside the image (`COGVAULT_LIVE_KCI=1 COGVAULT_KCI_NO_SANDBOX=1`) downloaded the fixture article: SHA-256 `75989f52…c405143c4`, equal to the operator's earlier browser copy; a nonexistent article ended `human_action_required`.
- **Rehearsal of the deployed shape** with the template's flags, limits, user, and mounts at the same container paths, on a temporary root: queue a browser job; make a backup, an empty replica, and a real `restore-test` report in containers; then the worker with capture off was refused (`capture_disabled`); with capture on but the evidence mounts omitted it was refused (`restore_test_not_passed`); with the read-only evidence mounts it passed the gate, claimed the job, drove Chromium to the live portal, and ended `held` (606,425 bytes, SHA-256 as above, `seen_during_research`, worker name `nas-kci`).

**linux/amd64 (the NAS architecture: Synology x86_64, Docker 24.0.2, as reported by Portainer)**
- `chromium-142.0.7444.59-r0` exists in Alpine v3.22 `community` for x86_64 (`apk policy` and `apk search -e`, run in an amd64 container).
- `docker build --platform linux/amd64 --target kci` **succeeded**: image architecture `amd64`, 837 MB; `cogvault-kci` is an x86-64 ELF; `chromium --version` prints `142.0.7444.59`; the worker's gate refusals behave as on arm64.
- **Chromium did not launch to completion under QEMU emulation.** A headless run stayed up for about seven minutes with a Chrome `FATAL` (GPU process not usable) and a `ptrace` "Function not implemented", and a second attempt capped at 60 s did not finish. User-mode emulation lacks syscalls Chromium uses, so this is an emulation limit and **not** evidence about a native x86 host, and it is **not a pass**. Both containers were removed. **Chromium on a real x86 host is unverified.**

### Unverified (exact)

1. **Chromium running on x86_64 at all** (see above), and therefore the amd64 driver tests and the live download on x86.
2. **Docker 24.0.2 and Synology:** the compose keys used (`mem_limit`, `cpus`, `pids_limit`, `read_only`, `tmpfs` with size, `cap_drop`, `security_opt`) were not run on that Docker or through Portainer; neither were Synology's cgroup limits, seccomp default, kernel user namespaces, or `/dev/shm`. The template avoids `/dev/shm` through `--disable-dev-shm-usage`, which the worker adds with `--no-sandbox`.
3. **File ownership on the NAS volume** (`NAS_UID:NAS_GID` writing the archive root); the engine VM maps ids differently and says nothing about Synology.
4. **Compose rendering** was done with a small script that substitutes variables; `docker compose config` was not available. The YAML parsed, one service rendered with no ports, no `privileged`, no `network_mode`, and each required variable failed when missing.
5. **No published image:** the digest to deploy does not exist yet, and a multi-platform build was not run end to end (arm64 native and amd64 emulated were built separately).
6. **Resource sizes** (1 GiB memory, 256 pids, 1 GiB `/tmp`) passed these checks on a small page and one 606 KB download; they are not tuned for the NAS.
7. **CI:** the real-browser tests are opt-in (`COGVAULT_BROWSER_TESTS=1`) so a browser launch on an unknown runner cannot block publishing the `cogvault` image; CI therefore does not run them.
8. **Terms and permission.** The KCI FAQ says free full text is offered where the society consented. That is availability to a reader; it is not a statement that automated collection, copying, or redistribution is allowed, and no terms review has been done. Also unverified: a real non-public article, and real login or challenge pages (ADR 0027).

### Rollout (after the preconditions) and rollback

1. **Publish and record.** Build the `kci` target for `linux/amd64`, push it, and record the digest. Pull that digest on a non-NAS x86_64 host and run the checks in the validation record, in particular **Chromium actually starting on native x86**. Set `COGVAULT_KCI_IMAGE` to the digest.
2. **Pre-flight on the NAS, archive untouched.** Run the image once with `--entrypoint chromium` and the compose limits (`--headless=new --no-sandbox --disable-gpu --disable-dev-shm-usage --dump-dom data:text/html,<p>ok</p>`), to prove the kernel and limits allow it. No archive mount.
3. **Deploy the stack with capture off** (stage 1 or 2a of the rollout above). Expect `paused: …capture_disabled` and a healthy container.
4. **After stage 2b** (capture on, gate passing) the worker logs `resumed`. Smoke with the fixture URL as a browser job, submitted from the serve or HTTP-collector container: `docker exec <container> cogvault archive --root /data/archive job submit '<KCI article URL>' --submitter operator --executor browser`. Expect `held`, a PDF capture of unknown completeness, and the operator's reference hash if the article is unchanged.
5. **Kill switch:** `capture_enabled: false` plus `docker kill --signal=HUP` on serve pauses this worker within one poll; a fetch in progress finishes.
6. **Rollback:** stop or remove the `cogvault-kci` stack. A fetch in progress loses its lease and the next worker resumes it; browser jobs stay queued. To roll the image back, set `COGVAULT_KCI_IMAGE` to the previous digest and redeploy. The archive root and the registry schema are not changed by this worker, so nothing is restored.

To run the real-browser tests on a development host (the live test contacts the portal once and is not a NAS procedure):

```
COGVAULT_BROWSER_TESTS=1 go test ./internal/kcibrowser -v
COGVAULT_BROWSER_TESTS=1 COGVAULT_LIVE_KCI=1 go test ./internal/kcibrowser -run TestLiveKCI -v
```

Inside the image, build the test binary for the image's architecture (`go test -c`), mount it from a directory the engine shares, and run it with `--entrypoint` and the compose constraints; pass `COGVAULT_KCI_NO_SANDBOX=1` for the live test.

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

The package tests run the real queue, lease, retry, SSRF refusal, and worker loop on temporary roots (the loop takes an injected gate in tests only). The browser executor is tested the same way: its contract with a fake driver, and its driver against a real headless Chrome and a local copy of the portal markup (`COGVAULT_BROWSER_TESTS=1 go test ./internal/kcibrowser`; opt-in, so a browser launch cannot block image publishing in CI). The compose shape itself was rehearsed on temporary roots in containers (see "Validation record" under the KCI browser worker). What needs the real NAS cannot be rehearsed before the gate: volume ownership can be checked in stage 1 (capture still off), and the NAS outbound path is first exercised by the single operator-submitted job in stage 2, after sign-off.

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

## What can be deployed today, and what must be decided first

**Nothing in this directory can be deployed to the NAS today, and no backup is assumed.** ADR 0027 item 14 requires an off-NAS backup with a confirmed daily schedule and a passed restore test before any production capture. The cost of an off-NAS backup is not decided, so this design does not make adopting one the default plan: it only records that the gate stays closed without one. Item 14 also limits any gate-free prototype to temporary roots with no live wiki, NAS, config, or credentials, so even a separate temporary Portainer stack on the live NAS conflicts with it. The existing stacks, data, and security settings are not changed by this PR.

| Allowed now | Not allowed now |
|---|---|
| Unit and package tests; isolated containers **on a development Mac** on temporary roots (this document's "Rehearsal" and "Validation record"); static checks of the amd64 image | Any run on the live NAS or Portainer, including a temporary stack; real credentials or the live config; enabling capture; publishing an image; a live request to KCI beyond the one recorded download |

**The one exception is the explicitly approved no-backup trial (ADR 0028, `docs/deployment/trial.md`)**: a separate binary, stack, root, and registry for public https PDFs on an exact allowlist, which does not use or satisfy the item 14 gate. It is also not deployed or applied; its own document lists what may be run and what was verified. The rest of this document (the production worker and the KCI worker) is unchanged and still blocked.

Decisions a human must make before the template can be used (each needs an ADR, or an update of ADR 0027 that supersedes the item it changes):

1. **Durability policy (item 14).** Pay for and run an off-NAS backup, replica, and restore test, **or** write a new ADR that replaces item 14 with a cheaper policy and says what loss is accepted. Without one of these the worker stays paused.
2. **Outbound requests and scope** (ADR 0027 "Missing decisions for MCP fetch scope", 1 to 8): whether the archive host may fetch at all, who may trigger it, destinations, rate, terms handling, and retention.
3. **Source terms.** Whether KCI's and BOK's terms allow automated collection (the KCI FAQ's free full text is availability to a reader, not permission to automate or redistribute), and whether the BOK source policy may cover more than one fixture.
4. **`--no-sandbox`**: approval from the permissions owner for the browser container, or a runtime where Chromium's sandbox works.
5. **Where Chromium is proven on x86_64.** The NAS is not a test bed, so native x86_64 evidence needs another host (for example an x86_64 development machine or CI runner) before the amd64 image is trusted.

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
      - type: bind
        source: ${EVIDENCE_DIR}/reports
        target: /evidence/reports
        read_only: true
        bind: {create_host_path: false}
      - type: bind
        source: ${EVIDENCE_DIR}/replica
        target: /evidence/replica
        read_only: true
        bind: {create_host_path: false}
```

The long form with `create_host_path: false` makes a missing `reports/` or `replica/` fail the deploy; the short form would create an empty directory (Docker's Compose file reference, "volumes"). The collector templates use it. It was checked as YAML only: it was not run on Portainer 2.45.2 or on Docker 24.0.2, and a Portainer stack is not to be applied (see "What can be deployed today").

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
- **HTTP worker, live:** run **inside a container** on an isolated temporary root (the host's own Go binaries have no outbound network, the unmodified `main` binary behaves the same, but containers do): the BOK public PDF was queued as an HTTP job, the worker passed the real gate on locally generated evidence, followed the real signed redirect, and the job ended `held` with the expected 2,744,626 bytes (SHA-256 `a7e925e7…aaca1f`); the registry holds no token. That is not a NAS run. The URL is admitted by the audited source policy (ADR 0027, "Audited source policies"); BOK's terms and robots rules were not reviewed.
- **Browser worker, live:** one official download of the KCI fixture article ran on a development host through headless Chrome, and again inside the arm64 `kci` image container under the template's constraints (see "Validation record"); both matched the operator's earlier copy byte for byte. The browser makes the requests; this process only resolves DNS and talks to the browser over loopback. Not run on the NAS or on native x86_64.
- **Restore test and evidence mounts, local only.** The documented restore-test form (`docker run` with `--read-only --tmpfs /tmp`, all capabilities dropped, the archive at `/data/archive`, `backups/` and `replica/` read-only, `reports/` writable, `archive --root /data/archive restore-test --backup … --report … --deletion-log … --scratch …`) was run in containers on temporary roots, with a locally built image tag (the default `cogvault` image; the `kci` image with a mounted `cogvault` binary), and its report was then accepted by the activation gate in a worker container that mounted `reports/` and `replica/` read-only. It was **not** run with a published image, on Docker 24.0.2, through Portainer, or on Synology paths and permissions. The gate is also covered by package tests: with real evidence under mount-style paths it passes, and it refuses a missing report, a missing replica, a `backup_target` at another path, and a replica at another path (`TestWorkerGateReadsEvidenceFromConfiguredMountPaths`). That `archive.deployment` is restart-only on reload is pinned by a reload test.
