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
mcp/, cmd/     ──▶ gitutil/
```

Unidirectional, no cycles. Two new packages in v2: `internal/llm` and
`internal/ingest`. `ingest` composes `storage` (wiki writes), `index` (Add),
`llm` (Digest), and `config`, and additionally reads source files directly.

`internal/gitutil` (0024) is a third leaf package alongside `errors/` and
`config/`: it imports nothing from cogvault, so both `internal/mcp`
(per-file auto-commits) and `cmd/cogvault` (the post-ingest whole-tree
snapshot) both depend on it without either depending on the other for it.
`cmd/cogvault` does import `internal/mcp` — the point is that sharing a
mechanism needs a common leaf, not a direction change. An earlier reading
treated "cmd depends on mcp, not the reverse" as a reason to duplicate the
commit logic, which instead produced two copies that drifted apart and would
have needed every
concurrency and signal fix applied twice.

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
those three absolute after expansion and rejects overlaps via
`adapter.HasPathPrefix` — the prefix/`..` path predicates live once in
`internal/adapter` (`HasPathPrefix`, `ContainsDotDot`) and config/storage
delegate to them; the former per-package copies were drift-prone duplicated
logic. Responsibility boundary unchanged (0001): config validates path-string
safety and policy; filesystem existence/permissions are enforced by
storage/runtime. Source directory existence is checked at ingest runtime, not
config load.

`GitConfig` (0024) adds `Git.AutoCommit string` (`"off"` default, `"write"`,
or `"write+ingest"`), validated against `ValidGitAutoCommitModes()` the same
way `Auth.Mode` is validated against `ValidAuthModes()`. `CommitsOnWrite()`
and `CommitsOnIngest()` are the two call-site predicates `internal/mcp` and
`cmd/cogvault` read instead of comparing the string directly.

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

`isGitControlled` (0024, third correction pass) hard-rejects every mutating
method — `Write`, `Delete`, and both ends of `Move` — for any path with a
`.git`, `.gitattributes`, or `.gitmodules` component at any depth, compared
with `strings.EqualFold`. This sits *outside* the configurable exclude lists
on purpose. It is not a visibility rule: `git add` executes the clean filter
`.gitattributes` names, with the command line taken from `.git/config`, so a
writable pair of those files turned the auto-commit path (§2.11) into
arbitrary command execution as the server process on the next ordinary
write. Reproduced end-to-end before the fix. An operator editing
`exclude`/`exclude_read` — which otherwise only affect search and listing —
must not be able to reopen that. The case-insensitive comparison is equally
load-bearing: APFS (macOS default, the primary platform here) resolves
`.GIT/config` to the real `.git/config`, so a case-sensitive check was a
complete bypass — also reproduced end-to-end, marker file and all. Matching
is uniform rather than filesystem-dependent so the boundary cannot vary by
host. `.gitignore` stays writable: it selects paths, it never names a
command to run.

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
  removed; the index holds wiki pages only. `matchQuery` quotes each
  whitespace-separated token individually and joins with implicit AND —
  whole-query quoting (the old `escapeMatch`) made every multi-word query a
  strict adjacent-phrase match and missed non-adjacent hits (F1/SC3). The
  LIKE fallback orders by path.
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
func NewClaudeCode(binPath, model string, opts ...Option) *ClaudeCode
func NewOllama(baseURL, model string, opts ...Option) *Ollama   // second backend
func WithTimeout(d time.Duration) Option                        // 0/negative => 5m default
```

**Responsibility**: define the digestion contract and two backends sharing the
`Option`/`WithTimeout` construction seam (timeout comes from
`llm.timeout_seconds`, default 5m). `claudecode`
runs `claude --print --output-format json --allowedTools "Read"` with the prompt
(schema text + instructions + absolute source path) on **stdin** (avoids ARG_MAX),
a per-call timeout, strips an optional leading/trailing ``` fence, and
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

`ollama` POSTs `/api/generate` (non-streaming) with the same prompt and
classifies like claudecode's spirit, not its letter: HTTP 408/429 and all 5xx
are `ErrTransient` (Ollama returns 500/503 while a model loads or under load —
burning the ledger's bounded attempts on those would permanently exhaust files
over self-healing blips), other 4xx are permanent client errors, transport
errors are transient, and the response is fence-stripped with the same
`stripFence` as claudecode so ```-wrapped model output is not a permanent
parse failure.

