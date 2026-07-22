---
title: cogvault v2 Refounding — Capture→Digest→Consume Pipeline
status: approved
date: 2026-07-22
schema: spec/v1
---

# cogvault v2 Refounding — Capture→Digest→Consume Pipeline Design

_Created 2026-07-22. Revision 3: independent review round 1 (C1, C1a, M1-M3, m1-m5), user YAGNI pass, user KISS/DRY pass (single-mode unification), independent review round 2 (MAJOR-1..4, minor 1-6) applied._

## Overview

Refound cogvault from "an MCP wiki server hosted inside an Obsidian vault" into a standalone personal knowledge pipeline: watch real source folders the user already fills, digest new material automatically with an LLM, and serve the digested wiki through MCP, CLI search, and a phone-readable synced folder. The vault concept is removed entirely (user decision, 2026-07-22): cogvault has one mode, in which `wiki_dir` is the sole storage root and `sources` are plain directories the ingest pipeline reads directly.

This supersedes decision 0020's narrow CLI-shortcut pivot. Basis of supersession: 0020's literal revisit trigger (CLI shortcut fails after 1 week) never ran — the shortcut was not built. The supersession is by extrapolation from Step 9 evidence plus explicit user decision (2026-07-22): any per-item manual instruction kills the habit, and a per-item CLI call is still a per-item instruction, so digestion must require zero per-item user action.

## User Scenarios

### S1: Backlog digestion
The user runs `cogvault ingest` once. The 65 PDFs already piled in `~/Downloads/_Articles` are digested into wiki source pages (summary, key claims, links, provenance frontmatter) and indexed. A report lists successes and failures.

### S2: Zero-touch inflow
The user saves a new PDF into `~/Downloads/_Articles` and does nothing else. A launchd-scheduled `cogvault ingest` run digests it within one schedule interval. No command is typed, no agent session is opened.

### S3: Re-find
Weeks later the user half-remembers an article. Today they re-search the web from memory and give up if that fails. Instead they ask Claude Code (`wiki_search` over MCP) or run `cogvault search "<terms>"` and get the source page: summary plus a pointer to the original file.

### S4: Phone viewing
The wiki lives in a folder under iCloud Drive. On iPhone, the user opens a source page in the Files app or any Markdown viewer. No Obsidian required anywhere.

### S5 (later phase): URL capture
From the phone share sheet, the user saves an article URL as a small file into a synced inbox folder; the same pipeline extracts and digests it. Phone capture is explicitly optional/secondary (user decision, 2026-07-22).

### S6 (later phase): Periodic digest
`cogvault digest` writes a daily/weekly summary page of newly digested material into the wiki, readable on the phone.

## Scope

### In (Phase 1)
- `cogvault ingest` command: scan configured source directories, detect unprocessed files by content hash, digest each via the LLM adapter, write wiki pages, index them (FTS5), record the outcome in a processing ledger, print a per-run report.
- `internal/llm` adapter interface with one Phase 1 backend: Claude Code CLI subprocess (`claude --print`, stdin pipe, JSON output). The interface must allow a local-LLM backend to be added later without changing the ingest pipeline (durability requirement, user decision 2026-07-22).
- **Single-mode refactor (this is a code change, not reuse).** Review round 1 verified the current code cannot run standalone: config rejects absolute `wiki_dir` (`internal/config/config.go:138-140`) and forces `db_path` under the root; storage only permits writes under the `wiki_dir/` prefix relative to a vault root (`internal/storage/fs.go:60-64`). Rather than adding a second mode, the vault concept is removed (KISS/DRY, user decision 2026-07-22): config becomes `wiki_dir` (absolute, the sole storage root, read-write) + `sources[]` (external dirs with allowed file types) + `db_path` (absolute, outside the synced folder). **Boundary contract (review round 2, MAJOR-1)**: `internal/storage` and every MCP tool operate on `wiki_dir` only; sources are read directly by the ingest pipeline (plain file reads with no symlink following and the size cap applied), never through storage and never addressable via MCP paths. Existing `init`/`serve`/`search` commands move to this model. Config path handling: a leading `~/` is expanded, all other paths must be absolute; `~` elsewhere in a path (e.g. iCloud's `com~apple~CloudDocs`) is literal. Config validation rejects overlapping boundaries: a source containing or contained by `wiki_dir`, a source equal to `wiki_dir`, or `db_path` inside `wiki_dir`.
- One-time migration, user-performed and documented in README: copy existing `_wiki` pages into the new wiki dir and re-index. **Accepted loss**: v1 also indexed vault notes (scope=vault search); v2's index contains wiki pages only, so that coverage disappears until a later phase digests markdown sources. Registering an Obsidian vault as a source is likewise a later-phase capability (Phase 1 digests PDFs only).
- Config discovery: every command takes `--config <path>`, default `~/.config/cogvault/config.yaml`; launchd invokes `cogvault ingest --config <path>` explicitly (launchd jobs have no meaningful cwd).
- Source originals are never moved or deleted; processed state lives in the SQLite ledger.
- Per-file failure isolation with error classification (review round 2, MAJOR-3): transient failures (quota/rate limit, timeout, CLI transport errors) are recorded but do **not** consume an attempt — the file simply retries on later runs; permanent failures (malformed output, schema violation) consume an attempt, bounded by the max-attempts constant. The run always continues past failures.
- Ingest concurrency safety: single-instance lock (lockfile) so scheduled and manual runs never overlap; **every DB opener** (ingest and serve/index connections) sets a busy timeout so concurrent writes surface as waits, not "database is locked" failures.
- launchd automation: repo ships a plist template plus README setup instructions (one-time `launchctl` load by the user). The template uses absolute paths for the `cogvault` binary and sets `PATH` to include the `claude` CLI's directory (launchd's default PATH excludes `~/.local/bin`); README documents the one-time grants: macOS TCC access to `~/Downloads` for the scheduled binary, and a verified non-interactive `claude` authentication.
- Canonical docs: decision record `docs/decisions/0021` superseding 0020 and recording vault-mode removal; README/SPEC/DESIGN updated to the v2 direction (including the `wiki_search` scope-parameter removal below).

