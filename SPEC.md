# Spec — cogvault v2

Version: v2 (Phase 1)
Scope: v2 Phase 1 — the capture→digest→consume pipeline. Later phases (URL
capture, periodic digest, local LLM backend) get their own specs.
Status: **implemented; 1-week validation pending.** Issues found during use are
folded back into this spec.

Canonical design: `docs/specs/2026-07-22-refound-capture-pipeline-design.md`.
Refounding rationale: `docs/decisions/0021-v2-refounding.md` (supersedes 0020).

---

## 1. Overview

### 1.1 Purpose

A standalone personal knowledge pipeline. cogvault watches source folders the
user already fills, digests new material with an LLM into a searchable Markdown
wiki, and serves that wiki over MCP, a CLI, and a phone-readable synced folder.

The v1 "MCP wiki server hosted inside an Obsidian vault" is gone. cogvault has
one mode: `wiki_dir` is the sole storage root, and `sources` are plain external
directories the ingest pipeline reads directly (0021).

### 1.2 In scope (Phase 1)

- `cogvault ingest`: scan sources → hash → digest each PDF via the LLM adapter →
  write + index wiki pages → record a per-file outcome in the ledger → print a
  report.
- MCP stdio server: six tools (read, write, list, search, scan, parse).
- CLI: `init`, `search`, `serve`, `ingest`.
- `internal/llm` adapter interface with a `claudecode` backend (`claude --print`).
- Single-mode config/storage: absolute `wiki_dir` root, `sources[]`, absolute
  `db_path` outside the synced folder.
- SQLite FTS5 full-text search (trigram, Korean-friendly).
- launchd template + setup docs for zero-touch scheduled ingest.

### 1.3 Out of scope (Phase 1)

- Phone capture (share sheet, synced inbox), URL/web extraction, image OCR.
- Local LLM backend implementation (interface only).
- `pdftotext` text-extraction step (conditional fallback; **not activated** — O1
  verified headless PDF reading works, see 0021 D6).
- Watch mode / resident daemon (batch + launchd chosen instead).
- Periodic `cogvault digest` command.
- `wiki_delete`, auto-commit, vector search, ontology graph, `ResolveLink`.

---

## 2. Runtime structure

### 2.1 Layout (single mode)

`wiki_dir` is the entire storage root. Sources live wherever the user keeps them
and are not part of the wiki tree.

```
<wiki_dir>/                     # the sole storage root (may live under iCloud Drive)
├── _schema.md                  # rules definition (read-only)
├── sources/                    # digested source pages (one per source file)
└── (other pages the agent writes)

<source dir>/                   # e.g. ~/Downloads/_Articles — read directly by ingest,
    *.pdf                       #   never through storage, never MCP-addressable

<db_path>                       # absolute, OUTSIDE wiki_dir (e.g. ~/.local/state/cogvault/)
```

### 2.2 Access model

| Area | Read | Write |
|------|------|-------|
| `wiki_dir/` (general) | allowed (`exclude_read` excepted) | allowed |
| `wiki_dir/_schema.md` | allowed | **denied** (user edits only) |
| `exclude_read` paths (under `wiki_dir`) | **denied** | **denied** |
| `sources[]` directories | read directly by the ingest pipeline only | never written |

**Boundary contract (0021 D1)**: `internal/storage` and every MCP tool operate on
`wiki_dir` only. Sources are read by the ingest pipeline via plain `os` calls
(`Lstat` to refuse symlinks, size cap applied), never through storage and never
addressable via an MCP path.

### 2.3 Index

`wiki_list` (file_meta cache) + `wiki_search` (FTS5). No `_index.md`. The index
contains wiki pages only.

### 2.4 Schema delivery

The MCP server passes a schema **summary** as server instructions; the full text
is read with `wiki_read("_schema.md")`.

---

## 3. Config file

Location: any path, passed via `--config`. Default: `~/.config/cogvault/config.yaml`.

### 3.1 Schema

