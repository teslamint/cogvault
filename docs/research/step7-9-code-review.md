# Step 7-9 Code Review

## Scope

Commits reviewed: `29f8b6d`, `9834f31`, `16d4ff8` (Steps 7, 8, 9)
Files changed: 34, Lines added: ~1,500

Review method: 3 parallel exploration agents (schema+MCP, cmd/, docs+fixtures), followed by manual verification of findings, sequential fix application with per-fix testing, and a final re-review pass with 2 parallel review agents.

## Findings

### HIGH — Reverted

1. **DESIGN.md file table paths**: Initially identified `schema/schema.go` as incorrect (should be `internal/schema/schema.go`). However, re-review revealed the entire table uses an abbreviated convention that omits the `internal/` prefix. The original paths were correct under this convention. Fix was reverted.

### MEDIUM — Fixed

2. **Custom `containsSubstring`/`containsCheck` in tools_test.go** (lines 673-684): Manual byte-by-byte reimplementation of `strings.Contains`. Replaced 3 call sites with `strings.Contains()`, added `"strings"` import, deleted both custom functions.

3. **`search.go` inline consistency error handling** (lines 36-43): Identical 7-line pattern to what was extracted into `handleConsistencyResult()` for init.go and serve.go. Replaced with shared helper call, removed unused `"errors"` and `index` imports.

4. **Inconsistent `.md` case sensitivity in `handleWikiWrite`** (tools.go line 76): Write handler used case-sensitive `strings.HasSuffix(path, ".md")` while parse handler (line 210) used case-insensitive `strings.HasSuffix(strings.ToLower(path), ".md")`. Normalized write handler to match parse handler.

5. **Race tests lacked post-wait assertions** (integration_test.go lines 759-819): Both `Race_ConcurrentWriteAndSearch` and `Race_ConcurrentListAndWrite` only detected panics/deadlocks. Added wiki_list count verification and wiki_search result checks after `wg.Wait()`.

### Debunked (NOT issues)

- **`Content` JSON `omitempty` tag**: Present in `adapter.go:15` (`json:"content,omitempty"`)
- **`min()` builtin compatibility**: Go 1.26.1; `min()` available since Go 1.21

## Test Results

**Baseline (pre-fix):**
```
ok  github.com/teslamint/cogvault/cmd/cogvault
ok  github.com/teslamint/cogvault/internal/mcp
ok  (all other packages)
go vet: clean
```

**Post-fix:**
```
ok  github.com/teslamint/cogvault/cmd/cogvault     1.857s
ok  github.com/teslamint/cogvault/internal/mcp     2.220s
ok  (all other packages, cached)
go vet: clean
```

All tests pass with `-race` flag. No regressions.

## Files Modified

| File | Change |
|------|--------|
| `internal/mcp/tools_test.go` | Replace containsSubstring with strings.Contains |
| `cmd/cogvault/search.go` | Use handleConsistencyResult shared helper |
| `internal/mcp/tools.go` | Case-insensitive .md check in write handler |
| `internal/mcp/integration_test.go` | Race test post-wait correctness assertions |

## Residual Items

- **DESIGN.md file table convention**: The table omits `internal/` from all paths. This is a valid convention but could confuse newcomers. Not a code issue — documentation style choice.
- **Race test flakiness risk**: The new assertions assume all 10 concurrent writes complete successfully. If the write mutex or index serialization has subtle ordering issues, these could theoretically flake. No evidence of flakiness observed in testing.
