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
The ingest error classes (§4.2) are separate: `llm.ErrTransient` and
`llm.ErrRefused` plus an internal `failureClass` enum
(permanent/transient/refused/infra) in `internal/ingest`. `ErrRefused` is
terminal only under the same configured model and content hash and consumes no
permanent attempt.

### 2.2 config

```go
type SourceDir struct { Path string; Types []string }
type LLMConfig struct { Backend string; Model string }
type Config struct {
    WikiDir, DBPath     string
    Sources             []SourceDir
    Exclude, ExcludeRead []string
    Adapter             string
    ConsistencyInterval int
    MaxFileSizeMB       int            // default 32; ingest uses int64(v)<<20
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
existing error mapping, feeding the index stat-gate. `Move(src, dst string) error`
resolves both paths, acquires the mutex, creates the destination parent directory,
and calls `os.Rename`; used by the ingest orphan sweep to archive pages.
`exclude_read`/schema permission checks happen before existence checks on both
source and destination. For allowed paths, missing source returns `ErrNotFound`
before destination occupancy is checked, and an occupied destination returns an
error that wraps `os.ErrExist`. Single global write mutex retained (0006).

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
- **Schema versioning** — `PRAGMA user_version=3` replaces the `mod_time`-column
  sniffing migration. A DB with tables at `user_version < 3` is dropped and
  recreated. `file_meta` gains `size INTEGER`, `mtime TEXT`, and
  `category TEXT DEFAULT ''` (F8: source-type classification).
- **busy_timeout** — every connection (via the pooled DSN pragma) sets
  `busy_timeout=5000` so multi-process contention waits instead of failing. One
  exception: `Add`'s DELETE-then-INSERT runs in a DEFERRED tx whose FTS5 DELETE
  reads shadow tables first; a concurrent committed writer makes the write upgrade
  return `SQLITE_BUSY_SNAPSHOT`, which `busy_timeout` does not retry. Ingest
  classifies this infra (attempts spared, self-heals next run); it is a documented
  limitation (`docs/deviations/2026-07-22-busy-timeout-fts-write-write.md`).
- **Stat-gate** — `CheckConsistency`'s scan callback calls `store.Stat(path)`
  first and only `store.Read` + re-hashes when size or mtime differ from the
  stored row. Dataless/eviction read errors stay per-file warnings (existing
  `errs` join path). This bounds iCloud re-download cost.

`Add` remains a pure indexing API (hash + FTS, single TX, no disk access);
direct `Add` calls pass zero size/mtime, CheckConsistency threads real values.

### 2.6 llm (new)

```go
type DigestRequest struct { SourcePath, SchemaText, PageSlug, SourceExt string }
type DigestResult  struct { PageContent string }
type Adapter interface {
    Digest(ctx context.Context, req DigestRequest) (*DigestResult, error)
    Name() string
}
var ErrTransient error   // wraps quota/rate-limit/timeout/transport/API failures
var ErrRefused error     // anchored provider policy refusal; model-gated
func NewClaudeCode(binPath, model string) *ClaudeCode
```

**Responsibility**: define the digestion contract and one backend. `claudecode`
runs `claude --print --output-format json --allowedTools "Read"` with the prompt
(schema text + instructions + absolute source path) on **stdin** (avoids ARG_MAX),
a per-call 5-minute timeout, strips an optional leading/trailing ``` fence, and
parses stdout as an event array. Only the final `type: "result"` event can
classify structured output or provide a diagnostic. `buildPrompt`
uses `SourceExt` to emit a type-aware read instruction (PDF/markdown/generic) and
carries the `category` classification instruction (article | legal | reference)
directly in code — not dependent on the wiki's `_schema.md` version. Error
classification and message selection are separate passes. A final
`terminal_reason: "api_error"` is refusal-classifiable even when `is_error` is
false; otherwise structured refusal classification requires `is_error` plus
`error_during_execution`. Classification checks that eligible result, stderr,
and non-JSON-looking plain stdout before selecting a message, so policy evidence
in one stream wins over a generic failure in another.

`isRefusalText` canonicalizes each candidate, then accepts only a candidate
beginning `policy refusal:`, or the anchored case-insensitive provider grammar
`^api error:\s*(?:refused\b|(?:[\p{L}\p{N} ._-]+(?:'s|’s)\s+)?safeguards flagged\b)`.
Generic `api_error`/`API Error:` messages, quoted or negated phrases, embedded or
suffix-only phrases, and `connection refused` are transient, including on an
exit-zero final API error.