```yaml
wiki_dir: string        # absolute (after leading ~/ expansion). The sole storage root. Required.
db_path: string         # absolute (after leading ~/ expansion). MUST be outside wiki_dir. Required.
sources:                # external directories the ingest pipeline reads. May be empty.
  - path: string        # absolute (after leading ~/ expansion)
    types: [string]     # allowed extensions, lowercase, no leading dot (e.g. [pdf])
llm:
  backend: string       # default "claudecode". Only "claudecode" accepted in Phase 1.
exclude: string[]       # scan + index exclusion, relative to wiki_dir. Default: [".obsidian", ".trash"]
exclude_read: string[]  # read + scan + index exclusion, relative to wiki_dir. Default: []
adapter: string         # default "obsidian". Allowed: "obsidian", "markdown".
consistency_interval: int  # min seconds between consistency checks. Default: 5.
```

### 3.2 Path handling

- A leading `~/` (or an exact `~`) expands to `$HOME`. This applies to `wiki_dir`,
  `db_path`, and every `sources[].path`.
- A `~` anywhere else in a path (e.g. iCloud's `com~apple~CloudDocs`) is literal.
- After expansion, `wiki_dir`, `db_path`, and `sources[].path` must be absolute.
  This inverts the v1 rule (v1 required these to be vault-relative).
- `exclude`/`exclude_read` remain wiki-root-relative (v1 rules retained: no `..`,
  no absolute, no empty, no `"."`).

### 3.3 Validation

Config validates path-string safety and policy only; filesystem state is enforced
later (0001). Rejected:

- `wiki_dir`, `db_path`, or any `sources[].path` not absolute after expansion.
- A source that contains, is contained by, or equals `wiki_dir` (overlap).
- `db_path` inside `wiki_dir`.
- `db_path` that is a directory-like path.
- `adapter` outside the allowed list; `llm.backend` other than `claudecode`.
- Unknown YAML keys (`KnownFields(true)`) and multi-document YAML.

`wiki_dir`/`db_path` have no invented defaults: an empty value is a validation
error, which drives the two-step `init` (§9.1).

---

## 4. Error types

### 4.1 Storage/adapter sentinels (`internal/errors`)

| Error | Meaning |
|------|------|
| `ErrNotFound` | path missing, or a directory op on a non-directory |
| `ErrPermission` | access denied (write protection, read exclusion) |
| `ErrTraversal` | path contains `..` or is absolute where relative is required |
| `ErrSymlink` | symlink component in the path |
| `ErrNotMarkdown` | parse attempted on a non-`.md` file |

### 4.2 Ingest error classes (`internal/ingest` / `internal/llm`)

Per-file failures are classified; the run always continues past them (§10.3).

| Class | Examples | Attempt cost |
|------|------|------|
| transient | quota/rate limit, timeout, CLI transport, `error_during_execution` | **none** — retries on later runs |
| permanent | malformed LLM output, schema-invalid page | consumes one attempt (bounded at 3) |
| infra | storage write / index add / ledger write failure | **none** — local infra, not the file |

`llm.ErrTransient` is the wrapped sentinel for the transient class.

---

## 5. Storage

Storage is rooted at the absolute `wiki_dir`. It never touches `sources[]`.

### 5.1 Interface

```
Read(path) → ([]byte, error)
Write(path, data []byte) → error
List(prefix) → ([]ListEntry, error)
Exists(path) → (bool, error)
Stat(path) → (size int64, mtime time.Time, error)
```

### 5.2 Path rules

- Paths are relative to `wiki_dir`.
- Empty path `""` → `ErrNotFound`.
- Absolute path or a `..` component → `ErrTraversal`.
- Symlink component → `ErrSymlink`.

### 5.3 Read / Stat

- `exclude_read` → `ErrPermission`.
- Missing file → `ErrNotFound`.
- `Stat` returns size and mtime for the consistency stat-gate (§6.9); same error
  mapping as Read.

### 5.4 Write

- The **whole `wiki_dir` root is writable** (no `wiki_dir/` sub-prefix check —
  that was the vault-mode rule and is removed).
- `_schema.md` → `ErrPermission`.
- Intermediate directories auto-created; existing files overwritten.
- Index reflection is eventual (§5.7).

### 5.5 List

- `prefix` must be a directory; a file path → `ErrNotFound`.
- Direct children only (non-recursive); directories end with `/`.
- `exclude`/`exclude_read` filtered out.
- Listing an `exclude_read` directory itself → `ErrPermission` (not an empty
  list — existence must not leak).

### 5.6 Exists

- `exclude_read` → always `false`.

### 5.7 Consistency model

Eventual consistency. Transient write↔index divergence is allowed; automatic
detection and repair are guaranteed (bounded staleness, §6.9).

### 5.8 Concurrency

- Same-path writes serialized (single global mutex, 0006); last-write-wins.
- Read↔Write concurrent.

---

## 6. Index

### 6.1 Interface

```
Add(path, content string, meta map[string]string) → error
Search(query string, limit int) → ([]Result, error)
Remove(path) → error
Rebuild(storage, adapter) → error
CheckConsistency(storage, adapter, force bool) → (added, removed, updated int, error)
GetMeta(path) → (*FileMeta, error)
```

`Search` has **no `scope` parameter** (removed in v2 — 0021 D5).
`GetMeta` on a missing path → `ErrNotFound`. `Add`'s `meta` map carries keys
`title`, `type`, `tags` (`tags` comma-separated).

### 6.2 Result / FileMeta

```
Result   { Path, Title, Type, Snippet string; Score float64 }
FileMeta { Path, Title, Type, ContentHash, IndexedAt string }
```

### 6.3 Data tables

```
wiki_fts(path, title, content, tags)                          -- FTS5, trigram
file_meta(path PK, title, type, content_hash, size, mtime, indexed_at)
```

`file_meta` gains `size`/`mtime` for the stat-gate (§6.9).

### 6.4 Schema versioning

`PRAGMA user_version = 2`. On open, a DB carrying tables at `user_version < 2` is
dropped and recreated at v2 (v2 uses a fresh DB at a new absolute `db_path`, so no
data migration exists). Every connection is opened with `busy_timeout=5000` so
multi-process contention surfaces as a wait, not "database is locked".

### 6.5 Index coverage

All wiki `.md` under `wiki_dir`, minus `exclude`/`exclude_read`. Sources are not
indexed (they are mostly binary; their digested pages carry the text).

### 6.6 Search behavior

`limit` default 10, max 100. Descending score. Empty result = empty array.
Korean supported, including queries ≤ 2 characters (LIKE fallback). Method is
internal (FTS5 MATCH for ≥ 3 chars with trigram; LIKE otherwise).

### 6.7 Consistency (bounded staleness)

- `wiki_list`/`wiki_search` guarantee consistency before returning.
- `force=false`: skipped if within `consistency_interval` (default 5s).
- `force=true`: immediate.
- Guarantees: deleted files removed, changed files re-indexed, new files added;
  `exclude` rules honored.
- **Stat-gate**: a file's content is re-read and re-hashed only when its size or
  mtime differs from the stored `file_meta` row (guards against forcing iCloud to
  re-download evicted files on every check). Dataless/eviction read errors are
  per-file warnings, not fatal.

---

## 7. Adapter

Two adapters implement this interface: `obsidian` (default) parses `wiki_dir`
pages using Obsidian wikilink syntax; `markdown` is a standard-Markdown fallback
(config `adapter: markdown`) using `[text](target)` / `![alt](target)` link
syntax instead of wikilinks. `ResolveLink` still not provided.

- `Scan`: a non-directory `root` → `ErrNotFound`; a callback (`fn`) error aborts
  Scan immediately (returned as the Scan error, not swallowed).

### 7.1 Interface

```
Name() → string
Scan(root, exclude []string, fn func(path string) error) → error
Parse(root, relPath string, includeContent bool) → (*Source, error)
```

### 7.2 Source

```
Source {
    Path, Title string
    Content string               # only when includeContent=true
    Frontmatter map[string]any
    Links []string               # bracket-free targets
    Attachments []string         # bracket-free filenames
    Tags []string
    DataviewFields map[string]string
    Aliases []string
    SourceType string            # "obsidian" | "markdown"
}
```

### 7.3 Parsing rules

- `.md` only; non-`.md` → `ErrNotMarkdown`.
- Frontmatter via `adrg/frontmatter`; on failure, empty frontmatter + whole body.
- Title: frontmatter `title` > first `#` heading > filename.
- Wikilinks stored bracket-free: `[[t]]`, `[[t|d]]`, `[[t#h]]` → Links `"t"`;
  `![[f]]`, `![[f|s]]` → Attachments `"f"`. Code-block wikilinks not excluded
  (false positives allowed in Phase 1).

---

## 8. MCP tools

### 8.1 Common rules

- Transport: stdio. Server name `cogvault`.
- `path`: relative to `wiki_dir`.
- Sentinel → message mapping:

| sentinel | message |
|----------------|-----------|
| `ErrNotFound` | `"not found: {path}"` |
| `ErrPermission` | `"access denied: {path}"` |
| `ErrTraversal` | `"invalid path: {path}"` |
| `ErrNotMarkdown` | `"not a markdown file: {path}"` |
| other | `"internal error: {message}"` |

### 8.2 wiki_read

`path: string` (required) → file content. Errors: `ErrNotFound`, `ErrPermission`,
`ErrTraversal`, `ErrSymlink`.

### 8.3 wiki_write

`path: string`, `content: string` (required) → `{status:"written", path, bytes, warnings:[]}`.
`bytes` = `len(content)`; `warnings` always empty in Phase 1. Side effect:
best-effort index reflection. Errors: `ErrPermission`, `ErrTraversal`, `ErrSymlink`.

### 8.4 wiki_list

`prefix: string` (optional, default `""`) → `[{path, name, is_dir, title, type}]`.
Non-recursive; title/type from the `GetMeta` cache. Errors: `ErrNotFound`,
`ErrTraversal`.

### 8.5 wiki_search

`query: string` (required), `limit: int` (optional, default 10) →
`[{path, title, type, snippet, score}]`. Empty result is not an error.

- **No `scope` parameter** (removed in v2). The input schema does not advertise
  `scope`. Note: mcp-go does not set `additionalProperties:false`, so a stray
  `scope` argument is silently **ignored**, not schema-rejected — the contract is
  simply that `scope` no longer exists.
- `snippet`: short excerpt around the match, or empty.
- `score`: sort-only `float64`; only relative order within one response is
  meaningful.

### 8.6 wiki_scan

`dir: string` (optional, default `""`) → array of `.md` paths under `wiki_dir`
(recursive). Errors: `ErrNotFound`.

### 8.7 wiki_parse

`path: string` (required), `include_content: bool` (optional, default `false`) →
Source (JSON). Errors: `ErrNotFound`, `ErrPermission`, `ErrNotMarkdown`.

---

## 9. CLI

Every command takes `--config <path>` (default `~/.config/cogvault/config.yaml`).
launchd invokes `cogvault ingest --config <path>` explicitly (no useful cwd).

### 9.1 init (two-step)

```
cogvault init [--config <path>]
```

Because `wiki_dir`/`db_path` have no defaults, `init` is two-step:

1. **First run** (no config file): scaffold a template config at the config path
   (creating the parent dir), print `created <path>; edit wiki_dir/db_path/sources,
   then re-run cogvault init`, and exit 0. If the file already existed but fails
   validation, return the error instead.
2. **Second run** (valid config): create `wiki_dir`, copy `_schema.md`, create the
   DB parent dir and DB; new DB → full index (Rebuild), existing DB →
   `CheckConsistency(force=true)`.

Idempotent; existing files are not overwritten.

### 9.2 search

```
cogvault search [--config <path>] <terms>
```

No `--scope` flag (removed in v2).

### 9.3 serve

```
cogvault serve [--config <path>]
```

Init failure → exit 1 + stderr. Runtime tool errors are returned to the client;
the server keeps running.

### 9.4 ingest

```
cogvault ingest [--config <path>] [--dry-run] [--limit N] [--scheduled]
```

- `--dry-run`: list what would be digested; write nothing (no LLM/store/index/
  ledger mutation).
- `--limit N`: process at most N files (backlog batching / quota control).
- `--scheduled`: set the ledger run origin to `scheduled` (used by the launchd
  job); otherwise the origin is `interactive`.
- Requires `claude` on PATH; if absent: `claude CLI not found in PATH; install
  Claude Code or add it to PATH`.
- Prints the per-run report (§10.4) to stdout.
- **Exit codes**: nonzero only on a run-level failure (e.g. lock contention:
  `ingest already running (lock held)`, or a ctx-cancelled run). Per-file
  failures inside a completed run do **not** fail the run (exit 0).

---

## 10. Ingest pipeline & ledger

### 10.1 Flow (per file)

```
scan source dir (top level only, Lstat: skip dirs/symlinks)
  → type filter (lowercase ext vs sources[].types)
  → size cap (32MB; oversized files reported, not persisted)
  → settle-window gate (skip files modified within 2m — mid-download guard)
  → sha256 content hash → ledger lookup (skip if already processed)
  → llm.Digest(source path, schema) via claude --print
  → validate page frontmatter from bytes (non-empty map + title)
  → collision-aware page path under sources/
  → storage.Write → index.Add → ledger: success
(any failure → ledger: failed + classified error; the run continues)
```

### 10.2 Page identity

One source file path → one wiki page `sources/<slug>.md`, where `<slug>` is the
source base name with its extension stripped. If that page path is already mapped
to a different source path, the page becomes `sources/<slug>-<hash8>.md`
(`hash8` = first 8 hex of sha256 of the absolute source path). Re-digestion of the
same source path with new content overwrites its page and marks the prior ledger
row `superseded`.

### 10.3 Failure isolation

Per-file failures are classified (§4.2) and never abort the run. Permanent
failures increment `attempts`, bounded at 3; at the bound the file is skipped in
later runs (reported as exhausted). Transient and infra failures leave `attempts`
unchanged so the file retries indefinitely.

### 10.4 Report

A per-run report with counts (digested, failed, skipped, deferred, unchanged) and
a per-file list (action ∈ `digested | would-digest | failed | skipped | deferred |
exhausted`, plus an error string). Oversized/type-excluded files appear in the
report but not the ledger. Unchanged (already-processed) files are counted but
kept out of the per-file list for readable backlog reports.

### 10.5 Concurrency

A single-instance exclusive `flock` on `<dir(db_path)>/ingest.lock`, acquired for
the duration of a run, prevents scheduled and manual runs from overlapping; a
second run fails fast with the already-running error. Every DB opener (ingest
ledger + index) sets `busy_timeout` so cross-process writes wait rather than fail.

### 10.6 Ledger (`ingest_ledger`)

```
ingest_ledger(
  source_path TEXT, content_hash TEXT, source_dir TEXT,
  digested_at TEXT, wiki_page TEXT,
  status TEXT,            -- success | failed | superseded
  attempts INTEGER, last_error TEXT,
  run_origin TEXT,        -- scheduled | interactive
  PRIMARY KEY (source_path, content_hash)
)
```

Owned by `internal/ingest` through its own DB connection to `db_path` (WAL +
busy_timeout make the second connection safe). Type-excluded/oversized files are
reported per run, not persisted. Source originals are never moved or deleted.

### 10.7 Behavior constants (not config)

LLM timeout 5m, max permanent-failure attempts 3, max file size 32MB, settle
window 2m. Each is promoted to a config key only on demonstrated need.

---

## 11. _schema.md

Read-only. The server passes a summary as instructions; the full text is read via
`wiki_read("_schema.md")`. Enforced type: `source` only; other types are free-form.
The default schema (embedded via `go:embed`) defines source-page frontmatter
(`type: source`, `source_path`, `ingested_at`) and required sections, plus
provenance rules (`[TODO: source needed]`, `[UNCERTAIN]`). The ingest digestion
prompt embeds these rules so generated pages conform.

---

## 12. Validation (v2)

Success criteria (full detail in the design spec, "Success Criteria"):

1. **Backlog digested** — ≥90% of the corpus PDFs have a `success` ledger row and
   an indexed page.
2. **Zero-touch inflow** — a newly dropped PDF gains a page with no manual command
   (ledger row with `run_origin = scheduled`).
3. **Re-find** — ≥4/5 real "where did I see that" attempts answered from the wiki
   over a 1-week validation.
4. **Zero per-item instructions** — validation-week ledger rows are all
   `scheduled`, none `interactive`.
5. **Tests pass** — `go test -race ./...`.

---

## 13. Dependencies

```
modernc.org/sqlite              # Pure Go SQLite
github.com/mark3labs/mcp-go     # MCP SDK (v0.47.0)
github.com/spf13/cobra          # CLI
gopkg.in/yaml.v3                # YAML
github.com/adrg/frontmatter     # frontmatter parsing
golang.org/x/sys                # unix.Flock for the ingest lock
```

The `claudecode` backend shells out to the `claude` CLI (Claude Code); it is a
runtime dependency of `cogvault ingest`, not a Go module.

---

## Appendix A: Test requirements (v2 highlights)

### Config
- Absolute `wiki_dir`/`db_path` accepted; leading `~/` expands; `~` mid-path
  literal. Relative after expansion → error. Source overlapping `wiki_dir` (either
  direction / equal) → error. `db_path` inside `wiki_dir` → error. Unknown key →
  error. `llm.backend != claudecode` → error.

### Storage
- Whole wiki root writable; `_schema.md` write → `ErrPermission`. `..`/absolute →
  `ErrTraversal`; symlink → `ErrSymlink`; `exclude_read` read → `ErrPermission`,
  Exists → false. `Stat` returns size/mtime.

### Index
- Add→Search round-trip incl. Korean ≤2-char queries. `Search(query, limit)` has
  no scope. `user_version < 2` DB dropped/recreated. Stat-gate skips re-hash when
  size+mtime unchanged. Per-file Read error surfaces as a joined error while other
  files index.

### LLM
- Fake `claude` in `testdata/bin` records argv/stdin, returns canned JSON.
  Success; timeout → `ErrTransient`; rate-limit/nonzero-exit → `ErrTransient`;
  malformed JSON → permanent; missing binary → transport (transient).

### Ingest
- Two files → pages + index rows + `success` rows. Second run digests nothing
  (hash dedup). Settle-window deferral. Content change → old row `superseded`.
  Slug collision → `-<hash8>` page. Oversized/type-excluded reported, not
  persisted. `--limit 1` processes one. Dry-run writes nothing. Transient → row
  `failed`, attempts 0. Permanent → attempts++, exhausted at 3. Unparsable page →
  permanent, no file written. Infra write failure → spares attempts. Second Runner
  holding the lock → fast already-running error. Mid-run ctx cancel → partial
  report, lock released.

### MCP
- Round-trip; sentinel mapping; write-then-search; list title/type = GetMeta;
  parse include_content; `wiki_search` schema has no `scope`.

### CLI
- `init` two-step (first run scaffolds + guidance; second run builds wiki/schema/
  db); idempotent. Missing config → error naming the path. `ingest --dry-run`
  lists pending; lock held → nonzero already-running; missing `claude` → clear
  error. `serve` init failure → exit 1.

### Integration (U9)
- Backlog run → N pages/rows/`success`. Incremental run digests only the new file
  with `run_origin` from `--scheduled`. Ratelimit on one file → that file `failed`
  attempts 0, others succeed, exit 0. Concurrent serve+ingest → no "database is
  locked" (busy_timeout effective).
