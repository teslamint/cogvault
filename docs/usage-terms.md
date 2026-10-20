# Record and inspect usage terms

Cogvault preserves explicit terms and tracks human review separately. It does not decide whether a use is legally permitted.
The public contract is [SPEC §9.15](../SPEC.md#915-terms).

## Safe demonstration

From the repository root, run:

```sh
python3 -B scripts/demo_usage_terms.py
```

The script builds a temporary CLI binary. It creates an isolated configuration and wiki from newly authored examples.
It does not use the repository's existing config, a personal wiki, a search DB, an LLM, or MCP.
It prints five inspection results and checks that page bytes stay unchanged and no DB appears.
It removes its temporary configuration, wiki, and binary on exit.

**Every example condition, evidence quote, and reviewer is fictional.**
They are validation settings, not evidence for rights to real material.
The examples live in `testdata/terms-review-demo/wiki/`.

## Record a review

1. State the intended use, such as an external submission of a summary. Keep it separate from copying the original.
2. Locate terms or explicit permission that address that use. Record the location and relevant wording, including restrictions.
3. Record the human reviewer and review date. Leave `status: unverified` when evidence is missing, ambiguous, or conflicting.
4. Use `status: recorded` only to indicate a complete human record. This state does not grant permission.
5. Inspect the record with the explicit command. Read its `record`, `reasons`, and `notice`, not just its status.

A fictional record template:

```yaml
usage_terms:
  status: recorded
  purpose: submit the synthetic summary in an external exercise
  scope: this fictional summary only; not the original
  where: external exercise reviewers
  level: synthetic summary only
  conditions: attribution required; redistribution of the fictional original prohibited
  evidence:
    reference: fictional-policy.md#synthetic-summaries
    quote: Synthetic summaries may be submitted with attribution. Redistribution of the fictional original is prohibited.
  reviewed_by: fictional-example-reviewer
  reviewed_at: "2026-10-20"
  example_notice: FICTIONAL validation settings only; not rights evidence for real material.
```

Use the YAML block inside a page's `---` frontmatter delimiters.
`example_notice` is demonstration-only metadata; it is not a required product field.
For a real record, record real evidence rather than copying this fictional template's values.

`reviewed_at` accepts a valid `YYYY-MM-DD` date with or without quotation marks.
The shared parser preserves date and timestamp text rather than changing its timezone or format.

## Inspect a record

```sh
cogvault terms sources/example.md --config /absolute/path/to/isolated-config.yaml
```

Use an explicit config when isolation matters. The example path is illustrative, not a file supplied by this guide.
The command reads one wiki-relative path through the storage boundary.
It does not read `source_path`, follow evidence references, open the DB, or call an LLM.

The JSON `status` is the effective inspection result:

- `recorded`: required strings and date are structurally complete. A human must still assess the evidence and intended use.
- `unverified`: the record explicitly says so, or the record is missing, incomplete, or malformed. `reasons` explains the structural result.

Both states return exit code 0 when inspection succeeds. Config/read/path failures return a command error.
This command is not an export permission gate. A source URL, free access, or paid subscription alone proves no reuse permission.
Existing MCP `wiki_read` returns the page and `wiki_parse` returns its frontmatter. Neither operation certifies rights.

## See sharing conditions in search

CLI `cogvault search <query> --config <path>` shows the applicability, audience
(`where`), portion/transformation (`level`), conditions (`how`), evidence reference,
and review status below each match. MCP `wiki_search` returns the same summary
in each result's `usage_terms` object, along with `reasons`.

For the summary, missing, blank, or non-text values use the explicit marker
`unverified`; CLI renders it as `미확인`. Unknown does not mean unrestricted.
A missing Where or Level does not erase a known condition or evidence reference.
`level` is text about a permitted portion or transformation, not an ordinal permission grade.
The summary never expands MIT, a source URL, free access, or a subscription into permission.

The review `status` uses the same inspection as `terms`. An older complete
record can still have unknown Where/Level; `recorded` is not legal clearance.
Full evidence quotations and reviewer/date details remain in `terms` or `wiki_parse`.
An unreadable result page gets an entirely unverified summary; other results remain visible.

Search matching, ranking, limits, and the index schema are unchanged. This is
an inspection aid, not a filter or automatic shareability classifier.
`search-ready.md` in the fictional demo directory exercises the full summary;
its conditions and reviewer are validation settings, not rights evidence.

## Automatic generation

Automatic ingest keeps explicit terms separately from review status.
It selects the structured input `usage_terms` first, the existing page record second,
and a model-extracted candidate third. Purpose, conditions, scope, Where, Level, and evidence stay in the record.
It sets `status: unverified` and removes reviewer/date claims before storage and indexing.
The `terms` command still displays the preserved conditions and evidence while explaining missing human review.

This applies to path-input Markdown and extracted-text backends, independently of the model's summary.
Regeneration without new source terms retains the previous declaration and resets review status only.
A new explicit source declaration replaces the previous declaration; conflicting permissions are not combined automatically.
Malformed supplied records fail without overwriting the wiki page.
Unchanged existing pages are not migrated or classified.
Ingest re-serializes the complete YAML frontmatter; key order, quoting, and scalar layout can change.
Existing wiki `_schema.md` copies are not automatically updated; deterministic preservation does not depend on that update.

When terms are known before ingest, put their `purpose`, `conditions`, and
`evidence` in the source's `usage_terms` frontmatter. Do not wait for a human
review to record those facts. Leave `status: unverified` until that review.
The application preserves this structured record even if the model omits it.

## Protection across write paths

MCP writes and generated-output commands use the same storage check.
An overwrite that drops or empties existing purpose, conditions, scope, where, level, or evidence is rejected before the page changes.
The caller must retain each populated protected field or provide its non-empty replacement.
Broken metadata without a parseable declaration can still be repaired; this does not certify the old record.

`fetch` copies a supplied structured declaration into the capture header while retaining the complete raw response body.
It records that copy as unverified data and removes remote reviewer/date claims from the copy.
It applies the same loss check before replacing an existing capture.
Recording an explicit declaration is not inferring permission from the URL.
Link-only digest and concept pages do not automatically acquire a license from linked works.

A human must inspect the page body as well: this change does not validate every legal statement that an LLM might generate in prose.

## Human review checklist

1. Compare the displayed purpose, conditions, and quote with `fictional-policy.md` in the demonstration.
2. Confirm that the ambiguous example remains unverified despite populated fields.
3. Confirm that the missing-evidence example remains unverified despite declaring `recorded`.
4. Confirm that access and subscription metadata do not change the unknown example's status.
5. Record your acceptance or rejection and reason in the task's work-result record.

No automated result substitutes for this human review.
