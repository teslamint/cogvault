---
schema: plan/v1
title: cogvault v2 Phase 1 — single-mode refactor + LLM ingest pipeline
type: feat
status: draft
date: 2026-07-22
execution: code
origin: docs/specs/2026-07-22-refound-capture-pipeline-design.md
---

# cogvault v2 Phase 1 Plan

## Goal

Implement the approved v2 refounding spec Phase 1: remove the vault concept (single mode: absolute `wiki_dir` root + read-only `sources`), and build `cogvault ingest` — a batch pipeline that digests PDFs from source directories into wiki pages via a Claude Code CLI subprocess, with a hash-keyed ledger, launchd automation, and updated canonical docs.

## Architecture notes

- **Boundary contract (spec MAJOR-1)**: `internal/storage` and all MCP tools operate only on `wiki_dir`. The ingest pipeline reads source files directly (plain `os` calls: `Lstat` to refuse symlinks, size cap 32MB) and never through storage. Sources are never addressable via MCP paths.
- **Single mode**: `FSStorage.root` becomes the absolute `wiki_dir`. The `Write` wiki-prefix check (`fs.go:60-64`) is deleted — the whole root is writable except `_schema.md`. `Config.SchemaPath()` returns `"_schema.md"` (root-relative). `resolvePath` (relative-only, `..` and symlink rejection) is unchanged.
- **Config**: `Load` takes an explicit config file path (no more vault-root discovery). New shape: `wiki_dir` + `db_path` absolute after leading-`~/` expansion; `sources: [{path, types}]`; `llm: {backend}`; existing `exclude`, `exclude_read`, `adapter`, `consistency_interval` keep their semantics relative to the wiki root. Validation: absolute-after-expansion required for `wiki_dir`/`db_path`/`sources[].path`; overlap rejection (source vs `wiki_dir` in either direction, equality, `db_path` inside `wiki_dir`). This inverts the v1 rule "absolute path not allowed" — v1 validation for relative fields (`exclude`, etc.) is kept.
- **DB versioning**: replace the `mod_time`-column sniffing migration (`sqlite.go:80-124`) with `PRAGMA user_version`. v2 sets `user_version=2` on create; a DB with tables but version < 2 is dropped and recreated. This unblocks re-adding size/mtime columns to `file_meta` (design-review carry-forward note 2). v2 uses a fresh DB at a new absolute `db_path`, so no data migration exists.
- **Consistency-check cost gate (spec, iCloud)**: `file_meta` gains `size INTEGER` and `mtime TEXT`. `CheckConsistency` stats each scanned file first and re-reads/re-hashes content only when size or mtime differ from the stored row. Dataless/eviction read errors stay per-file warnings (existing `errs` path). Storage gains a `Stat(path) (size int64, mtime time.Time, err error)` method for this.
- **Scope removal**: `Search(query, limit, scope)` → `Search(query, limit)`; `appendScopeFilter` deleted; MCP `wiki_search` tool and `cogvault search` lose the scope parameter/flag.
- **Multi-process**: every `sql.Open` connection sets `PRAGMA busy_timeout=5000`. Ingest additionally takes an exclusive `flock` on `<db_dir>/ingest.lock`; a second ingest run exits immediately with a clear message.
- **LLM adapter**: `internal/llm` defines the interface and error classification (transient = quota/rate-limit/timeout/subprocess-transport; permanent = malformed output, schema-invalid page). The `claudecode` backend runs `claude --print` with the prompt on stdin; exact argv/flags come from the U1 spike. A future local backend implements the same interface.
- **Validate-then-write**: the generated page's frontmatter is parsed from bytes in memory before `storage.Write`; an unparsable page is a permanent failure and nothing lands in the synced wiki folder.
- **Page identity**: `sources/<slug>.md` where slug derives from the source file's base name; if the ledger maps that page to a different source path, the page becomes `sources/<slug>-<hash8>.md` (first 8 hex of sha256 of the absolute source path). Re-digestion of the same source path overwrites its page and marks the prior ledger row `superseded`.
- **Ledger ownership**: `internal/ingest` owns the `ingest_ledger` table through its own DB connection to the same `db_path` (WAL + busy_timeout make the second connection safe). The index package never touches the ledger.
- **Known Pattern** (docs/decisions): error wrapping `fmt.Errorf("pkg.Fn %s: %w", path, err)` with sentinels in `internal/errors` (0007); config validates string safety, storage/runtime validates filesystem state (0001) — source-dir existence is checked at ingest runtime, not config load; single write mutex in storage retained (0006).

