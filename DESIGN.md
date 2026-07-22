# Design Document — cogvault v2

Spec: `SPEC.md` (v2). This document covers **how** the spec is realized.
Refounding rationale: `docs/decisions/0021-v2-refounding.md`.

---

## 1. Component dependency graph

```
cmd/cogvault/main.go
    │
    ├──────────────┬───────────────────────────┐
    ▼              ▼                             ▼
┌─────────┐   ┌──────────┐                 ┌──────────┐
│ mcp/    │──▶│ storage/ │◀────────────────│ ingest/  │──┐
│ server  │   │ fs       │                 │ (ledger) │  │
│ tools   │──┐└──────────┘                 └──────────┘  │
└─────────┘  │ ┌──────────┐   ┌──────────┐     │  │      │
             ├▶│ index/   │──▶│ adapter/ │◀────┘  │      │
             │ │ sqlite   │   │ obsidian │        ▼      │
             │ └──────────┘   └──────────┘   ┌──────────┐│
             └▶ adapter/obsidian             │ llm/     ││
                                             │ claudecode│
   ingest/ reads sources[] directly ────────┴──────────┘│
   (plain os calls, NOT through storage) ◀───────────────┘

all packages ──▶ errors/, config/
```

Unidirectional, no cycles. Two new packages in v2: `internal/llm` and
`internal/ingest`. `ingest` composes `storage` (wiki writes), `index` (Add),
`llm` (Digest), and `config`, and additionally reads source files directly.

---

## 2. Component design

### 2.1 errors

Sentinel error package (SPEC §4.1). Mapping lives in `mcp/tools.go` `mapError`.
The ingest error classes (§4.2) are separate: `llm.ErrTransient` plus an internal
`failureClass` enum (permanent/transient/infra) in `internal/ingest`.

### 2.2 config

```go
type SourceDir struct { Path string; Types []string }
type LLMConfig struct { Backend string }
type Config struct {
    WikiDir, DBPath     string
    Sources             []SourceDir
    Exclude, ExcludeRead []string
    Adapter             string
    ConsistencyInterval int
    LLM                 LLMConfig
}
func Load(configPath string) (*Config, error)   // explicit path, no vault discovery
func Save(configPath string) error
func DefaultConfigPath() (string, error)         // ~/.config/cogvault/config.yaml
func (c *Config) SchemaPath() string             // "_schema.md" (root-relative)
```

`expandTilde` applies to `WikiDir`, `DBPath`, and each `Sources[i].Path`
(leading `~/` or exact `~` → `$HOME`; `~` mid-path literal). `validate` requires
those three absolute after expansion and rejects overlaps via `hasPathPrefix`.
Responsibility boundary unchanged (0001): config validates path-string safety and
policy; filesystem existence/permissions are enforced by storage/runtime. Source
directory existence is checked at ingest runtime, not config load.

### 2.3 storage/fs

```go
type FSStorage struct { root string; cfg *config.Config; mu sync.Mutex }
```

`root` is the absolute `wiki_dir` (single mode). The path pipeline is unchanged
(raw `..` check → `filepath.Clean` → abs → per-component symlink check →
per-method checks). The v1 `Write` wiki-prefix check is **deleted**: the whole
root is writable except `_schema.md` (guarded via `cfg.SchemaPath()`). New
`Stat(path) (int64, time.Time, error)` uses `resolvePath` + `os.Stat` with the
existing error mapping, feeding the index stat-gate. Single global write mutex
retained (0006).

### 2.4 adapter/obsidian

Unchanged from v1 (`scanner.go` + `parser.go`; frontmatter, wikilink, tag,
dataview extraction). It parses wiki pages under `wiki_dir`. `ResolveLink` still
absent (v0.3 with Lint).

### 2.5 index/sqlite

```go
type SQLiteIndex struct {
    db *sql.DB; cfg *config.Config; root string
    lastConsistency atomic.Int64
    mu sync.RWMutex; ccMu sync.Mutex; useTrigram bool
}
```

v2 changes:

- **`Search(query, limit)`** — the `scope` parameter and `appendScopeFilter` are
  removed; the index holds wiki pages only.
- **Schema versioning** — `PRAGMA user_version=2` replaces the `mod_time`-column
  sniffing migration. A DB with tables at `user_version < 2` is dropped and
  recreated. `file_meta` gains `size INTEGER` and `mtime TEXT`.
- **busy_timeout** — every connection (via the pooled DSN pragma) sets
  `busy_timeout=5000` so multi-process contention waits instead of failing.
- **Stat-gate** — `CheckConsistency`'s scan callback calls `store.Stat(path)`
  first and only `store.Read` + re-hashes when size or mtime differ from the
  stored row. Dataless/eviction read errors stay per-file warnings (existing
  `errs` join path). This bounds iCloud re-download cost.

