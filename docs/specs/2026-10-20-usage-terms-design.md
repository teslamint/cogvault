---
status: approved
---
# Usage terms records and explicit inspection

## User agreement

The user chose a record template and an explicit inspection command. Automatic ingest must leave usage terms unverified.
The user did not state a separate reason for this choice. Do not infer one.

## Scope and contract

- Store `usage_terms` in page YAML frontmatter. Reuse Markdown storage and existing MCP read/parse operations.
- Fields: `status` (`unverified` or `recorded`), `purpose`, `conditions`, `evidence` (`reference`, `quote`), `reviewed_by`, `reviewed_at` (YYYY-MM-DD string).
- `recorded` means a human recorded terms, not permission or a legal determination. All six descriptive fields must be non-empty strings. The date must be valid.
- Missing, malformed, incomplete, unknown-status, or explicitly unverified records produce effective status `unverified`. Display the record and reasons without claiming permission.
- The human leaves ambiguous or conflicting evidence unverified. The tool checks structure, not the truth, authenticity, or legal meaning of evidence.
- `cogvault terms <wiki-relative-path> --config <path>` reads one page through storage. It outputs JSON with the effective status, the original record, reasons, and a non-legal-decision notice.
- The command does not open a search DB, call an LLM, fetch evidence, or modify a page. Read and path errors return a command error.
- Before storing any successful automatic ingest result, replace its entire `usage_terms` record with `{status: unverified}`. This applies to every backend and to regeneration. Preserve the body and unrelated metadata semantically. Existing untouched pages stay unchanged.
- Source URLs, free access, and subscriptions do not establish permission. No automatic legal judgment, bulk classification, new MCP tool, or DB migration.

## Completion criteria

1. An explicit synthetic human record is visible with its purpose, conditions, evidence, reviewer, and date.
2. Missing evidence, malformed fields, invalid dates, absent records, and unverified records remain unverified.
3. The inspection command preserves storage traversal, symlink, and read-exclusion boundaries and does not create a DB.
4. Automatic ingest stores unverified terms even when model output claims a completed human record.
5. New synthetic examples carry a visible fictional-terms notice. They are not evidence for rights to real material.
6. Exercise the real CLI with a new isolated config, wiki, and no personal services. Run focused regressions and report their scope.
7. Deliver separate AI-use and work-result records. Human review remains a user action, not an AI certification.

## Implementation sequence

1. Add observable failing regressions for CLI inspection and ingest status replacement.
2. Implement the CLI inspector and the common ingest write-boundary transformation.
3. Update SPEC.md, DESIGN.md, the default schema, and a synthetic workflow guide.
4. Run focused regressions, the available suite, and a real isolated CLI smoke scenario.

## Privacy and evidence boundary

Use repository code and newly authored examples only. Do not open the pre-existing untracked config.yaml or testdata/fixtures/usage-terms/.
Do not use personal wiki pages, originals, search databases, or personal MCP services.
The assistant has not started or verified full-screen recording. Recording and human review must not be reported as completed without observation.
