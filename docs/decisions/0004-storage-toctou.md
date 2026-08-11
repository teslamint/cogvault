# 0004-storage-toctou

Status: accepted
Date: 2026-04-06

## Context

`FSStorage.Write` validates symlinks via component-by-component `Lstat` in `resolvePath`, then creates directories and writes the file. A race window exists between the symlink check and the actual filesystem mutation (`MkdirAll`/`WriteFile`). An external actor could replace a parent directory with a symlink during this window, causing writes outside the vault root.

## Decision

MVP accepts this TOCTOU window. No additional re-validation after `MkdirAll`.

## Why

- Mitigating this race requires `O_NOFOLLOW`-based open or parent chain re-validation after `MkdirAll`, both of which add significant complexity.
- The mu lock serializes all writes, so cogvault itself cannot race against itself.

Originally this decision also rested on cogvault running as a local
single-user stdio server, with the wiki owner as the only actor holding
filesystem access. The remote `sse` and `http` transports added in 2026-08
weaken that half: the wiki is now reachable, and writable, from the internet.

The decision still stands, for a reason worth stating explicitly so nobody has
to re-derive it. The race requires an actor who can **replace a directory with
a symlink** inside `wiki_dir`. No MCP tool offers that primitive — `wiki_write`
creates files and parent directories, `wiki_delete` removes paths, and neither
can create a symlink. A remote client, authenticated or not, therefore cannot
reach this window; it still takes local filesystem access, which is the same
threat model the decision accepted.

## Revisit Triggers

- SSE transport or multi-user access is introduced (v0.3+).
  **Fired (2026-08)** by the remote `sse`/`http` transports, and assessed: see
  the note above. Remote clients have no symlink-creation primitive, so the
  window still requires local filesystem access and the decision is unchanged.
  Re-open if a tool that can create symlinks, or an ingest source that follows
  them into `wiki_dir`, is ever added.
- Cloud/container deployment where the vault filesystem is shared.
- Security audit flags this as a finding.

## Related Files

- `internal/storage/fs.go` (Write method)
