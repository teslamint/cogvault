# 0027: Research Source Archive

Status: proposed (draft) — not accepted; not binding on `SPEC.md` or `DESIGN.md`.
The user approved the policy defaults marked below; open questions and
deployment settings remain, so the record as a whole stays proposed.
A local prototype of most items is implemented on a branch (see
"Prototype status"); nothing is deployed, and production capture stays
blocked by item 14.
Date: 2026-11-02 (revised: item 12, open question 8, approved policy defaults, prototype status)

## Context

Research sessions in Claude and ChatGPT find sources. Today a found source
reaches cogvault only as a file that the operator puts under `sources[]`, or
as a `cogvault fetch` capture. Cogvault has no durable record of the source
itself: where it came from, whether the bytes are complete, and which report
cited it. The working plan is `docs/plans/research-archive-ingest.md`
(non-canonical). This record proposes the boundaries for that archive. It
changes no deployed behavior; the prototype's behavior is in `SPEC.md`
§9.16.

Facts that constrain the design:

- The NAS container mounts only `wiki/` and `state/`
  (`deploy/nas/compose.yaml`). `state/` holds a search index that cogvault can
  rebuild from `wiki/`. The Mac runs the local LLM, keeps `sources[]` and its
  own SQLite DB (index and `ingest_ledger`) on local disk, and writes `wiki/`
  over SMB (`docs/deployment/nas.md`). SQLite WAL does not work over SMB.
- `wiki_dir` is the only MCP read-write root. `sources[]` are read-only ingest
  inputs (`SPEC.md` §3, §8).
- `ingest_ledger` is keyed by `(source_path, content_hash)`, and
  `source_path` is an absolute Mac path (`SPEC.md` §10.6). The same bytes at two
  paths are two ledger rows.
- `cogvault fetch` writes a `.md` file: added YAML (`source_url`,
  `fetched_at`, copied `usage_terms`) followed by the response body. The body
  stays byte-identical, but the file hash is not the body hash (`SPEC.md`
  §9.5). Bodies over 10 MiB are rejected.
- `research_evidence.claims[].sources[].ref` is an exact wiki-root-relative
  `.md` path (`SPEC.md` §11.1, ADR 0026). A ref to a missing page produces
  warning EVID004.
- Ingest sets some frontmatter fields in code, not through the LLM
  (`ingested_at`, a carried `research_evidence`).
- OAuth `RequiredScopes` applies to the whole MCP server. There is no
  per-tool scope today (`internal/httpauth/auth.go`).
- Backups are the operator's responsibility (`docs/deployment/remote-mcp.md`,
  ADR 0024).

## Decision (proposed)

Labels used below:

- **Approved default:** the user adopted this policy as the default for the
  intended production design. Approval is not deployment: its deployment
  details (actual paths, targets, owners) can still be open. The prototype
  states only the scope it implements; it does not claim compliance with
  these policies or production activation.
- **Candidate:** the preferred option; a design review must confirm it.
- Unlabeled text explains or constrains a labeled item.

Approval of a default is not completion of its deployment settings. Item 14
blocks production activation until those settings exist. The open questions
are listed separately.

1. **Separate archive store on the NAS (approved default).** Add an
   `archive/` directory next to `wiki/` and `state/`, mounted into the
   container as its own persistent volume. It is not inside `wiki_dir`,
   `sources[]`, or `state/`. Removing or rebuilding the search index never
   touches it. The actual host path and volume are deployment settings.

2. **Registry DB is container-local (approved default).** The capture
   registry is a SQLite DB on a persistent volume that only the NAS
   container opens. No host opens it over SMB, and it never runs WAL on a
   network file system. There is no shared SQLite between hosts. Other
   hosts read archive data only through immutable object files and
   hand-off files (item 9).

