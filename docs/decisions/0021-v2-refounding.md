# 0021-v2-refounding — capture→digest→consume pipeline, vault mode removed

Status: accepted
Date: 2026-07-22

## Context

Step 9 (SPEC Section 11) prescribed a 1-week real-world validation. The
5-day execution (recorded in 0020) confirmed the technical components work but
found workflow friction high enough that no daily-use habit formed: the ingest
workflow required 3-6 explicit MCP calls per note with manual user orchestration
each time. 0020's response was a per-item CLI shortcut (`cogvault ingest <path>`).

That shortcut was **never built**. Its literal revisit trigger — "CLI shortcut
alone does not resolve friction after 1 week of use" — therefore never fired.
Re-examining the Step 9 evidence, the user concluded (decision, 2026-07-22) that
the friction is not the *number* of steps but the *existence of any per-item
instruction*: a per-item CLI call is still a per-item instruction, so it would
have hit the same wall. Digestion must require zero per-item user action.

This decision records the resulting refounding: cogvault becomes a standalone
personal knowledge pipeline (capture → digest → consume) instead of an MCP wiki
server hosted inside an Obsidian vault. The approved design is
`docs/specs/2026-07-22-refound-capture-pipeline-design.md`.

## Decision

### D1: Single mode — `wiki_dir` is the sole storage root; the vault concept is removed

