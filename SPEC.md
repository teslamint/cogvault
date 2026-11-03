# Spec — cogvault v2

Version: v2 (Phase 1)
Scope: the capture→digest→consume pipeline. §1.2 and §1.3 describe what the
program does now, not what Phase 1 delivered — the delivery record is
ROADMAP.md. A later phase large enough to need its own design still gets its
own spec.
Status: **implemented; validation complete** (SC1–SC4 met, 2026-07-29; see F1 in
`docs/research/v2-follow-ups.md`). Issues found during use are folded back into
this spec.

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

### 1.2 In scope

- `cogvault ingest`: scan sources → hash → digest each source file via the LLM adapter →
  write + index wiki pages → record a per-file outcome in the ledger → print a
  report.
- MCP server: three transports (`stdio`, `sse`, `http`/Streamable HTTP); seven
  tools (read, write, delete, list, search, scan, parse).
- CLI (§9): `init`, `search`, `serve`, `ingest`, plus `fetch`, `digest`,
  `synthesize`, `embed`, `similar`, `graph`, `index`, `lint`.
- `internal/llm` adapter interface with `claudecode` and `ollama` path-mode
  backends, plus a local `openai` text-mode backend for extracted PDFs and
  Markdown/plain-text sources.
- SQLite FTS5 full-text search (trigram, Korean-friendly).
- launchd template + setup docs for zero-touch scheduled ingest.

### 1.3 Out of scope

- Native provider PDF/image attachments; local providers receive extracted text.
- Dedicated phone-capture tooling: a share-sheet target, and consume-and-archive
  semantics for inbox directories. A folder the phone syncs into is already
  usable as an ordinary `sources[].path`.
- PDF chunking/map-reduce or partial summaries.
- Watch mode / resident daemon (batch + launchd chosen instead).
- General git auto-commit is opt-in, off by default (`git.auto_commit`, §3.1,
  0024). `wiki_delete` attempts to auto-commit its own deletion regardless of
  this setting, but only actually commits when the deleted file was already
  git-tracked (§8.8, unchanged since before 0024).

Six entries that this list carried through Phase 1 have since shipped and are
gone from it: URL/web extraction, the local LLM backend, periodic `cogvault
digest`, vector search, the ontology graph, and `ResolveLink`. They are recorded
as delivered in ROADMAP.md. This section states current boundaries; it is not a
record of what Phase 1 excluded.

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
    *.pdf, *.md, ...            #   never through storage, never MCP-addressable

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

`wiki_list` (file_meta cache) + `wiki_search` (FTS5). The index contains wiki
pages only.

Retrieval never depends on an `_index.md` file. `cogvault index` (§9.11) writes
one as a generated, human-readable view of the wiki, and the index treats it as
an ordinary page; nothing reads it back.

### 2.4 Schema delivery

The MCP server passes a schema **summary** as server instructions; the full text
is read with `wiki_read("_schema.md")`.

### 2.5 Signed installation helper

`scripts/install-signed.sh` provides the one-command installation path for a
scheduled macOS deployment. It must:

- require exactly one valid `Developer ID Application` identity unless the
  caller passes an identity explicitly;
- delegate atomic build and destination signing to `make install`;
- reject an installed binary whose strict signature verification fails or whose
  identifier is not `dev.tmint.cogvault`;
- restart the loaded `com.teslamint.cogvault.ingest` launchd job, and skip that
  step without error when the job is not loaded; and
- never reset or modify TCC grants.

---

## 3. Config file

Location: any path, passed via `--config`. Default: `~/.config/cogvault/config.yaml`.

### 3.1 Schema

```yaml
wiki_dir: string        # absolute (after leading ~/ expansion). The sole storage root. Required.
db_path: string         # absolute (after leading ~/ expansion). MUST be outside wiki_dir. Required.
sources:                # external directories the ingest pipeline reads. May be empty.
  - path: string        # absolute (after leading ~/ expansion)
    types: [string]     # allowed extensions, lowercase, no leading dot (e.g. [pdf, md])
llm:
  backend: string       # default "claudecode". Allowed: "claudecode", "ollama", "openai".
  model: string         # optional generally; required by "openai".
  base_url: string      # ollama/openai API root; openai must be loopback http.
  max_input_chars: int  # openai source text ceiling, 1..1000000; required by openai.
  timeout_seconds: int  # optional. Per-digest-call bound. Default: 300. Negative rejected.
  embedding_model: string    # optional. Required by `cogvault embed` (§9.8).
  embedding_base_url: string # optional. Default: http://localhost:11434.
adapter: string         # default "obsidian". Allowed: "obsidian", "markdown".
consistency_interval: int  # min seconds between consistency checks. Default: 5.
auth:                   # HTTP/SSE transport authorization (§8.1, §9.3). Ignored for stdio.
  mode: string             # "none" (default), "bearer", or "oauth".
  max_body_mb: int         # request body cap in MB. Default: 4.
  max_stream_seconds: int  # stream lifetime cap in seconds, applied when no token expiry governs it. Default: 3600.
  oauth:
    issuer: string           # required when mode: oauth. Absolute https:// URL of the identity provider.
    audience: string         # optional. Defaults to the advertised resource (<public-url><endpoint-path>);
                              # an explicit value that disagrees with it is a startup error.
    required_scopes: [string]  # optional. Every scope must be present in the token's scope claim.
    jwks_ttl_seconds: int    # JWKS cache TTL in seconds. Default: 900.
git:                     # opt-in wiki auto-commit safety net (0024). Off by default.
  auto_commit: string      # "off" (default), "write", or "write+ingest".
archive:                 # experimental research archive (§9.16, ADR 0027 proposed). Omitted = no archive.
  root: string             # absolute. Must not overlap wiki_dir, any sources[].path, or contain db_path.
  capture_enabled: bool    # default false. Exposes the archive MCP tools (§9.16). The only key a reload changes.
  deployment:              # operator-supplied attestations; all required to enable capture (§9.16).
    backup_target: string
    backup_schedule: string
    restore_test_report: string   # absolute path of an `archive restore-test` report.
    permissions_owner: string
    handoff_mount: string
    deletion_log_replica: string  # absolute path outside root: off-NAS copy of the deletion log.
```

`auth.max_body_mb`, `auth.max_stream_seconds`, and `auth.oauth.jwks_ttl_seconds`
reject only negative values. An explicit `0` is indistinguishable from an
omitted key — both are the Go zero value for a plain `int` — so it takes the
default rather than erroring; see
`docs/deviations/2026-08-11-auth-zero-value-indistinguishable-from-absent.md`.

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
- `adapter` outside the allowed list; `llm.backend` outside
  `claudecode`/`ollama`/`openai`. For `openai`: a missing or non-loopback
  `base_url`, a missing `model`, a `sources[].types` entry other than `pdf`,
  `md`, `markdown`, or `txt`, or
  `max_input_chars` outside 1..1000000.
- `max_file_size_mb` negative (zero or omitted defaults to 32).
- `auth.mode` outside `none`/`bearer`/`oauth`.
- `auth.oauth.issuer` empty or not an absolute `https://` URL, when `auth.mode`
  is `oauth`.
- `auth.max_body_mb`, `auth.max_stream_seconds`, `auth.oauth.jwks_ttl_seconds`
  negative (zero or omitted defaults to 4/3600/900 — see the deviation
  addendum above).
- `auth.max_stream_seconds` above 86400. Durations derived from it — the
  streamable HTTP session sweeper's idle TTL is twice this value — overflow
  int64 nanoseconds into a negative duration at absurd inputs, which mcp-go
  reads as "sweeper disabled" and which would silently restore the session
  leak the sweeper prevents. The ceiling refuses the input rather than failing
  open.
- Unknown YAML keys (`KnownFields(true)`) and multi-document YAML.

`Config.AllExcluded()` returns the effective scan/index exclusion list. It always
contains `sources/_archived` exactly once, even for older configs whose explicit
`exclude` list omits it. This does not mutate the user's configured `exclude`
list.

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
| transient | quota/rate limit, timeout, CLI transport, generic API/CLI failure, `error_during_execution` | **none** — retries on later runs |
| permanent | malformed LLM output, schema-invalid page | consumes one attempt (bounded at 3) |
| infra | storage write / index add / ledger write failure | **none** — local infra, not the file |
| refused | anchored provider policy/AUP refusal | **none** — terminal under the same model and content hash |

`llm.ErrTransient` and `llm.ErrRefused` are the wrapped LLM sentinels. Refusal
classification and diagnostic persistence are deliberately separate. Only the
final `type: "result"` event participates. It is refusal-classifiable when
`terminal_reason == "api_error"` regardless of `is_error`, or when
`is_error == true` and `subtype == "error_during_execution"`. Its structured
`result` is eligible for persistence only when `is_error == true`, one of those
two failure channels is present, and the trimmed result is non-empty, contains
no CR/LF, and does not begin with `---`, `#`, or `` ``` ``.

Every authoritative candidate is canonicalized before classification or
display: recognized ANSI SGR/OSC sequences are removed, Unicode whitespace is
collapsed to one ASCII space, and remaining Unicode Control or Format (`Cf`)
runes become `U+FFFD`. The case-insensitive refusal predicate is anchored to the
start of the canonicalized candidate: `policy refusal:`, or `API Error:`
followed by `refused`, bare `safeguards flagged`, or the exact observed provider
envelope `Fable 5's safeguards flagged` (ASCII or curly apostrophe). Unknown
provider-name safeguard envelopes, quoted, negated,
embedded, suffix-only, `connection refused`, generic `api_error`, and generic
`API Error:` messages remain transient.

---

## 5. Storage

Storage is rooted at the absolute `wiki_dir`. It never touches `sources[]`.

### 5.1 Interface

