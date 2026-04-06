# 0007-storage-error-mapping

Status: accepted
Date: 2026-04-06

## Context

The storage layer mixes domain-specific access control with raw filesystem operations.
Reviews repeatedly clarified that callers need stable sentinel errors for contract-level conditions, but should still see raw wrapped filesystem errors for unexpected runtime failures.

## Decision

Storage maps only contract-level conditions to project sentinel errors:

- traversal or absolute-path escape attempts → `ErrTraversal`
- symlink access → `ErrSymlink`
- write boundary or read prohibition → `ErrPermission`
- missing path or directory-operation-on-file → `ErrNotFound`

Other runtime filesystem errors are propagated as wrapped raw errors with `%w`.

## Why

- Sentinel errors give MCP handlers and higher layers a stable contract for expected failure modes.
- Raw filesystem errors preserve useful debugging context for unexpected states such as `ENOTDIR`, permission issues from the OS, or storage corruption.
- Forcing every runtime error into a sentinel would hide too much information too early.

## Alternatives Considered

- Map every storage error into a project sentinel
  Rejected because it destroys debugging detail and creates artificial categories for unrelated OS failures.
- Return only raw filesystem errors
  Rejected because traversal, symlink, permission, and not-found semantics are part of the public storage contract.

## Revisit Triggers

- MCP error handling needs more structured internal/error categories.
- The project introduces centralized logging that requires stronger classification at the storage boundary.
- Repeated production issues show that callers mis-handle raw wrapped filesystem errors.

## Related Files

- `CLAUDE.md`
- `SPEC.md`
- `DESIGN.md`
- `internal/storage/fs.go`
