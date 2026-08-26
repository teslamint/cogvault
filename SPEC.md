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
- `internal/llm` adapter interface with a `claudecode` backend (`claude --print`).
- Single-mode config/storage: absolute `wiki_dir` root, `sources[]`, absolute
  `db_path` outside the synced folder.
- SQLite FTS5 full-text search (trigram, Korean-friendly).
- launchd template + setup docs for zero-touch scheduled ingest.

### 1.3 Out of scope

- Image OCR.
- Dedicated phone-capture tooling: a share-sheet target, and consume-and-archive
  semantics for inbox directories (project-background S5/O5). A folder the phone
  syncs into is already usable as an ordinary `sources[].path`, which needs no
  code and is not what this entry defers.
- `pdftotext` text-extraction step (conditional fallback; **not activated** — O1
  verified headless PDF reading works, see 0021 D6).
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
  backend: string       # default "claudecode". Allowed: "claudecode", "ollama".
  model: string         # optional. Passed as --model to the LLM backend. Default: empty (backend default).
  base_url: string      # optional, "ollama" backend only. Default: http://localhost:11434.
  timeout_seconds: int  # optional. Per-digest-call bound for either backend. Default: 300. Negative rejected.
  embedding_model: string    # optional. Required by `cogvault embed` (§9.8); enables embedding-ranked `similar` (§9.9).
  embedding_base_url: string # optional. Default: http://localhost:11434. Rejected unless embedding_model is set.
max_file_size_mb: int   # ingest file size cap in MB. Default: 32. Negative rejected.
exclude: string[]       # scan + index exclusion, relative to wiki_dir. Default: [".obsidian", ".trash", "sources/_archived"]
exclude_read: string[]  # read + scan + index exclusion, relative to wiki_dir. Default: []
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
- `adapter` outside the allowed list; `llm.backend` other than `claudecode`.
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
  `![[f]]`, `![[f|s]]` → Attachments `"f"`. Code-block wikilinks not excluded
  (false positives allowed in Phase 1).

---

## 8. MCP tools

### 8.1 Common rules

- Transport: `stdio` (default), `sse`, or `http` (Streamable HTTP), selected
  via `cogvault serve --transport` (§9.3). Server name `cogvault`.
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
`bytes` = `len(content)`; `warnings` always empty in Phase 1. Side effect:
best-effort index reflection. When `git.auto_commit` is `write` or
`write+ingest` (§3.1, default `off`), also auto-commits the write
(`git add` + `git commit -m "wiki: write <path>"`) when `wiki_dir` is a git
repository; a failed `git add`/`git commit` is logged, not returned as a
tool error, and is bounded as described in §8.8.1 (0024).

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
`[{path, title, type, category, snippet, score}]`. Empty result is not an error.

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
snapshot never commits it as wiki content. Commits against *different*
repositories do not block each other.

**Timeouts.** The lock wait, `git add`, and `git commit` are each bounded
independently by the same 10s budget, so one slow step cannot consume
another's. Worst case for a single auto-commit is therefore ~30s, not 10s. A
step that exceeds its budget is signalled with `SIGTERM` and given a 2s grace
period before being force-killed: `git` removes `.git/index.lock` from its
own signal handler, and an untrappable `SIGKILL` would instead strand that
lock on disk, breaking every later commit until an operator removed it by
hand — manufacturing the wedged lock this timeout exists to prevent.

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

Every command except `fetch` and `status` opens the index and runs a forced
consistency check first (§2.3). A check error warns on stderr and does not stop
the command. `fetch` reads the config only; it never opens the index or the
wiki. `status` loads the config and the ingest ledger only.

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
- See `docs/deployment/remote-mcp.md` for the full remote setup (tunneling,
  identity-provider prerequisites, security posture).

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
- **The response body is read through a 10 MiB limit and silently truncated
  past it** — a larger page produces a short file, not an error.
- **Content validation**: a response whose `Content-Type` is not text-ish
  (`text/*`, `application/json|xml`; an absent header is allowed and left to
  the UTF-8 gate) is rejected with `unsupported Content-Type`, and a body
  that is not valid UTF-8 is rejected — binary media must not become a
  `.md` capture.
- Writes YAML frontmatter (`source_url` **quoted**, `fetched_at` as a UTC
  date) followed by the raw response body. No HTML-to-text extraction happens
  here; the LLM digest step handles the markup. The file is written to a
  temp name and renamed into place, so a crash cannot leave a partial
  capture.

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
cogvault lint [--config <path>] [--strict]
```

Reports, one line per issue as `<kind> <path> <message>`:

- `parse` — the page could not be parsed.
- `frontmatter` — missing `title`, missing `type`, or, for `type: source`
  pages, a missing `source_path` or `ingested_at` (§11). `_schema.md` is exempt
  from this check: it defines the page contract rather than being written under
  it. Its links are still checked. A freshly initialized wiki is therefore
  lint-clean, which is what makes `--strict` usable from the first run.
- `broken-link` — a `[[wikilink]]` that resolves to no page. Resolution tries
  `<link>.md`, `sources/<link>.md`, and `<link>` verbatim, then falls back to a
  case-insensitive match on any page's basename.
- `orphan` — no incoming links. `_schema.md`, `_index.md`, and every
  `type: source` page are exempt, since source pages are expected to be reached
  through search rather than links.

- Prints `lint: no issues found` when clean, otherwise the issue lines and
  `N issue(s) found`.
- `--strict` makes findings fail the command. It changes **only** the exit
  code: stdout is byte-identical with and without it, so a script can add the
  flag without re-parsing anything.
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
  → collision-aware page path under sources/
  → storage.Write → index.Add → ledger: success
(digest/validation/storage/index failure → attempt ledger status `failed` or `refused` + classified error)
(ledger persistence failure → report/log failure; the failed write cannot guarantee its own row persisted)
(the run continues)
```

### 10.2 Page identity

One source file path → one wiki page `sources/<slug>.md`. `<slug>` is derived from
the source base name (extension stripped) by: lowercasing, replacing spaces with
`-`, dropping every character that is not a Unicode letter, Unicode digit, `.`, `_`,
or `-`, collapsing runs of `-` to one, and trimming leading/trailing `-`. If that
leaves the slug empty (e.g. a name with no letters or digits), it falls back to
`src-<hash8>` (`hash8` = first 8 hex of sha256 of the file content). If the resulting page path is already mapped to a different source path,
the page becomes `sources/<slug>-<hash8>.md` (here `hash8` = first 8 hex of sha256
of the absolute source path). Re-digestion of the same source path with new content
overwrites its page and marks the prior ledger row `superseded`.

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
  llm_model TEXT,
  PRIMARY KEY (source_path, content_hash)
)
```

Owned by `internal/ingest` through its own DB connection to `db_path` (WAL +
busy_timeout make the second connection safe). Type-excluded/oversized files are
reported per run, not persisted. Source originals are never moved or deleted.
Existing ledger rows are not migrated when refusal classification changes. A
historical generic API failure already recorded as `refused` remains terminal
until `llm.model` or source content changes, which activates the existing
model/content-hash re-attempt gate.

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
digestion prompt embeds these rules so generated pages conform.

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

### CLI
- `init` two-step (first run scaffolds + guidance; second run builds wiki/schema/
  db); idempotent. Missing config → error naming the path. `ingest --dry-run`
  lists pending; lock held → nonzero already-running; missing `claude` → clear
  error. `serve` init failure → exit 1.

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
