# U7 Report — cmd: --config single-mode CLI + ingest command, scope removal

## Status
DONE — repo builds again; `go build ./...`, `go vet ./...`, `go test -race ./...` all green.

## What was done
Reshaped `cmd/cogvault/*` and `internal/mcp/*` to the v2 single-mode contract (config-path model, no vault root, no scope). TDD where behavior is pinned: mcp mocks/tests adapted first, cmd harness rewritten from `--vault` to `--config`, then implementation.

### cmd/cogvault
- **main.go**: root `Short` → "personal knowledge pipeline: ingest sources, serve a searchable wiki over MCP". Deleted persistent `--vault`; added persistent `--config` (help: "path to config file (default ~/.config/cogvault/config.yaml)"). Registered `newIngestCmd()`.
- **wire.go**: `resolveVaultRoot` → `resolveConfigPath(cmd)` — returns `--config` value or `config.DefaultConfigPath()`, no filesystem stat (config.Load reports the missing-path error). `bootstrap(configPath)` → Load → newAdapter → `storage.NewFSStorage(cfg.WikiDir, cfg)` → `index.NewSQLiteIndex(cfg.WikiDir, cfg.DBPath, cfg)` (both absolute from config, no filepath.Join with a root).
- **init.go**: two-step config-path model. (1) stat configPath (record existed?), `MkdirAll(filepath.Dir(configPath))`, `config.Save(configPath)` (U2 finding: Save does not MkdirAll the parent — init does). (2) `config.Load`; if the file was freshly scaffolded and Load fails validation (default config has empty wiki_dir/db_path) → print `created <path>; edit wiki_dir/db_path/sources, then re-run cogvault init` and return nil (success). If the file pre-existed and Load fails → return the error. (3) valid config: `MkdirAll(cfg.WikiDir)`, `WriteSchema`, `MkdirAll(filepath.Dir(cfg.DBPath))`, NewSQLiteIndex + Rebuild (new DB) / CheckConsistency(force) (existing DB). Two-step flow documented in Short/Long.
- **serve.go**: resolveConfigPath+bootstrap; passes `cfg.WikiDir` as root to `cogmcp.NewServer`.
- **search.go**: deleted `--scope` flag; `idx.Search(query, limit)`.
- **ingest.go** (new): flags `--dry-run`, `--limit int`, `--scheduled`. Origin = "scheduled" if `--scheduled` else "interactive". `exec.LookPath("claude")` at runtime → `llm.NewClaudeCode(binPath)` (missing → `claude CLI not found in PATH; install Claude Code or add it to PATH`). `ingest.New(cfg, store, idx, adapter, cfg.DBPath)`, `Run(cmd.Context(), ...)`, prints `report.String()` to stdout. `ErrAlreadyRunning` → `ingest already running (lock held)`; run-level error → nonzero exit; per-file failures inside a completed run → exit 0.

### internal/mcp
- **server.go**: `wikiSearchTool()` dropped the `scope` param. Reworded tool descriptions "vault" → "wiki root" (wiki_read/write/list/scan) and the default-schema instructions.
- **tools.go**: `handleWikiSearch` no longer reads `scope`; calls `idx.Search(query, limit)`.

## Tests
- **mcp**: `mockStorage` gained `Stat` (U3 interface); `mockIndex.Search(query, limit)` (U4); dropped the `scope` search arg from server_test/integration_test; deleted `TestIntegration_RealVault_ScopeFiltering` (scope concept removed); reframed the ingest-workflow "empty wiki search" step to assert no `_wiki/` result (single-root now indexes the whole root); retargeted the schema-write-guard test to `_schema.md` and deleted `TestIntegration_Security_WriteOutsideWikiDir` (whole root writable in single-root mode, per U3 precedent); added `TestWikiSearchToolSchemaHasNoScope` (mcp-go does not enforce `additionalProperties:false`, so the enforceable assertion is schema absence of `scope`, not runtime rejection).
- **cmd**: rewrote the harness — `testVault`/`writeConfigFile` helpers write a valid config with absolute non-overlapping `wiki_dir`/`db_path`; all init/search/serve tests moved to `--config`; deleted the scope-filtering test; `TestConfigMissingError` asserts the error names the attempted path; `TestInitFirstRunScaffoldsConfig` covers the fresh-config guidance flow; `TestInitPerFileErrorContinues` now changes file size (not just perms) so the U4 stat-gate does not skip the re-read. Added ingest tests: `TestIngestDryRunListsPending` (fake `claude` on PATH via `internal/llm/testdata/bin`, source aged past the 2m settle window), `TestIngestLockHeldFails` (hold the flock in-test → already-running), `TestIngestMissingClaudeBinary` (scrubbed PATH).

