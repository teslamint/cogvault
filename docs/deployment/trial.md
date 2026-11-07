# No-backup public-PDF trial: NAS deployment design

**Status: isolated NAS trial running (2026-11-07). The production gate remains closed.** Policy: `docs/decisions/0028-no-backup-public-pdf-trial.md`. The ADR 0027 item 14 production gate is unchanged and still closed; this trial does not satisfy it.

## What this is, and is not

A separate Portainer stack runs one worker, `cogvault-trial`, that fetches **public https PDFs from an exact host allowlist** into **its own root and registry** in **a dedicated Docker named volume**. The operator has accepted, in writing, that **there is no backup**: if the NAS fails, the trial's originals and job history are lost.

| It is | It is not |
|---|---|
| A way to learn whether the image, the worker, a restart, and one fetch work on the real NAS | A way to enable production capture, or evidence for ADR 0027 item 14 |
| Its own root, registry, and volume; one worker; no inbound port | Connected to the existing stack, wiki, archive, config, credentials, or ports |
| Fixed host allowlist, request limits, PDF-only, https-only | A crawler, a KCI or BOK collector, a login path, or a production MCP tool |
| Controlled by `docker exec` / the Portainer console | Handing anything to Cogvault's ingest (the binary does not contain it) |

The existing stack (Portainer stack id 10, mounts under `/volume1/docker/cogvault/*`) is **not touched**. Use a **new stack name** (for example `cogvault-trial`). The named volume `cogvault-trial-data` lives under the Docker root directory (`/volume1/@docker`), physically apart from `/volume1/docker/cogvault`.

## Trial chat MCP software (not deployed)

The separately approved software adds `cogvault-trial serve`, defaulting to
stdio, and optional authenticated Streamable HTTP. Only `trial_submit(url)` and
`trial_status(job_id?)` are exposed. Neither tool offers downloads, worker
controls, Docker/Portainer access, or wiki/ingest access. Submission uses the
worker's policy service; status is sanitized rather than raw archive JSON.

HTTP requires authentication even on loopback, an explicit pathless public HTTPS
URL and an exact validated endpoint path (default `/mcp`). Bearer or OAuth can be
used; OAuth requires a distinct trial resource audience and required scopes.
Bodies are bounded and foreign Origins are refused. Do not reuse production
resource authorization. No credentials, grants, client registrations, proxy
routes or network changes are created by this software change. Deployment and
those access changes require separate approval. There is no verified trial
HTTP endpoint or connected ChatGPT/Claude client yet. The stack template remains
worker-only with no published ports; it does not enable the MCP server.

### Software interface reference (not deployment instructions)

`serve` accepts flags only. `--transport` defaults to `stdio`; `http` is opt-in.
HTTP flags are `--listen` (default `127.0.0.1:8081`), `--public-url` (required,
pathless HTTPS), `--endpoint-path` (default `/mcp`), and `--auth-mode` (`bearer`
or `oauth`). OAuth uses `--oauth-issuer`, `--oauth-audience` (exactly public URL
plus endpoint path), and `--oauth-scopes` (space-separated required scopes).
The sole transport environment variable is `COGVAULT_TRIALMCP_BEARER_TOKEN`;
unknown `COGVAULT_TRIALMCP_*` names fail closed. The separate
`COGVAULT_TRIAL_*` policy namespace remains strict. Stdio refuses HTTP options
and credential settings. These names document the software, not authorization
to create credentials or activate a route.

Submission returns a job view and `existing`. Job views contain only `job_id`,
`state`, `attempts_made`, and `max_attempts`. Status may include a capture view
with `sha256`, `size`, and allowlisted `completeness`. With no job ID, it returns
`paused`, `waiting_or_running`, `requests_last_hour`, `requests_last_day`, and
`submissions_last_day`. Stored URLs are omitted, including valid public URLs;
raw archive records and arbitrary error details are never returned.

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

The table describes generic program defaults, not permission to widen the NAS
trial. The repository stack/environment templates use the recorded deployment
limits: two queued jobs, two submissions/day, one fetch/hour, two fetches/day,
30 seconds between attempts and two attempts maximum. Changing the host set or
these limits requires separate operator approval.

