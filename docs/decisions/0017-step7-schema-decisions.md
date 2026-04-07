# 0017-step7-schema-decisions

Status: accepted
Date: 2026-04-07

## Context

Step 7 migrated the hardcoded `const defaultSchemaContent` in `cmd/cogvault/init.go` to a `//go:embed` asset in `internal/schema/`. During planning and review, three decisions emerged that are not captured in SPEC.md, DESIGN.md, or prior decision records.

## Decision

### D1: Embedded asset lives in `internal/schema/`, not top-level `schema/`

DESIGN.md §4.4 and the file table reference `schema/default_schema.md` as the embed target. The actual implementation places it at `internal/schema/default_schema.md`.

The deviation was pre-decided in 0016-step6-deferred-items item 1: "the embedded file should live in an `internal/` package (not `cmd/`) so that future entry points (test harness, HTTP handler) can reuse it without importing from `cmd/`."

Using `internal/` rather than a top-level `schema/` directory keeps the asset under Go's `internal` visibility boundary. External consumers of the module cannot import it directly, which is intentional — the schema content is an implementation detail of this binary, not a public API.

DESIGN.md §4.4 and file table updated to reflect `internal/schema/` as the actual location.

### D2: MCP instructions fallback policy unchanged

During planning, an option was considered to replace the 3-line summary in `defaultSchemaInstructions()` with the full embedded schema content (applying the same truncation logic). This was rejected.

DESIGN.md §2.6 specifies the fallback for `_schema.md` read failure as "하드코딩된 기본 요약" (hardcoded default summary). The existing test `TestSchemaInstructions/read failure fallback` partially guards this by asserting the fallback is non-empty and references `wiki_dir`, though it does not explicitly verify the response is a summary rather than the full schema. Changing the fallback would constitute a user-visible behavior change to MCP instructions, which is outside Step 7's scope (const→embed migration only).

If a future step wants richer fallback instructions, it should update DESIGN.md §2.6 first, then modify `defaultSchemaInstructions()` and its test together.

### D3: Byte-for-byte equivalence between embedded asset and init output

`init` passes `schema.DefaultContent` directly to `fsStore.WriteSchema([]byte(...))` with no normalization, templating, or interpolation. The file written to `_wiki/_schema.md` is byte-identical to `internal/schema/default_schema.md`.

This means `_schema.md` contains literal placeholder strings like `<wiki_dir>` rather than the actual configured `wiki_dir` value. This is by design — the schema is a static document that agents read as-is. Runtime values (actual wiki_dir path) are communicated via MCP instructions and tool descriptions, not baked into the schema file.

`TestInitSchemaMatchesEmbed` locks this invariant by comparing the init-written file content against `schema.DefaultContent`.

## Why

### Why internal/ over top-level

Top-level directories in a Go module are importable by any downstream module. The default schema is not a stable API — its content may change between versions. Placing it under `internal/` communicates that external consumers should not depend on its structure or existence.

### Why not change the fallback

Step 7's contract is narrowly scoped: move the source of truth from a Go const to an embedded file. Changing what MCP clients see when `_schema.md` is unreadable is a separate concern with its own compatibility implications. Keeping changes orthogonal makes each step independently reviewable and reversible.

### Why no interpolation

Interpolating `wiki_dir` into the schema file would create a divergence between the embedded asset and the written file, breaking the single-source-of-truth property. It would also mean re-running `init` with different config could silently change `_schema.md` content, which conflicts with WriteSchema's idempotency contract (skip if file exists).