`isRefusalText` canonicalizes each candidate, then accepts only a candidate
beginning `policy refusal:`, or the anchored case-insensitive provider grammar
`^api error:\s*(?:refused\b|safeguards flagged\b|fable 5(?:'s|’s) safeguards flagged\b)`.
Generic `api_error`/`API Error:` messages, unknown provider-name safeguard
envelopes, quoted or negated phrases, embedded or suffix-only phrases, and
`connection refused` are transient, including on an exit-zero final API error.

Structured diagnostic persistence is stricter: the final event must have
`is_error == true`, must carry `api_error` or `error_during_execution`, and its
trimmed result must be non-empty, contain no CR/LF, and not begin with
frontmatter, a Markdown heading, or a code fence. After refusal classification, message
precedence is safe structured result, stderr, non-JSON-looking plain stdout,
then process error. Malformed, wrong-shaped, or final-result-less stdout that
begins with `{` or `[` is never reused as plain text. Recognized ANSI SGR/OSC is
removed, Unicode whitespace collapses to one ASCII space, and remaining Unicode
Control/Format (`Cf`) runes become `U+FFFD` before both classification and
display. Diagnostics over 2,000 runes retain 1,999 plus `…`.

**Capture bounds** (F16): stdout and stderr from the CLI subprocess are captured
through a `cappedWriter` that silently discards bytes past the cap and sets an
overflow flag. Defaults: stdout 4 MiB, stderr 1 MiB. If stdout overflows, Digest
returns a permanent error (not transient — a deterministic oversized output would
retry forever under the transient unlimited-retry rule, repeating the F6 failure
mode). The overflow check runs after refusal classification so a refusal envelope
on stderr still outranks the overflow. Stderr overflow requires no special
handling because the diagnostic pipeline already bounds to 2,000 runes.

A future local backend implements the same interface without touching `ingest`.

### 2.7 ingest (new)