### Out (Phase 1)
- Phone capture paths (share sheet, synced inbox) — S5, later phase.
- Screenshot/image OCR (the single `.webp` in the corpus is skipped and reported).
- URL/web-article extraction.
- Local LLM backend implementation (interface only in Phase 1). A text-extraction step (pdftotext) is also out — **unless O1 fails**, in which case a minimal pdftotext step enters Phase 1 as the conditional fallback already named in O1.
- Watch mode / resident daemon (batch model chosen over daemon, user decision 2026-07-22).
- Periodic digest command (S6).
- `wiki_delete`, auto-commit, vector search, ontology (unchanged from prior scope decisions).

## Assumptions and Preconditions

| Claim | Command | Observed at | Observed result | Evidence source |
|---|---|---|---|---|
| Real seed corpus exists: `~/Downloads/_Articles` holds 66 files (65 pdf, 1 webp), 159MB total | `ls ~/Downloads/_Articles \| wc -l; ls \| sed 's/.*\.//' \| sort \| uniq -c; du -sh` | 2026-07-22T11:41Z (reconfirmed by review 2026-07-22) | 66 files; 65 pdf, 1 webp; 159M | Live filesystem inspection |
| Claude Code CLI is installed and invocable | `which claude && claude --version` | 2026-07-22T11:49:13Z (reconfirmed by review) | `/Users/teslamint/.local/bin/claude`, version 2.1.217 | Live filesystem inspection |
| SQLite (Pure Go) and mcp-go dependencies are pinned | `grep "modernc.org/sqlite\|mcp-go" go.mod` | 2026-07-22T11:49:13Z (reconfirmed by review) | `mcp-go v0.47.0`, `modernc.org/sqlite v1.48.1` | Working tree `go.mod` |
| Current config/storage cannot run standalone without changes | reviewer code inspection | 2026-07-22 | absolute `wiki_dir` rejected (config.go:138-140); write prefix check vault-relative (fs.go:60-64); `serve` startup force-scans entire root (serve.go:29, sqlite.go:368) | Working tree, review round 1 |
| Claude Code headless mode can read a PDF given its path | _not yet verified_ | — | — | Must be verified as the first implementation task (see Open Decisions O1) |

## Architecture

Three layers; Phase 1 builds the middle one.

- **Capture** — nothing to build in Phase 1: the capture surface is "directories the user already fills" (`~/Downloads/_Articles`). Later phases add synced inbox folders fed from phones.
- **Digest** — new `internal/ingest` package orchestrates: enumerate sources → stability gate → content-hash → ledger lookup (skip processed) → LLM adapter digests the file → wiki page written through `internal/storage` (rooted at `wiki_dir`) → indexed through `internal/index` → ledger updated. New `internal/llm` package holds the `Adapter` interface and the `claudecode` backend. The digestion prompt embeds the `_schema.md` page rules so output pages conform to the existing schema.
- **Consume** — existing `internal/mcp` server and `cogvault search` operate on the single-mode wiki root. Phone viewing is a property of the wiki's location (iCloud Drive), not a feature.