## Assumption Recheck

Origin spec retains five live assumptions; all commands rerun at 2026-07-22T12:38:36Z.

| Approved claim | Fresh evidence | Outcome |
|---|---|---|
| `~/Downloads/_Articles`: 66 files (65 pdf, 1 webp), 159MB | rerun: 66 files; 65 pdf, 1 webp; 159M | match |
| Claude CLI installed, `~/.local/bin/claude` 2.1.217 | rerun: same path, same version | match |
| `mcp-go v0.47.0`, `modernc.org/sqlite v1.48.1` pinned | rerun of `grep go.mod`: same | match |
| Current config/storage cannot run standalone | re-read at planning time: `config.go:138-140` rejects absolute paths; `fs.go:60-64` wiki-prefix write check; `serve.go:29` startup force-check; `sqlite.go:368` full-root scan | match |
| Headless PDF reading works (`claude --print`) | not runnable at planning time | unavailable — spec assigns verification to the first implementation unit (U1 spike); carried under Open unknowns |

## File structure

- `internal/config/config.go` (+test) — v2 shape, `~` expansion, overlap validation, explicit-path Load/Save. Modified.
- `internal/storage/fs.go`, `storage.go` (+tests) — root-is-wiki, `Stat`, write-boundary change. Modified.
- `internal/index/sqlite.go`, `index.go` (+tests) — user_version, size/mtime gate, scope removal, busy_timeout. Modified.
- `internal/llm/llm.go`, `internal/llm/claudecode.go` (+tests, `testdata/bin/claude` fake) — new package.
- `internal/ingest/ingest.go`, `ledger.go`, `report.go` (+tests) — new package.
- `internal/mcp/server.go`, `tools.go` (+tests) — scope removal, wording (vault→wiki root). Modified.
- `cmd/cogvault/main.go`, `wire.go`, `init.go`, `serve.go`, `search.go`, new `ingest.go` (+tests) — `--config` model, ingest command. Modified/created.
- `deploy/com.teslamint.cogvault.ingest.plist` — launchd template. Created.
- `README.md`, `SPEC.md`, `DESIGN.md`, `CLAUDE.md`, `docs/decisions/0021-v2-refounding.md`, `docs/research/o1-headless-pdf-verification.md` — canonical docs. Modified/created.

## Scenario coverage map

| S-ID | Unit chain | Scenario evidence |
|---|---|---|
| S1 backlog digestion | U2→U3→U4→U5→U6→U7 | U9 integration "backlog run" (Covers S1); manual validation run over the real 66-file corpus (spec SC1, post-merge) |
| S2 zero-touch inflow | U2→U3→U4→U5→U6→U7→U8 | U9 integration "incremental second run" (Covers S2, in-process half); launchd-context digestion evidence from U1 spike + spec SC2 manual validation (scheduling itself is not CI-testable) |
| S3 re-find | U4→U6→U7 | U9 integration "search finds digested page" (Covers S3) |
| S4 phone viewing | U2 (absolute `wiki_dir` support) | U2 unit test accepts an absolute synced-folder path; the viewing itself is an observable manual check during the spec's 1-week validation (no code surface beyond path support) |
| S5 URL capture | later phase — explicitly out of Phase 1 scope per spec Scope Out | none required |
| S6 periodic digest | later phase — explicitly out of Phase 1 scope per spec Scope Out | none required |

## Implementation Units