```go
type Runner struct { /* cfg, store, idx, llm, dbPath, ledger, seams */ }
func New(cfg *config.Config, store storage.Storage, idx index.Index,
         llmAdapter llm.Adapter, dbPath string) (*Runner, error)
func (r *Runner) Run(ctx context.Context, opts RunOptions) (*Report, error)
func (r *Runner) Notify(report *Report)
func (r *Runner) Close() error
func AttentionRows(dbPath, model string) ([]AttentionRow, error)
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

`Run` adds a `NewAttention` entry when the current run first reaches an
exhausted or refused state. `Notify` formats those entries for best-effort
delivery after a successful scheduled run. The Darwin notifier passes the
title and body as `osascript` arguments. It uses `exec.CommandContext` with a
five-second timeout. Other platforms use a no-op notifier.

**Ledger** (`ledger.go`): owns its **own** `sql.Open("sqlite", dsn)` to `db_path`
with DSN pragmas `busy_timeout(5000)` + `journal_mode(WAL)` on **every** pooled
connection (not a one-shot `PRAGMA` exec). The `index` package never touches the
`ingest_ledger` table; WAL + busy_timeout make the second connection to the same
DB file safe. DDL and transitions (`lookup`, `supersedePrevSuccess`, `upsert`) per
SPEC §10.6.

`AttentionRows` opens only the ledger connection. It selects the latest row
for each source path with a fixed-width UTC nanosecond sort key. It returns
exhausted and refused rows for the current model. A missing database returns
an empty result without creating files.

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
index if it was indexed, then unconditionally calls `gitAutoCommit`, a thin
wrapper over `gitutil.Commit` (§2.11) that only chooses the pathspec and the
log wording; it best-effort `git add`s and `git commit`s the deletion when
`wiki_dir` is a git repository — failures are logged, not returned as a tool
error (§2.10 covers the authorization layer that gates this over the
network). "Unconditional" means the call always happens, not that it always
commits: `git add` on a path git never tracked (e.g. under the default
`git.auto_commit: off`, where `wiki_write` never committed it) fails with
no matching pathspec, so that delete produces no commit either.
`handleWikiWrite` calls the same `gitAutoCommit` after a successful write
only when `cfg.Git.CommitsOnWrite()` (default false) — this is the only
conditional caller; `wiki_delete`'s call is unconditional and predates 0024.
Instructions/`mapError`/write-then-index otherwise unchanged.

### 2.9 cmd/cogvault

Root persistent flag `--config` (default `config.DefaultConfigPath()`); the old
vault flag is deleted. `wire.go`: `resolveConfigPath(cmd)` + `bootstrap(configPath)` →
`config.Load` → adapter → `storage.NewFSStorage(cfg.WikiDir, cfg)` →
`index.NewSQLiteIndex(cfg.WikiDir, cfg.DBPath, cfg)`. `init.go` is the two-step
flow (SPEC §9.1). `ingest.go` (new): flags → `ingest.RunOptions`, `exec.LookPath`
for `claude` → `llm.NewClaudeCode`, prints `report.String()`, nonzero exit only on
run-level failure. When `cfg.Git.CommitsOnIngest()` (0024) and the run digested
at least one file, `postIngestGitCommit` runs after `postIngestEmbed`: one
`gitutil.Commit` (§2.11) doing `git add -A -- .` + `git commit -m "wiki:
ingest snapshot"` over the whole `cfg.WikiDir` tree, best-effort — failures
(including "nothing to commit", the expected outcome when a digest
reproduces identical content) log and never fail the command.
The `-- .` pathspec is load-bearing, not cosmetic: `git -C wikiDir add -A`
resolves against the enclosing repository's root, not `wikiDir`, whenever
`wikiDir` is a plain subdirectory of a larger repo rather than its own git
root — a bare `-A` would stage and commit dirty files anywhere in that
outer repo. `TestIngestGitCommit_NestedWikiDirDoesNotStageOutsideFiles`
(`cmd/cogvault/ingest_git_commit_test.go`) pins this: it fails against a
`-A`-without-pathspec regression. The lock, timeouts, and signal handling
are `gitutil`'s, shared with `internal/mcp`: a scheduled ingest and a live
`serve` commit into the same repository, so they must contend on one lock
rather than race.

`status.go` loads the config and calls `ingest.AttentionRows` directly. It does
not bootstrap storage, the index, or the ingest runner. Human output uses local
timestamps. JSON preserves the stored canonical UTC timestamp.

`serve.go`: `serve` takes `--transport` (`stdio` default, `sse`, or `http`),
`--addr` (default `localhost:8080`, `sse`/`http` only; `requireAddrHost`
rejects an empty host part — `:8080` binds every interface and would build
the malformed SSE base URL `http://:8080`), `--endpoint-path`
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

`sse`/`http` serve through `serveUntilSignal`: the TCP listener binds before
signal handling starts (bind failures surface immediately), and SIGINT/SIGTERM
trigger `srv.Shutdown` with a 10-second grace period so in-flight
SSE/Streamable HTTP streams drain instead of dying with the process.
`serveListener` is the listener-injected core tests drive; the signal path
wraps it via `signal.NotifyContext`.

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


### 2.11 gitutil (new, 0024)

```go
var CommitTimeout = 10 * time.Second
var TerminateGrace = 2 * time.Second

func Commit(ctx context.Context, repoDir string, pathspecs []string, message string) (Stage, error)
```

A leaf package (§1): imports nothing from cogvault, so `internal/mcp` and
`cmd/cogvault` share one commit mechanism without adding a reverse dependency
or a cycle. `cmd/cogvault` continues to depend on `internal/mcp`; both callers
also depend on `internal/gitutil`. They keep their own wording; `Commit`
reports the failing `Stage` (`StageLock`/`StageAdd`/`StageCommit`) so the log
line stays caller-specific, and never logs or fails the caller itself — the
best-effort contract is unchanged.

Three properties are load-bearing, each with a mutation-verified regression
test in `internal/gitutil/commit_test.go`:

- **One advisory `flock` per repository.** Git refuses concurrent index
  operations, so unsynchronized callers lose commits: measured, 8 concurrent
  commits produced 1 commit and 7 exit-128 failures, each of which the
  best-effort contract would have swallowed as a warning. `flock` excludes
  per *open file description*, so two goroutines in one process contend
  exactly as two processes do — verified by probe, not assumed; a first
  implementation carried a redundant in-process semaphore on the opposite
  belief. The lock file lives in an owner-only directory under
  `os.UserCacheDir()` keyed by a hash of the resolved repo path — never in a
  shared temp dir, where a deterministic name is squattable by another local
  user, and never inside `wiki_dir`, where the whole-tree ingest snapshot
  would commit it as wiki content. The path is walked from
  `os.UserCacheDir()` one component at a time with `Mkdirat`/`Openat`, each
  descriptor validated before the next is opened, and the lock file is opened
  with `Openat` against the final one: `O_NOFOLLOW` guards only the final
  component, so resolving the joined path would let a same-euid process swap
  an intermediate directory for a symlink and redirect the lock out of the
  validated tree. The anchor rejects group/world-*write* bits but tolerates
  read bits — `~/Library/Caches` is `0755` on macOS, and rejecting it would
  disable auto-commit on a normal install.
- **Bounded, non-blocking lock acquisition.** `flock`'s blocking mode ignores
  context, so a caller queued behind a wedged commit would hang forever —
  precisely the failure the timeout exists to prevent. The wait is a retry
  loop bounded by `CommitTimeout`.
- **`SIGTERM` with grace, never bare `SIGKILL` — for the subprocesses only.**
  `git add`/`git commit` hold `.git/index.lock` while running and release it
  from their own signal handler. Go's `CommandContext` default is `SIGKILL`,
  which git cannot trap: a timeout would strand the lock with no cleanup path
  in cogvault, breaking every later commit until an operator deleted it by
  hand. `cmd.Cancel` sends `SIGTERM` and `cmd.WaitDelay = TerminateGrace`
  bounds the wait before Go force-kills. This applies to the two git
  subprocesses and nothing else: the lock wait is an in-process retry loop
  with no process to signal, so it simply ends at its context deadline.

`add` and `commit` are bounded independently by `CommitTimeout` rather than
sharing one budget, so a slow (not wedged) add cannot starve commit. The three
10s deadline budgets bound one `Commit` to ~30s before subprocess grace;
including up to `TerminateGrace` for each of `add` and `commit`, the wall-clock
upper bound is ~34s (SPEC §8.8.1).

Each stage's deadline is derived through a `withTimeout` seam and dispatched
through a `runGit` seam, so `TestCommitGivesEachStageAnIndependentDeadline`
can substitute both, derive deadlines from a bookkeeping timestamp it
advances by 9s between stages, and read the budget `commit` actually
receives. Nothing intercepts time — the deadlines are still real, they simply
never fire, because the substituted runner returns immediately — so the
assertion is arithmetic on derived deadlines rather than elapsed measurement.
Those seams exist because the property is otherwise observable only as
elapsed wall-clock time, and the timing-based tests that measured it flaked
under load until they were replaced (0024, fourth correction pass).

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
| `ingest/attention.go` | Exported attention rows and missing-database guard |
| `ingest/ledger.go` | `ingest_ledger` DDL + transitions; latest attention query; own DB connection |
| `ingest/notify_*.go` | Platform notification delivery; five-second Darwin process bound |
| `ingest/report.go` | Report struct + String(); per-file diagnostic and new-attention items |
| `mcp/server.go` | MCP server, instructions, tool registration + annotations |
| `mcp/tools.go` | seven tools, mapError, listWithMeta (no scope), gitAutoCommit |
| `httpauth/auth.go` | Config, Middleware, Mount, credential checks, resource bounds |
| `httpauth/metadata.go` | Protected Resource Metadata handler |
| `httpauth/oauth.go` | OAuthValidator (JWT validation via golang-jwt/jwt/v5) |
| `httpauth/jwks.go` | JWKSCache (OIDC discovery + JWKS fetch, key decode) |
| `cmd/cogvault/*` | cobra CLI: `--config`, init/search/serve/ingest/status |
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