Structured diagnostic persistence is stricter: the final event must have
`is_error == true`, must carry `api_error` or `error_during_execution`, and its
trimmed result must be non-empty, contain no CR/LF, and not begin with
frontmatter, a Markdown heading, or a code fence. After refusal classification, message
precedence is safe structured result, stderr, non-JSON-looking plain stdout,
then process error. Malformed, wrong-shaped, or final-result-less stdout that
begins with `{` or `[` is never reused as plain text. Recognized ANSI SGR/OSC is
removed, Unicode whitespace collapses to one ASCII space, and remaining Unicode
Control/Format (`Cf`) runes become `U+FFFD` before both classification and
display. Diagnostics over 2,000 runes retain 1,999 plus `…`. A future local
backend implements the same interface without touching `ingest`.

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
`flock` on `<dir(dbPath)>/ingest.lock` (fail fast → `ErrAlreadyRunning`). Before
the main file loop, `sweepOrphans` queries success rows and archives pages whose
source files no longer exist on disk to `sources/_archived/`, but only when an
exact source-dir snapshot still contains at least one tracked survivor and
exactly one missing success-row source. Zero-survivor and multi-missing states
are treated as ambiguous and remain unchanged. `sweepOrphans` reads the exact
directory entries again before each move, so a restored source cancels that
archive action. Source-dir snapshot errors log/report and skip that directory
unless the error is context cancellation. `Run` checks cancellation before sweep,
before each sweep candidate, and between scanned files; cancellation stops future
mutations but does not roll back rows already completed. The main loop then scans
each `cfg.Sources` dir **top level only** (`os.ReadDir` + `os.Lstat`, skipping
dirs and symlinks) applying the type filter, configurable size cap (default 32MB,
`cfg.MaxFileSizeMB`), and 2m settle window,
streams a sha256 hash (`hashFile`, no full file in memory), looks up the ledger,
calls `Storage.Stat` for found `success` rows, and reports `Unchanged` only when
that page still exists. `ErrNotFound` falls through to re-digest the present
source; other stat errors increment `Failed`, append `actionFailed` with
`stat wiki page: <error>`, and leave the success row unchanged. For re-digestion
it validates the page's frontmatter from bytes, resolves the collision-aware page
path, writes through `storage`, indexes via `idx.Add`, and finalizes the ledger
row. It honors ctx cancellation between files (partial report + wrapped ctx
error) and releases the lock via defer. Error classes drive `attempts`
(permanent ++, transient/refused/infra unchanged; §4.2). Refused rows persist
the configured model and are skipped only while both model and content hash are
unchanged; either change permits a re-attempt. Historical refused rows are not
reclassified or migrated.

**Ledger** (`ledger.go`): owns its **own** `sql.Open("sqlite", dsn)` to `db_path`
with DSN pragmas `busy_timeout(5000)` + `journal_mode(WAL)` on **every** pooled
connection (not a one-shot `PRAGMA` exec). The `index` package never touches the
`ingest_ledger` table; WAL + busy_timeout make the second connection to the same
DB file safe. DDL and transitions (`lookup`, `supersedePrevSuccess`, `upsert`) per
SPEC §10.6.

**Report** (`report.go`): builds the `Report` struct + a `String()` renderer;
printing is the CLI's job. Actionable transient diagnostics flow unchanged from
the adapter error into the per-file report error and, when the ledger write
succeeds, ledger `last_error`; the adapter's eligibility, shape,
canonicalization, and 2,000-rune gates run before that persistence boundary.

### 2.8 mcp

`server.go`: registers seven tools (`wiki_read`, `wiki_write`, `wiki_delete`,
`wiki_list`, `wiki_search`, `wiki_scan`, `wiki_parse`) via `registerTools`,
each wrapped in `noExtra`, which sets `InputSchema.AdditionalProperties =
false` on every tool's advertised schema (`mcp.NewTool` itself does not).
mcp-go's dispatcher never validates call arguments against the schema, so this
changes only what `tools/list` advertises, not runtime enforcement — a stray
argument (e.g. the `scope` param `wiki_search` no longer takes) is still
silently ignored by the handler, not rejected. Every tool also declares
`ReadOnlyHintAnnotation`/`DestructiveHintAnnotation` explicitly: `wiki_write`
and `wiki_delete` are the only two marked non-read-only/destructive; the other
five are read-only, non-destructive. `IdempotentHint`/`OpenWorldHint` stay at
`mcp.NewTool`'s library defaults and are not part of this contract.