3. **Original bytes are immutable objects.** An object is stored once under
   its SHA-256 (`object_id`), written to a temporary name and then renamed.
   Size and hash are checked before the object is accepted. Bytes over a
   size limit are rejected or recorded as partial. They are never truncated
   and stored as complete.

4. **Provenance is separate from bytes.** A `capture_id` (a new opaque ID per
   acquisition) records one acquisition: URL or supplier, known source
   identity and edition, acquisition time, media type, completeness judgment
   and its evidence, and links to reports or citations. Storing identical
   bytes again is idempotent at the object level but still creates a new
   capture record when the provenance differs. A hash match alone does not
   prove two captures are the same source or edition. Host paths are never
   persistent IDs.

5. **Original bytes and processed outputs are different things.** Derived
   text records the `object_id`, extractor, extractor version, and options,
   and can be regenerated. A `cogvault fetch` `.md` file is a processed
   output (added frontmatter plus body), not the original. **Candidate:**
   when the archive is enabled, fetch also stores the decoded response body
   as an object and writes `capture_id` and the body SHA-256 into the fetch
   frontmatter. When the archive is disabled, fetch keeps its current
   behavior. The Go HTTP client removes transfer compression, so the
   "original" is the decoded body; the capture records response headers so
   this stays visible.

6. **Status authority is split, not duplicated.**
   - The registry owns: acquisition (none / partial / acquired / unknown),
     completeness (verified / unknown / incomplete), extraction (pending /
     succeeded / partial / failed), and recovery history.
   - The Mac `ingest_ledger` owns ingest success and failure.
   - The NAS search index owns search visibility. It is derived and is
     queried, not stored.
   - The registry stores no `ingested` or `indexed` booleans and derives no
     ingest state of its own. It may keep Mac receipts (item 9) as an
     append-only log of non-authoritative observations: what the Mac
     reported, and when. A receipt is evidence that the Mac's ledger said
     something at a point in time; it does not replace the ledger.
   - A status view reads ingest state from the latest stored receipt for the
     capture and labels it as a Mac observation with its observed time. It
     reads search visibility from a live NAS index query at view time. It
     shows the source and observation time of each value. With no receipt it
     shows `awaiting_ingest`, a computed display state, not a success or
     failure claim. Cogvault never edits a receipt to change the Mac's result.

7. **Recovery history is append-only.** A recovery attempt adds a new capture
   or attempt record (cause, source tried, time, result, remaining gap). It
   never replaces an earlier partial capture, report, or citation.
   Completeness becomes `verified` only from checkable evidence (declared
   length, file integrity, page or section structure, attachment lists).
   Otherwise it stays `unknown`. If the original bytes are present and only
   extraction failed, only extraction is rerun. Credentials are never stored,
   and access controls are never bypassed. Access-restricted items wait as
   `human_action_required`.
   - **Approved default: incomplete originals are kept but not
     auto-ingested.** Objects whose completeness is `unknown` or whose
     acquisition is `partial` stay in the archive. Automatic hand-off to
     ingest is held for them. How an operator releases one for ingest is
     open question 5.
   - **Approved default: a finished summary is a summary, not an
     original.** A completed summary text (for example a research report's
     digest of a source) may be recorded and used as a summary record. It
     never sets the source's acquisition or completeness, and it is never
     shown as the original. When the original is recovered later, the
     summary, the recovery attempts, and the new capture stay linked, so the
     provenance of each is kept.

8. **Citations keep the existing contract (approved default).** The link is
   `capture_id → source wiki page → research_evidence ref`. Refs stay exact
   wiki `.md` paths. This record adds no capture-ID ref form and no second
   citation model. A ref to a source page that does not exist yet stays
   unresolved and is reported by the existing EVID004 warning. A new edition
   never replaces an earlier one automatically: each edition is its own
   capture, and a ref keeps pointing at the page it names. **Candidate:**
   ingest writes `capture_id` into the source page frontmatter in code (as
   it already does for `ingested_at`). The page path the Mac reports in its
   receipt is kept with that receipt as an observation, not as a registry-owned
   state.

