# 0015-step6-cmd-decisions

Status: accepted
Date: 2026-04-07

## Context

Step 6 introduced the Cobra CLI layer in `cmd/cogvault/` (init, search, serve) and wired it to the existing internal packages. During planning and three rounds of review, several decisions crystallized that are not captured in SPEC.md, DESIGN.md, or prior decision records.

## Decision

### D1: `FSStorage.WriteSchema()` as a concrete method, not an interface method

`storage.Write()` blocks writes to `_schema.md` (fs.go:65-67) to protect against agent-initiated overwrites at runtime. However, `init` must create the schema file during bootstrap.

Rather than bypassing storage entirely with `os.WriteFile()`, a concrete `WriteSchema(data []byte) error` method was added to `*FSStorage`. It reuses `resolvePath()` for symlink/traversal security but skips the schema write block and wiki-dir prefix check.

This method is NOT on the `Storage` interface — only `init` needs it via the concrete type. This keeps the runtime security contract intact while providing a controlled init-only escape hatch.

The method also validates that the target path is not a directory (IsDir check), returning an error if a directory occupies the schema path.

→ Background: Codex review identified that `os.WriteFile()` bypasses the entire storage trust boundary (symlink, traversal), not just the schema write block. The concrete method approach preserves all security checks except the two that are init-specific.

### D2: `bootstrap()` returns `*storage.FSStorage` (concrete), not `storage.Storage` (interface)

`bootstrap()` returns the concrete `*FSStorage` type so that `init` can call `WriteSchema()`. `search` and `serve` use it only through `Storage` interface methods, so the concrete type leaks only at the CLI composition root.

This was a deliberate trade-off: adding `WriteSchema` to the `Storage` interface would force mock implementations to include a method that only `init` uses. Returning the concrete type from the composition root is acceptable since `cmd/` is the application boundary, not a library.

### D3: CLI `search` runs the same bounded-staleness reconciliation as MCP

The CLI `search` command calls `idx.CheckConsistency(store, adpt, false)` before querying.

This keeps the user-visible search contract aligned with MCP `wiki_search`: search results are reconciled on demand, but interval-gated by `consistency_interval` instead of forcing a full rescan every time.

The project accepted the extra filesystem scan cost here in exchange for parity between the CLI and MCP read paths. A user who searches from the terminal after adding or modifying notes should see the same bounded-staleness behavior as a long-lived MCP session.

### D4: CLI consistency error policy mirrors MCP

`init`, `search`, and `serve` all apply the same error classification as MCP handlers (0013-D2) when they call `CheckConsistency`:

- `errors.Is(err, index.ErrConsistencySystemic)` → fatal error, command fails
- Other errors (per-file read/parse failures) → warning on stderr, command continues

This is tested at the CLI level by `TestInitPerFileErrorContinues` (chmod 000 file → per-file error → init succeeds with warning) and `TestInitSystemicErrorFails` (corrupted DB → systemic error → init fails). `serve` still lacks an equivalent CLI-level warning test because `ServeStdio` blocks.

### D5: `config.Save()` writes the current default config explicitly

`config.Save()` writes `DefaultConfig()` as YAML without a separate "minimal config" normalization step.

This means `init` creates a concrete baseline config file that reflects the defaults at initialization time. Future default changes do not retroactively affect vaults that already have `.cogvault.yaml`; users opt into new defaults by editing the file or reinitializing with a new config.

### D6: Path semantics — config values stay vault-relative

Config values (`WikiDir`, `DBPath`, `SchemaPath()`, `Exclude`, `ExcludeRead`) are always vault-relative. They are never converted to absolute paths before being passed to storage, adapter, or index.

Absolute path construction (`filepath.Join(vaultRoot, cfg.XXX)`) happens ONLY at CLI-level OS I/O call sites: `os.MkdirAll` for wiki directory, `os.Stat` for DB existence check, and `NewSQLiteIndex` for the DB path argument.

This preserves the storage prefix checks (wiki_dir enforcement), SchemaPath comparison, and adapter exclude matching, all of which rely on relative path semantics.

→ Background: Codex review caught that an earlier plan draft said "convert all config paths to absolute" which would break storage's `wiki_dir` prefix check, `SchemaPath()` comparison, and adapter exclude contract.

## Why

### Why concrete method instead of interface method

The `Storage` interface is implemented by `FSStorage` and potentially by test mocks. Adding `WriteSchema` to the interface forces all implementations to handle a concern that only exists during init bootstrap. Keeping it concrete follows the Interface Segregation Principle.

### Why bounded-staleness still runs in CLI search

Even though each CLI invocation opens a fresh SQLite connection, the on-disk index can still be stale relative to the vault contents. Running the same interval-gated reconciliation as MCP keeps CLI and MCP search semantics aligned and avoids a separate user mental model for "terminal search" versus "server search."

### Why Save writes explicit defaults

`init` is a bootstrap command, not a migration layer. Writing a complete baseline config makes the initialized vault self-describing and easy to inspect. The project accepts that future default changes are not automatically inherited by already-initialized vaults.