`tools.go`: `wiki_search` drops the `scope` parameter; tool descriptions say
"wiki root" instead of "vault". `handleWikiSearch` calls `idx.Search(query,
limit)`. `handleWikiDelete` calls `store.Delete`, removes the path from the
index if it was indexed, then `gitAutoCommit` best-effort `git add`s and
`git commit`s the deletion when `wiki_dir` is a git repository — failures are
logged, not returned as a tool error (§2.10 covers the authorization layer
that gates this over the network). Instructions/`mapError`/write-then-index
otherwise unchanged.

### 2.9 cmd/cogvault

Root persistent flag `--config` (default `config.DefaultConfigPath()`); the old
vault flag is deleted. `wire.go`: `resolveConfigPath(cmd)` + `bootstrap(configPath)` →
`config.Load` → adapter → `storage.NewFSStorage(cfg.WikiDir, cfg)` →
`index.NewSQLiteIndex(cfg.WikiDir, cfg.DBPath, cfg)`. `init.go` is the two-step
flow (SPEC §9.1). `ingest.go` (new): flags → `ingest.RunOptions`, `exec.LookPath`
for `claude` → `llm.NewClaudeCode`, prints `report.String()`, nonzero exit only on
run-level failure.

`serve.go`: `serve` takes `--transport` (`stdio` default, `sse`, or `http`),
`--addr` (default `localhost:8080`, `sse`/`http` only), `--endpoint-path`
(default `/mcp`, `http` only — normalized to a leading slash, no trailing
slash), and `--public-url` (required in `oauth` mode; also feeds SSE message
endpoints and Origin checks). `stdio` calls `server.ServeStdio` directly.
`sse`/`http` route through `buildServeHandler`, which runs the startup guards
(SPEC §9.3) before ever listening — `auth.mode: none` refuses a non-loopback
`--addr` (`isLoopbackAddr` matches `localhost` as a literal string, not
through DNS resolution) and refuses a non-empty `--public-url` (a public URL
has no function in `none` mode and signals tunnel-exposure intent); `oauth`
mode requires `--public-url` and refuses `--transport sse` (`sse`'s fixed
`/sse`/`/message` paths can never match the advertised `resource`, so no
conformant OAuth client can complete the RFC 9728 §3.3 metadata flow); `bearer`
mode requires `COGVAULT_BEARER_TOKEN` at least 32 bytes; an explicitly
configured `auth.oauth.audience` that disagrees with the advertised resource
(`<public-url><endpoint-path>`) is rejected. It then builds `httpauth.Config`
(wiring the JWKS cache and `OAuthValidator` in `oauth` mode), wraps the `http`
transport in `exactPathHandler` — mcp-go's `StreamableHTTPServer.ServeHTTP`
ignores the path entirely when used as a bare `http.Handler` via
`server.NewStreamableHTTPServer`, so this restores exact-path matching and
404s elsewhere — and mounts everything through `httpauth.Mount` (§2.10). The
`sse` transport keeps mcp-go's default `/sse` and `/message` paths —
`--endpoint-path` deliberately does not apply to it — and points its message
endpoint at `--public-url` when set, `http://<addr>` otherwise. `newHTTPServer`
sets `ReadHeaderTimeout=10s` and `IdleTimeout=2m` on the `*http.Server` for
both network transports — the httpauth stream deadline only starts once a
handler runs, after headers are parsed, so nothing else bounds a
slowloris-style client dribbling headers at this public endpoint;
`WriteTimeout` is deliberately left unset, since it would apply to the whole
connection and cut off long-lived SSE/Streamable HTTP event streams — the
per-request bound those need instead lives in the httpauth middleware's
socket-level read/write deadline (§2.10). `validatePublicURL` also rejects
userinfo (`user:pass@host`) in `--public-url`, alongside the query/fragment/
trailing-slash checks, since it would otherwise leak into the advertised
resource, the token `aud`, and the `WWW-Authenticate` challenge.

### 2.10 httpauth (new)

```go
type Config struct {
    Mode             string   // "none" | "bearer" | "oauth"
    BearerToken      string
    PublicURL        string
    EndpointPath     string
    MaxBodyBytes     int64
    MaxStreamSeconds int
    Validator        TokenValidator  // non-nil only in "oauth" mode
    Issuer           string
    RequiredScopes   []string
}
func Middleware(cfg Config) func(http.Handler) http.Handler
func Mount(cfg Config, mcp http.Handler) http.Handler
func MetadataHandler(cfg Config) http.Handler
type TokenValidator interface {
    Validate(ctx context.Context, token string) (expiresAt time.Time, err error)
}
func NewOAuthValidator(issuer, audience string, requiredScopes []string, keys *JWKSCache) *OAuthValidator
func NewJWKSCache(issuer string, ttl time.Duration, client *http.Client) *JWKSCache
```

