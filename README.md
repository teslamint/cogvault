# cogvault

MCP tool server for building LLM-curated wikis in Obsidian vaults.

**Status:** MVP in progress — Step 4/9 complete

## MVP capabilities (planned)

- **6 MCP tools** — read, write, list, search, scan, parse
- **Passthrough mode** — agent orchestrates, engine provides tools only
- **Hybrid Obsidian integration** — wiki lives inside the vault in `_wiki/`
- **SQLite FTS5 full-text search** — trigram tokenizer, CJK-friendly
- **Path security** — traversal prevention, exclude patterns, `_schema.md` write protection
- **Single binary** — pure Go, no CGo

### Current state

Steps 1–4 complete: sentinel error types (`internal/errors`), YAML config loading with strict validation (`internal/config`), storage interface with fs security (`internal/storage`), adapter interface with obsidian/markdown parsers (`internal/adapter`), and index interface with SQLite FTS5 + consistency (`internal/index`).

## Planned CLI

These commands will be available after Step 6.

```text
go build -o cogvault ./cmd/cogvault
cogvault init --vault ~/my-vault
cogvault serve
```

## Target architecture

```mermaid
graph TD
  CLI["cmd/cogvault"] --> MCP["mcp/server+tools"]
  MCP --> Storage["storage/fs"]
  MCP --> Index["index/sqlite"]
  MCP --> Adapter["adapter/obsidian"]
  Index --> Adapter
```

## Development

Requires Go 1.26.1+.

```bash
go test -race ./...
```

### Roadmap

- [x] Step 1: errors + config
- [x] Step 2: storage (interface + fs + security tests)
- [x] Step 3: adapter (interface + obsidian scanner/parser)
- [x] Step 4: index (interface + sqlite + consistency)
- [ ] Step 5: mcp (server + tools + round-trip tests)
- [ ] Step 6: cmd (cobra: init/search/serve)
- [ ] Step 7: schema (default_schema.md + go:embed)
- [ ] Step 8: integration tests
- [ ] Step 9: 1-week real-world validation

## Project docs

- [SPEC.md](SPEC.md) — MVP specification (behavior contract)
- [DESIGN.md](DESIGN.md) — Architecture and component design
- [CLAUDE.md](CLAUDE.md) — Decision context and background
- [docs/decisions/](docs/decisions/) — Architectural decision records

## License

[MIT](LICENSE)