`Add` remains a pure indexing API (hash + FTS, single TX, no disk access);
direct `Add` calls pass zero size/mtime, CheckConsistency threads real values.

### 2.6 llm (new)

```go
type DigestRequest struct { SourcePath, SchemaText, PageSlug string }
type DigestResult  struct { PageContent string }
type Adapter interface {
    Digest(ctx context.Context, req DigestRequest) (*DigestResult, error)
    Name() string
}
var ErrTransient error   // wraps quota/rate-limit/timeout/transport failures
func NewClaudeCode(binPath string) *ClaudeCode
```

**Responsibility**: define the digestion contract and one backend. `claudecode`
runs `claude --print --output-format json --allowedTools "Read"` with the prompt
(schema text + instructions + absolute source path) on **stdin** (avoids ARG_MAX),
a per-call 5-minute timeout, strips an optional leading/trailing ``` fence, parses
the final JSON array element, and treats any non-`success` subtype or
`is_error:true` as failure regardless of exit code (O1 findings). Error
classification: quota/rate-limit/timeout/transport/`error_during_execution` →
`ErrTransient` (permanent otherwise). A future local backend implements the same
interface without touching `ingest`.

### 2.7 ingest (new)

```go
type Runner struct { /* cfg, store, idx, llm, dbPath, ledger, seams */ }
func New(cfg *config.Config, store storage.Storage, idx index.Index,
         llmAdapter llm.Adapter, dbPath string) (*Runner, error)
func (r *Runner) Run(ctx context.Context, opts RunOptions) (*Report, error)
func (r *Runner) Close() error
type RunOptions struct { DryRun bool; Limit int; Origin string }
```

**Responsibility**: orchestrate the digest stage. `Run` acquires an exclusive
`flock` on `<dir(dbPath)>/ingest.lock` (fail fast → `ErrAlreadyRunning`), scans
each `cfg.Sources` dir **top level only** (`os.ReadDir` + `os.Lstat`, skipping
dirs and symlinks) applying the type filter, 32MB size cap, and 2m settle window,
streams a sha256 hash (`hashFile`, no full file in memory), looks up the ledger,
calls `llm.Digest`, validates the page's frontmatter from bytes, resolves the
collision-aware page path, writes through `storage`, indexes via `idx.Add`, and
finalizes the ledger row. It honors ctx cancellation between files (partial report
+ wrapped ctx error) and releases the lock via defer. Error classes drive
`attempts` (permanent ++, transient/infra unchanged; §4.2).

**Ledger** (`ledger.go`): owns its **own** `sql.Open("sqlite", dsn)` to `db_path`
with DSN pragmas `busy_timeout(5000)` + `journal_mode(WAL)` on **every** pooled
connection (not a one-shot `PRAGMA` exec). The `index` package never touches the
`ingest_ledger` table; WAL + busy_timeout make the second connection to the same
DB file safe. DDL and transitions (`lookup`, `supersedePrevSuccess`, `upsert`) per
SPEC §10.6.

**Report** (`report.go`): builds the `Report` struct + a `String()` renderer;
printing is the CLI's job.

### 2.8 mcp

`server.go`/`tools.go`: the `wiki_search` tool drops the `scope` parameter; tool
descriptions say "wiki root" instead of "vault". `handleWikiSearch` calls
`idx.Search(query, limit)`. mcp-go does not enforce `additionalProperties:false`,
so a stray `scope` arg is ignored, not rejected — the enforceable contract is
schema absence of `scope`. Instructions/`mapError`/write-then-index otherwise
unchanged.

### 2.9 cmd/cogvault

Root persistent flag `--config` (default `config.DefaultConfigPath()`); the old
vault flag is deleted. `wire.go`: `resolveConfigPath(cmd)` + `bootstrap(configPath)` →
`config.Load` → adapter → `storage.NewFSStorage(cfg.WikiDir, cfg)` →
`index.NewSQLiteIndex(cfg.WikiDir, cfg.DBPath, cfg)`. `init.go` is the two-step
flow (SPEC §9.1). `ingest.go` (new): flags → `ingest.RunOptions`, `exec.LookPath`
for `claude` → `llm.NewClaudeCode`, prints `report.String()`, nonzero exit only on
run-level failure.

---

## 3. Data flow

### 3.1 Ingest (Phase 1)

```
sources[].path ──scan──▶ stability gate ──▶ content hash ──new?──▶ llm.Adapter.Digest(file, schema)
                                                                      │ (claude --print subprocess)
                                                                      ▼
                                                           markdown source page
                                                                      │
                                        storage.Write ──▶ index.Add ──▶ ledger: success
                                        (failure at any step ──▶ ledger: failed + class, run continues)
