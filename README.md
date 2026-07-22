# cogvault

A personal knowledge pipeline: drop files into folders you already fill, let an
LLM digest each one into a searchable wiki, and consume the result over MCP, the
CLI, or your phone.

**Status:** v2 Phase 1 implemented; a 1-week real-world validation is pending.

## How it works

Three stages — Phase 1 builds the middle one:

1. **Capture** — nothing to run. The capture surface is directories you already
   fill (e.g. `~/Downloads/_Articles`). Later phases add phone-fed inbox folders.
2. **Digest** — `cogvault ingest` scans the configured sources, detects
   unprocessed files by content hash, digests each PDF through the Claude Code CLI
   into a wiki source page (summary, key points, provenance frontmatter), indexes
   it (SQLite FTS5), and records the outcome in a processing ledger. A
   launchd-scheduled run makes this zero-touch.
3. **Consume** — `cogvault search` and the `wiki_search` MCP tool query the
   digested wiki. Because the wiki can live under iCloud Drive, source pages are
   also readable on a phone in the Files app or any Markdown viewer — no Obsidian
   required.

The vault concept from v1 is gone: `wiki_dir` is the single storage root, and
`sources` are plain directories the ingest pipeline reads directly. See
[docs/decisions/0021-v2-refounding.md](docs/decisions/0021-v2-refounding.md).

## Requirements

- macOS (the launchd automation is macOS-specific; the CLI itself is portable).
- The [Claude Code](https://claude.com/claude-code) CLI (`claude`), installed and
  authenticated non-interactively (subscription auth via the login keychain).
- Go 1.26.1+ to build.

## Setup

### 1. Build

```bash
go build -o cogvault ./cmd/cogvault
```

### 2. Create and edit the config (two-step `init`)

`cogvault init` is a two-step flow because the config has no safe defaults for
`wiki_dir`/`db_path`:

```bash
# First run: scaffolds a template config and prints guidance, then stops.
cogvault init
# → created ~/.config/cogvault/config.yaml; edit wiki_dir/db_path/sources, then re-run cogvault init
```

Edit `~/.config/cogvault/config.yaml`:

```yaml
wiki_dir: /Users/you/Library/Mobile Documents/com~apple~CloudDocs/cogvault-wiki  # absolute, writable root
db_path: /Users/you/.local/state/cogvault/cogvault.db                            # absolute, OUTSIDE the synced folder
sources:
  - path: /Users/you/Downloads/_Articles
    types: [pdf]        # Phase 1 digests PDFs only; the filter skips e.g. .webp
llm:
  backend: claudecode
```

Notes on paths: a leading `~/` is expanded; every other path must be absolute; a
`~` elsewhere in a path (like iCloud's `com~apple~CloudDocs`) stays literal. Keep
`db_path` outside the synced wiki folder so iCloud never syncs or evicts the DB.
A source may not contain, be contained by, or equal `wiki_dir`.

```bash
# Second run: creates the wiki dir, _schema.md, and the database.
cogvault init
```

### 3. Digest the backlog

Preview first, then run in bounded batches (each PDF costs one LLM call, ~30-40s):

```bash
cogvault ingest --dry-run          # list what would be digested; writes nothing
cogvault ingest --limit 10         # digest at most 10 files (quota / batch control)
cogvault ingest                    # digest everything pending
```

The run prints a per-file report and continues past failures. Per-file failures
do not fail the run (exit code stays 0); only a run-level error is nonzero.

### 4. Search and serve

```bash
cogvault search "your terms"       # full-text search over the wiki
cogvault serve                     # MCP stdio server (register in your MCP client)
```

### 5. Schedule zero-touch ingest (launchd)

```bash
# 1. Create the log directory (launchd will not create it for you):
mkdir -p ~/Library/Logs/cogvault

# 2. Copy the template and edit the placeholders inside it:
#    - the absolute path to your built cogvault binary
#    - every /Users/USERNAME/... path → your real home
cp deploy/com.teslamint.cogvault.ingest.plist ~/Library/LaunchAgents/

# 3. Load it:
launchctl load ~/Library/LaunchAgents/com.teslamint.cogvault.ingest.plist
```

The default interval is 3600s (1 hour). launchd's PATH excludes `~/.local/bin`,
so the template sets an explicit PATH that includes the `claude` CLI directory
(verified by the O1 spike). One-time grants the scheduled binary needs:

- **TCC**: macOS may prompt for access to `~/Downloads` (or your source folder)
  the first time the scheduled job reads it; grant it.
- **Auth**: `claude` must resolve auth non-interactively under launchd (it does
  when subscription auth is in the login keychain and the GUI session is active).

## Migrating from v1

v2 uses a fresh wiki root and database — there is no in-place upgrade.

1. Copy your existing `_wiki` pages from the old vault into the new `wiki_dir`.
2. Run `cogvault init` (it indexes the copied pages).

Accepted loss: v1 also indexed raw vault notes (`scope=vault` search); v2's index
contains wiki pages only, so full-text search over un-digested vault notes is gone
until a later phase digests markdown sources. See
[0021](docs/decisions/0021-v2-refounding.md).

## Development

```bash
go test -race ./...
```

## Project docs

- [SPEC.md](SPEC.md) — behavior contract (canonical)
- [DESIGN.md](DESIGN.md) — architecture and component design (canonical)
- [CLAUDE.md](CLAUDE.md) — decision context and background
- [docs/decisions/](docs/decisions/) — architectural decision records
- [docs/specs/2026-07-22-refound-capture-pipeline-design.md](docs/specs/2026-07-22-refound-capture-pipeline-design.md)
  — the approved v2 design

## License

[MIT](LICENSE)