9. **Mac–NAS hand-off (approved default for the first version): manifest
   and receipt files.** No shared SQLite and no new network API.
   - The NAS writes one manifest per capture to hand off
     (for example `archive/outbox/<capture_id>.json`) next to its object.
   - The scheduled Mac ingest reads manifests over the existing SMB mount and
     copies each object to a dedicated Mac source directory. The copy is
     named by `object_id`, so identical bytes become one `source_path`, one
     ledger key, and one source page. The registry records
     `capture_id → object_id → source_path`. The registry also keeps the fact
     that pre-existing copies of the same bytes at other Mac paths are
     separate ledger keys.
   - After ingest, the Mac writes a receipt (for example
     `archive/receipts/<capture_id>.<attempt>.json`) that copies the ledger
     key, ledger status, and wiki page from its own ledger at that time. It
     writes to a temporary name and then renames. The NAS appends each
     receipt to the registry's receipt log unchanged (item 6). The log is a
     history of Mac observations; the Mac ledger stays the authority, and a
     later receipt supersedes an earlier one only for display.
   - When the Mac is offline, manifests wait. Nothing reports ingest success
     before a receipt arrives. Ingest authority is the Mac ledger as observed
     through receipts, with the observation time. NAS search visibility comes
     from a live index query (item 6).
   - The actual outbox and receipt paths, the SMB mount the Mac uses for
     them, and the ingest schedule are deployment settings (item 14).

10. **Capture MCP surface is separate and off by default (approved
    default).** New capture tools (names to be fixed in `SPEC.md`) are
    exposed only when an archive setting enables them. They may read capture
    status and add captures. They may not delete or overwrite objects or
    records, and they accept no host path, so they give no access to
    arbitrary paths. They do not extend `wiki_*` tools and do not change the
    `wiki_dir` root or `sources[]`. A size limit applies. The actual
    permission model, credential, scope, and the decision to enable are made
    separately at deployment time (open question 3, item 14). The enable
    setting can change without a restart only through item 12.

11. **Retention and deletion of originals (approved default).**
    - **No automatic expiry.** Original objects, editions, and capture
      records never expire by age.
    - **User deletion request.** Cogvault first reports what depends on the
      target: captures, summaries, reports and citations that reference it,
      hand-off manifests and receipts, and source wiki pages linked through
      item 8. The operator sees that impact before any change.
    - **Quarantine, not deletion.** An accepted request moves the target to
      quarantine. It leaves capture tools, status views, and hand-off, and it
      can be restored for at least 30 days. Nothing purges it automatically
      when the 30 days end; the window only marks when purge becomes
      eligible.
    - **Permanent purge.** Purge needs a separate confirmation for that one
      case. No batch or scheduled purge exists.
    - **Operator-only.** Deletion requests, quarantine, restore, and purge
      run only through a local operator interface, never through MCP
      (item 10).
    - **Durable deletion record.** Each quarantine, restore, and purge
      writes a deletion record (a tombstone) that holds no original content:
      the `object_id`, the affected `capture_id`s, the action, and its time.
      Item 13 makes restores re-apply these records.