```

### 3.2 init (two-step)

```
run 1: stat config → absent/invalid-fresh → Save template + guidance, exit 0
run 2: Load (valid) → MkdirAll(wiki_dir) → WriteSchema → MkdirAll(dir(db_path))
       → NewSQLiteIndex → Rebuild (new DB) | CheckConsistency(force) (existing DB)
```

### 3.3 serve

```
resolveConfigPath → Load → bootstrap(store/index/adapter) → CheckConsistency(force)
  → mcp.NewServer(wiki root) → ServeStdio (blocking) → cleanup
```

---

## 4. Design decisions

- **Single mode over dual mode** (0021 D1): one root, no vault/wiki split.
- **Batch + launchd over daemon** (0021 D2): no long-lived process.
- **Eventual consistency** retained: write-then-index + bounded-staleness
  CheckConsistency (now stat-gated for iCloud).
- **Ledger owns its DB connection**: keeps `index` free of ingest state; DSN
  busy_timeout + WAL make concurrent openers safe (multi-process contract).
- **Validate-then-write**: an unparsable generated page is a permanent failure and
  nothing lands in the synced wiki folder.
- **trigram tokenizer** retained (Korean already validated; LIKE fallback ≤ 2
  chars).

---

## 5. File responsibilities

| File | Responsibility |
|------|------|
| `errors/errors.go` | sentinel errors |
| `config/config.go` | YAML, `~` expansion, absolute/overlap validation, explicit-path Load/Save |
| `storage/storage.go` | interface + ListEntry + `Stat` |
| `storage/fs.go` | filesystem (single wiki root), security, mutex |
| `adapter/obsidian/*` | Scan, frontmatter/wikilink/tag/dataview parse |
| `adapter/markdown/parser.go` | standard-markdown fallback |
| `index/index.go` | interface + Result + FileMeta |
| `index/sqlite.go` | FTS5, file_meta (+size/mtime), user_version=2, stat-gated CheckConsistency, busy_timeout |
| `llm/llm.go` | Adapter interface, DigestRequest/Result, ErrTransient |
| `llm/claudecode.go` | `claude --print` backend, JSON parse, error classification |
| `ingest/ingest.go` | Runner: scan, hash, digest, validate, write, index, ledger, lock, ctx |
| `ingest/ledger.go` | `ingest_ledger` DDL + transitions; own DB connection |
| `ingest/report.go` | Report struct + String() |
| `mcp/server.go` | MCP server, instructions |
| `mcp/tools.go` | six tools, mapError, listWithMeta (no scope) |
| `cmd/cogvault/*` | cobra CLI: `--config`, init/search/serve/ingest |
| `schema/schema.go` + `default_schema.md` | `go:embed` default schema |

---

## 6. Concurrency

```
Storage.Read/Stat  — no lock
Storage.Write      — Storage.mu
Index.Search/GetMeta — Index.mu.RLock (WAL read)
Index.Add/Remove   — Index.mu.Lock
CheckConsistency   — ccMu.Lock (serialize) + mu (read then apply)
Ingest run         — flock on ingest.lock (single instance, cross-process)
DB (all openers)   — busy_timeout=5000 per connection (contention → wait)
```

Storage.mu ↔ Index.mu never held together; no deadlock. Cross-process safety
(scheduled ingest vs live serve) rests on the ingest flock + WAL + busy_timeout.

---

## 7. Test design

| Target | Method |
|------|------|
| config | YAML → Load → validate (absolute/overlap/expansion) |
| storage/fs | `t.TempDir()` fixtures; Stat; whole-root write; security |
| adapter | fixtures/obsidian, edge |
| index/sqlite | temp DB; user_version recreation; stat-gate Read-count |
| llm | fake `claude` in `testdata/bin` (argv/stdin/mode) |
| ingest | mock `llm.Adapter` + real temp dirs; ledger transitions; lock; ctx |
| mcp | mcp-go test client; schema has no scope |
| cmd | in-process cobra; `--config` temp files; ingest via fake `claude` on PATH |
| integration (U9) | backlog/incremental/contention e2e |
| race | `go test -race ./...` |

---

## 8. Implementation order (v2 Phase 1)

```
U1: O1 spike — headless PDF digestion verification (research note)
U2: config v2 (single mode, explicit path, sources, ~ expansion)
U3: storage — root is the wiki, Stat added
U4: index — user_version=2, size+mtime stat-gate, scope removal, busy_timeout
U5: llm — adapter interface + claudecode backend + error classes
U6: ingest — pipeline, ledger, lock, report
U7: cmd — --config model, ingest command, scope-flag removal
U8: launchd template + canonical docs (this document set)
U9: end-to-end integration tests
```
