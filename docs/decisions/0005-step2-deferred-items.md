# 0005-step2-deferred-items

Status: deferred
Date: 2026-04-06

## Context

Step 2 introduced the filesystem-backed storage boundary. Reviews found one remaining behavior that is known and understood but intentionally not addressed in the MVP implementation.

## Decision

The following item is deferred:

1. `Storage.List()` may expose direct child symlink entries even though later `Read`, `Write`, or `Exists` on those paths return `ErrSymlink`.

## Why

The primary Step 2 goal is to enforce traversal rejection, ancestor/leaf symlink rejection on access, write boundary protection, and `exclude`/`exclude_read` semantics.
Filtering or remapping direct child symlink entries at list time would make the namespace more internally consistent, but it is not required to preserve the current security boundary.

The team prefers to ship the access boundary first and revisit namespace presentation once real MCP list consumers exist.

## Alternatives Considered

- Hide child symlink entries from `List()`
  Deferred, not rejected.
- Return `ErrSymlink` from `List()` if any child is a symlink
  Rejected for now because it would make one bad child poison an otherwise usable directory listing.
- Extend `ListEntry` with a symlink flag
  Deferred until there is a real consumer need.

## Revisit Triggers

Revisit this item if any of the following appears:

- MCP `wiki_list` or another caller treats storage listings as the authoritative accessible namespace,
- reviewers repeatedly report confusion because `List()` shows paths that later fail with `ErrSymlink`,
- symlink filtering or richer entry metadata becomes needed for UX consistency.

## Related Files

- `CLAUDE.md`
- `internal/storage/fs.go`
- `internal/storage/fs_test.go`
