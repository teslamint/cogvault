# No-backup public-PDF trial: NAS deployment design

**Status: isolated NAS trial running (2026-11-07). The production gate remains closed.** Policy: `docs/decisions/0028-no-backup-public-pdf-trial.md`. The ADR 0027 item 14 production gate is unchanged and still closed; this trial does not satisfy it.

## What this is, and is not

A separate Portainer stack runs one worker, `cogvault-trial`, that fetches **public https PDFs from an exact host allowlist** into **its own root and registry** in **a dedicated Docker named volume**. The operator has accepted, in writing, that **there is no backup**: if the NAS fails, the trial's originals and job history are lost.

| It is | It is not |
|---|---|
| A way to learn whether the image, the worker, a restart, and one fetch work on the real NAS | A way to enable production capture, or evidence for ADR 0027 item 14 |
| Its own root, registry, and volume; one worker; no inbound port | Connected to the existing stack, wiki, archive, config, credentials, or ports |
| Fixed host allowlist, request limits, PDF-only, https-only | A crawler, a KCI or BOK collector, a login path, or an MCP tool |
| Controlled by `docker exec` / the Portainer console | Handing anything to Cogvault's ingest (the binary does not contain it) |

The existing stack (Portainer stack id 10, mounts under `/volume1/docker/cogvault/*`) is **not touched**. Use a **new stack name** (for example `cogvault-trial`). The named volume `cogvault-trial-data` lives under the Docker root directory (`/volume1/@docker`), physically apart from `/volume1/docker/cogvault`.

## Files

| File | Purpose |
|---|---|
| `Dockerfile` target `trial` | The 21 MB image: `cogvault-trial` on Alpine, non-root uid 1000, `/trial/data` pre-created. The default target is unchanged. |
| `deploy/nas/compose.collector-trial.yaml` | The stack template. Read-only root, `tmpfs /tmp`, all capabilities dropped, `no-new-privileges`, 256 MB, 64 pids, no ports, a restart policy. |
| `deploy/nas/env.trial.example` | The stack's environment variables, including the acceptance sentence. |
| `cmd/cogvault-trial`, `internal/trial` | The program and its policy code. |

## Policy (environment variables)

The worker and a command run with `docker exec` read the same container environment, so one policy applies to both. An unknown `COGVAULT_TRIAL_*` name is refused. A refusal names the setting, never the value.

| Variable | Default | Hard cap / rule |
|---|---|---|
| `COGVAULT_TRIAL_ACK` | required | exactly: `NO-BACKUP-TRIAL: I accept that every original and job record in this trial is lost if the NAS fails` |
| `COGVAULT_TRIAL_ACCEPTED_BY` | required | one line, at most 120 characters (who accepted, and when) |
| `COGVAULT_TRIAL_ALLOWED_HOSTS` | required | exact lowercase DNS names, comma separated, at most 10; no address, wildcard, scheme, or port |
| `COGVAULT_TRIAL_ROOT` | `/trial/data` | absolute, clean, two levels deep, see "Root rules" |
| `COGVAULT_TRIAL_FORBIDDEN_PATHS` | none | extra protected paths (built in: `/volume1/docker/cogvault`, `/data`, `/config`, `/evidence`) |
| `COGVAULT_TRIAL_MAX_QUEUED` | 10 | 1 to 50 |
| `COGVAULT_TRIAL_MAX_SUBMITS_PER_DAY` | 20 | 1 to 100 |
| `COGVAULT_TRIAL_MAX_FETCHES_PER_HOUR` | 20 | 1 to 60, not above the daily limit |
| `COGVAULT_TRIAL_MAX_FETCHES_PER_DAY` | 60 | 1 to 200 |
| `COGVAULT_TRIAL_MIN_INTERVAL` | 10s | 2s to 24h |
| `COGVAULT_TRIAL_MAX_ATTEMPTS` | 3 | 1 to 5 |
| `COGVAULT_TRIAL_MAX_BYTES` | 33554432 | 1024 to 33554432 |

The first smoke test allows only `www.rfc-editor.org`. The RFC Editor's usage notice says RFCs may be freely downloaded, copied, and distributed (modification is limited): <https://www.rfc-editor.org/series/rfc-use/>. KCI and BOK terms are undecided, so neither is on the list.

## Root rules

The worker and every command refuse, before writing anything, a root that: is a symbolic link; overlaps, contains, or sits inside a protected path (compared by real path); has a parent directory that holds `registry.db`, `archive.lock`, `objects`, `.cogvault.db`, `config.yaml`, `_wiki`, or `.obsidian`; or has content but no trial marker (`.cogvault-trial.json`). A new or empty root gets the marker. **Use the named volume only.** A bind mount would hide its host source path from the container, so the in-container guard could not prove it is outside the production directory.

## What the worker does

- **Submit** (`cogvault-trial submit URL`): https only, an allowlisted host, no userinfo or non-default port, at most 2048 characters; refuses a full queue or the daily submission limit.
- **Before every claim** the worker checks the budget (job attempts in the last hour and day, and the minimum interval since the last try), counted from the registry, so it survives a restart. A cross-process claim lock serializes the budget check with the attempt record, so a second worker cannot use the same remaining attempt slot. A redirect can make more than one HTTP request within one attempt; redirect hops are capped separately.
- **After every claim, before any request,** it checks the job again (https, host, executor, attempt cap). A job queued by another route, such as `cogvault archive job submit` on the same root, cannot widen the policy: it ends as failed with "nothing was sent". `browser` jobs are never claimed.
- **Per request:** the public-address rule, the checked-address connection, the redirect limit, and the size and time bounds of the production `acquire`; a redirect to a host that is not allowed is refused before any request to it; only a body that starts `%PDF-` is stored.
- **Result:** a held capture of unknown completeness. The attempt records the response (final URL, status, headers, size, fetch time) and the object's SHA-256. If a public document later changes, the new digest is recorded beside the old response; it is not an error.

