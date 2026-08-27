# cogvault

A personal knowledge pipeline: drop files into folders you already fill, let an
LLM digest each one into a searchable wiki, and consume the result over MCP, the
CLI, or your phone.

**Status:** v2 Phase 1 complete — all four success criteria met (2026-07-29).
See [ROADMAP.md](ROADMAP.md) for what comes next.

## How it works

Three stages — Phase 1 builds the middle one:

1. **Capture** — nothing to run. The capture surface is directories you already
   fill (e.g. `~/Downloads/_Articles`). A folder your phone syncs into works the
   same way: send a file to an iCloud Drive or Dropbox folder from the share
   sheet, list that folder in `sources[]`, and the scheduled run digests
   whatever lands there. cogvault has no phone app and needs none.
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
make build                         # build + adhoc codesign (default, no certificate needed)
make install CODESIGN_IDENTITY="Developer ID Application: <your name> (<team>)"
```

The default `-` identity produces an ad-hoc signature whose TCC grants die on
every rebuild. A stable identity can preserve existing grants only when every
rebuild uses that same identity. Changing the code-signing identifier resets
the binary's TCC identity once, so the first signed install costs one final
round of prompts; later rebuilds signed with the same identity do not.

A manual build silently restores the linker's ad-hoc signature and
`Identifier=a.out`, even after a stable identity was applied:

```bash
go build -o cogvault ./cmd/cogvault
```

Run `make build` or `make install` again with the same `CODESIGN_IDENTITY` to
restore the stable identity.

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

`cogvault serve` also supports two network transports for remote clients (the
Claude apps, ChatGPT) or Claude Code over a tunnel — `--transport sse` or
`--transport http` (Streamable HTTP) — gated by `auth.mode: none` (loopback
only, the default), `bearer`, or `oauth`. `--public-url` has no function
under the default `auth.mode: none` and cogvault refuses to start with both
set together, so set `auth.mode: bearer` or `auth.mode: oauth` in your config
file before using `--public-url`.

In `~/.config/cogvault/config.yaml`:

```yaml
auth:
  mode: bearer
```

Then:

```bash
export COGVAULT_BEARER_TOKEN=$(head -c 32 /dev/urandom | base64)
cogvault serve --transport http --public-url https://cogvault.example.com
```

Full setup — tunneling, `--public-url`, identity-provider prerequisites, and
the security posture — is in
[docs/deployment/remote-mcp.md](docs/deployment/remote-mcp.md); read it
before exposing either transport to the internet.

### 5. Schedule zero-touch ingest (launchd)

```bash
# Create the log directory (launchd will not create it for you):
mkdir -p ~/Library/Logs/cogvault
```

The default interval is 3600s (1 hour). launchd's PATH excludes `~/.local/bin`,
so the template sets an explicit PATH that includes the `claude` CLI directory
(verified by the O1 spike). Grant the scheduled binary's permissions through
this ceremony:

1. ```bash
   make install CODESIGN_IDENTITY="Developer ID Application: <your name> (<team>)"
   ```
2. ```bash
   cp deploy/com.teslamint.cogvault.ingest.plist ~/Library/LaunchAgents/
   ```

   Edit the copied template's binary and home-directory placeholders for this
   machine, then load it:

   ```bash
   launchctl load ~/Library/LaunchAgents/com.teslamint.cogvault.ingest.plist
   ```
3. ```bash
   launchctl kickstart -k gui/$(id -u)/com.teslamint.cogvault.ingest
   ```
4. Answer each consent prompt once.

Step 3 must be a `kickstart`, not a manual `cogvault ingest` in a terminal:
macOS attributes a terminal-spawned process to the terminal, so a manual run
grants the terminal rather than cogvault and the scheduled job keeps prompting.

A folder prompt covers reads of that one source directory. The observed "data
from other apps" prompt creates a `kTCCServiceSystemPolicyAppData` row for
cogvault. The exact access that triggers it remains unmeasured. **Unresolved
(Open Decision 2):** whether one Full Disk Access grant supersedes these
individual prompts must be verified on the maintainer's machine.

- **Auth**: `claude` must resolve auth non-interactively under launchd (it does
  when subscription auth is in the login keychain and the GUI session is active).

User-level Claude Code hooks that invoke `node` will log harmless "node: command
not found" errors under the template's minimal PATH — they don't affect ingest's
exit code or result. Extend PATH with the node directory in the plist if the
noise bothers you (see O1 spike finding 2 in
[docs/research/o1-headless-pdf-verification.md](docs/research/o1-headless-pdf-verification.md)).

#### Removing stale grants

The same binary path can retain TCC grant rows bound to earlier code
requirements. Inspect them before changing anything:

```bash
sqlite3 "$HOME/Library/Application Support/com.apple.TCC/TCC.db" \
  "select service, length(csreq), hex(csreq) from access where client like '%cogvault%';"
```

`tccutil reset <service>` clears that service for every application on the
machine, not only cogvault. Use System Settings' per-application view when you
need the narrower instrument.

`~/bin/cogvault` also backs the `com.teslamint.cogvault` `serve` job. A running
`serve` process keeps using the pre-install image, so restart that job after an
install when the new identity must apply to it too.

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
make test                          # go test -race ./...
make clean                         # remove built binary
```

## Project docs

- [ROADMAP.md](ROADMAP.md) — forward-looking summary with canonical owner references
- [SPEC.md](SPEC.md) — public behavior and contract canon
- [DESIGN.md](DESIGN.md) — architecture, package boundaries, and component boundaries
- [CONCEPTS.md](CONCEPTS.md) — shared terminology
- [docs/decisions/](docs/decisions/) — durable project decision records
- [docs/specs/2026-07-22-refound-capture-pipeline-design.md](docs/specs/2026-07-22-refound-capture-pipeline-design.md)
  — approved v2 design that complements `SPEC.md` and `DESIGN.md`
- [docs/context/project-background.md](docs/context/project-background.md) — non-canonical background, history, and archaeology
- [CLAUDE.md](CLAUDE.md) and [AGENTS.md](AGENTS.md) — agent entrypoints and routing guides, not product canon
- [docs/decisions/0022-repository-working-conventions.md](docs/decisions/0022-repository-working-conventions.md)
  — repository-wide working and verification conventions
- [docs/decisions/0023-stale-agent-convention-reconciliation.md](docs/decisions/0023-stale-agent-convention-reconciliation.md)
  — current documentation-maintenance and context-propagation boundaries
- [docs/plans/](docs/plans/) — non-canonical working notes that may become stale

## License

[MIT](LICENSE)