12. **Explicit local reload of capture activation (user-approved design;
    prototype implemented, not deployed).** This item is the contract; the
    prototype's behavior is in `SPEC.md` §9.16 and §9.3.

    - **Trigger (candidate): `SIGHUP` to the running `cogvault serve`
      process.** Only a local operator who can signal that process can start
      a reload, for example `launchctl kill SIGHUP gui/<uid>/<label>` on the
      Mac or `docker kill --signal=HUP <container>` on the NAS. The image
      runs cogvault as PID 1 through an exec-form `ENTRYPOINT`, so the signal
      reaches it directly. Before the prototype, `serveListener` trapped only
      `SIGINT` and `SIGTERM`: an untrapped `SIGHUP` ended the process on
      macOS (launchd then restarted it), and PID 1 in the container ignored
      it. The prototype traps `SIGHUP` only when `archive.root` is set and
      the transport is `sse` or `http`; an older binary still dies or ignores
      it, so no operator document should tell anyone to send it before a
      deployment ships this code.
    - **No agent-reachable trigger.** No MCP tool, HTTP route, or file
      watcher starts a reload. An MCP client can never change capture
      activation.
    - **Parse everything, apply one value.** A reload reads the config path
      that `serve` resolved at startup and calls `config.Load` (strict YAML
      decode with `KnownFields`, defaults, full validation). Any error
      rejects the reload. The running state does not change, and the error
      is logged.
    - **Reloadable value:** only `archive.capture_enabled`.
    - **Restart-only values:** archive root, registry DB path, `wiki_dir`,
      `db_path`, every `auth` value (mode, scopes, issuer, audience,
      credential source), and the upload and size policy. **Candidate:** if
      any restart-only value differs from the running snapshot, the whole
      reload is rejected. The log names the changed keys, never their
      values. Other keys (for example `llm`, `git`) are not applied; the log
      says that a restart is needed for them.
    - **One state, serialized reloads.** The active value is the gate's open
      flag, read under the gate's lock on every call. Reloads run one at a
      time; signals that arrive during a reload collapse into one more
      reload.
    - **Enable needs a startup archive.** If the archive root is configured
      at startup, `serve` opens and checks the archive even when capture is
      disabled. If it is not configured at startup, a reload that enables
      capture is rejected; a restart is needed.
    - **Disable.** The order is: close the gate, remove the tools, then wait
      for in-flight capture calls. After the gate closes, a new capture call
      gets a tool error and stores nothing. A reload does not cancel an
      in-flight store; the store completes or fails by its own rules
      (staging and one transaction leave no half state). The drain bound is
      open question 8.
    - **Enable.** The order is: open the gate, then add the tools, so a
      listed tool is callable. Enabling changes no authentication or
      authorization. Each capture call still passes the `httpauth`
      middleware (server-wide mode and `RequiredScopes`) on its own, and
      then the gate. Per-tool scope stays open question 3.
    - **Tool list is a hint, not a boundary.** Facts for mcp-go v0.47.0:
      cogvault sets no `WithToolCapabilities`, so the first `AddTool` sets
      `tools.listChanged: true`, and `initialize` already advertises it.
      `AddTools` and `DeleteTools` send `notifications/tools/list_changed`
      to every initialized, registered session. The send is non-blocking
      into a 100-entry channel; a full channel drops it (only an `OnError`
      hook sees that). Streamable HTTP delivers it on a `GET` listen stream,
      or on the next `POST` response, which it upgrades to SSE. A client is
      not required to refresh its list, and the refresh behavior of the
      Claude and ChatGPT connectors is not verified. mcp-go copies a tool
      handler under its lock and calls it after the unlock, so a call that
      started before `DeleteTools` still runs the handler. Therefore the
      gate inside each capture handler is the boundary. The implementation
      uses `AddTools`/`DeleteTools` by name, never `SetTools`, which empties
      the map before it adds, so a concurrent `tools/list` can see no tools.
    - **Logging.** Each reload logs its trigger, result, old and new value,
      and a fixed rejection code with config key names only. It logs no
      config value, path, or credential.
    - **Session sweeper.** `sessionIdleTTLFor` in `cmd/cogvault/serve.go`
      assumed that cogvault sends no server notifications. Its comment now
      covers `tools/list_changed`, which also refreshes a listen stream.