Package impact of the single-mode refactor: `internal/config` and `internal/storage` are reshaped (simpler than today — one root, no vault/wiki split); `internal/index` reused (FTS5 trigram — Korean search already validated; accepts any db path); `internal/mcp` loses the `scope` parameter (below); `internal/schema` as-is; `internal/adapter` remains for parsing wiki pages. Obsidian is demoted to an optional viewer.

Design policies:

- **Page identity**: one source file path → one wiki page, deterministically derived from the source path (collisions across different paths get a deterministic disambiguating suffix; exact naming scheme is planning's job). Re-digestion of a changed file overwrites its page; search never returns two pages for one source.
- **Source mutation**: ledger keys on (source path, content hash). Same path with a new hash → re-digest, overwrite the page, mark the old row `superseded`. A stability gate skips files modified within a settle window (guards against hashing mid-download/mid-sync partial files).
- **Search scope**: the v2 index contains wiki pages only (sources are mostly binary PDFs; their digested pages carry the searchable text). The `wiki_search` MCP tool and `cogvault search` drop the `scope` parameter — a contract simplification recorded in 0021 and SPEC.md.
- **DB location**: `db_path` is absolute and lives outside the synced wiki folder (e.g. `~/.local/state/cogvault/`), so iCloud never syncs or evicts the DB.
- **Behavior knobs are code constants, not config** (KISS: no speculative configurability): LLM timeout 5m, max permanent-failure attempts 3, max file size 32MB, settle window 2m. Each is promoted to a config key only on demonstrated need. Files over the size cap are reported per run (like type-excluded files), not persisted as failures.
- **Consistency checks on a synced wiki dir**: the existing bounded-staleness check re-reads file content; on iCloud that can force re-downloads of evicted files. v2 gates content re-hash on a size+mtime change and treats dataless-read errors as per-file warnings, keeping search/serve non-blocking.

Data flow (Phase 1):

```
sources[].path ──scan──▶ stability gate ──▶ content hash ──new?──▶ llm.Adapter.Digest(file, schema)
                                                                      │ (claude --print subprocess)
                                                                      ▼
                                                           markdown source page
                                                                      │
                                        storage.Write ──▶ index.Upsert ──▶ ledger: success
                                        (failure at any step ──▶ ledger: failed + error, run continues)
```

## Interface

CLI:

```
cogvault ingest [--config <path>] [--dry-run] [--limit N]
```

`--dry-run` lists what would be digested; `--limit` bounds a run (backlog batching / quota control). A `--source` filter is deliberately omitted (YAGNI: Phase 1 has one source; add it with the phone-capture phase). Exit code is nonzero only on run-level failure, not per-file failures (those are in the report and ledger).

Config (v2, exact key names finalized in planning):

```yaml
wiki_dir: /Users/…/Mobile Documents/com~apple~CloudDocs/cogvault-wiki  # absolute, writable root
db_path: ~/.local/state/cogvault/cogvault.db                           # absolute, outside synced folder
sources:
  - path: ~/Downloads/_Articles
    types: [pdf]        # Phase 1 digests PDFs only (the real corpus); the filter itself is needed to skip e.g. .webp
llm:
  backend: claudecode   # interface admits future: local
```

## Data Model

New SQLite table `ingest_ledger`: source path, content hash, source directory, digested-at timestamp, resulting wiki page path, status (`success | failed | superseded`), attempt count, last error text, run origin (`scheduled | interactive` — Success Criterion 4 reads this). Keyed on (source path, content hash). Type-excluded files (e.g. `.webp`) are reported per run, not persisted. FTS5 tables unchanged.

## Testing

- Unit: `internal/ingest` with a mock `llm.Adapter`; ledger state transitions (new / already-processed / transient-failure-no-attempt / permanent-failure-attempt / attempts-exhausted / superseded-on-rehash); stability-gate skip; hash-based dedup including renamed files.
- Unit: `internal/llm/claudecode` against a fake `claude` executable in `testdata/bin` (records argv/stdin, returns canned JSON) — success, timeout, malformed output, nonzero exit.
- Unit: single-mode config (absolute `wiki_dir`/`db_path` accepted, leading-`~` expansion, overlap rejection); storage boundary = wiki root only; ingest source reads refuse symlinks and oversized files.
- Integration: end-to-end ingest over `testdata/fixtures` sources with the fake CLI, asserting pages exist, index hits, ledger rows; concurrent ingest+serve write smoke test (busy timeout effective).
- Validation (manual, not CI): real run over the 66-file corpus; results feed Success Criteria 1.
- All: `go test -race ./...`.

## Risks

- **Claude Code CLI interface or policy changes** — the standing risk from CLAUDE.md §8, now load-bearing. Mitigation: everything behind `llm.Adapter`; JSON output mode; local backend is the designed escape hatch.
- **PDF exceeds model context or headless PDF reading fails** — mitigation: max-file-size cap, failures isolated per file, O1 verified before the pipeline is built.
- **Quota exhaustion during the 66-file backlog** — mitigation: `--limit` batching; quota failures are classified transient (no attempt consumed), so files resume on later runs indefinitely instead of burning out the attempt budget.
- **launchd execution context differs from an interactive shell** — TCC may block `~/Downloads` reads, launchd's PATH excludes `~/.local/bin/claude`, and the CLI must be authenticated non-interactively. Mitigation: plist template with absolute paths + explicit PATH; README one-time grant procedure; O1 explicitly verifies digestion from a launchd-launched run, not just an interactive one.
- **Multi-process SQLite contention (scheduled ingest vs live serve)** — mitigation: single-instance ingest lock + busy timeout (in scope); WAL already enabled.
- **iCloud Drive quirks on the wiki dir** — writes: plain local file I/O; DB kept outside the synced folder (absolute `db_path`). Reads: consistency checks and search read wiki files, and iCloud may have evicted them (dataless files) — mitigation: treat dataless-read errors as per-file consistency warnings, not fatal; keep consistency-check cadence bounded (existing bounded-staleness design).
- **Schema non-compliance by the digestion LLM** — mitigation: prompt embeds `_schema.md`; page frontmatter is parsed after generation and a parse failure marks the file `failed`, not silently indexed.
- **Single-mode refactor breaks existing tests/contract** — accepted deliberately: v2 is a breaking release; SPEC.md is rewritten, and the migration is a one-time copy (in scope). The sole user is the project owner.

## Success Criteria

1. Backlog digested: ≥90% of the 65 corpus PDFs have a `success` ledger row and an indexed wiki page after backlog runs complete.
   - **Measured by**: `sqlite3 <db> "select status, count(*) from ingest_ledger group by status"` plus `cogvault search` spot checks on 5 known articles.
2. Zero-touch inflow works: a new PDF dropped into `_Articles` gains a wiki page with no manual command issued.
   - **Measured by**: drop one test PDF, wait one schedule interval, verify a `success` ledger row with `run origin = scheduled` (`launchctl list` shows the job; ledger row exists).
3. Re-find beats web re-search: over a 1-week validation, at least 4 of 5 real "where did I see that" attempts are answered from the wiki.
   - **Measured by**: judgment rubric — user logs each attempt (query, hit/miss, faster-than-web yes/no) in the validation note; pass = ≥4/5 hits.
4. Zero per-item instructions (the Step 9 inversion): between file arrival and page availability the user performs no action.
   - **Measured by**: ledger rows for the validation week all have `run origin = scheduled`, none `interactive` (backlog runs before the validation week start are exempt).
5. Test suite passes.
   - **Measured by**: `go test -race ./...` → all packages pass.

## Open Decisions

- **O1 — Headless PDF digestion mechanics**: whether `claude --print` can read a PDF by path directly (permissions, flags, output format) or needs `--output-format json` + tool allowlist tuning — verified both interactively **and from a launchd-launched run** (TCC/PATH/auth, review round 2 MAJOR-4). Owner: `implementing`, as the first task; result recorded in 0021. A negative result activates the conditional pdftotext fallback named in Scope Out.
- **O2 — Schedule interval default**: proposal 1h. Owner: user, at launchd setup.
- **O3 — Local LLM backend choice** (ollama vs llama.cpp vs other): owner: user, at the later phase that implements it.
- **O4 — Exact iCloud wiki folder path**: owner: user, at setup.
- **O5 — Consume-and-archive for dedicated phone inbox dirs** (vs leave-in-place used for `_Articles`): owner: `planning`, at the phone-capture phase.
- **O6 — resolved 2026-07-22**: vault mode is removed, not maintained (single-mode unification, user decision). Kept here for ID stability.
