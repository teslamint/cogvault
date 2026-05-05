# Step 9 Validation Report

Date: 2026-05-05
Duration: 5 days (of planned 7)
Outcome: partial success

## SPEC Section 11 Checklist

| Criterion | Result | Notes |
|-----------|--------|-------|
| Day 1: init, ingest 3 notes | Pass | After MCP serialization fix (see step9-mcp-unblock-plan.md) |
| Day 2-5: add 1+ daily | Partial | Friction prevented consistent daily additions |
| Day 3: search leverages prior ingest | Pass | FTS5 trigram search returned relevant results |
| Day 7: wiki-first reference habit | Fail | Not reached; workflow friction prevented habit formation |

## What Worked

- **Technical correctness**: all 6 MCP tools function as specified after the array serialization fix.
- **Path security**: exclude patterns, traversal prevention, `_schema.md` write protection all hold.
- **Search quality**: trigram tokenizer handles Korean adequately for existing page volume.
- **MCP stability**: after the unblock fix (commit 29f8b6d), no further serialization errors observed.
- **Schema enforcement**: `_schema.md` instructions guide agent page creation with correct frontmatter.

## What Didn't Work

### Workflow friction (primary failure)

The ingest workflow requires 3-6 MCP calls per note:

1. `wiki_scan` to locate source
2. `wiki_parse` to extract content
3. Agent reasoning (outside tool boundary)
4. `wiki_search` to check existing pages
5. `wiki_write` to create/update wiki page(s)

Each step requires explicit user instruction. No single "ingest this note" shortcut exists. The manual orchestration cost exceeded the perceived benefit before the validation period completed.

### Search chicken-and-egg

Search utility scales with accumulated wiki content. With insufficient pages accumulated, search rarely surfaced non-obvious connections. The value proposition ("search wiki instead of raw vault") requires a critical mass that the friction barrier prevents reaching.

### Habit formation failure

SPEC Section 11 defines success as "1 week daily use, wiki was useful." The friction-to-value ratio inverted mid-validation: each additional ingest took the same effort but produced diminishing marginal utility because the wiki was too sparse to serve as a primary reference.

## Failure Signal Mapping

Per SPEC Section 11 pivot table:

| Signal | Observed? | Pivot prescribed |
|--------|-----------|-----------------|
| Search useless | No (search works when data exists) | Tokenizer switch |
| Friction high | **Yes** | CLI shortcut, workflow simplification |
| Schema non-compliance | No | Schema simplification |
| Quality low | No | v0.2 compiler early start |

Primary match: **"friction high"** — the pivot is CLI shortcut and workflow simplification.

## Pivot Recommendation

Apply SPEC Section 11 prescribed pivot: CLI shortcut + workflow simplification.

Concrete direction:
- Add a single-step ingest CLI command (e.g. `cogvault ingest <path>`) that wraps scan-parse-search-write into one operation.
- Keep passthrough mode as the MCP interface (agents still orchestrate via tools).
- The CLI shortcut is a convenience layer, not an engine change — consistent with 0014-roadmap-adoption-boundaries D1 (passthrough maintained).

If CLI shortcut alone does not resolve friction: escalate to v0.2 compiler (2-pass summarize-index automation). This is the next pivot in the severity chain.

See: `docs/decisions/0020-step9-validation-outcome.md` for the formal decision record.

## Related

- `docs/research/step9-mcp-unblock-plan.md` — pre-validation fix
- `docs/decisions/0014-roadmap-adoption-boundaries.md` — passthrough mode boundary
- `docs/decisions/0020-step9-validation-outcome.md` — pivot decision
- `SPEC.md` Section 11 — success criteria and pivot table