`wiki_dir` (absolute) is the one storage root, read-write. `sources[]` are plain
external directories the ingest pipeline reads directly. There is no vault root,
no `wiki_dir/` sub-prefix within a larger tree, and no dual mode. Review round 1
verified the v1 code could not run standalone (config rejected absolute
`wiki_dir`; storage's write check was vault-relative); rather than add a second
mode, the vault concept is removed entirely (KISS/DRY, user decision 2026-07-22).

**Boundary contract** (spec MAJOR-1): `internal/storage` and every MCP tool
operate on `wiki_dir` only. Sources are read directly by the ingest pipeline via
plain `os` calls (`Lstat` to refuse symlinks, size cap applied), never through
storage and never addressable via MCP paths.

Config path handling: a leading `~/` is expanded; all other paths must be
absolute; `~` elsewhere in a path (e.g. iCloud's `com~apple~CloudDocs`) is
literal. Validation rejects overlapping boundaries: a source containing, contained
by, or equal to `wiki_dir`, and `db_path` inside `wiki_dir`.

### D2: Batch ingest + launchd, not a resident daemon

`cogvault ingest` is a batch command: scan configured sources, detect
unprocessed files by content hash, digest each via the LLM adapter, write and
index wiki pages, record outcomes in a processing ledger, print a per-run report.
Zero-touch inflow is achieved by a launchd-scheduled `cogvault ingest --scheduled`
run (template shipped under `deploy/`), not by a watch mode or long-lived daemon
(user decision 2026-07-22). Source originals are never moved or deleted; processed
state lives in the SQLite `ingest_ledger` table, keyed on (source path, content
hash).

### D3: LLM adapter with a `claudecode` backend

`internal/llm` defines an `Adapter` interface with one Phase 1 backend:
`claudecode` (runs `claude --print --output-format json`, prompt on stdin,
per-call 5-minute timeout). The interface admits a future local-LLM backend
without changing the ingest pipeline (durability requirement, user decision
2026-07-22).

### D4: Error classification — transient / permanent / infra

Per-file failures are classified so a run always continues past them:

- **transient** (quota/rate limit, timeout, CLI transport, `error_during_execution`)
  — recorded but consumes **no** attempt; the file simply retries on later runs.
- **permanent** (malformed LLM output, schema-invalid page) — consumes an attempt,
  bounded by the max-attempts constant (3); at the bound the file is skipped in
  later runs.
- **infra** (storage write, index add, ledger write failure) — recorded but spares
  attempts, since the failure is local infrastructure, not the file.

Behavior knobs are code constants, not config (KISS): LLM timeout 5m, max
permanent-failure attempts 3, max file size 32MB, settle window 2m.

### D5: `scope` parameter removed from `wiki_search` and `cogvault search`

The v2 index contains wiki pages only (sources are mostly binary PDFs; their
digested pages carry the searchable text), so the `scope` filter has no meaning.
The parameter is dropped from the `wiki_search` MCP tool and the `cogvault search`
command. Note: mcp-go (v0.47.0) does not set `additionalProperties:false` on tool
input schemas, so a stray `scope` argument is silently **ignored**, not
schema-rejected. The contract is "no `scope` parameter"; the enforceable assertion
is that the input schema no longer advertises `scope`.

### D6: O1 spike result — headless PDF digestion verified

`claude --print` reads a PDF by path and emits a frontmatter markdown page, both
from an interactive shell and from a launchd-spawned process (TCC access to
`~/Downloads`, PATH with the claude CLI dir, and non-interactive auth all held).
The conditional `pdftotext` fallback named in the spec's Scope Out is **not
activated**. Details: `docs/research/o1-headless-pdf-verification.md`.

## Why

- The Step 9 failure signal was habit-blocking friction, and the only friction
  cure that survives scrutiny is zero per-item instructions — which a batch +
  scheduled pipeline delivers and a per-item CLI shortcut does not.
- Removing the vault concept is the smallest change that makes the tool run
  standalone: one root, no vault/wiki split, no second mode to maintain.
- Classifying failures keeps a quota exhaustion or a transient CLI error from
  burning the bounded attempt budget, so a large backlog resumes indefinitely
  instead of poisoning files as permanently failed.

## Supersedes

- **0020-step9-validation-outcome** — superseded. Basis of supersession is
  **extrapolation from Step 9 evidence plus explicit user decision (2026-07-22)**,
  **not** a literally-fired 0020 revisit trigger: 0020's D1 CLI shortcut was never
  built, so its "shortcut fails after 1 week" trigger never ran. Recorded honestly
  per the spec Overview.

## Prior decisions staying in force

- **0001-config-validation** — config validates path-string safety and policy;
  filesystem state/permissions are enforced by storage/runtime. Retained (the v2
  overlap and absolute-path rules extend this boundary, they do not overturn it).
- **0006-storage-write-serialization** — single global write mutex in storage.
  Retained.
- **0007-storage-error-mapping** — sentinel errors in `internal/errors`, wrapping
  `fmt.Errorf("pkg.Fn %s: %w", path, err)`. Retained; the ingest error classes
  (D4) layer on top of, and do not replace, these sentinels.
- **0003-canonical-context-locations** — SPEC = contract canon, DESIGN =
  architecture canon, `docs/decisions/` = decision canon. Retained; this record
  and the v2 SPEC/DESIGN updates follow it.

## Alternatives Considered

- **Dual mode (keep vault mode, add standalone mode)** — rejected. Two modes
  double the config/storage surface and the test matrix for a single-user tool.
  KISS/DRY: remove the vault concept instead (D1).
- **Resident daemon / watch mode (fsnotify)** — rejected for Phase 1. A batch
  command driven by launchd is simpler, has no long-lived process to supervise,
  and meets the zero-touch criterion (D2). Revisit only if schedule latency proves
  unacceptable.
- **Headless agent on cron issuing per-item instructions** — rejected. Still a
  per-item instruction path; fails the same friction test that killed 0020's
  shortcut.

## Accepted losses

- **v1 vault-note search coverage disappears.** v1 indexed vault notes
  (`scope=vault` search); v2's index contains wiki pages only. Re-finding a raw
  vault note by full text is gone until a later phase digests markdown sources.
- **Phase 1 digests PDFs only.** Screenshot/image OCR, URL/web extraction, and
  registering an Obsidian vault as a source are later-phase capabilities.

## Revisit Triggers

- 1-week v2 validation (spec Success Criteria) shows scheduled digestion still
  does not form a habit → reconsider capture surface, not the pipeline.
- Backlog quota exhaustion recurs despite `--limit` batching and transient
  classification → promote a knob (e.g. rate cap) from constant to config.
- Users need raw source-note search back → prioritize the markdown-digestion
  later phase.

## Related Files

- `docs/specs/2026-07-22-refound-capture-pipeline-design.md`
- `docs/plans/2026-07-22-001-feat-v2-capture-pipeline-plan.md`
- `docs/research/o1-headless-pdf-verification.md`
- `docs/decisions/0020-step9-validation-outcome.md`
- `SPEC.md`, `DESIGN.md`, `README.md`, `CLAUDE.md`
- `deploy/com.teslamint.cogvault.ingest.plist`