The deployed trial allows only `www.rfc-editor.org`. The RFC Editor's usage notice says RFCs may be freely downloaded, copied, and distributed (modification is limited): <https://www.rfc-editor.org/series/rfc-use/>. KCI and BOK terms are undecided, so neither is on the list.

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

## Internal MCP staging record (2026-11-07 UTC)

Stack 11 now also runs `cogvault-trial-mcp` from the locally built and imported
linux/amd64 image `cogvault-trial:mcp-worktree`, image ID
`sha256:ccb68c7d67b698c5cc8a220c087991a6e64a25de2e096d044e1e08fd51473f9c`.
The build used the tested, uncommitted worktree based on `ff0c367`; the SHA-256 of
its sorted path-to-file-SHA-256 JSON manifest is
`91f9e204ba45a2d3a7cb3bc94c857b52c97ec04a215e35655b4844d319a5d028`.
This identifies the built snapshot **before this documentation-only run record**.
No source commit or registry publication was made.

The listener shares only the existing trial named volume and exact worker policy.
Its sole network, `cogvault-trial-proxy`, is durably declared in stack 11 and has no
other member yet. No host port is published. The original trial worker,
production wiki service, and cloudflared retained their container IDs, start
times, and zero restart counts; the production service remained healthy. The
existing worker definition, environment, and no-backup acceptance were preserved.

Internal HTTP checks through the container console verified:

- `/trial/mcp`: 401 with a Bearer challenge pointing to
  `https://cogvault.guu.moe/.well-known/oauth-protected-resource/trial/mcp`.
- That discovery path: 200, resource `https://cogvault.guu.moe/trial/mcp`, issuer
  `https://teslamint.jp.auth0.com/`, scopes `trial:submit` and `trial:status`.
- `/mcp` and `/trial/mcp/`: 404.
- The existing RFC 9110 job above: still held, one attempt, same capture and object.
  No submission or fetch was invoked for these checks.

**At this staging checkpoint, not yet public or client-ready:** Auth0
resource/scope configuration awaited sign-in. The tunnel configuration and cloudflared network membership remain
unchanged. Pending work is to configure Auth0, apply the prepared anchored trial
and discovery routes plus durable cloudflared proxy-network membership, verify
only cloudflared is recreated and production stays unchanged, then test the
public endpoints and genuine ChatGPT/Claude OAuth connections. Internal 401 and
discovery checks do not establish successful token validation or client access.

## Trial tunnel activation record (2026-11-07 UTC)

After the Auth0 trial resource and the existing client's user-delegated grant for
`trial:submit` and `trial:status` were configured, the staged listener was exposed
through the existing tunnel. The earlier internal-only record describes the
pre-activation state; the image and source snapshot above were not rebuilt.

The tunnel now matches these rules before its unchanged production host rule:

1. `^/trial/mcp$` forwards to `http://cogvault-trial-mcp:8081`.
2. `^/\.well-known/oauth-protected-resource/trial/mcp$` forwards to the same
   listener without rewriting the path.
3. `^/trial/` returns HTTP 404, preventing unmatched trial paths from falling
   through to the production service.

Stack 10 durably declares cloudflared on both its original default network and
the external `cogvault-trial-proxy` network. The production service definition
and stack environment were unchanged; no image pull or forced recreation was
requested. Only cloudflared was recreated, then restarted once to load the
fail-closed unmatched-path rule. The proxy network contains only cloudflared and
the trial listener. Production and the original trial worker retained their
container IDs, start times, environment and mounts, with zero restart counts;
production remained healthy. No host ports, new fetches, or data deletions were
introduced.

Public HTTPS checks passed: `/trial/mcp` returned 401 with the trial-suffixed
resource metadata challenge; discovery returned 200 with the exact trial
resource, issuer and two scopes; `/trial/mcp/` and `/trial/unknown` returned 404.
Production `/mcp` retained its original 401 challenge, and bare protected-resource
discovery returned the same body as before activation.

Rollback copies of the original tunnel and stack configuration were retained
with the deployment artifacts. Restore both together and recreate only
cloudflared to remove trial exposure; preserve the trial named volume and worker.
At this activation checkpoint, client credential setup and genuine
ChatGPT/Claude OAuth connection checks were pending. The public checks above
verify routing and unauthenticated discovery; authenticated status checks are
recorded below.

## Authenticated client verification (2026-11-07 UTC)