## U1: O1 spike — verify headless PDF digestion (interactive + launchd)
Execution note: skip-test-first (evidence spike; no production code)
Files:
  Create: docs/research/o1-headless-pdf-verification.md
Interfaces:
  Consumes: `claude` CLI 2.1.217 at /Users/teslamint/.local/bin/claude
  Produces: verified argv template for the claudecode backend (flags, output format, permission mode), and a launchd-context verdict
Steps:
  1. Pick one small PDF from `~/Downloads/_Articles` (do not commit it; reference by hash only).
  2. Interactive check: run `claude --print` variants that ask the model to read the PDF path and emit a markdown page (try `--output-format json`, tool allowlist flags, prompt on stdin). Record the exact working argv and output shape.
  3. launchd check: wrap the working command in a one-shot `launchctl submit`/temporary plist, run it, and record whether TCC (~/Downloads read), PATH, and CLI auth hold outside an interactive shell; record required plist env (absolute paths, PATH).
  4. If no variant can read the PDF: record the negative result and the pdftotext fallback decision (spec Scope Out conditional); the U5 backend then shells `pdftotext` first and sends text on stdin.
  5. Write findings to docs/research/o1-headless-pdf-verification.md (working argv, env requirements, failure modes, fallback verdict); store raw transcripts under .release-loop/evidence/U1/.
  6. Commit: "docs(research): O1 spike — headless PDF digestion verification"
Acceptance: the research note contains a copy-pasteable argv that produced a markdown page from a PDF in both contexts, or a documented negative result activating the pdftotext fallback.

## U2: config v2 — single mode, explicit path, sources
Execution note: test-first
Files:
  Modify: internal/config/config.go
  Test: internal/config/config_test.go
Interfaces:
  Consumes: `gopkg.in/yaml.v3` (KnownFields(true) retained)
  Produces: `type SourceDir struct { Path string; Types []string }`; `type LLMConfig struct { Backend string }`; Config fields `WikiDir, DBPath string; Sources []SourceDir; LLM LLMConfig` (plus retained `Exclude, ExcludeRead, Adapter, ConsistencyInterval`); `func Load(configPath string) (*Config, error)`; `func Save(configPath string) error`; `func DefaultConfigPath() (string, error)` returning `~/.config/cogvault/config.yaml` expanded; `func (c *Config) SchemaPath() string` returning `"_schema.md"`
Test scenarios:
  happy: absolute `wiki_dir`/`db_path` and one source load and validate; leading `~/` in any of the three expands to $HOME (Covers S4)
  edge: `~` mid-path (`com~apple~CloudDocs`) stays literal; empty `sources` list is valid (ingest then has nothing to do); `types` entries normalized lowercase without dots
  error: relative `wiki_dir` after expansion rejected; source path equal to / containing / contained by `wiki_dir` rejected; `db_path` inside `wiki_dir` rejected; unknown YAML key still errors (KnownFields); `llm.backend` other than "claudecode" rejected
  integration: n/a — leaf unit
Steps:
  1. Write failing tests in internal/config/config_test.go for the happy/edge/error scenarios above (table-driven, matching the existing `field: message` error style from decision 0001).
  2. Run `go test ./internal/config/`; confirm failures are missing-field/validation mismatches.
  3. Rework config.go: new fields; `expandTilde(path)` applied to `wiki_dir`, `db_path`, `sources[].path`; `validate()` requires `filepath.IsAbs` on those three after expansion and keeps v1 relative-path rules for `exclude`/`exclude_read`; add overlap checks using the existing `hasPathPrefix` idiom; `Load`/`Save` take the config file path directly; delete the vault-root `configFileName` join.
  4. Run `go test -race ./internal/config/`; confirm pass.
  5. Commit: "feat(config): v2 single-mode config — absolute roots, sources, ~ expansion"
Acceptance: `go test -race ./internal/config/` passes; a config equal to the spec's Interface example (paths adjusted) loads without error.

## U3: storage — root is the wiki
Execution note: test-first
Files:
  Modify: internal/storage/fs.go, internal/storage/storage.go
  Test: internal/storage/fs_test.go