13. **Backup (approved default).**
    - **Daily backup** of the original objects together with a consistent
      registry copy (taken by the SQLite backup API or `VACUUM INTO`, never
      by copying a live WAL database file).
    - **One copy outside the NAS.**
    - **Backup retention is 30 days.** This is how long backup copies are
      kept. It is not a retention period for originals (item 11 keeps them
      without expiry) and not the quarantine window. Cogvault does not
      promise that every copy of a deleted original is gone exactly 30 days
      after deletion: removal depends on each backup tool's own expiry and
      on copies outside its control.
    - **Restore re-applies deletions.** A restored backup can be older than
      a later quarantine or purge. The restore procedure therefore re-applies
      the newest durable deletion records (item 11) before the archive
      serves or hands off anything, so a deleted original does not return.
      Deletion records must survive the loss of the registry they describe;
      where they are kept is open question 1.
    - The actual target, method, schedule, credentials, permissions, and
      the restore test that proves this are deployment settings (item 14).

14. **Production activation gate (approved default).** Production capture
    stays disabled while any of these deployment settings is empty or
    unconfirmed:
    - the real archive root and its persistent volume;
    - the off-NAS backup target, a confirmed daily schedule, and a passed
      restore test that includes re-applied deletion records;
    - an off-NAS replica of the deletion log, so the newest deletion records
      survive the loss of the NAS and can be re-applied to an older backup,
      and a restore test that used that replica;
    - the owner of permissions, credential, and scope for the capture
      surface;
    - the hand-off transfer mount and paths for manifests and receipts.

    The prototype enforces this as a check of operator-supplied attestations
    (`archive.deployment` in `SPEC.md` §3.1, §9.16): every setting present;
    the restore test report passed, tested a backup under the configured
    target, and used the configured replica; and the replica holds every
    live record at activation time. The check cannot verify that the target
    or replica is off the NAS, that backups run daily, that the replica stays
    current, or that permissions and mounts are correct. A passed check is
    not a deployment approval; production enable needs a separate human
    review. No config key bypasses the check.

    Development and tests in an isolated prototype (temporary roots, no live
    wiki, NAS, configuration, or credentials) are allowed before these
    settings exist.

## Prototype status