Both ChatGPT and Claude completed OAuth using the existing confidential Auth0
client, its already-allowed callback, and exactly the trial permissions
`trial:submit` and `trial:status`. No callback or permission expansion was needed.
The operator entered the client secret directly in the official setup forms and
completed service-user sign-in; the assistant did not access the secret.

The active ChatGPT connection uses the native trial plugin, version **1.0.1**,
including the `trial-archive` skill. Native `trial_status` calls successfully read
both the budget and the existing RFC job. At verification, collection was not
paused, waiting and running counts were zero, requests in the last hour were zero,
requests in the last day were two, and submissions in the last day were two.

Claude performed exactly one authorized status-only call; its expanded request
and actual tool response were checked.
Both clients returned job `job_881ca154ea435c7fb0840ff92d969689` as held, with one
of two attempts used, a 2,858,365-byte capture, object SHA-256
`60b30efa1048900833d1758440247fe8ac85a3134f2327388dcb24e07d814c89`, and unknown
completeness.

No submission test was run because the rolling daily submission limit was
exhausted. No limit was altered, and these checks created no job, fetch or retry.
Authenticated status access is verified; authenticated submission remains
untested in these clients.

The initial portable plugin remains unconnected and unused, pending authorization
to delete it; it is not the active native connection above. The automatically
created Auth0 Test Application was left untouched.

## Selected-source report trial software

This section describes the software workflow; the completed import-only
deployment is recorded below. Do not change live hosts, paths, media, request limits, authorization or network routes
merely because these options exist. Preserve the existing trial volume and
registry when updating an image: consumed attempt/submission budgets must not
reset. No new Auth0 scope or route is needed for these trial-only helpers.

1. Prepare a selected-source manifest from an already completed report. Keep the
   report body, raw export, research activity and attachments outside it. Explicitly
   select at most five sources; larger input is rejected, never silently reduced.
   A `body_url` with `citation_count: 0` is not a formal citation. The selected-body
   digest does not prove complete report/source coverage or citation coordinates.
2. Register with `trial_import_manifest(manifest)` or local
   `cogvault-trial import-manifest --file selected-sources.json`. The former takes
   a JSON string, not a path. Import returns `capture_id`, `object_id`, `existing`
   and does not fetch or queue anything. The opaque report ID or returned capture
   ID identifies later report status. There are ten report slots; immutable
   identical repeats do not consume another slot.
3. Use `trial_report_status(report_id)` or `cogvault-trial report-status REPORT_ID`.
   It separates each stored capture's hash, media, completeness and provenance
   from external-copy claims. An external verified-original reference alone is
   not trial preservation. Unacquired sources stay visible.
4. With exact file-copy approval, an operator can import one selected local file:
   `cogvault-trial import-original --report REPORT_ID --source SOURCE_ID
   --file SELECTED_FILE --sha256 sha256:EXPECTED_HEX --bytes EXPECTED_BYTES
   --media application/pdf --completeness unknown --method existing_file`.
   HTML uses `--media text/html`; an abstract/access response is `incomplete`,
   never promoted because a PDF exists. `browser_download` or `direct_https`
   records a prior method as an operator statement, not a new request. The CLI
   refuses symlinks, nonregular files, wrong bytes/hash/media and files over 32 MiB.
   Multiple captures remain distinct, capped at five per source. No MCP tool can
   read a path/upload bytes, and no production archive should be mounted into
   the trial container. Deliver only the explicitly selected files by an approved
   operator route; do not give the collector access to their containing archive.
5. Future HTTP collection needs separate robots/terms review and specific
   operator policy. The software does not perform those checks. Configure
   `COGVAULT_TRIAL_ALLOWED_URLS` as a JSON array of exact initial HTTPS URLs and
   `COGVAULT_TRIAL_REDIRECT_URLS` as exact destination URLs without temporary
   signatures. Hosts must already be allowlisted. An empty initial list refuses
   report fetches. `MEDIA_TYPES=pdf,html` is opt-in; default `pdf` is unchanged.
   `MAX_REDIRECTS` is 0..5 (default 5). Values belong to `COGVAULT_TRIAL_*`.
   The archive still validates signed redirects itself. A report cannot add
   hosts, paths or arbitrary queries. KCI browser/network collection remains out.