Interfaces:
  Consumes: `config.Config` from U2 (`SchemaPath()` = `"_schema.md"`)
  Produces: `Storage` interface gains `Stat(path string) (size int64, mtime time.Time, err error)`; `NewFSStorage(root string, cfg *config.Config)` where root is the absolute `wiki_dir`; `Write` permits any root-relative path except `_schema.md`
Test scenarios:
  happy: write `sources/page.md` at root level succeeds (no `_wiki/` prefix needed); Stat returns size and mtime for an existing file
  edge: write to nested new directories still auto-creates parents; Stat on excluded-read path returns ErrPermission
  error: write to `_schema.md` returns ErrPermission; absolute path and `..` still return ErrTraversal; symlink component still returns ErrSymlink
  integration: n/a — leaf unit
Steps:
  1. Update tests: replace vault-rooted fixtures (`_wiki/...` write expectations) with wiki-rooted ones; add Stat tests; keep all resolvePath security tests unchanged.
  2. Run `go test ./internal/storage/`; confirm the new expectations fail against v1 behavior.
  3. In fs.go: delete the wiki-prefix check block in `Write` (lines using `cfg.WikiDir` prefix); keep the `_schema.md` guard via `cfg.SchemaPath()`; implement `Stat` using `resolvePath` + `os.Stat` with the existing error-mapping idiom (ErrNotFound/ErrPermission).
  4. Run `go test -race ./internal/storage/`; confirm pass.
  5. Commit: "feat(storage): single-root mode — whole wiki root writable, Stat added"
Acceptance: `go test -race ./internal/storage/` passes; grep confirms no `WikiDir`-prefix logic remains in fs.go.

## U4: index — user_version, size+mtime gate, scope removal, busy_timeout
Execution note: test-first
Files:
  Modify: internal/index/sqlite.go, internal/index/index.go
  Test: internal/index/sqlite_test.go
Interfaces:
  Consumes: `storage.Storage.Stat` from U3; `adapter.Adapter.Scan/Parse` (unchanged)
  Produces: `Search(query string, limit int) ([]Result, error)` (scope dropped); `file_meta` columns `path, title, type, content_hash, size, mtime, indexed_at`; `PRAGMA user_version=2`; every connection opened with `busy_timeout=5000`
Test scenarios:
  happy: Add/Search round-trip unchanged for Korean trigram queries; CheckConsistency skips re-hash when size+mtime match stored row (assert via a storage mock counting Read calls)
  edge: a DB file with old tables and user_version<2 is dropped and recreated with the v2 schema; touching mtime without content change updates the stored mtime but does not change content_hash
  error: storage.Read failing for one file (simulated dataless) surfaces as a joined per-file error while other files still index (existing errs contract)
  integration: n/a — consumed by U6/U7 chains