## Commands

Run them from Portainer's container console, or `docker exec cogvault-trial cogvault-trial <command>`:

```
cogvault-trial check                 validate the policy and the root (creates nothing)
cogvault-trial submit URL            queue one public PDF       (--label TEXT, --json)
cogvault-trial status [JOB_ID]       one job, or the budget in use     (--json)
cogvault-trial run [--once]          the worker (the container's default command)
cogvault-trial pause | resume        create / remove the PAUSE file; the worker stays up and logs once
cogvault-trial version
```

There is **no delete or cleanup command**.

## NAS smoke test

1. Create a new stack (new name) from `compose.collector-trial.yaml` with the variables from `env.trial.example`. Do not change the existing stack.
2. In the console: `cogvault-trial check`, then `cogvault-trial submit https://www.rfc-editor.org/rfc/rfc9110.pdf`, then `cogvault-trial status JOB_ID` until it is `held`.
3. Compare with a direct download made at the same time: the object digest and size, and record the response and time. A changed RFC is recorded, not a failure. Reference from 2026-11-07 UTC: HTTP 200, `application/pdf`, 2,858,365 bytes, SHA-256 `60b30efa1048900833d1758440247fe8ac85a3134f2327388dcb24e07d814c89`.
4. Stop the container in Portainer and start it again; `cogvault-trial status JOB_ID` must show the same job and object.
5. `cogvault-trial pause`, confirm the worker logs `paused:` once and stays up, then `resume`.
6. Submit the same URL again: it returns the same job, with no second download. Submit a URL on another host: it is refused.

Fetch the file **once**. Do not repeat it for timing.

## Getting the image onto the NAS

No image is published. The approved trial used the local image import option: build the amd64 image on a development machine (`docker build --platform linux/amd64 --target trial -t cogvault-trial:amd64 .`), `docker save` it, and use Portainer's **Images, Import**; or publish it through the release flow of ADR 0025 and pin the digest in `COGVAULT_TRIAL_IMAGE`. The Dockerfile pins the Alpine base by digest.

## Manual cleanup (documented, never automatic)

When the trial ends and its data is no longer wanted: stop the stack, then, as an explicit operator action in Portainer, remove the stack and the `cogvault-trial-data` volume. Nothing in the program does this. Keep the volume if the job history is still wanted. Removing the volume loses the trial's originals and job history, which is the accepted outcome.

## What was verified, and what was not

Verified on a development Mac (isolated containers, a named volume, no NAS): the package and command tests; a linux/arm64 container with the stack's constraints doing one real download of the RFC 9110 PDF (2,858,365 bytes, SHA-256 as above, held, `completeness unknown`), the same job read after a stop and start, pause and resume, a repeated submission returning the same job, and a production-looking root refused; a linux/amd64 image built, run under emulation (`uname -m` is x86_64), applying the policy and refusing an overlapping root, without fetching.

Still unverified: backup and restore after NAS failure, long-running behavior, a published release image, and production ingest. The NAS run below verified native x86_64 execution, Portainer deployment, volume ownership, the outbound path, and stack restart persistence.

## NAS run record (2026-11-07 UTC)

Portainer endpoint 1, separate stack ID 11 `cogvault-trial`, status active. The final
imported image is `cogvault-trial:sha-01bd371` (linux/amd64, image ID
`sha256:3008b80659bbebecbd9e682ce3008c689db4cf80dfff86feebf4577c31ea5827`).
The stack uses named volume `cogvault-trial-data`, mounted at `/trial`, with Docker
mountpoint `/volume1/@docker/volumes/cogvault-trial-data/_data`. There is no
production path mount and no port binding. Docker inspect showed a read-only root,
all capabilities dropped, `no-new-privileges`, and a 256 MiB memory limit. The
Synology kernel rejected Compose `cpus: 0.5` (CPU CFS quota unsupported), so that
setting was removed before the successful deployment. Docker inspect returned
`PidsLimit: null` despite the Compose setting; a process limit is therefore not
claimed as verified on this host.

The only allowed host was `www.rfc-editor.org`, with two queued jobs, two submissions
per day, one fetch per hour, two per day, 30 seconds between attempts, and two
attempts maximum. `check` passed. Submitting RFC 9110 PDF created job
`job_881ca154ea435c7fb0840ff92d969689`. One attempt ended `held`, capture
`cap_ca5af47bebd8fe960208f9c5ff55708a`, 2,858,365 bytes, object
`sha256:60b30efa1048900833d1758440247fe8ac85a3134f2327388dcb24e07d814c89`.
A `sha256sum` of the actual file on the mounted volume matched that object ID and
the direct download made at the same time. The response recorded HTTP 200,
`application/pdf`, final URL, resolved address, response time and ETag. This is a
**post-research recovery**, not the original viewed during research, and its
completeness is unknown; ingest stayed held.

A request to `example.com` was refused before submission. Submitting the same RFC
URL again returned the same job, with one attempt. `pause`, status, and `resume`
worked. The Portainer stack was stopped and started; the named volume remained,
and the same trial ID, job ID, attempt count, capture, and object ID were readable
again. Redeployment to the corrected image kept them too. The existing production
`cogvault` container remained healthy. This proves persistence across container
and stack restarts on this NAS, not survival of a NAS failure or a restore from
backup.

The trial is still running. To stop collection without deleting data, stop stack
11 in Portainer. Do not remove the named volume unless the trial history is no
longer wanted.