6. Only then submit an HTTP-mode source using `trial_submit(url, report_id,
   source_id)` or `submit --report REPORT_ID --source SOURCE_ID URL`. Manual
   sources cannot be submitted. Legacy `trial_submit(url)` still works under
   its existing host/PDF policy. HTML is not rendered and no linked resource
   is followed. Supported network HTML is UTF-8/ASCII identity data with matching
   MIME; recognizable login/challenge pages and 429 stop for human action.

The `requests_last_hour/day` keys are retained compatibility aliases for charged
job attempts, not measured HTTP requests. `attempts_last_hour/day`,
`budget_unit: job_attempts`, and `max_http_requests_per_attempt` give the true
units and bound. Each attempt sends at most 1 + MAX_REDIRECTS HTTP requests, so an
hour/day attempt allowance B has an upper bound B*(1+MAX_REDIRECTS). Redirect hops
are not independently spaced. New software preserves existing rolling counters.
Neither file import nor manifest registration consumes/frees network capacity.

A minimal manifest is:

```json
{"version":1,"report_id":"report-example","selected_sha256":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","sources":[{"id":"S0001","citation_url":"https://example.org/paper","relation":"citation","citation_count":1,"mode":"manual"}]}
```

This is a schema example, not an acquisition approval or live endpoint. The hash
is illustrative. HTTP-mode sources may have a separate `acquire_url` for an
explicitly approved public copy. Optional `external_original` has `sha256`,
`size`, and `verification` (`unknown`, `incomplete`, `verified`) and is only a
reference claim. No report title/body, transcript, activity log or local path is
accepted by the remote schema. Do not paste a raw Deep Research JSON export.

## First selected-report import record

The approved first real-report trial replaced only the trial MCP listener with
the locally built linux/amd64 image
`sha256:e0f30c702890b27917967299e283d20f64047c385b934485eaf956ff700b3517`.
The reviewed build input contained 433 files; its canonical sorted
path-to-SHA-256 manifest digest was
`d2438029a704c87268a3fa47cf3ef364c380acc008423412e3067bd5fc4bd7ac`.
This identifies the uncommitted build snapshot, not a published commit or image
release. This operational record was added afterward without code changes.

Three explicitly selected existing files, totaling 3,454,694 bytes, were copied
into the isolated trial and associated with a completed report's selected source
manifest. The CLI status reported three stored captures across two sources and
one source still missing. The HTML abstract remained `incomplete`; both PDFs
remained `unknown`. External verification claims did not promote imported
completeness. All three stored files were reread and their hashes and sizes
matched the selected originals. The original files were preserved.

The manifest's coverage is `selected_sources_only`. It retains grouped source
URLs, citation counts, the distinction between a citation and a bare body URL,
and a declared selected-body hash. It does not retain individual citation
offsets or prove exact paragraph/span matches, coordinate interpretation, or
complete research-source coverage. The report body and research activity log
were not imported.

No source was fetched, no job was submitted, and no rolling budget was changed
or reset. All selected sources used manual mode. The existing RFC job, original
worker, production service, cloudflared, network configuration and authorization
remained unchanged. The trial still has no external backup and no production
wiki or ingest connection. This run proves selected-file import and fixity, not
fresh multi-domain network acquisition, full-text verification or disaster recovery.

Client verification is deliberately separated from server verification:

- The refreshed ChatGPT and Claude connection UIs listed all four trial tools.
- Actual new report status was verified through the operator CLI.
- The current native ChatGPT execution catalog still exposed only the original
  two tools, so `trial_report_status` was not invoked through that native tool
  interface. Existing native budget and RFC-job reads succeeded afterward.
- No new Claude chat message or report-status execution was performed for this run.
- Private guides were updated to native trial plugin version 1.1.0 and archive
  guide version 0.2.0 while preserving their existing identities, app connections
  and permission scope. Private report/capture identifiers and evidence paths
  are intentionally absent from this repository record.

Final code verification passed Go race tests, vet, lint, whitespace checks and
independent review. Full race tests used process-local isolation from the user's
Git signing configuration for disposable fixture repositories; no saved signing
setting changed and no source commit was made. The separate, unchanged
`install-signed_test.sh` baseline still fails because its mocked Developer ID
Application identity does not match the install script's Apple Development-only
selection. That failure is not counted as a passed check or a trial regression.