**Responsibility**: the entire HTTP access boundary for the `sse` and `http`
transports (see Resource server, CONCEPTS.md). `Middleware` runs, in order,
for every request: the `MaxBodyBytes` cap (`413` over it), the `Origin` check
(`originAllowed` — absent header passes, a loopback origin passes on any
port/scheme, otherwise scheme+host+port must match `PublicURL` exactly; `403`
on mismatch), then the mode-specific credential check (`none` skips it;
`bearer` does a length-then-`subtle.ConstantTimeCompare` match against
`BearerToken`; `oauth` requires a bearer-format `Authorization` header and
delegates to `Validator.Validate`, mapping `ErrInsufficientScope` to `403`
and everything else to `401`). On success it derives a stream deadline
(`MaxStreamSeconds` from now, tightened to the token's `exp` when the
validator reports one) — the Stream lifetime bound (CONCEPTS.md). A deadline
that has already elapsed (an `exp` inside the JWT parser's 60s leeway but
still in the past) is rejected as `401 invalid_token` rather than handed to
the handler dead. Otherwise `Middleware` wraps the request context with the
deadline **and** sets it as a socket-level read/write deadline via
`http.NewResponseController` — the context alone bounds only code that
re-checks `ctx.Done()`, not a goroutine already blocked in `r.Body.Read` or a
`Write` stalled on a full send buffer; a `ResponseWriter` that doesn't
support socket deadlines (`http.ErrNotSupported`) degrades to the
context-only bound instead of failing the request. `Middleware` and `Mount`
both panic on an unusable `Config` (bad `Mode`, empty `BearerToken` in
`bearer` mode, nil `Validator` in `oauth` mode) so a misconfiguration fails
at server construction, once, rather than per request.

`Mount` additionally serves the Protected Resource Metadata document
(CONCEPTS.md) in `oauth` mode, unauthenticated, at exactly two paths matched
by string equality — `/.well-known/oauth-protected-resource` and that path
suffixed with `cfg.EndpointPath` (Claude probes the suffixed form first) —
never a prefix match, which would let any path merely starting with the
well-known prefix bypass `Middleware`. Every other path, in every mode,
routes through `Middleware` first. The document carries a hardcoded
`resource_name: "cogvault"` (RFC 9728 §2, RECOMMENDED) alongside `resource`,
so a consent screen has a human-readable name instead of falling back to the
raw resource URL.

`oauth.go`'s `OAuthValidator` parses and verifies JWTs with
`github.com/golang-jwt/jwt/v5`, restricted to `RS256`/`ES256`, with `exp`
mandatory (`jwt.WithExpirationRequired`), `iss`/`aud` pinned at construction,
and 60s leeway. It additionally requires every scope in `requiredScopes` to
appear in the token's `scope` claim (space-delimited string or JSON array,
per differing identity-provider conventions), returning
`ErrInsufficientScope` when one is missing.

`jwks.go`'s `JWKSCache` resolves signing keys by `kid` via OIDC discovery
(`<issuer>/.well-known/openid-configuration` → `jwks_uri`), enforcing
`https` on both the issuer and the discovered `jwks_uri`, comparing the
discovery document's `issuer` field against the configured issuer and
rejecting a mismatch (OIDC Discovery 1.0 §4.3), refusing to follow redirects
(a compromised issuer 302'ing from `https` to `http` must not be followed
silently), and capping both response bodies at 1MiB. Concurrent misses for
the same `kid` collapse onto one in-flight fetch; an unknown `kid` on an
otherwise-fresh cache forces at most one refetch per
`minForcedRefetchInterval` (60s), so a stream of unknown-`kid` tokens cannot
drive unbounded outbound requests to the issuer. The in-flight fetch's
`c.fetching`/channel-close cleanup runs in a `defer` registered immediately
after the channel is created, so a future panic inside the fetch can't leave
`c.fetching` permanently non-nil and every later `KeyFor` call waiting
forever on a channel that never closes.

No rejection path in this package logs a credential, a bearer token, or a raw
JWT — `logRejection` records only a reason class and remote address.

---

## 3. Data flow

### 3.1 Ingest (Phase 1)