### `go test -race ./cmd/... ./internal/mcp/`
```
ok  	github.com/teslamint/cogvault/cmd/cogvault	1.855s
ok  	github.com/teslamint/cogvault/internal/mcp	2.695s
```

### `go test -race ./...`
```
ok  	github.com/teslamint/cogvault/cmd/cogvault
ok  	github.com/teslamint/cogvault/internal/adapter/markdown
ok  	github.com/teslamint/cogvault/internal/adapter/obsidian
ok  	github.com/teslamint/cogvault/internal/config
ok  	github.com/teslamint/cogvault/internal/errors
ok  	github.com/teslamint/cogvault/internal/index
ok  	github.com/teslamint/cogvault/internal/ingest
ok  	github.com/teslamint/cogvault/internal/llm
ok  	github.com/teslamint/cogvault/internal/mcp
ok  	github.com/teslamint/cogvault/internal/schema
ok  	github.com/teslamint/cogvault/internal/storage
```
`go build ./...` and `go vet ./...` clean.

## Deviations (with reasons)
- **mcp integration_test.go touched beyond the brief's named test files.** The brief listed server_test.go/tools_test.go for mcp, but integration_test.go carried scope-filtering and dual-space (write-outside-wiki / schema-at-`_wiki/_schema.md`) assertions that v2 single-root deliberately removes. Fixed/deleted them per the U3 precedent (which dropped the analogous storage tests) so `./...` passes. No production behavior invented.
- **scope "rejected by schema" (brief error scenario) → asserted as schema absence.** mcp-go v0.47.0 does not set `additionalProperties:false`, so a stray `scope` arg is silently ignored, not rejected. The enforceable, brief-aligned assertion is that `wiki_search`'s input schema no longer advertises `scope` (`TestWikiSearchToolSchemaHasNoScope`).
- **`TestInitPerFileErrorContinues` now mutates file size, not only permissions.** U4's stat-gate skips the Read when size+mtime are unchanged, so a chmod-only change produced zero per-file errors. Changing the size forces the re-read that then fails on the 000 perms — the honest post-gate way to exercise per-file tolerance.
- **root.go / root_test.go (`mcp.ResolveRoot`) left untouched** — no production caller remains, but it compiles standalone and is outside the brief's touch list; removing it would be scope creep.

## Final CLI help (`cogvault --help`)
```
personal knowledge pipeline: ingest sources, serve a searchable wiki over MCP

Usage:
  cogvault [command]

Available Commands:
  completion  Generate the autocompletion script for the specified shell
  help        Help about any command
  ingest      Digest configured source files into wiki pages
  init        Create the config file, then the wiki directory, schema, and database
  search      Full-text search across indexed wiki files
  serve       Start the MCP server in stdio mode

Flags:
      --config string   path to config file (default ~/.config/cogvault/config.yaml)
  -h, --help            help for cogvault

Use "cogvault [command] --help" for more information about a command.
```

## Post-review correction

The line above claiming `ingest.go` "prints `report.String()` to stdout" was false as originally
implemented: `newRootCmd` never called `cmd.SetOut`, so cobra's `cmd.Print*`/`cmd.Println` family
(used by `ingest.go`, `init.go`, `search.go`) fell back to `os.Stderr`, not `os.Stdout`. In
production this meant search results, the ingest report, and init guidance all landed on stderr,
and `launchd`'s empty `StandardOutPath` captured nothing. Fixed in review round 1
(`fix(cmd): U7 review round 1 — stdout routing, wiki_parse description`) by adding
`cmd.SetOut(os.Stdout)` in `newRootCmd`; `main()`'s top-level error printing to `os.Stderr` is
unchanged. Verified empirically: `cogvault init --config <path>` scaffold guidance now appears on
stdout with stderr empty, and `cogvault --help` output is on stdout.