Steps:
  1. Update sqlite_test.go: rewrite scope-filter tests as scope-free `Search(query, limit)`; add user_version recreation test (hand-create a v1-shaped DB in a temp file first); add Read-call-count gate test using the existing mock storage pattern.
  2. Run `go test ./internal/index/`; confirm failures.
  3. In sqlite.go: delete `appendScopeFilter` and the scope parameter through `Search/searchFTS/searchLIKE`; replace the `mod_time` sniffing block (initSchema) with `PRAGMA user_version` check-drop-recreate-set; add `size, mtime` to `file_meta` DDL and `addTx` (values via a new meta side-channel: `addTx` gains size/mtime args threaded from CheckConsistency's Stat, with zero values from direct `Add`); in `CheckConsistency`'s scan callback, call `store.Stat(path)` first and only `store.Read` + rehash when size/mtime differ; set `busy_timeout` in `initSchema` next to WAL.
  4. Run `go test -race ./internal/index/`; confirm pass.
  5. Commit: "feat(index): v2 schema versioning, stat-gated consistency, scope removal"
Acceptance: `go test -race ./internal/index/` passes; `grep -n "scope" internal/index/*.go` returns nothing.

## U5: internal/llm — adapter interface + claudecode backend
Execution note: test-first
Files:
  Create: internal/llm/llm.go, internal/llm/claudecode.go, internal/llm/claudecode_test.go, internal/llm/testdata/bin/claude
Interfaces:
  Consumes: U1 spike's verified argv template; `os/exec` with `context.Context`
  Produces: `type DigestRequest struct { SourcePath string; SchemaText string; PageSlug string }`; `type DigestResult struct { PageContent string }`; `type Adapter interface { Digest(ctx context.Context, req DigestRequest) (*DigestResult, error); Name() string }`; sentinel `ErrTransient` (wrapped by quota/rate-limit/timeout/transport failures — everything else is permanent); `func NewClaudeCode(binPath string) *ClaudeCode` with the 5-minute timeout constant applied per call
Test scenarios:
  happy: fake `claude` script echoes canned JSON; Digest returns the markdown page content; argv and stdin recorded by the fake match the U1 template
  edge: fake sleeps past a short test-injected timeout → error wraps ErrTransient; fake emits rate-limit-shaped stderr + nonzero exit → ErrTransient
  error: fake emits malformed JSON → permanent error (not ErrTransient); fake missing from PATH → transparent exec error wrapping ErrTransient (transport class)
  integration: n/a — consumed by U6
Steps:
  1. Write internal/llm/testdata/bin/claude (shell script: records argv to a file, reads stdin, emits output per env-var-selected mode: ok/timeout/ratelimit/garbage).
  2. Write failing tests claudecode_test.go covering the four scenario rows, overriding the timeout constant via an unexported field for the timeout case.
  3. Run `go test ./internal/llm/`; confirm compile/behavior failures.
  4. Implement llm.go (types, ErrTransient) and claudecode.go: build argv from the U1 template, prompt (schema text + instructions + source path) on stdin via `cmd.StdinPipe`, capture stdout/stderr, classify errors; wrap with `fmt.Errorf("llm.Digest %s: %w", req.SourcePath, err)`.
  5. Run `go test -race ./internal/llm/`; confirm pass.
  6. Commit: "feat(llm): adapter interface + claudecode backend with error classes"
Acceptance: `go test -race ./internal/llm/` passes with the fake CLI; no real `claude` invocation occurs in tests.

## U6: internal/ingest — pipeline, ledger, lock, report
Execution note: test-first
Files:
  Create: internal/ingest/ingest.go, internal/ingest/ledger.go, internal/ingest/report.go, internal/ingest/ingest_test.go, internal/ingest/ledger_test.go
Interfaces:
  Consumes: `llm.Adapter` (U5), `storage.Storage` (U3), `index.Index.Add` (U4), `config.Config` (U2), `adrg/frontmatter` (existing dependency) for validate-then-write
  Produces: `type Runner struct` with `func New(cfg *config.Config, store storage.Storage, idx index.Index, llmAdapter llm.Adapter, dbPath string) (*Runner, error)`; `func (r *Runner) Run(ctx context.Context, opts RunOptions) (*Report, error)` where `RunOptions struct { DryRun bool; Limit int; Origin string }` (`Origin` ∈ "scheduled"|"interactive"); `ingest_ledger` DDL `(source_path TEXT, content_hash TEXT, source_dir TEXT, digested_at TEXT, wiki_page TEXT, status TEXT, attempts INTEGER, last_error TEXT, run_origin TEXT, PRIMARY KEY (source_path, content_hash))`; lockfile `<dir(db_path)>/ingest.lock` via `golang.org/x/sys/unix.Flock` (new dependency, pinned)
Test scenarios:
  happy: two fixture files in a temp source dir → mock LLM digests → pages exist under `sources/`, index rows exist, ledger rows `success` with the run's origin; report counts match
  edge: second Run with unchanged files digests nothing (hash dedup); file modified within the 2-minute settle window is deferred (test injects a clock/window); same path re-digested after content change → old row `superseded`, page overwritten; slug collision from a different source path → `-<hash8>` suffix page; oversized and type-excluded files appear in the report but not the ledger; `--limit 1` processes exactly one file; dry-run writes nothing anywhere
  error: mock LLM returns ErrTransient → row `failed`, attempts stays 0; permanent error → attempts increments, and at 3 the file is skipped in later runs; generated page with unparsable frontmatter → permanent failure, no file written to the wiki root; a second Runner holding the lock → Run returns a distinct already-running error immediately
  integration: n/a — walked end-to-end in U9
Steps:
  1. Write failing ledger_test.go (DDL, upsert transitions, supersede, attempts logic) and ingest_test.go (pipeline scenarios above with mock llm.Adapter and real temp dirs).
  2. Run `go test ./internal/ingest/`; confirm failures.
  3. Implement ledger.go (own `sql.Open` to dbPath with WAL + busy_timeout; transition helpers), ingest.go (walk `cfg.Sources` with `os.ReadDir` + `Lstat` symlink refusal + type filter + 32MB cap + settle window; sha256; ledger check; `llm.Digest`; frontmatter validate from bytes; page-name policy; `store.Write`; `idx.Add`; ledger finalize; honor ctx cancellation between files), report.go (counts + per-file lines).
  4. Run `go test -race ./internal/ingest/`; confirm pass.
  5. Commit: "feat(ingest): batch pipeline with hash ledger, lock, and error classes"
Acceptance: `go test -race ./internal/ingest/` passes; `go vet ./...` clean.

## U7: cmd — --config model, ingest command, scope-flag removal
Execution note: test-first
Files:
  Create: cmd/cogvault/ingest.go
  Modify: cmd/cogvault/main.go, cmd/cogvault/wire.go, cmd/cogvault/init.go, cmd/cogvault/serve.go, cmd/cogvault/search.go, internal/mcp/server.go, internal/mcp/tools.go
  Test: cmd/cogvault/cli_test.go, internal/mcp/server_test.go, internal/mcp/tools_test.go
Interfaces:
  Consumes: everything above; `ingest.Runner`
  Produces: root persistent flag `--config` (default from `config.DefaultConfigPath()`), `--vault` flag deleted; `cogvault ingest [--dry-run] [--limit N] [--scheduled]` (`--scheduled` sets run origin "scheduled", used only by the launchd plist); `bootstrap(configPath string)` wiring storage/index at `cfg.WikiDir`/`cfg.DBPath`; `cogvault init` creates the config file (if absent), wiki dir, `_schema.md`, and DB at their configured locations; `wiki_search` tool schema without scope; serve/search unchanged otherwise
Test scenarios:
  happy: `init` against a temp config creates config/wiki/schema/db at the configured absolute paths; `ingest --dry-run` prints the pending file list; `search` returns results from a digested fixture page
  edge: missing config file yields a clear error naming the attempted path; `init` is idempotent (second run no-ops config and schema)
  error: `ingest` while another holds the lock exits nonzero with the already-running message; MCP `wiki_search` request carrying a scope argument is rejected by schema (unknown parameter) — asserted in tools_test
  integration: serve+ingest concurrency smoke lives in U9
Steps:
  1. Update cli_test.go and mcp tests: replace `--vault` usage with `--config` + temp config files; delete scope-flag/scope-param test cases; add ingest-command cases with a mock-backed Runner via the fake `claude` binary from U5 testdata (PATH injection).
  2. Run `go test ./cmd/... ./internal/mcp/`; confirm failures.
  3. Rework wire.go (`resolveVaultRoot` → `resolveConfigPath`; `bootstrap(configPath)`), main.go (flag swap, add ingest cmd), init.go (config-path model; `config.Save(path)` then mkdir wiki root, WriteSchema, index init with Rebuild), search.go/serve.go (drop scope flag; pass config path), tools.go/server.go (remove scope param; reword "vault" descriptions to "wiki root"), ingest.go (flags → `ingest.RunOptions`, print report, nonzero exit only on run-level failure).
  4. Run `go test -race ./cmd/... ./internal/mcp/`; confirm pass.
  5. Commit: "feat(cmd): --config single-mode CLI with ingest command"
Acceptance: `go test -race ./...` passes; `cogvault --help` shows init/search/serve/ingest with `--config` and no `--vault`.

## U8: launchd template + canonical docs
Execution note: skip-test-first (docs-only unit; non-code template)
Files:
  Create: deploy/com.teslamint.cogvault.ingest.plist, docs/decisions/0021-v2-refounding.md
  Modify: README.md, SPEC.md, DESIGN.md, CLAUDE.md
Steps:
  1. Write the plist template: absolute `cogvault` binary path placeholder, `ProgramArguments` = `ingest --config <path> --scheduled`, `StartInterval` 3600, `EnvironmentVariables.PATH` including the claude CLI directory (values verified by U1's launchd check), stdout/stderr to `~/Library/Logs/cogvault/`.
  2. Write 0021: supersedes 0020 (extrapolation basis per spec Overview), vault-mode removal, scope-parameter removal, boundary contract, error classification, O1 spike result; list which prior decisions stay in force (0001, 0006, 0007 conventions) and which are superseded (0020).
  3. Rewrite README (v2 pitch, setup: init → edit config → backlog ingest → launchctl load, migration note: copy `_wiki` pages then `init`), update SPEC.md (tool contract: scope removed; config schema; ingest command; single mode) and DESIGN.md (package map with `internal/llm`, `internal/ingest`; data flow diagram from the spec) and CLAUDE.md (§0 pointer to the spec, §6 roadmap rewrite: v2 phases replace old v0.2/v0.3 numbering where superseded).
  4. Self-review against spec sections Scope In (docs bullet), Architecture, and decision 0003 (canonical locations); verify no doc still instructs `--vault` or scope.
  5. Commit: "docs: v2 canonical docs — 0021 supersedes 0020, launchd template"
Acceptance: `grep -rn "\-\-vault" README.md SPEC.md DESIGN.md` returns nothing; 0021 exists and names 0020 as superseded; plist passes `plutil -lint`.

## U9: end-to-end integration tests
Execution note: test-first
Files:
  Create: cmd/cogvault/ingest_integration_test.go
  Modify: cmd/cogvault/integration_test.go (if shared fixtures move), testdata/fixtures/ (small non-PDF text fixtures named `.pdf` for the fake CLI, plus expected page goldens)
Interfaces:
  Consumes: full binary wiring with the fake `claude` from U5 testdata via PATH; temp dirs for wiki root, source dir, db
Test scenarios:
  happy: backlog run over 3 fixture files → 3 pages, 3 index rows, 3 `success` ledger rows, report says 3/3 (Covers S1); `cogvault search` for a term in a digested page returns it (Covers S3)
  edge: add a 4th file, rerun → exactly 1 new digestion, prior 3 untouched, origin recorded from `--scheduled` flag (Covers S2); rerun with no changes → zero work
  error: fake CLI in ratelimit mode for one file → that file `failed` with attempts 0, other files succeed, exit code 0 (per-file failures don't fail the run)
  integration: concurrent smoke — `serve` process alive (stdio idle) while `ingest` runs; ingest completes without "database is locked" (busy_timeout effective)
Steps:
  1. Write failing integration tests using the existing cli_test harness pattern (cobra command execution in-process; fake claude via PATH env).
  2. Run `go test ./cmd/...`; confirm failures.
  3. Fix wiring gaps only (no new features) until green.
  4. Run the full suite `go test -race ./...`; confirm all packages pass.
  5. Commit: "test(e2e): ingest pipeline integration — backlog, incremental, contention"
Acceptance: `go test -race ./...` passes (spec SC5); the four scenario rows above all exist and pass.

## Mutation/failure-state matrix

Stateful ceremony: an ingest run (durable state: ledger rows, wiki page files, index rows, lockfile). Evidence fixtures under `.release-loop/evidence/U6/` (unit-level) and `.release-loop/evidence/U9/` (end-to-end). Worked-example contract: `skills/planning/references/stateful-ceremony-matrix-example.md`; deviation authority: `docs/solutions/workflow-issues/review-introduced-state-machine-deviation.md`.

| Transition | Pre-state | Action | Post-state | Unit / evidence owner |
|---|---|---|---|---|
| T1 digest-success | no ledger row for (path,hash) | digest → validate → page write → index add → ledger insert `success` | page + index row + `success` row | U6 / U6 tests |
| T2 digest-failure | no or `failed` row | digest or validate fails | `failed` row; attempts +1 only for permanent class; no page for validate-fail | U6 / U6 tests |
| T3 supersede | `success` row for (path, oldHash); file content changed | re-digest → overwrite page → old row `superseded`, new row `success` | one page, two rows (superseded+success) | U6 / U6 tests |
| T4 lock lifecycle | no lock | flock acquire → run → release on exit | lock absent; concurrent acquire fails fast | U6 / U6 + U9 tests |

Outcome classes (apply to each transition):
- **success**: as post-state above; asserted by U6 unit tests and U9 e2e.
- **forced failure**: injection boundary = mock `llm.Adapter` return values / fake CLI modes (isolated temp dirs, no real LLM). T1 forced fail at index-add step: page exists, ledger `failed` → covered by rerun class. T4 forced fail = second Runner instance in-test.
- **rerun**: all transitions are idempotent by (path,hash) key — a crash between page write and ledger insert re-digests the same hash and overwrites the same page (extra LLM cost, no corruption). Asserted by U6 "second Run" and U9 incremental tests.
- **rollback/compensation**: no rollback is implemented (spec: eventual consistency, best-effort). Compensation is the rerun path above; page-without-ledger and ledger-without-index states self-heal on the next run (re-digest / index CheckConsistency). Irreversible external effect = LLM quota spend on re-digest: accepted, bounded by `--limit`.
- **headless**: launchd run with `--scheduled` — same code path, origin column differs; verified by U1 launchd spike evidence + spec SC2 manual validation (launchctl-driven run produces a `scheduled` success row).
- **cancellation/abort**: ctx cancellation between files (SIGTERM from launchctl unload) — current file's transition either completes or its partial state is the rerun case; lock released via defer; asserted by a U6 test cancelling ctx after the first file.

## Deferred to Follow-Up Work

- Escalating repeated deterministic timeouts on the same (path,hash) to permanent after N runs (design-review carry-forward 1) — spec classifies timeouts transient; change would deviate from the approved spec. Revisit with 1-week validation data.
- `--source` filter and multi-source ergonomics — phone-capture phase (spec YAGNI decision).
- Deleting dead v1 code paths beyond what the refactor touches (e.g. `wiki_scan`/`wiki_parse` tool wording review for source semantics) — v2 keeps them operating on the wiki root; revisit when sources become MCP-relevant.
- Local LLM backend, pdftotext extraction layer (unless U1 activates it), watch mode, digest command, URL capture — later phases per spec.

## Open unknowns

Planning-time: none remaining — the single planning-time unknown (headless PDF viability) was converted by the approved spec into U1, the first implementation unit, with a defined fallback either way (recheck outcome: unavailable, spec-sanctioned).

Implementation-time:
- Exact `claude --print` argv/flags and JSON output shape (U1 resolves; U5 consumes).
- Exact digestion prompt wording (U5/U6; golden-tested via fake CLI, tuned during the real backlog run).
- Whether `x/sys/unix.Flock` or `os` O_EXCL lockfile suffices on the target mac (U6 picks during implementation; both satisfy the fail-fast contract).
- `addTx` size/mtime threading shape in U4 (side-channel args vs meta map extension) — either satisfies the stat-gate acceptance test.

## Handoff

Headless under release-loop: plan path recorded in `.release-loop/progress.md`; implementing proceeds unit-by-unit (U1 first) after the plan-approval gate.
