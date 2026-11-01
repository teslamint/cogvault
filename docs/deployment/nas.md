# NAS deployment

The MCP server runs as a container on the NAS. Ingest keeps running on the Mac
because it needs the local LLM (`llm.base_url` must be a loopback address) and
the source PDFs. Versioning and image publishing: `docs/decisions/0025-versioning-and-container-release.md`.

```
Mac                                   NAS (Container Manager / Portainer)
  launchd: cogvault ingest   ──SMB──►  wiki/   ◄── cogvault serve ◄── cloudflared ◄── clients
  local LLM, sources, ledger DB        state/ (index DB, rebuildable)
```

- The NAS `wiki/` directory is the source of truth. The Mac mounts it over SMB
  and points `wiki_dir` there.
- The Mac keeps its own `db_path` (ingest ledger and index) on local disk.
  Never put a SQLite DB on the SMB share: WAL does not work over a network file system.
- The NAS has its own index DB in `state/`. It is rebuilt from `wiki/` by the
  consistency check, so losing it costs nothing.

## Files

`deploy/nas/` in this repository is copied to `/volume1/docker/cogvault/` on the NAS:

| File | Purpose |
|---|---|
| `compose.yaml` | `cogvault` (serve) and `cloudflared` services |
| `config.yaml` | container-side config (paths are container paths) |
| `env.example` | copy to `.env`: image version, public URL, uid/gid |
| `cloudflared.config.example.yaml` | copy to `cloudflared/config.yaml`; put the tunnel credentials JSON beside it (mode 600) |

`NAS_UID`/`NAS_GID` must be the NAS account that owns `wiki/`, so SMB writes
from the Mac and `wiki_write` from the container share one owner.

## Upgrade and rollback

1. Tag a release: `git tag -a vX.Y.Z -m vX.Y.Z && git push origin vX.Y.Z`. The `container` workflow publishes `ghcr.io/teslamint/cogvault:X.Y.Z`.
2. On the NAS, set `COGVAULT_VERSION=X.Y.Z` in `.env`, then redeploy the stack.
3. Rollback: set the previous version and redeploy. The index DB is rebuildable, so no data step is needed.

Check the running version: `docker exec <container> cogvault --version`.

## Mac side

1. Mount the NAS share that contains `cogvault/wiki` (for example `smb://<nas>/docker`).
2. In `~/.config/cogvault/config.yaml`, set `wiki_dir` to the mounted `cogvault/wiki` path. Keep `db_path`, `sources`, and `llm` unchanged.
3. Keep the share mounted across reboots (Login Items). `/Volumes` is not writable by a normal user, so an unmounted path is not recreated as a local directory; confirm with `cogvault status` after a reboot.
4. The `com.teslamint.cogvault` (serve) and `com.teslamint.cloudflared-cogvault` launchd agents are unloaded after cutover.

## Deploying through Portainer

When SSH access cannot reach the Docker socket, deploy with the Portainer API (access token from Portainer, kept in an untracked `.env`):

1. `POST /api/stacks/create/standalone/string?endpointId=<id>` with `stackFileContent` (this `compose.yaml`) and `env` (from `env.example`). Set `COGVAULT_DIR` to the absolute NAS path: Portainer resolves relative bind paths inside its own data directory.
2. Redeploy with `PUT /api/stacks/<id>?endpointId=<id>` after changing the compose file or `COGVAULT_VERSION`.

## File names must be NFC

APFS (iCloud) matches file names ignoring Unicode normalization; the NAS file system (btrfs, via SMB) compares bytes. A wiki that came from iCloud can hold decomposed (NFD) Hangul names while the ingest ledger and wikilinks use composed (NFC) names, which makes the ledger report pages as missing and re-digest them. After copying a wiki to the NAS, rename every entry to NFC and rewrite `ingest_ledger.wiki_page` to NFC (103 files and 83 ledger rows in the first migration). A directory listing over SMB can lag server-side renames for a few seconds.

## Cutover record (2026-11-01)

Mac `serve`, `cloudflared` and `ingest` agents were stopped; `serve` and `cloudflared` were also `launchctl disable`d so they do not return at login. Rollback: `launchctl enable` both, bootstrap their plists, restore `~/.config/cogvault/config.yaml.pre-nas`, and stop the stack.