Implemented on branch `proto/research-archive` (PR #59), not deployed:
items 2–4, 6–12, and the mechanisms of 13 and 14 (`SPEC.md` §9.16,
`DESIGN.md` §2.14). Also implemented without settling an open question:

- An operator reassessment that adds a linked verified capture of held
  bytes (the basis is the operator's statement).
- A local import of an existing `cogvault fetch` file that keeps the body
  as an original of unknown format and the file as a derived object.
- Item 8's candidate, prototype form: Pickup keeps a capture map
  (`.cogvault-archive-captures.json`) in the destination source directory;
  ingest writes `archive_capture_ids` and `archive_object_id` on the source
  page in code, only when the map's object ID is the digested bytes' hash,
  and always drops model-written values. These keys are a digest-time
  snapshot, not the full list: ingest never rewrites a page for an
  unchanged row, because a frontmatter-only update would need an atomic
  compare-and-write that no wiki writer offers. The map is trusted as far
  as the source directory is. The page keys are editable through
  `wiki_write`, so `archive_status` reports them as what the page claims,
  not as an independent check.
- A separate capture-to-page link record in the archive registry
  (`page_links`), made only from collected receipts that observe ledger
  success with a wiki page. Each capture, including a later capture of the
  same bytes, gets its own link with its basis receipt; the wiki page is
  never changed. The ingest host's ledger stays the authority for the
  ingest result and the archive for the association. Links are
  append-only, carry the exact `.md` path the ledger names, and are not
  offered as an evidence ref when the page is missing now; moved pages are
  not followed. Receipt order is the per-capture receipt number, not the
  ingest host's clock: the lowest-numbered linking receipt is the basis
  and the highest-numbered receipt is the latest state.
- An HTML text extractor (`html-text/1`, standard library only, no scripts
  or resource loads) that stores derived text as its own object and records
  failures; derived text is never handed off.
- An operator-run public URL acquisition and re-acquisition (`archive
  acquire`): every DNS answer must be public, the connection goes to the
  checked address, and URLs with credentials, a query, or a fragment are
  refused on every redirect. Completeness is always unknown.

- A durable collection queue (`archive job`, MCP `archive_job_submit`,
  `archive_job_status`, `archive_job_retry`; `SPEC.md` §9.16 "Collection
  jobs"): jobs and append-only per-try attempts in the registry (schema 6),
  a lease so a stopped worker's job resumes, bounded retry with backoff,
  idempotent submit, and a provenance label on each stored capture link
  (`seen_during_research` only when the submitter's digest matches the
  stored bytes, else `recovered_after_research`). The worker reuses
  `Acquire`, so it is public-address-only and never uses credentials; it
  stores bytes held (completeness `unknown`). The MCP tools are archive
  tools: off while capture is disabled, behind the server-wide auth, with no
  per-tool scope. An interactive official-UI executor (for example for KCI)
  is not implemented; its claim/complete contract exists and no test
  exercises a real browser flow. Nothing deploys a worker.

Not implemented: item 1's NAS volume, changes to `cogvault fetch` itself
(item 5 candidate), release of `unknown`/`partial` bytes without a verified
judgment (open question 5), scheduled re-acquisition, robots or
terms handling, charset decoding of non-UTF-8 HTML, backup scheduling and
expiry, and keeping the deletion log replica current. The prototype limits
that answer open questions only provisionally (1 MiB MCP text, 30 s drain,
status tools hidden while disabled, no `stdio` reload, all URL queries
refused, 32 MiB / 60 s acquisition bounds) remain open below.

## Open Questions (must be decided before acceptance)

1. **Backup deployment details.** The backup tool, target outside the NAS,
   schedule, credentials and permissions, verification, and restore test
   (item 13). Where the off-NAS deletion log replica lives, how it is kept
   current after each deletion action, and how a restore reads it when the
   NAS is lost. Until these are decided, item 14 keeps production capture
   off.
2. **Deletion operation details.** The impact report format, the quarantine
   location, the per-case purge confirmation step, and what a purge removes
   besides the object (for example derived text and hand-off copies on the
   Mac). The defaults in item 11 are approved; these details are not.
3. **Security permissions and scope.** The actual permission model,
   credential, and scope for capture tools, who owns them, and whether they
   need per-tool scope checks separate from `wiki_*`. Enabling capture in
   production is a separate decision. No live configuration, credential, or
   scope changes come from this draft.
4. **Hand-off paths and schedule.** Manifest and receipt files are the
   approved first mechanism (item 9). The concrete paths, mount, and ingest
   schedule are open. An HTTP API stays a possible later replacement.
5. **Release of held material.** How an operator releases an `unknown` or
   `partial` original for ingest, and how its source page shows the limit.
   How a summary record is labeled if it is ingested.
6. **Upload transport** for user-supplied files, **maximum object size**, and
   **handling of access-restricted material**.
7. **fetch integration** (item 5) and **`capture_id` on source pages**
   (item 8): confirm or reject the candidates. For item 8, confirm the
   prototype's capture map trust scope (the source directory), the
   frontmatter keys as a digest-time snapshot, the registry `page_links`
   record as the association authority (append-only; moved pages not
   followed), and whether derived text may be handed off.
8. **Reload operation details.** Confirm `SIGHUP` as the trigger, or add a
   local CLI wrapper (which needs a new local control channel, such as a
   PID file or Unix socket). Confirm the rejection rule for changed
   restart-only values, or apply the boolean and only warn. Set the disable
   drain bound and what the log reports when the bound expires. Decide if
   the `stdio` transport supports reload, and if a disable also hides
   read-only capture status tools.
9. **Acquisition policy.** Which URL queries (if any) may be fetched,
   robots and terms handling, the size and time limits, whether the
   archive host may make outbound requests at all, and who may run it.
   Queueing a URL through the MCP job tools makes the archive host's worker
   connect to a public address the client chose, so this question also
   decides whether those tools may be enabled; they stay behind the capture
   gate and the *Activation* check until it is answered. A worker process on
   the NAS (its image, service definition, and credentials-free network
   path) is a further deployment setting; `deploy/nas/compose.yaml` has none.

## Why

- A separate store keeps durable evidence apart from the rebuildable index
  and from the LLM-written wiki, so neither a rebuild nor a page overwrite
  can lose an original.
- A container-local registry follows the existing rule that SQLite never runs
  over SMB.
- Splitting bytes from provenance makes storage idempotent without merging
  different sources that happen to share bytes.
- Single authority per state avoids a registry that claims ingest or index
  results it cannot observe.
- Keeping the ref contract avoids two citation models and needs no change to
  ADR 0026 or `internal/evidence`.
- Files over the existing SMB mount reuse the scheduled Mac ingest and add no
  new network API or credential.
- Quarantine before purge, plus a per-case purge confirmation, keeps a
  mistaken deletion recoverable and keeps irreversible removal a deliberate
  act.
- Backup retention, original retention, and the quarantine window are
  separate clocks; keeping them separate stops a backup policy from
  silently becoming a deletion policy.
- Re-applying deletion records on restore keeps a backup from bringing back
  an original the user asked to delete.

## Alternatives Considered

- Registry as JSONL files on the share: no SQLite over SMB, but weaker
  concurrency and no transactional updates. Not preferred.
- Archive on the Mac: the NAS MCP server could not write captures. Rejected.
- Capture IDs directly in evidence refs: changes the ADR 0026 contract and
  creates a second resolution path. Rejected for this record.
- Treating the fetch `.md` as the original: its hash differs from the body
  and it contains added data. Rejected.
- Storing `ingested`/`indexed` flags in the registry: duplicates the ledger
  and the index. Rejected.
- Materializing one Mac file per capture instead of per object: identical
  bytes would get several ledger keys and several source pages. Not
  preferred.
- HTTP API hand-off: kept as a later option under open question 4; needs a
  new authenticated Mac-to-NAS surface.
- Automatic expiry of originals after a fixed age: would lose evidence that
  wiki pages still cite. Rejected.
- Immediate permanent deletion on request: a mistaken request could not be
  undone. Rejected.
- Automatic ingest of `unknown` or `partial` originals: would present
  incomplete material as a complete source. Rejected as the default.
- Reload by a file watcher: an edit would change activation with no explicit
  operator act, and a half-written file could be read. Rejected.
- Reload through an MCP tool or HTTP admin route: an agent or remote client
  could change its own permissions. Rejected.
- Changing every setting on reload: auth, storage roots, and DB handles are
  bound at startup, and a partial swap is hard to make atomic. Rejected for
  the first version; these values need a restart.

## Revisit Triggers

- Any open question above is decided (update this record before acceptance).
- mcp-go changes `listChanged` defaults, notification delivery, or tool
  lookup (update item 12).
- The Mac stops being the only ingest host, or the NAS stops hosting MCP.
- `research_evidence` gets a new version with a different ref form.
- Per-tool MCP authorization is added for another reason.

## Related Files

- `docs/plans/research-archive-ingest.md` (non-canonical working plan)
- `SPEC.md` §§3, 8, 9.5, 10.6, 11.1
- `DESIGN.md` §§2.7, 2.8
- `docs/deployment/nas.md`, `docs/deployment/remote-mcp.md`, `deploy/nas/compose.yaml`
- `docs/decisions/0024-wiki-git-safety-net.md`, `docs/decisions/0026-research-evidence-record.md`
- `cmd/cogvault/fetch.go`, `internal/ingest/ledger.go`, `internal/httpauth/auth.go`
- `cmd/cogvault/serve.go` (`serveListener`, `sessionIdleTTLFor`), `internal/config/config.go` (`Load`), `internal/mcp/server.go` (`NewServer`)