```
sources[].path ──scan──▶ stability gate ──▶ content hash ──new/model gate?──▶ llm.Adapter.Digest(file, schema)
                                                                            │ (claude --print subprocess)
                                                                            ▼
                                                       parse final result + authoritative streams
                                                          │ classify refusal before message choice
                                                          │ apply diagnostic safety + 2,000-rune bound
                                                          ├──failure──▶ attempt ledger: failed/refused + report
                                                          ▼
                                                   validated markdown source page
                                                          │
                                  storage.Write ──▶ index.Add ──▶ ledger: success
                                  (storage/index failure ──▶ attempt ledger: failed + report)
                                  (ledger persistence failure ──▶ report/log only; run continues)
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
  → mcp.NewServer(wiki root) → transport switch:
       stdio      → ServeStdio (blocking)
       sse | http → buildServeHandler (startup guards, httpauth.Config, exact-path
                     wrapper for http) → httpauth.Mount → http.Server.ListenAndServe (blocking)
  → cleanup
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
| `index/index.go` | interface + Result + FileMeta + NormalizeCategory |
| `index/sqlite.go` | FTS5, file_meta (+size/mtime/category), user_version=3, stat-gated CheckConsistency, busy_timeout |
| `llm/llm.go` | Adapter interface, DigestRequest/Result, ErrTransient, ErrRefused |
| `llm/claudecode.go` | `claude --print` backend; final-event parsing; refusal classification; safe diagnostic selection, canonicalization, and bound |
| `ingest/ingest.go` | Runner: scan, hash/model gate, digest, classify, validate, write, index, ledger, lock, ctx |
| `ingest/ledger.go` | `ingest_ledger` DDL + transitions; model-gated refused rows; own DB connection |
| `ingest/report.go` | Report struct + String(); per-file normalized diagnostic |
| `mcp/server.go` | MCP server, instructions, tool registration + annotations |
| `mcp/tools.go` | seven tools, mapError, listWithMeta (no scope), gitAutoCommit |
| `httpauth/auth.go` | Config, Middleware, Mount, credential checks, resource bounds |
| `httpauth/metadata.go` | Protected Resource Metadata handler |
| `httpauth/oauth.go` | OAuthValidator (JWT validation via golang-jwt/jwt/v5) |
| `httpauth/jwks.go` | JWKSCache (OIDC discovery + JWKS fetch, key decode) |
| `cmd/cogvault/*` | cobra CLI: `--config`, init/search/serve/ingest |
| `Makefile` | build/install (with adhoc codesign at destination), test, clean |
| `schema/schema.go` + `default_schema.md` | `go:embed` default schema |

---

## 6. Concurrency

```
Storage.Read/Stat  — no lock
Storage.Write/Move — Storage.mu
Index.Search/GetMeta — Index.mu.RLock (WAL read)
Index.Add/Remove   — Index.mu.Lock
CheckConsistency   — ccMu.Lock (serialize) + mu (read then apply)
Ingest run         — flock on ingest.lock (single instance, cross-process)
DB (all openers)   — busy_timeout=5000 per connection (contention → wait)
```

Storage.mu ↔ Index.mu never held together; no deadlock. Cross-process safety
(scheduled ingest vs live serve) rests on the ingest flock + WAL + busy_timeout.
Archive moves share the same storage mutex as writes, so sweep mutations stay
serialized with other wiki writes.

---

## 7. Test design

| Target | Method |
|------|------|
| config | YAML → Load → validate (absolute/overlap/expansion) |
| storage/fs | `t.TempDir()` fixtures; Stat; Move; whole-root write; security |
| adapter | fixtures/obsidian, edge |
| index/sqlite | temp DB; user_version recreation; stat-gate Read-count |
| llm | fake `claude` in `testdata/bin` (argv/stdin/mode) |
| ingest | mock `llm.Adapter` + real temp dirs; ledger transitions; lock; ctx |
| mcp | mcp-go test client; schema has no scope; tool annotation coverage |
| httpauth | `httptest` stub JWKS server + locally generated RSA/EC keys; auth-mode, challenge-shape, and body/Origin/stream-bound unit tests |
| cmd | in-process cobra; `--config` temp files; ingest via fake `claude` on PATH; `--transport http/sse` startup guards |
| integration (U9) | backlog/incremental/contention e2e |
| race | `go test -race ./...` |

---

## 8. Implementation order (v2 Phase 1)

```
U1: O1 spike — headless PDF digestion verification (research note)
U2: config v2 (single mode, explicit path, sources, ~ expansion)
U3: storage — root is the wiki, Stat and Move added
U4: index — user_version=2, size+mtime stat-gate, scope removal, busy_timeout
U5: llm — adapter interface + claudecode backend + error classes
U6: ingest — pipeline, ledger, lock, report
U7: cmd — --config model, ingest command, scope-flag removal
U8: launchd template + canonical docs (this document set)
U9: end-to-end integration tests
```
