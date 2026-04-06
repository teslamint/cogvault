# 0004-storage-toctou

Status: accepted
Date: 2026-04-06

## Context

`FSStorage.Write` validates symlinks via component-by-component `Lstat` in `resolvePath`, then creates directories and writes the file. A race window exists between the symlink check and the actual filesystem mutation (`MkdirAll`/`WriteFile`). An external actor could replace a parent directory with a symlink during this window, causing writes outside the vault root.

## Decision

MVP accepts this TOCTOU window. No additional re-validation after `MkdirAll`.

## Why

- cogvault runs as a local single-user stdio MCP server. The vault owner is the only actor with filesystem access during operation.
- Mitigating this race requires `O_NOFOLLOW`-based open or parent chain re-validation after `MkdirAll`, both of which add significant complexity.
- The mu lock serializes all writes, so cogvault itself cannot race against itself.

## Revisit Triggers

- SSE transport or multi-user access is introduced (v0.3+).
- Cloud/container deployment where the vault filesystem is shared.
- Security audit flags this as a finding.

## Related Files

- `internal/storage/fs.go` (Write method)