```
Read(path) → ([]byte, error)
Write(path, data []byte) → error
Move(src, dst) → error
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

Exact-path reads remain allowed for archived pages unless that exact path also
falls under `exclude_read`. Internal archive exclusion affects scan/index
surfaces, not direct retention reads.

### 5.4 Write

- The **whole `wiki_dir` root is writable** (no `wiki_dir/` sub-prefix check —
  that was the vault-mode rule and is removed).
- `_schema.md` → `ErrPermission`.
- Intermediate directories auto-created; existing files overwritten.
- Index reflection is eventual (§5.7).
- Before replacing an existing Markdown page, reject omission or emptying of
  its explicit `usage_terms` purpose, conditions, scope, where, level, or evidence fields.
  The original bytes remain unchanged on rejection. This applies to every
  storage caller, including MCP and generated-output commands; no silent
  content rewrite is performed behind the index caller.

### 5.5 Move

- `exclude_read` on either source or destination → `ErrPermission`.
- `_schema.md` on either side → `ErrPermission`.
- Permission checks happen before existence checks.
- For allowed paths, missing source → `ErrNotFound`.
- For allowed paths, occupied destination returns an error that wraps
  `os.ErrExist`.
- Exact-path archive moves use this contract; a missing live page takes
  precedence over an occupied archive destination.
- Parent directories for the destination are auto-created.

### 5.6 List

- `prefix` must be a directory; a file path → `ErrNotFound`.
- Direct children only (non-recursive); directories end with `/`.
- `exclude`/`exclude_read` filtered out.
- Listing an `exclude_read` directory itself → `ErrPermission` (not an empty
  list — existence must not leak).

### 5.7 Exists

- `exclude_read` → always `false`.

### 5.8 Consistency model

Eventual consistency. Transient write↔index divergence is allowed; automatic
detection and repair are guaranteed (bounded staleness, §6.9).

### 5.9 Concurrency

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
multi-process contention surfaces as a wait, not "database is locked" — **except**
the FTS5 read→write tx-upgrade path (`index.Add`'s DELETE-then-INSERT runs in a
DEFERRED transaction; the FTS5 DELETE reads shadow tables first, taking a read
snapshot). A concurrent committed writer makes the upgrade return
`SQLITE_BUSY_SNAPSHOT`, which `busy_timeout` by design does not retry. On the
ingest side this lands in the infra error class (attempts spared, self-heals next
run); see §10.5.

### 6.5 Index coverage

All wiki `.md` under `wiki_dir`, minus `exclude`/`exclude_read`. Sources are not
indexed (they are mostly binary; their digested pages carry the text).

### 6.6 Search behavior

`limit` default 10, max 100. Descending score. Empty result = empty array.
Korean supported, including queries ≤ 2 characters (LIKE fallback). Method is
internal (FTS5 MATCH for ≥ 3 chars with trigram; LIKE otherwise). Multi-word
FTS queries match **per token**: each whitespace-separated token is quoted
individually and joined with implicit AND, so `hacking bitcoin` finds pages
containing both words anywhere — not only the adjacent phrase (F1/SC3).
LIKE-fallback results are ordered by path for determinism.

`Rebuild` clears `wiki_fts` and `file_meta` **and the `embeddings` table when
it exists** — embeddings are keyed `(path, model)` with no FK to `file_meta`,
so a bulk clear that skips them would orphan rows for deleted pages forever
(the per-path `Remove` path already cleans its own). A rebuild on a DB that
never used embeddings must not fail on the absent table.

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
  `![[f]]`, `![[f|s]]` → Attachments `"f"`. Wikilinks inside fenced or inline
  code are excluded.

---

## 8. MCP tools

### 8.1 Common rules

- Transport: `stdio` (default), `sse`, or `http` (Streamable HTTP), selected
  via `cogvault serve --transport` (§9.3). Server name `cogvault`.
- The `wiki_*` tools below are always registered. The experimental
  `archive_*` tools are registered only while archive capture is enabled
  (§9.16); they never act on `wiki_dir`.
- Authorization (`sse`/`http` only — `stdio` has no network boundary and is
  never gated): `auth.mode` (§3.1) selects one of three modes. `none` requires
  a loopback `--addr` and applies no credential check. `bearer` requires an
  `Authorization: Bearer <token>` header matching `COGVAULT_BEARER_TOKEN`
  (constant-time compare). `oauth` requires a JWT access token validated
  against an operator-configured identity provider (issuer, audience,
  required scopes, signature via JWKS) — opaque access tokens are not
  supported. See `docs/deployment/remote-mcp.md` for setup and the security
  posture.
- Rejection responses (`sse`/`http` only): `401` with a `WWW-Authenticate:
  Bearer` header for a missing credential, or the same header additionally
  carrying `error="invalid_token"` for an invalid one (the `oauth`-mode
  challenge additionally carries `resource_metadata` pointing at the
  Protected Resource Metadata document in both cases). Two distinct `403`s:
  `error="insufficient_scope"` with a `WWW-Authenticate` header for a valid
  `oauth` token missing a required scope; a bare `403` with no
  `WWW-Authenticate` header at all for a request whose `Origin` header names
  neither a loopback origin nor `--public-url` — the `insufficient_scope`
  error code never applies to an `Origin` rejection. `413` for a request
  whose `Content-Length` header declares a size over `auth.max_body_mb`; a
  chunked request with no `Content-Length` is instead cut off mid-body by
  `MaxBytesReader` once it crosses the same limit, which surfaces as a
  JSON-RPC parse error, not a `413`.
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
`bytes` = `len(content)`. For `.md` paths, `warnings` lists frontmatter problems
(missing frontmatter or `title`; a `type: source` page missing `source_path` or
`ingested_at`; a `sources/` page whose `type` is not `source`; a `source_path`
under `claude-chat-attachment/`) and one `unresolved wikilink: [[t]]` per
distinct non-embed link target that resolves to no page (§9.12 resolution; the
written page itself counts). Evidence diagnostics are appended after these
existing warnings as `[EVIDnnn] claim-id: message; fix: hint` (omitting
`claim-id: ` when empty). These warnings never block the write; status remains
`written` (§11.1). Side effect: best-effort index reflection. When
`git.auto_commit` is `write` or `write+ingest` (§3.1, default `off`), also
auto-commits the write (`git add` + `git commit -m "wiki: write <path>"`) when
`wiki_dir` is a git repository; a failed `git add`/`git commit` is logged, not
returned as a tool error, and is bounded as described in §8.8.1 (0024).

Paths git reads as configuration are rejected with `ErrPermission`,
independently of `exclude`/`exclude_read`: any path containing a `.git`,
`.gitattributes`, or `.gitmodules` component, at any depth, compared
case-insensitively. This is not a tidiness rule — `git add` executes the
clean filter that `.gitattributes` names, using the command line
`.git/config` defines, so a writable `.gitattributes` plus a writable
`.git/config` turns the auto-commit path into arbitrary command execution as
the server process on the *next* ordinary write. The comparison is
case-insensitive because on a case-insensitive filesystem (APFS, NTFS) a
write to `.GIT/config` lands in the real `.git/config`; the rule is applied
uniformly so the boundary does not vary by host. `.gitignore` remains
writable: it selects paths, it never names a command. `wiki_delete` and
internal moves apply the same rule.

Errors: `ErrPermission`, `ErrTraversal`, `ErrSymlink`.

### 8.4 wiki_list

`prefix: string` (optional, default `""`) → `[{path, name, is_dir, title, type, category}]`.
Non-recursive; title/type/category from the `GetMeta` cache. Errors: `ErrNotFound`,
`ErrTraversal`.

### 8.5 wiki_search

`query: string` (required), `limit: int` (optional, default 10) →
`[{path, title, type, category, snippet, score, usage_terms, research_evidence}]`. Empty result is `[]`, not an error.

- **No `scope` parameter** (removed in v2). The input schema does not advertise
  `scope`. `internal/mcp/server.go`'s `noExtra` wrapper sets
  `InputSchema.AdditionalProperties = false` on every tool (`mcp.NewTool`
  itself does not), so `tools/list` now advertises no extra properties. That
  changes only the advertisement: mcp-go's dispatcher never validates call
  arguments against the schema, so a stray `scope` argument is still silently
  **ignored** by the handler at runtime, not rejected — the contract is simply
  that `scope` no longer exists.
- `snippet`: short excerpt around the match, or empty.
- `score`: sort-only `float64`; only relative order within one response is
  meaningful.
- `usage_terms` is the shared search summary: `scope`, `where`, `level`,
  `conditions`, `evidence: {reference}`, review `status`, and `reasons`.
  Missing, blank, or non-text summary values use the explicit string
  `"unverified"`; it means unknown, never unrestricted permission.
- The summary reads each result through storage permissions. An unreadable or
  malformed page gets an entirely unverified summary and a reason; other results
  still return. No evidence URL, original source, or LLM is accessed.
- Review status uses the same inspection as `terms` (§9.15). `recorded` is not
  a shareability verdict, and missing Where/Level remain unknown on older records.
  Long evidence quotes and reviewer details remain available via `wiki_parse`.
  Summary `scope` describes applicability; it does not restore the removed query parameter.
- `research_evidence` is a per-result summary with `status` `absent`,
  `recorded`, or `unavailable`; `claim_count`; `claims` (up to 3 in document
  order, each with `id`, `kind`, `checks` and `gaps` kind lists capped at 4
  each, and `limitation` capped at 200 runes); and `truncated`. It does not
  resolve source refs. More than 3 claims, more than 4 check/gap kinds in a
  claim, or a limitation longer than 200 runes sets `truncated`.
- No field yields `absent`. A page read failure yields `unavailable` with
  reason `page could not be read`; frontmatter YAML failure yields
  `frontmatter could not be parsed`; EVID001 yields `unsupported
  research_evidence version`; any EVID002 or EVID003 yields
  `research_evidence structure is invalid`. Otherwise the status is `recorded`,
  including records with EVID004, EVID005, or EVID007 warnings.
- The `research_evidence` record is self-reported structured evidence, not
  fact-checking or legal judgment.

### 8.6 wiki_scan

`dir: string` (optional, default `""`) → array of `.md` paths under `wiki_dir`
(recursive). Errors: `ErrNotFound`.

### 8.7 wiki_parse

`path: string` (required), `include_content: bool` (optional, default `false`) →
Source (JSON). Errors: `ErrNotFound`, `ErrPermission`, `ErrNotMarkdown`.

### 8.8 wiki_delete

`path: string` (required) → `{status:"deleted", path}`. Deletes the file from
`wiki_dir` and removes it from the index if it was indexed. Side effect:
attempts to auto-commit the deletion to git (`git add` + `git commit -m
"wiki: delete <path>"`) when `wiki_dir` is a git repository, regardless of
`git.auto_commit` (§3.1) — this per-delete commit attempt predates and is
unaffected by that setting. The commit only actually lands when the deleted
file was already git-tracked: `git add` on a path git never tracked (e.g. a
page `wiki_write` wrote under the default `git.auto_commit: off`, which
never commits) fails with no matching pathspec, so the delete produces no
commit either — deleting an untracked page leaves no git record of the
deletion at all. A failed `git add`/`git commit` is logged, not returned as
a tool error, and is bounded as described in §8.8.1 (0024). With
`git.auto_commit: off` (the default), a delete commit that does land records
only the deletion — `wiki_write` overwrites without committing, and nothing
commits on ingest, so it does **not** make the wiki recoverable (see
`docs/deployment/remote-mcp.md` "Security posture"). Setting `git.auto_commit`
to `write` or `write+ingest` narrows, but does not eliminate, that gap — see
§3.1 and the deployment guide.

Git-controlled paths (§8.3) are rejected with `ErrPermission` here too.
Errors: `ErrNotFound`, `ErrPermission`, `ErrTraversal`, `ErrSymlink`.

#### 8.8.1 Auto-commit bounds and consequences

Applies to every auto-commit path: `wiki_write` (§8.3), `wiki_delete` (§8.8),
and the post-ingest snapshot (§9.4).

**Serialization.** Commits against one repository are serialized by an
advisory lock, covering both concurrent MCP tool calls and a scheduled
`cogvault ingest` running against a live `cogvault serve`. Git refuses
concurrent index operations, so without this an interleaved commit fails and
is only logged — the write lands on disk while its history entry silently
never exists. The lock lives outside `wiki_dir`, so the whole-tree ingest
snapshot never commits it as wiki content, and inside a directory only the
running user can open, so another local user cannot disable auto-commit by
pre-creating it. Commits against *different* repositories do not block each
other.

**Timeouts.** The lock wait, `git add`, and `git commit` are each bounded
independently by the same 10s deadline budget, so one slow step cannot consume
another's. The deadline budget for a single auto-commit is therefore ~30s, not
10s. The two `git` steps differ from the lock wait when their budget runs out:
a `git` subprocess is signalled with `SIGTERM` and given a 2s grace period
before being force-killed, because `git` removes `.git/index.lock` from its
own signal handler and an untrappable `SIGKILL` would instead strand that lock
on disk, breaking every later commit until an operator removed it by hand —
manufacturing the wedged lock this timeout exists to prevent. Including up to
two 2s grace periods, the wall-clock upper bound is ~34s. The lock wait has no
subprocess to signal; it simply gives up at its deadline and the commit is
reported as a lock failure.

**History is permanent.** Enabling `write` or `write+ingest` is a durability
tradeoff in both directions. Once a page is committed, a later `wiki_delete`
removes only the working-tree copy: the prior content stays recoverable from
git history for as long as the repository exists, and propagates to any
backup taken afterwards. If a page held a secret or personal data, deleting
it through the tool surface does **not** erase it — that requires history
rewriting outside cogvault. Under the default `off`, deleting an untracked
page leaves no trace at all.

---

## 9. CLI

Every command takes `--config <path>` (default `~/.config/cogvault/config.yaml`).
launchd invokes `cogvault ingest --config <path>` explicitly (no useful cwd).

Every command except `fetch`, `status`, `access-check`, and `terms` opens the index and
runs a forced consistency check first (§2.3). A check error warns on stderr and
does not stop the command. `fetch` reads the config only; it never opens the
index or the wiki. `status` loads the config and the ingest ledger only.
`terms` loads the config and reads one wiki page through storage; it opens no DB.

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

Each result keeps its path, title, type/category, and snippet, then displays
applicability, Where, Level, How (`conditions`), evidence reference, and review
status. Unknown summary values display as `미확인`; review reasons are shown when
present. A notice states that `recorded` is not legal permission.
CLI and MCP use the same `internal/terms` inspection and summary. Search order,
limits, and query matching are unchanged; no sharing-allowed filter or automatic
license-to-permission mapping is added. The index schema is unchanged.

### 9.3 serve

```
cogvault serve [--config <path>] [--transport stdio|sse|http] [--addr <host:port>]
                [--endpoint-path <path>] [--public-url <https://...>]
```

- `--transport` (default `stdio`): `stdio` talks over standard I/O; `sse` and
  `http` (Streamable HTTP) serve over a TCP listener at `--addr` (default
  `localhost:8080`).
- `--endpoint-path` (default `/mcp`, `http` only): the `http` transport
  answers only at this path (normalized to a leading slash, no trailing
  slash) and `404`s elsewhere. `sse` ignores this flag and keeps mcp-go's
  fixed `/sse` and `/message` paths.
- `--public-url` (required when `auth.mode: oauth`; optional otherwise): the
  externally reachable `https://` base URL — absolute, no userinfo, trailing
  slash, query, or fragment. Also used as the SSE message-endpoint base and for
  `Origin` checks. Effectively required for a usable remote `sse` deployment
  too: without it, a remote client is handed a loopback message endpoint it
  cannot reach.
- `auth.mode` (config, §3.1) gates every non-`stdio` request. `bearer` mode
  reads its credential from the `COGVAULT_BEARER_TOKEN` environment variable
  only — never a flag or config key, and at least 32 bytes — keeping it out
  of the config file and shell history.
- Startup guards (`sse`/`http` only), each a fatal error before the server
  starts listening: `--addr` with an empty host part (`:8080` binds every
  interface and would also produce a malformed SSE base URL); `auth.mode:
  none` on a non-loopback `--addr`; `auth.mode: oauth` without
  `--public-url`; `auth.mode: bearer` with `COGVAULT_BEARER_TOKEN` unset or
  under 32 bytes; a configured `auth.oauth.audience` that disagrees with the
  advertised resource (`<public-url><endpoint-path>`).
- **Shutdown**: `sse`/`http` run until SIGINT/SIGTERM, then drain in-flight
  requests with a 10-second grace period instead of dying with the process.
- **Archive reload** (only when `archive.root` is set; `sse`/`http` only):
  SIGHUP reloads `archive.capture_enabled` (§9.16) and nothing else; the
  process keeps serving. Without `archive.root`, SIGHUP is not trapped. On
  shutdown, a running reload's drain wait is canceled and serve waits for the
  reload to finish before it closes the archive.
- See `docs/deployment/remote-mcp.md` for the full remote setup (tunneling,
  identity-provider prerequisites, security posture).

Init failure → exit 1 + stderr. Runtime tool errors are returned to the client;
the server keeps running.

### 9.4 ingest

```
cogvault ingest [--config <path>] [--dry-run] [--limit N] [--scheduled] [--run-log-dir <dir>]
```

- `--dry-run`: list what would be digested; write nothing (no LLM/store/index/
  ledger mutation).
- `--limit N`: process at most N files (backlog batching / quota control).
- `--scheduled`: set the ledger run origin to `scheduled` (used by the launchd
  job); otherwise the origin is `interactive`.
- **Run timestamps on stderr**: every invocation that reaches the ingest
  handler brackets its run with two lines on stderr:

  ```
  <RFC3339 local> ingest start origin=<scheduled|interactive>
  <RFC3339 local> ingest end origin=<scheduled|interactive> result=<ok|error|panic>[ <summary counts>]
  ```

  Both timestamps use the local UTC offset. `result=ok` means the handler
  returned nil; `result=error` means it returned a non-nil error;
  `result=panic` means a panic escaped, and that panic still propagates.
  `<summary counts>` is the stdout summary counts, and it appears only when a
  report exists. The end line precedes the command error line. A flag-parse
  error rejected before the handler runs writes neither line. Stdout and exit
  codes are unchanged.
- **Per-run log file**: `--run-log-dir <dir>` (absolute; a relative value is a
  command error before the start line) writes one file per run,
  `<dir>/ingest.<start>.<pid>.err.log`. `<start>` is the start time in ISO 8601
  basic format with a numeric offset (`20260924T134018+0900`); `<pid>` is the
  ingest process ID. The file holds the run's start line, then one line per
  state-change event as it happens, then the end line. The start and end lines
  are identical to the stderr bracket lines. An event line is
  `<RFC3339 local> <action> <path>[  <error>]` for `digested`, `failed`, a new
  `refused`, `archived`, and `source-error`. It is not written for `skipped`,
  `deferred`, `exhausted`, unchanged, already-refused, or `would-*` results. A
  run that ends with `result=error` or `result=panic` adds
  `<end timestamp> ingest error: <error>` or `<end timestamp> ingest panic:
  <value>` after the end line. The file is created on the first event, or at
  the end of a run that is not `result=ok`; an `ok` run with no events creates
  no file. Each line is written unbuffered, so a killed run keeps its earlier
  lines and has no end line. Files are mode 0600 and are never deleted by
  cogvault. A failure to create or write the file logs a warning and does not
  change the run result or exit code.
- For `claudecode`, requires `claude` on PATH; if absent: `claude CLI not found
  in PATH; install Claude Code or add it to PATH`.
- For `openai`, requires `pdftotext`, `pdfinfo`, `pdftoppm`, `tesseract`, and
  Tesseract `eng`/`kor` data. It then checks the configured loopback
  `<base_url>/models` for the configured ready model. Any failure occurs before
  Runner/ledger construction and is reported as a command error.
- When `--dry-run` is not set and `git.auto_commit: write+ingest` (§3.1) and
  the run digested at least one file, commits the whole `wiki_dir` tree once
  (`git add -A -- .` + `git commit -m "wiki: ingest snapshot"`, run with
  `wiki_dir` as the git working directory) after the run completes. The
  `-- .` pathspec scopes the add to `wiki_dir` itself: without it, a
  `wiki_dir` that is a plain subdirectory of a larger git repository (not
  its own repo root) would stage every dirty file across that whole outer
  repository, not only `wiki_dir`'s contents. A failed `git add`/`git
  commit` (including "nothing to commit", which is the expected outcome
  when a digest reproduced identical content) is logged, not returned as a
  command error, and is bounded as described in §8.8.1 (0024).
- **Exit codes**: nonzero only on a run-level failure (e.g. lock contention:
  `ingest already running (lock held)`, or a ctx-cancelled run). Per-file
  failures inside a completed run do **not** fail the run (exit 0).

### 9.5 fetch

```
cogvault fetch [--config <path>] [--source-dir <dir>] [--name <file>] <url>
```

- `--source-dir` (default: the first entry of `sources[]`): where the file is
  written. With no `sources[]` configured and no flag: `no source directories
  configured; use --source-dir`. The directory is created if absent.
- `--name` (default: derived from the URL path, or the host when the path is
  empty, prefixed `web-` and truncated to **80 runes** without splitting a
  multi-byte character): `.md` is appended unless already present. An explicit
  `--name` must be a plain filename (no path separators, not `.` or `..`,
  checked before the `.md` suffix is appended) — a name that could escape the
  source directory is rejected with `must be a plain filename`.
- `GET` with a 30-second timeout. A non-`200` response is an error
  (`fetch <url>: HTTP <code>`); so is an unparsable URL.
- **The response body is limited to 10 MiB.** A larger body is rejected with
  `response body exceeds 10 MiB limit` and no file is written; it is never
  truncated into a short capture.
- **Content validation**: a response whose `Content-Type` is not text-ish
  (`text/*`, `application/json|xml`; an absent header is allowed and left to
  the UTF-8 gate) is rejected with `unsupported Content-Type`, and a body
  that is not valid UTF-8 is rejected — binary media must not become a
  `.md` capture.
- Adds YAML `source_url` (retaining the exact URL value) and UTC `fetched_at`.
  A parseable explicit `usage_terms` declaration is copied into the capture
  header as unverified data, not accepted as a human review or legal conclusion;
  remote reviewer/date claims are removed from that copied record.
  The complete response body remains byte-identical, including original or
  malformed frontmatter. No HTML extraction happens here.
- Before replacing an existing capture, apply the same terms-loss check as
  storage (§5.4). On rejection the old file is unchanged. Successful writes use
  a temp name and rename, so a crash cannot leave a partial capture.

### 9.6 digest

```
cogvault digest [--config <path>] [--days N]
```

- `--days N` (default 7): include pages under `sources/` whose **index**
  `indexed_at` is newer than `now - N days`. Recency comes from the index, not
  from the page's `ingested_at` frontmatter, so re-indexing a page makes it
  recent again.
- Writes `digests/weekly-<UTC date>.md` (`type: synthesis`) listing each
  included page as a wikilink, then indexes it. **Overwrites** an existing file
  with the same date, so a second run on the same day replaces the first.
- With no page in range: prints `no recent sources to summarize`, writes
  nothing, exits 0.
- Does not commit to git (§1.3).

### 9.7 synthesize

```
cogvault synthesize [--config <path>]
```

- Scans the wiki and groups pages by outgoing wikilink target and by tag.
- A link target referenced by **2 or more** pages produces
  `concepts/<slug>.md`; a tag on 2 or more pages produces
  `concepts/tag-<slug>.md`. Both are `type: concept` and list their members as
  wikilinks. Slugs lowercase the name; spaces become `-` for links, `/` becomes
  `-` for tags.
- **Never overwrites**: a concept page that already exists is skipped, whatever
  its content. This is the opposite of `digest` and `index`, which always
  rewrite their output, and it is what makes hand-editing a generated concept
  page safe.
- A page that fails to parse is skipped silently. A write failure warns on
  stderr and continues.
- Prints `synthesize: created N concept pages`. Exits 0 even when N is 0.

### 9.8 embed

```
cogvault embed [--config <path>] [--batch-size N]
```

- Requires `llm.embedding_model`; without it: `embedding_model not configured;
  set llm.embedding_model in config`.
- Embeds only pages whose stored embedding is missing or stale for the
  configured model (content-hash keyed). With none stale: `embed: all
  embeddings up to date`, exit 0.
- `--batch-size N` (default 32; any value ≤ 0 becomes 32): texts per request to
  the embedding backend.
- The embedded text is the page title followed by the first 2000 runes of its
  content — not the whole page.
- Reports `embed: E embedded, S skipped, F failed, T total`. **`skipped` and
  `failed` are different outcomes**: a page that cannot be read is skipped, a
  batch or store error is a failure.
- **Exit codes**: nonzero when `failed > 0`; skipped pages alone do not fail
  the run.

### 9.9 similar

```
cogvault similar [--config <path>] [--limit N] <path>
```

- `--limit N` (default 5): maximum results.
- Ranks by embedding cosine similarity when `llm.embedding_model` is set and
  embeddings exist; otherwise falls back to FTS title matching. The output
  format is identical either way, so the score's meaning depends on which path
  ran.
- Prints `<score>  <title>  (<path>)` per result, or `No similar pages found.`
- Read-only; exits 0 whether or not anything matched.

### 9.10 graph

```
cogvault graph [--config <path>]
```

- Prints the wiki link graph to stdout as indented JSON:
  `{"nodes":[{"path","title","type"}],"edges":[{"source","target"}]}`.
- Edge targets are resolved with the same wikilink resolution `lint` uses
  (§9.12), including its case-insensitive basename fallback. An unresolvable
  link produces no edge and no error here — `lint` is where it is reported.
- A page that fails to parse still contributes a node, but no edges, silently.
- Read-only; exits 0.

### 9.11 index

```
cogvault index [--config <path>]
```

- Writes `_index.md` (`type: index`) listing every wiki page as a wikilink,
  sorted by path, then indexes it. `_schema.md` and `_index.md` itself are
  excluded.
- **Overwrites** any existing `_index.md`. The file is a generated view, not a
  page to edit.
- Does not commit to git (§1.3).

### 9.12 lint

```
cogvault lint [--config <path>] [--strict] [--json]
```

Reports, one line per issue as `<kind> <path> <message>` in text mode:

- `parse` — the page could not be read or parsed.
- `frontmatter` — missing `title`, missing `type`, or, for `type: source`
  pages, a missing `source_path` or `ingested_at` (§11). `_schema.md` is exempt
  from this check: it defines the page contract rather than being written under
  it. Its links are still checked. A freshly initialized wiki is therefore
  lint-clean, which is what makes `--strict` usable from the first run.
- `broken-link` — a `[[wikilink]]` that resolves to no page. Resolution tries
  `<link>.md`, `sources/<link>.md`, and `<link>` verbatim, then falls back to a
  case-insensitive match on any page's basename. Names compare in Unicode NFC
  form, so a link and a file name that differ only in Hangul composition (NFC
  vs. NFD, as macOS and iCloud may store names) match. `graph`, ingest
  (§10.1), and `wiki_write` (§8.3) use the same resolution.
- `orphan` — no incoming links. `_schema.md`, `_index.md`, and every
  `type: source` page are exempt, since source pages are expected to be reached
  through search rather than links.
- `evidence` — one diagnostic for a present `research_evidence` field per
  validator finding. Pages without the field are never reported. Text messages
  keep the `<kind> <path> <message>` format and omit diagnostic IDs.

| ID | Severity | Trigger |
| --- | --- | --- |
| EVID001 | error | Field is present but is not a mapping with `version` exactly integer `1`; sole diagnostic for the page. |
| EVID002 | error | Missing/non-array `claims`; non-mapping claim; missing/empty/non-string claim `id` or `text`; missing/empty/non-string claim `kind`; duplicate claim `id` on second and later occurrences. |
| EVID003 | error | Present `checks` or `gaps` not arrays; non-mapping item; missing/empty/non-string/out-of-set check or gap kind; claim kind outside its closed set; present `limitation` that is not a string. |
| EVID004 | warning | Sources absent, empty, non-array; non-mapping source; invalid/missing ref; unsafe or malformed ref; referenced path is not an existing regular file (a directory is not found). |
| EVID005 | warning | Check result missing/empty, or required locator missing/empty for `fulltext`, `table`, or `replication`. |
| EVID006 | reserved | Never emitted. |
| EVID007 | warning | Gap detail or next_step missing/empty. |

Enums are closed: claim `kind` is `empirical`, `causal`, or `normative`;
check `kind` is `bibliography`, `fulltext`, `table`, or `replication`; gap
`kind` is `source_unavailable`, `verification_pending`, `replication_pending`,
`applicability_unknown`, or `additional_evidence_needed`. Required string
values must be YAML strings whose trimmed value is non-empty. Unknown keys
inside the record are ignored. `version` must be YAML integer `1` (not `"1"`
or `1.0`).

References must be exact wiki-root-relative `.md` paths. Reject leading/trailing
whitespace, absolute/drive/backslash forms, `\\`, `://`, wikilink delimiters,
`#`, non-`.md` suffixes, non-canonical `path.Clean` forms, and any `..`
segment. Existence is checked at the supplied exact path, without basename
resolution or adding `sources/`; filesystem lookup determines case and
normalization behavior. A nil existence checker skips only the existence
check. Invalid check/gap items and non-mapping items do not get downstream
EVID005/EVID007 findings; a non-mapping claim has no further findings, and a
non-mapping source gets one EVID004. A claim with missing ID is still checked
with empty claim ID.

`--json` writes exactly one JSON object `{"issues":[...]}` to stdout (empty
issues are `[]`, never null), with no summary line. Each issue has `kind`,
`path`, `message`, `id`, `claim_id`, `severity`, and `fix_hint`; non-evidence
issues use empty strings for the last four fields. Evidence issue `message`
contains the diagnostic message only; fix hint is a separate field. All issues
are stably ordered by Path, Kind, ClaimID, ID, then Message. `--strict` changes
only the exit code in either mode.

- Prints `lint: no issues found` when clean, otherwise the issue lines and
  `N issue(s) found` in text mode.
- **Exit codes**: without `--strict`, always 0 — `lint` reports, it does not
  gate, and that default does not change because the command shipped exiting 0
  and a caller may depend on it. With `--strict`: nonzero when any issue was
  reported, 0 when clean.

### 9.13 status

```
cogvault status [--config <path>] [--json]
```

Reports source files that need ingest attention for the configured LLM model.
The command selects the latest ledger row for each `source_path`. A latest
`failed` row with at least three attempts appears as `exhausted`. A latest
`refused` row appears as `refused`. Both rows must match the configured model.
A newer row for the same source path excludes every older attention row.

Human output uses the source filename and a local-time minute:

```
주의 필요: <N>건
  <exhausted|refused>  <filename>  <error>  (<YYYY-MM-DD HH:MM>)
```

When no row needs attention, human output is `주의 필요 항목 없음.`.

`--json` writes an object with `attention` and `model` fields. Each
`attention` item contains `path`, `status`, `error`, `last_attempt`,
`llm_model`, and `attempts`. JSON preserves `last_attempt` as the canonical UTC
RFC3339 timestamp from the ledger. An empty result uses `"attention": []`.

If the ledger database does not exist, the command returns the clean empty
output and does not create the database. For an existing database, it opens
the ledger and may run its idempotent schema DDL. The command does not open the
index. It does not open wiki storage or acquire the ingest lock.

### 9.14 access-check

```
cogvault access-check [--config <path>]
```

The command loads and validates only the selected config. Positional arguments
are rejected. It does not bootstrap the database, wiki schema, index, Git, LLM,
notification, or ingest pipeline.

For `wiki_dir` and the parent directory of `db_path`, the command creates a
unique sentinel file. It writes, closes, reads, compares, and removes that file.
Each successful probe prints `passed: <name>: <path>`.

For each configured source, the command lists only its top-level entries. It
selects regular files that match `sources[].types` and `max_file_size_mb`.
It opens each selected file without following symlinks, verifies its descriptor
identity, and reads at most one byte. An empty file passes. Each successful
source probe prints `passed: source: <path>`.

The command stops on the first failed probe and names the operation and path.
A complete run prints `configured ingest access check passed` last.

This result covers only the configured paths and operations above. It does not
prove that macOS stored a TCC decision. It does not prove that a complete ingest
will run without prompts. It does not prove access to unconfigured Documents,
Pictures, Photos Library, or network shares.

### 9.15 terms

```
cogvault terms <wiki-relative-path> [--config <path>]
```

Read one page through storage and output JSON: `path`, effective `status`,
original `record`, `reasons`, and `notice`. This command does not write pages,
open a DB, call an LLM, or fetch evidence. Storage read restrictions apply.
Read/config errors return a command error. An unverified record is a successful
inspection, not a command error.

The page's optional `usage_terms` YAML mapping contains:

| Field | Contract |
|---|---|
| `status` | `unverified` or `recorded` |
| `purpose` | Non-empty string describing the intended use |
| `conditions` | Non-empty string preserving permissions, restrictions, and obligations |
| `scope` | Optional applicability description, preserved when supplied |
| `where` | Optional text naming the declared sharing audience or destination |
| `level` | Optional text describing permitted portion or transformation, not an ordinal grade |
| `evidence.reference` | Non-empty string locating the terms or permission evidence |
| `evidence.quote` | Non-empty string containing the relevant evidence text |
| `reviewed_by` | Non-empty string naming the human reviewer |
| `reviewed_at` | Valid `YYYY-MM-DD` text; quoted or unquoted date scalars retain their text |

`recorded` means a complete human record, **not permission or a legal determination**.
Missing, malformed, incomplete, invalid-date, unknown-status, and explicitly
unverified records have effective status `unverified`, with reasons.
Invalid YAML frontmatter also yields `unverified`.
The inspector checks structure only. A human must assess evidence truth,
scope, authenticity, ambiguity, and conflicts for the intended use.
Leave ambiguous or conflicting evidence explicitly `unverified`.
URLs, free access, and subscriptions alone do not establish permission.

Review status and terms data are independent. An unverified record may contain
explicit permission, restrictions, and evidence; those values must not be erased.
For automatic ingest, select the structured input record first, an existing
wiki record second, and a generated candidate third. Preserve its purpose,
conditions, scope, and evidence before storage and indexing. Set status to
`unverified` and remove automatic reviewer/date claims; do not claim legal clearance.
Path-input Markdown and extracted-text inputs use the same selection rule.
Regeneration without new input terms retains the previous declaration but
requires review again. An explicit new input declaration may replace field
values but must retain populated protected fields or provide non-empty
replacements for them (§5.4). Conflicting declarations are not automatically
combined or legally resolved.
Absent records remain `{status: unverified}` without fabricated conditions.
Malformed supplied records fail rather than silently discarding evidence.
Untouched existing pages are not migrated. Existing MCP `wiki_read` and
`wiki_parse` expose the stored record; search snippets are not terms decisions.

The isolated example workflow is documented in
`docs/usage-terms.md`. All example terms are fictional validation settings,
not evidence for rights to real material.

### 9.16 archive (experimental prototype)

```
# archive host (reads no config file)
cogvault archive --root <dir> capture --file <path> [--url <http(s) URL>]
    [--supplier <s>] [--title <s>] [--media-type <s>]
    [--completeness verified|unknown|incomplete] [--basis <s>] [--key <s>]
cogvault archive --root <dir> import-report --file <path> [--supplier <s>]
    [--title <s>] [--cite <capture-id|http(s) URL>]... [--key <s>]
cogvault archive --root <dir> summary --file <path> [--of <capture-id>] [--url <URL>]
    [--supplier <s>] [--title <s>] [--key <s>]
cogvault archive --root <dir> attempt <capture-id> --cause <c> --tried <s>
    --result acquired|partial|failed|human_action_required [--result-capture <id>]
    [--gap <s>] [--next <s>]
cogvault archive --root <dir> reassess <capture-id> --basis <s>
cogvault archive --root <dir> import-fetched --file <cogvault fetch .md> [--key <s>]
cogvault archive --root <dir> acquire <public http(s) URL>
cogvault archive --root <dir> acquire --recover <capture-id> [--cause <c>]
cogvault archive --root <dir> extract <capture-id>
cogvault archive --root <dir> status [<capture-id>] [--json]
cogvault archive --root <dir> handoff [--types pdf,md,markdown] [--json]
cogvault archive --root <dir> collect-receipts
cogvault archive --root <dir> page-links <wiki-page> [--json]
cogvault archive --root <dir> impact <capture-or-object-id>
cogvault archive --root <dir> quarantine <id> --impact <digest>
cogvault archive --root <dir> unquarantine <id>
cogvault archive --root <dir> purge <object-id> --confirm <object-id>
cogvault archive --root <dir> backup --to <new dir>
cogvault archive --root <dir> restore-test --backup <dir> --report <new file>
    [--deletion-log <path>]... [--scratch <dir>]
# ingest host (reads --config; never opens the registry)
cogvault archive --root <share> --config <path> pickup --dest <source dir> [--json]
cogvault archive --root <share> --config <path> receipts --dest <source dir>
```

An implemented prototype of the archive proposed in ADR 0027. That ADR is
**proposed, not accepted**, and nothing here is deployed: these commands and
the archive MCP tools may change or be removed. The user approved policy
defaults (ADR 0027) shape the behavior below; deployment settings are empty
and production capture stays blocked (see *Activation*). There is no
automatic or scheduled re-acquisition, robots or terms handling, charset
decoding, release of held material without a verified judgment, backup
scheduling, or backup expiry.

- `--root` is required. Layout: `objects/sha256/<hex>` (original bytes),
  `objects/tmp/` (staging), `registry.db` (SQLite), `outbox/` (hand-off
  manifests), `receipts/` (ingest-host receipts), `quarantine/`,
  `deletions/log.jsonl`, `archive.lock`. At open, the root itself, every
  managed directory, and `registry.db` with its `-wal`, `-shm`, and
  `-journal` files are refused if they exist as symlinks or other
  non-regular entries. Symlinks in the root's ancestors are trusted. The
  check runs at open only: another same-user process that swaps entries
  inside the root while a command runs is out of scope. Use a local disk;
  the registry must not live on SMB. Registry schema 5; schema-1 through
  -4 registries are migrated in place, and capture IDs are unchanged.
  Registry transactions take the write lock at `BEGIN` (`_txlock=immediate`),
  so concurrent writers wait instead of failing. Schema 5 allows one linking
  attempt per (held capture, result capture) pair; a repeated link is a
  conflict, and `reassess` treats it as already linked.
- **Locking.** Captures, attempts, hand-offs, and receipt collection share
  `archive.lock` (flock, one descriptor per operation, so it orders
  processes too); backups and deletion actions take it exclusively.
- **Objects.** Input is staged under `objects/tmp`, hashed, synced, made
  read-only, and hard-linked to `objects/sha256/<hex>`. A link never replaces
  a file. Whenever a request reuses an existing object, including an identical
  repeated request that returns `existing`, the object is opened without
  following symlinks and its size and SHA-256 are checked; a mismatch is an
  integrity error and the stored file is left unchanged. Input over 32 MiB is
  rejected, never truncated. Object ID is `sha256:<hex>`. Bytes of a
  quarantined or purged object are refused (`object is quarantined or
  purged`) until an operator restores them.
- **Captures** hold provenance separately from bytes: kind (`source`,
  `report`, or `summary`), object ID, base file name (never a host path),
  URL, supplier, title, media type (sniffed when omitted), acquisition
  (`acquired`, or `partial` when completeness is `incomplete`), completeness
  (`verified`, `unknown` default, `incomplete`), basis (required for
  `verified`), and recording time. URLs must be absolute `http`/`https`.
- **Identity and idempotency.** The request key is `--key`, or by default a
  digest of the canonical request (object ID plus all provenance fields and
  cites). The capture ID is `cap_` plus 32 hex characters derived from the key,
  so it is stable. A repeated key with the same request prints `existing`;
  with a different request it fails with `request key already used with
  different content`. The same bytes with different provenance create a new
  capture that shares one object.
- **Reports** store the report bytes and their cites in order. A capture-ID
  cite must name an existing `source` capture of an active object. A URL cite
  records the citation only: it creates no capture and claims no original.
- **Summaries** are captures of kind `summary` with acquisition and
  completeness `not_applicable`. They name the summarized source capture
  (`--of`), a URL, or both. A summary never changes the summarized capture
  and is never handed off.
- **Recovery attempts** are append-only: cause, what was tried, result, the
  new source capture for `acquired`/`partial` (required then, forbidden
  otherwise), remaining gap, next action. The earlier capture is never
  changed; the recovered capture keeps its own completeness.
- **Reassess** (`reassess`, operator only): for an `acquired` source capture
  whose completeness is `unknown`, adds a new source capture of the same
  bytes with the same name, URL, title, media type, and supplier,
  completeness `verified`, and `--basis`, and links it by an attempt (cause
  `other`; the attempt text says the basis is the operator's statement, not
  an independent check by cogvault). The held capture is never changed.
  `partial`/`incomplete` bytes are refused: a complete original must be
  captured as new bytes and linked with `attempt`. Repeating the same
  reassessment returns the same capture, links it if an earlier run stopped
  before linking, and never links twice. There is no release of
  `unknown`/`partial` bytes without a verified judgment (ADR 0027 open
  question 5).
- **Import of a fetch file** (`import-fetched`): reads an existing
  `cogvault fetch` `.md` file (never fetches). The frontmatter may hold only
  `source_url`, `fetched_at`, and `usage_terms`; the split is accepted only
  if re-encoding the frontmatter and body gives back the file byte for byte.
  The body becomes a source capture named `<stem>.response` (fetch keeps no
  media type and stores HTML as `.md`, so the format is unknown and the
  capture is never handed off, even after `reassess`), completeness
  `unknown`, URL and fetch date as written in the file (not an independent
  check of the HTTP exchange), supplier `imported cogvault fetch file (URL
  and date as written in the file)`. The fetch file's bytes are stored as
  their own object and recorded as `derived` from that capture, so the file
  can be reproduced if its copy moves. Deletion actions on the body's object
  apply to its derived objects too. All refusals (fields, size, deleted
  objects) happen before the body is stored; if recording the derivation
  then fails, the body capture stays without it, the error says so, and the
  same import repeated adds it once. The file itself is not changed.
- **Hand-off** (`handoff`): writes `outbox/<capture_id>.json` (version,
  capture ID, object ID, size, extension; no host path) for each source
  capture whose acquisition is `acquired`, completeness `verified`, and
  extension one of `--types` (default `pdf,md,markdown`; `txt` only for an
  openai ingest host). Others are reported `held` with a reason; a held
  capture lists `recovered_as` captures recorded by attempts. The first
  handed-off capture of an object fixes the object's extension; later
  captures of the same bytes reuse it, so one object has one source path.
  Manifests are never replaced.
- **Pickup** (ingest host): `--dest` must be a configured `sources[].path`.
  Accepted extensions are that source's types the backend digests
  (`openai`: pdf, md, markdown, txt; `claudecode`/`ollama`: pdf, md,
  markdown); other manifests are reported `refused`. Each object is copied to
  `<dest>/<hex>.<ext>` with size and hash checked; an existing file is kept
  if it matches and is an integrity error otherwise. Manifests with unknown
  fields, trailing data, or one object under two extensions are rejected.
  Pickup then adds each copied or matching capture to the **capture map**
  `<dest>/.cogvault-archive-captures.json` (`version` 1; `entries` keyed by
  copy file name: `object_id`, sorted `capture_ids`), replaced atomically
  under an exclusive `flock` on the destination directory, so overlapping
  pickups keep each other's entries. A map that would pass 16 MiB is refused
  before writing. Pickup opens objects without following symlinks or
  blocking, and refuses non-regular files. IDs only accumulate; an entry for
  other bytes is a conflict. The map is trusted as far as the source
  directory is: whoever can write source files can write it.
- **Capture IDs on source pages** (ingest, §10): on every digest ingest
  removes `archive_capture_ids` and `archive_object_id` from the generated
  frontmatter, then sets them from the capture map only if the map entry for
  the source file names an object ID equal to `sha256:` plus the digested
  bytes' hash. A missing, unreadable, or mismatched map sets nothing (an
  unreadable map is logged). These keys are a snapshot of the map when the
  page was digested, not the full list: ingest never rewrites a page for an
  unchanged success row, so a capture handed off later is not added to it.
- **Page links** (`page_links` in the registry, added in schema version 3;
  upgrading a schema 2, 3, or 4 registry applies the receipt-order rule
  below to links already recorded, by relinking every collected receipt once
  on open): the
  archive's own record of which captures belong to which wiki source page,
  and the full list for a page (`page-links <wiki-page>`). A link is
  recorded only from a collected receipt whose ledger status is `success`
  with a wiki page; it holds the capture ID, object ID (content hash), the
  exact wiki-relative `.md` page path from the ledger, the basis receipt
  name, the ledger's `digested_at`, and the observation time. The ingest
  host's ledger stays the authority for the ingest result; the archive is
  the authority for the capture-to-page association. Each capture gets its
  own link, so a later capture of the same bytes keeps its own provenance
  and is linked without changing the page (a user edit stays). Links are
  append-only: the basis of a (capture, page) link is the linking receipt
  with the lowest receipt number, whatever order receipts are collected or
  backfilled in; `current` means the capture's latest receipt (highest
  number) still observes `success` with this page. A later receipt that
  observes another status or page makes the link not `current` and may add
  another link, never removing one. A
  receipt's object must equal its capture's object. Captures of a
  quarantined or purged object are hidden. Links are in the registry, so
  backups carry them; a version 2 registry (for example a restored older
  backup) gains links for every collected receipt on open. A link does not
  show that the page exists now, describes this edition, or supports any
  claim. A moved page is not followed: the link keeps the ledger's path.
- **Acquire** (`acquire`, operator only, never MCP): downloads one URL with
  `GET` and stores the body exactly as sent as a new source capture
  (completeness `unknown`, supplier `cogvault archive acquire`) plus a
  record of the requested and final URL, each hop's status, and selected
  response headers. Refused before any connection: non-`http(s)` URLs,
  userinfo, any query (including a bare `?`), and any fragment, on the first
  URL and on every redirect (at most 5). Every DNS answer must be a public
  address (no loopback, private, link-local, shared, documentation,
  benchmark, multicast, reserved, NAT64, 6to4, Teredo, or cloud metadata
  ranges); the connection goes to the checked address, so a second lookup
  cannot redirect it. No proxy, cookie jar, credentials, or compression
  request (`Accept-Encoding: identity`). Only status 200 is stored;
  401/403/407/451 are reported as access-restricted. Bodies over 32 MiB or a
  Content-Length mismatch are rejected; the default time bound is 60 s. A
  body with a content coding is stored as sent with a `.bin` name. Errors
  and records never contain a refused URL. `--recover` downloads an existing
  capture's URL again and records an attempt (`acquired` with the new
  capture, `human_action_required` for access-restricted, `failed`
  otherwise); the earlier capture is not changed.
- **Extract** (`extract`): `html-text/1`, standard library only, runs no
  scripts and loads nothing. Input must be valid UTF-8, sniff as text,
  carry no content coding, and be HTML by media type, sniff, or extension.
  `script`, `style`, `template`, `noscript`, `iframe`, `object`, `svg`,
  `math`, `head` content except `title`, and attributes are dropped; block
  elements become line breaks, headings `#`, list items `- `. The text is
  stored as its own object with the extractor version and limits; a parse
  failure is recorded and returned, never guessed; repeating a failed
  extraction returns the recorded failure as an error. Only a `title` whose
  parent is `head` (or `html`) becomes the heading. The original is not
  changed, extraction is per object and repeatable, and derived text is
  never handed off. Extraction holds the shared lock from the source check
  to the commit, so it never records output for a quarantined or purged
  source; deletion actions apply to extracted text with its source (see
  *Deletion* for text shared by several sources).
- **Receipts** (ingest host): opens the ingest ledger read-only
  (`mode=ro&immutable=1`) under the existing `ingest.lock`, which it never
  creates; refuses a missing DB, a missing ledger table, a missing lock file,
  or a leftover `-wal`. For each picked-up object with a ledger row it writes
  `receipts/<capture_id>.<n>.json` when the observation changed; `n` is the
  capture's next receipt number. **Receipt order** is that number and
  nothing else: `observed_at` comes from the ingest host's clock, which can
  go backwards, and file names sort `.10` before `.2`. The latest receipt
  of a capture is the one with the highest number. No ledger
  row, no receipt. `collect-receipts` (archive host) stores each receipt
  unchanged, in receipt order per capture, and in the same transaction
  records the page link it observes (see *Page links*); a changed collected
  receipt or one for a capture never handed off is refused.
- **status** shows the capture, `same bytes`, `cited by`, summaries,
  attempts, derived files, a download record, extractions, a report's
  numbered citations, and the ingest
  view: `held` (reason, `recovered_as`), `not_handed_off`, `awaiting_ingest`
  (no receipt; not a success or failure claim), or `observed` (the latest
  receipt's ledger status and time, labeled as an ingest-host observation).
  `ref candidate` shows the observed page only when the ledger status is
  `success`; the CLI does not check that the page exists, so it is a ref
  only once that wiki page exists. `page link` lines list
  the capture's page links with their basis receipt and whether they are
  current; the CLI does not check that the page exists.
- **Deletion** (operator only, never MCP): `impact` lists dependents
  (captures, reports, summaries, attempts, manifests, ingest-host copies,
  source pages, and the derived objects the next action would move) with a
  digest. `quarantine` needs the current digest, moves the object to
  `quarantine/`, removes its manifests, and hides its captures from status,
  list, hand-off, and MCP. `unquarantine` restores it only when a
  quarantined or active file matches the object's size and SHA-256
  (otherwise an integrity error and no record). `purge` needs `--confirm`
  with the object ID and at least 30 days in quarantine; nothing purges
  automatically. Purge removes this archive's bytes only, not backups or
  ingest-host copies. **Derived objects** (fetch files, extracted text) move
  with their source, except one that is itself a capture's original, or one
  another source still needs: quarantine keeps it while another source is
  active, purge while another source is active or quarantined; restore
  brings back every derived object that is not a capture's original. Every
  action first appends a record (seq, action, object ID, capture IDs, time;
  no content) to `deletions/log.jsonl`; a record longer than the log's
  64 KiB line limit is refused before writing, so the log stays readable.
  Open re-applies the log, ordered by seq, never by time. When a file exists
  both active and in quarantine, the copy that matches the object's size and
  hash is kept; if neither matches, nothing is removed and the action fails.
- **Backup** (`backup`): under the exclusive lock, `VACUUM INTO` copies the
  registry, then objects, quarantine, receipts, and the deletion log are
  copied and every object the registry copy references is checked by size
  and hash before `backup.json` (all files and hashes) is written.
  `restore-test` restores into a scratch directory, merges the backup's log
  with each `--deletion-log` (a log must extend the other; diverging logs
  fail), re-applies deletions, re-hashes active objects, and writes a report
  that lists the logs used (path, hash, record count). Without
  `--deletion-log` it uses only this root's live log and says so; such a
  report does not show recovery after the archive host is lost.
- **Activation.** `serve` enables capture only if `archive.deployment` passes
  a presence-and-file check: every setting set; the restore test report
  passed, tested a backup under `backup_target`, and used
  `deletion_log_replica`; and the replica holds every record of the live
  log right now. This checks operator-supplied attestations only. It does not
  verify that the target or replica is off the NAS, that backups run daily,
  that the replica stays current, or that permissions and mounts are right;
  production enable needs a separate human review. No config key bypasses the
  check. A blocked activation at startup stops `serve`; on reload it is
  rejected.
- **MCP tools** (registered only while capture is enabled): `archive_capture_text`,
  `archive_capture_report`, `archive_capture_summary`, `archive_status`,
  `archive_page_links`. Text
  up to 1 MiB (rejected, never truncated); `name` must be a base file name
  ending in `.md`, `.markdown`, or `.txt`; completeness `unknown` or
  `incomplete` only (only an operator records `verified`). Undeclared
  arguments are refused. No tool deletes, overwrites, takes a host path, or
  reloads. `archive_status` returns a projection without host paths, plus
  live checks of the source page: `search_visible` (index),
  `evidence_ref_path_exists` (a file exists at the observed page path, so
  a ref to it passes the EVID004 path check; this does not show the page
  describes this capture or edition, or support any claim), and
  `page_frontmatter_lists_capture` (the page frontmatter lists this capture
  ID and object ID; a digest-time snapshot, editable through `wiki_write`,
  not the full list and not an independent check). In the MCP ingest view,
  `wiki_page` is the observed ledger path, unchecked; `ingest.evidence_ref`
  is set only when the observation is `success` and a file exists at that
  path now, the same rule as a link's `evidence_ref`. It also returns the
  download record, extractions, and `page_links`. `archive_page_links`
  (`wiki_page`: a wiki-relative `.md` path) lists every capture linked to
  one page. Each link view adds a live `page_exists`, and `evidence_ref` is
  set only when the link is current and the page exists now; a moved or
  deleted page is never offered as a ref.
  Internal errors
  return a fixed message. The existing `auth.mode` checks apply unchanged.
- **Reload** (SIGHUP, `sse`/`http`): re-reads and fully validates the config,
  then applies only `capture_enabled`. A change to `wiki_dir`, `db_path`,
  `auth`, `archive.root`, or `archive.deployment` rejects the reload; other
  changed keys are logged as not applied until a restart. Disable closes the
  gate (new calls fail at once), removes the tools, and waits up to 30 s for
  admitted calls; enable re-runs activation, opens the gate, and adds the
  tools. Every tool checks the gate itself: `tools/list_changed` is sent, but
  a client need not refresh, and a call looked up before removal still
  reaches the gate. Reload logs carry a fixed code and key names, never
  values. `stdio` has no reload.
- Provenance records what was supplied; it is not a fact check. Capture IDs
  are not `research_evidence` refs; refs remain wiki `.md` paths (§11.1).

---

## 10. Ingest pipeline & ledger

### 10.1 Flow (per file)

```
scan source dir (top level only, Lstat: skip dirs/symlinks)
  → type filter (lowercase ext vs sources[].types)
  → size cap (config max_file_size_mb, default 32MB; oversized files reported, not persisted)
  → settle-window gate (skip files modified within 2m — mid-download guard)
  → sha256 content hash → ledger lookup (skip if already processed)
  → llm.Digest(source path, schema) via claude --print
      → parse stdout event array; inspect only the final result event
      → classify policy refusal across eligible final result, stderr, and permitted plain stdout
      → otherwise select a safe diagnostic: eligible result → stderr → plain stdout → process error
  → validate page frontmatter from bytes (non-empty map + title)
  → set `ingested_at` to the run's local date (YYYY-MM-DD), replacing any model value
  → drop model `archive_capture_ids`/`archive_object_id`; set them from the archive
    capture map only when its object ID matches these bytes (§9.16, experimental)
  → replace each non-embed `[[link]]` that resolves to no wiki page (§9.12; the
    page being written counts) with its display text; a wiki scan error is an infra failure
  → collision-aware page path under sources/
  → storage.Write → index.Add → ledger: success
(digest/validation/storage/index failure → attempt ledger status `failed` or `refused` + classified error)
(ledger persistence failure → report/log failure; the failed write cannot guarantee its own row persisted)
(the run continues)
```

### 10.2 Page identity

One source file path → one wiki page `sources/<slug>.md`. `<slug>` is derived from
the source base name (extension stripped) by: Unicode NFC normalization (APFS
reports Hangul names decomposed), lowercasing, replacing spaces with
`-`, dropping every character that is not a Unicode letter, Unicode digit, `.`, `_`,
or `-`, collapsing runs of `-` to one, and trimming leading/trailing `-`. If that
leaves the slug empty (e.g. a name with no letters or digits), it falls back to
`src-<hash8>` (`hash8` = first 8 hex of sha256 of the file content). If the resulting page path is already mapped to a different source path,
the page becomes `sources/<slug>-<hash8>.md` (here `hash8` = first 8 hex of sha256
of the absolute source path). Re-digestion of the same source path with new content
overwrites its page and marks the prior ledger row `superseded`. If re-digestion
produces a different page path than the previous success row (for example, after
a slug rule change), the previous page and its index entry are removed after the
new page is saved. The index entry is removed even when the previous file is
already gone. A previous path that names the same file keeps the file and loses
only its index entry.

A renamed or deleted source file's page and ledger row survive the immediate
change. On the next ingest run, a pre-scan sweep archives only when the source
directory snapshot still shows at least one tracked survivor and exactly one
missing success-row source. Zero-survivor and multi-missing directories are
treated as ambiguous and remain unchanged. The sweep rechecks the exact directory
entries again before each move; a restored source cancels that archive action.
Completed archives move the page to `sources/_archived/<slug>-<hash8>.md` and
mark the ledger row `superseded`. `sources/_archived` is an internal scan/index
exclusion, but archived pages remain readable by exact path until manual
removal.

### 10.3 Failure isolation

Per-file failures are classified (§4.2) and never abort the run. Classification
precedes diagnostic-message selection, so an anchored refusal in any
authoritative stream wins over a generic diagnostic in another stream. Stdout
whose first non-whitespace rune is `{` or `[` is JSON-looking; if it is
malformed, wrong-shaped, or lacks a final result, it is never reused as plain
text. On a non-refused failure the persisted message uses the safe eligible
final result first, then non-empty stderr, then non-JSON-looking plain stdout,
then the process error. The selected candidate is canonicalized as in §4.2 and
bounded to exactly 2,000 runes: values over the bound retain the first 1,999
runes plus `…`.

Permanent failures increment `attempts`, bounded at 3; at the bound the file is
skipped in later runs (reported as exhausted). Transient, refused, and infra
failures leave `attempts` unchanged. Transient and infra failures retry on later
runs. A refused row is skipped while its model and content hash are unchanged.
A final generic `api_error`, including on exit zero, is transient unless the
anchored refusal predicate matches.

### 10.4 Report

A per-run report with counts (scanned, digested, failed, refused, skipped,
deferred, unchanged, archived, source-errors, not-examined) and a per-file list
(action ∈ `digested | would-digest | failed | refused | skipped | deferred |
exhausted | source-error | archived | would-archive`, plus an error string).

**Sum invariant.** `scanned == digested + failed + refused + skipped + deferred +
unchanged + not-examined`. `archived` (wiki-page sweep) and `source-errors`
(directory-level) are excluded. A mismatch is surfaced as a `!! report sum
mismatch` warning on the summary line. `not-examined` counts source files the
main loop never reached due to `--limit`; it is omitted from the summary line
when zero.

Per-file actions describe the reason, not the summary bucket; they do not tally
1:1 to the summary counts.

Oversized/type-excluded files appear in the report but not the ledger. Files
whose `Lstat` fails appear as `skipped` with error `"stat: <err>"`. Unchanged
(already-processed) files are counted but kept out of the per-file list for
readable backlog reports. A `success` row is unchanged only when `Storage.Stat`
confirms that its wiki page still exists. `ErrNotFound` falls through to
re-digest the present source. Other stat errors report `failed` with
`stat wiki page: <error>` and leave the ledger row unchanged.

A source directory or file whose read fails with a permission error reports
`permission denied: cannot read source`; on macOS it adds
`; macOS consent required, see README "Schedule zero-touch ingest"`. A source
directory emits `source-error` once during the orphan sweep and once during the
scan because `Run` sweeps before scanning. A source file emits `skipped` with
the `read: ` prefix.

For actionable transient failures, the report error and, when ledger
persistence succeeds, `last_error` contain the same canonicalized, bounded
diagnostic. A structured final result is rejected when it is empty, contains
CR/LF, or begins with YAML frontmatter, a Markdown heading, or a code fence;
stderr or the process error then supplies the safe fallback. These shape gates
do not prove provenance: an accepted, bounded single-line provider diagnostic
may still contain source-derived wording. That local residual is accepted by
the diagnostic-safety deviation.

### 10.5 Concurrency

A single-instance exclusive `flock` on `<dir(db_path)>/ingest.lock`, acquired for
the duration of a run, prevents scheduled and manual runs from overlapping; a
second run fails fast with the already-running error. Every DB opener (ingest
ledger + index) sets `busy_timeout` so cross-process writes wait rather than fail,
**except** the FTS5 read→write tx-upgrade path (`index.Add`, DELETE-then-INSERT in
a DEFERRED tx): a concurrent writer there yields `SQLITE_BUSY_SNAPSHOT`, which
`busy_timeout` does not retry. That failure is classified infra (the file's
attempt is spared and it self-heals on the next run), so it never corrupts state
or exhausts retries. This write×write FTS case is a documented limitation; see
`docs/deviations/2026-07-22-busy-timeout-fts-write-write.md`.

### 10.6 Ledger (`ingest_ledger`)

```
ingest_ledger(
  source_path TEXT, content_hash TEXT, source_dir TEXT,
  digested_at TEXT, wiki_page TEXT,
  status TEXT,            -- success | failed | refused | superseded
  attempts INTEGER, last_error TEXT,
  run_origin TEXT,        -- scheduled | interactive
  llm_model TEXT NOT NULL DEFAULT '',
  digest_profile TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (source_path, content_hash)
)
```

Owned by `internal/ingest` through its own DB connection to `db_path` (WAL +
busy_timeout make the second connection safe). Type-excluded/oversized files are
reported per run, not persisted. Source originals are never moved or deleted.
Existing ledger rows are not migrated when refusal classification changes. A
historical generic API failure already recorded as `refused` remains terminal
until its `digest_profile` or source content changes, which activates the
retry gate. `digest_profile` encodes backend, model, canonical base URL,
`max_input_chars`, and extraction-contract version; changing any part makes a
prior failed or refused row retryable. A legacy row with an empty profile is
retryable once under the current profile.

### 10.7 Behavior constants and promoted knobs

LLM timeout default 5m (`llm.timeout_seconds`, default 300, must be positive),
maximum diagnostic length 2,000 runes (including `…`), max permanent-failure
attempts 3, settle window 2m — the timeout is the promoted knob; the rest
remain code constants, promoted only on demonstrated need.

Max file size was promoted to `max_file_size_mb` (default 32MB) after a real
corpus file (42MB PDF) was permanently skipped (F9).

---

## 11. _schema.md

Read-only. The server passes a summary as instructions; the full text is read via
`wiki_read("_schema.md")`. Enforced type: `source` only; other types are free-form.
The default schema (embedded via `go:embed`) defines source-page frontmatter
(`type: source`, `source_path`, `ingested_at`) and required sections, plus
provenance rules (`[TODO: source needed]`, `[UNCERTAIN]`). Optional frontmatter:
`category` (`article` | `legal` | `reference`), classified by the LLM during
digestion via the `buildPrompt` instruction (not schema-dependent). The ingest
digestion prompt embeds these rules so generated pages conform. MCP initialize
instructions append server-owned evidence guidance after the schema text; the
2,000-rune cap applies only to the schema portion, not the appended guidance.

### 11.1 Optional research evidence

Any page may include an optional `research_evidence` frontmatter mapping. Version
1 has required integer `version: 1` and a required `claims` array. Each claim
requires non-empty string `id`, `text`, and `kind`, and a non-empty `sources`
array. Claim kinds are the closed set `empirical`, `causal`, `normative`.
Sources are objects requiring `ref`; `locator` is optional. A ref is an exact
wiki-root-relative `.md` path; case and normalization follow filesystem lookup.
Optional claim fields are `checks` (array of objects with required kind and
result, optional locator), `gaps` (array of objects with required kind, detail,
next_step), and string `limitation`. Check kinds are `bibliography`,
`fulltext`, `table`, `replication`; gap kinds are `source_unavailable`,
`verification_pending`, `replication_pending`, `applicability_unknown`,
`additional_evidence_needed`. `locator` is required and non-empty for
`fulltext`, `table`, and `replication` checks. Unknown keys anywhere within
the record are ignored.

This record is self-reported structured evidence, not a fact-check or legal
judgment. `wiki_write` reports findings as non-blocking warnings (§8.3), lint
reports diagnostics (§9.12), and search returns a bounded summary (§8.5).
Search does not resolve refs. Re-digesting a source page carries the previous
page's `research_evidence` record into the new page unchanged; ingest never
writes or revalidates it, so claims about the old text may need review.

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
github.com/golang-jwt/jwt/v5    # JWT parsing/validation (oauth mode)
```

The `claudecode` backend shells out to the `claude` CLI; the `ollama` and
`openai` backends use HTTP. The `openai` PDF path additionally requires Poppler,
Tesseract, and `eng`/`kor` language data at runtime. For `openai`, `.md`,
`.markdown`, and `.txt` sources are sent verbatim; they must be valid UTF-8 and
at most `max_input_chars` runes, otherwise the file fails permanently and is
never truncated. Each `openai` digest request
sends `reasoning_effort: "none"` and `max_tokens: 4096`; a response with
`finish_reason: "length"` is a permanent failure.

---

## Appendix A: Test requirements (v2 highlights)

### Config
- Absolute `wiki_dir`/`db_path` accepted; leading `~/` expands; `~` mid-path
  literal. Relative after expansion → error. Source overlapping `wiki_dir` (either
  direction / equal) → error. `db_path` inside `wiki_dir` → error. Unknown key →
  error. Backend must be `claudecode`, `ollama`, or `openai`; OpenAI requires a
  loopback base URL, model, sources limited to `pdf`/`md`/`markdown`/`txt`, and
  `max_input_chars` in 1..1000000.

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
- Ollama backend (`httptest`): success and fenced ```-wrapped output both
  yield the unwrapped page; 408/429/5xx → `ErrTransient`; 4xx (400/404) →
  permanent; empty response → permanent error.

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

### Research evidence
- Optional `research_evidence` frontmatter validates version integer `1`, required
  fields, closed enums, exact-path refs, ignored unknown keys, and EVID001–EVID007
  diagnostics including skip rules. Check write warning ordering/format and that
  evidence findings never block writes.
- Search summaries cover absent/recorded/unavailable states and reasons, caps
  (3 claims, 4 check/gap kinds each, 200-rune limitation), `claim_count`,
  `truncated`, read failures, and no ref resolution.
- Lint reports only present fields, checks diagnostic severities, deterministic
  ordering across all issue kinds, text messages without IDs, exact JSON shape
  and empty-array encoding; `--strict` changes only exit status.
- MCP initialize appends server-owned evidence guidance after schema text, with
  the schema rune cap independent of that appended guidance.

### Archive prototype (§9.16)
- Object bytes equal input, read-only, one file per hash; no staging files
  remain after success or rejection; oversize input and non-http(s) URLs are
  rejected without storing.
- Identical requests return the same stable capture ID (also after reopen); a
  reused key with different content conflicts without changing the archive;
  same bytes with different provenance share one object as separate captures.
- `verified` requires a basis; `incomplete` gives `partial` acquisition.
- A tampered existing object is an integrity error for both a new request and
  an identical repeated request, and is not overwritten; a symlinked root,
  `objects`, `registry.db`, `-wal`, `-shm`, or `-journal` and a relative root
  are refused without touching the symlink target.
- Report cites keep order; URL cites create no capture; unknown or report
  capture cites are rejected; status shows `same bytes` and `cited by`.
- Schema-1 registries migrate with capture IDs unchanged. A summary never
  changes its source and is never handed off; recovery attempts append and
  link a new capture without changing the old one.
- Hand-off holds `unknown`/`incomplete`/`partial` and non-hand-off types;
  same bytes under different names get one extension and one source path;
  pickup refuses types the ingest backend does not digest, never replaces a
  file, and rejects manifests with unknown fields, trailing JSON, or two
  extensions for one object. The ledger reader creates no file (no `-wal`,
  `-shm`, or `ingest.lock`) and refuses missing DB/table/lock and a leftover
  `-wal`. Receipts are written only for ledger rows; an evidence ref appears
  only for `success`; changed or unmatched receipts are refused.
- Quarantine needs the current impact digest and hides the capture
  everywhere; re-capturing its bytes fails; purge needs the confirmation and
  30 days; a crash between log and apply is repaired at open; deletion order
  follows seq even when the clock moves backward; diverging logs fail.
- Backups under concurrent writes from a second handle always hold every
  referenced object; a missing referenced object fails the backup; a tampered
  backup fails verification; a restore with the newer log brings back no
  deleted object.
- MCP archive tools refuse while the gate is closed, including a handler
  looked up before removal; reject paths, undeclared arguments, `verified`,
  and text over 1 MiB without storing; expose no host path or internal error
  text; list no admin, delete, or reload tool; keep bearer 401s.
- Reload toggles only `capture_enabled`; invalid config, restart-only
  changes, and blocked activation keep the state and log no values; disable
  drains admitted calls and stops waiting on shutdown; SIGHUP reloads without
  stopping the server, and serve returns only after the reload worker ends;
  enable sends `tools/list_changed`. `serve` with capture enabled and no
  deployment settings refuses to start on loopback with bearer auth.
- Reassess adds a linked verified capture with the held capture's
  provenance, never changes the held capture, refuses incomplete, verified,
  and non-source captures, and links once even after an interrupted run.
- A fetch file splits only when it re-encodes byte for byte; the body is
  held as `.response` even after reassessment; the fetch file is kept as a
  derived object and is quarantined and purged with the body; an oversize
  derived file is refused before the body is stored; a partial import is
  completed by repeating it.
- A source page named by a receipt: a ref to it warns EVID004 until the page
  exists; `evidence_ref_path_exists` follows the file's presence only;
  `page_frontmatter_lists_capture` is true only when the page lists both
  this capture ID and its object ID; the page link stays when the
  frontmatter does not list it, and a deleted page keeps the link with
  `page_exists` false and no `evidence_ref`; `ingest.evidence_ref` is
  omitted until the page exists while `ingest.wiki_page` keeps the path.
- Pickup records captures in the capture map, rejects repeated or malformed
  IDs and symlinked maps, and a lookup matches only the same name and bytes.
  Real ingest writes the map's IDs on the page, drops model-written archive
  keys, and sets none for changed bytes. A later capture of the same bytes
  is linked through its own receipt after the next ingest without changing
  the page (a user edit made meanwhile stays) and the frontmatter snapshot
  still lists only the first capture.
- Page links: each capture of the same bytes has its own link and basis
  receipt; the first link and basis stay unchanged; re-collection and
  reopening add nothing; other bytes never join another page's list; a
  failed observation links nothing; a later failure makes a link not
  current and a re-digest to another page adds a link; forged receipts,
  object mismatches, and non-wiki page paths are refused; quarantined
  captures are hidden; links survive backup and restore, a receipt
  collected after restore links there, and a schema-2 registry is
  backfilled with the same basis on open.
- Receipt order: receipts `.1`, `.2`, `.10` collected in one run give
  basis `.2`; a newer receipt whose `observed_at` is earlier (clock went
  back) is still the latest; linking `.10` before `.2` still gives basis
  `.2`; a schema-2 backfill gives the same basis and latest state; a
  schema-3 registry holding a `.10` basis with `.2` already collected
  (which re-collection does not change) gets basis `.2` on open.
- Deletion and pickup hardening: text shared by two HTML sources stays
  while either needs it and goes with the last; a damaged or missing
  quarantined file blocks `unquarantine` with no log record; when a file
  exists in both places, the verified copy wins and two damaged copies are
  an error with nothing removed; an oversized deletion record is refused
  and the log stays readable; FIFOs are refused without blocking;
  overlapping capture-map updates keep every entry and an oversized map is
  refused unchanged; backups keep objects read-only; concurrent
  reassessments from two handles link once; a repeated failed extraction
  returns the failure; an svg `title` is not the document title.
- Acquire: public-address table; loopback, private, mixed, metadata,
  mapped, and literal hosts refused with no unchecked dial; the dial goes to
  the checked address when DNS rebinds; queries, bare `?`, fragments,
  userinfo, and query redirects refused without echoing the secret; no
  cookie or credential is sent; size, time, redirect, 403, and 404 limits
  store nothing; re-acquisition records attempts and never changes the old
  capture; a content-coded body is stored as `.bin` and never extracted.
- Extract drops script, style, frames, SVG, and attributes, keeps headings
  and lists, refuses non-UTF-8 and non-HTML input, records failures, never
  changes the original, refuses a quarantined source, quarantines extracted
  text with its source, and leaves no active text for a quarantined source
  under concurrent extraction and quarantine; a derivation is not recorded
  for a body quarantined after its capture.

### CLI
- `init` two-step (first run scaffolds + guidance; second run builds wiki/schema/
  db); idempotent. Missing config → error naming the path. `ingest --dry-run`
  lists pending; lock held → nonzero already-running; missing `claude` → clear
  error. `serve` init failure → exit 1. `ingest --run-log-dir` records only
  state-change events with timestamps, creates no file for a quiet `ok` run,
  records the run error or panic, and never fails the run on a log write error.

### Integration (U9)
- Backlog run → N pages/rows/`success`. Incremental run digests only the new file
  with `run_origin` from `--scheduled`. Ratelimit on one file → that file `failed`
  attempts 0, others succeed, exit 0. Concurrency: a writer + concurrent reader on
  the production DSN completes with no "database is locked"
  (`TestE2EConcurrentIndexWriteDuringRead`), and two write-first writers serialize
  under `busy_timeout` (`TestE2EBusyTimeoutSerializesWriters`, ledger-style plain
  table). The FTS5 write×write tx-upgrade case is not covered by an e2e test — it
  is a known limitation (`SQLITE_BUSY_SNAPSHOT`, infra-classed on ingest); see
  `docs/deviations/2026-07-22-busy-timeout-fts-write-write.md`.
