# Work result: usage terms records and explicit inspection

Current implementation: see [preservation and live verification](#correction-preserve-declarations-and-reset-review-only) and [CLI/MCP search summaries](#cli-and-mcp-search-summaries).
Earlier sections retain chronological snapshots, including the rejected reset-all behavior and its observed failure.

## Initial delivered behavior (superseded snapshot)

`cogvault terms <wiki-relative-path> --config <path>` displays a usage-terms record and its effective status as JSON.
It reads one wiki page through existing storage restrictions. It does not open a search DB, fetch evidence, call an LLM, or modify the page.

The record keeps intended purpose, conditions, evidence location and wording, reviewer, and date together.
`recorded` means structural completeness only, not permission or legal clearance.
Missing, malformed, incomplete, invalid-date, unknown-status, and explicitly unverified records remain `unverified`, with reasons.
A human leaves ambiguous or conflicting evidence unverified.
URLs, free access, and paid subscriptions alone do not establish permission.

Every successful automatic ingest replaces the entire model-generated `usage_terms` mapping with `{status: unverified}` before storage and indexing.
The rule applies at the common backend-independent write boundary, including regeneration.
Untouched existing pages are not migrated. Existing MCP read and parse operations expose page metadata without a new tool.

## Files

| Purpose | Files |
|---|---|
| Explicit inspector | `cmd/cogvault/terms.go`, registration in `cmd/cogvault/main.go` |
| CLI regressions | `cmd/cogvault/terms_test.go` |
| Common ingest enforcement | `internal/ingest/ingest.go` |
| Synthetic ingest regression | `internal/ingest/usage_terms_test.go` |
| Canonical behavior and architecture | `SPEC.md` §9.15, `DESIGN.md` |
| Default generation instructions | `internal/schema/default_schema.md` |
| Workflow and entry point | `docs/usage-terms.md`, `README.md` |
| Agreed scope | `docs/specs/2026-10-20-usage-terms-design.md` |
| Reproducible isolated demo | `scripts/demo_usage_terms.py`, `testdata/terms-review-demo/wiki/*.md` |
| Separate AI-use record | `docs/reports/2026-10-20-usage-terms-ai-use.md` |

## Observed verification

1. `go run ./cmd/cogvault search --help` ran during discovery without opening configured data.
2. Pre-change CLI regression failed with `unknown command "terms" for "cogvault"`.
3. Pre-change ingest regressions failed because the stored mapping retained a synthetic recorded claim or was absent.
4. `go test ./cmd/cogvault ./internal/ingest -run '^(TestTerms|TestRunReplacesUsageTerms)' -count=1 -v` passed after implementation.
5. Additional malformed-YAML, whitespace-evidence, and complete-but-unverified cases passed in the final suite.
6. `python3 -B scripts/demo_usage_terms.py` ran the actual compiled CLI on an isolated temporary wiki and config.
7. Final `go test ./...` passed for all tested packages. The adapter interface package has no test files.

The first full suite exposed a 2,133-rune default schema exceeding its existing 2,000-rune maximum.
The assistant shortened the new instructions without increasing the limit. The subsequent full suite passed.

### Real CLI demo outcomes

All conditions below are fictional validation settings, not rights evidence for real material.

| Synthetic scenario | Effective result |
|---|---|
| Complete human-style record with a conditions quote | `recorded`; purpose, restrictions, evidence, reviewer, and date visible |
| URL, access, and subscription metadata without terms evidence | `unverified` |
| Complete fields but ambiguous evidence explicitly marked unverified | `unverified` |
| No usage record | `unverified` |
| Declared recorded state with missing evidence quote | `unverified`; reason names `evidence.quote` |

The demo compared page bytes before and after: unchanged.
No database was created. Its temporary configuration, wiki, and binary were removed.

### Regression scope

CLI tests cover missing and malformed records, field types, invalid dates, unknown states, empty evidence, explicit ambiguity, and read-only behavior.
Storage boundary tests cover parent traversal, absolute paths, symlinks, missing pages, and read exclusions.
Ingest tests use an explicitly fake LLM adapter with fictional output, plus real temporary storage and index implementations.
They verify the saved mapping, indexed status, and preservation of body and unrelated metadata.
No real LLM backend was invoked; no personal MCP service was started.

## Human review and remaining limits

Implementation and automated validation are complete. Human result review is not recorded as complete.
Full-screen recording was not started or verified by the assistant.
The full three-hour window was not independently timed.

A complete record does not establish evidence authenticity, applicable rights, or actual reviewer identity.
The command does not interpret conflicting terms or validate arbitrary generated prose.

Automatic ingest re-serializes the complete frontmatter with YAML v3. Key order,
quoting, and scalar layout can change outside `usage_terms`; byte-identical
frontmatter layout is not preserved or covered by these regressions.
The tests establish body preservation and selected metadata values, not all possible YAML layouts.

`reviewed_at` must be a quoted YAML string. A separate isolated CLI check observed
`"2026-10-20"` as `recorded` and unquoted `2026-10-20` as `unverified`.
YAML v3 interprets the latter as a timestamp rather than the required string.
Both checks used the fictional record and created no database.
Automatic regeneration resets the mapping, including any prior human record; review it again afterward.

For final human review, run the isolated demo and compare its displayed evidence with the fictional policy page.
Record acceptance or rejection and the reason in a dated addition to this file; do not substitute an AI claim for that decision.

## Additional live ingest-to-search demonstration

On 2026-10-20, the user requested an actual demonstration from ingest through search.
Unlike the initial mock-backed regressions, this run used the real Claude CLI with model alias `sonnet`.
Only a newly authored fictional article, `AURORA-DEMO-731`, entered the pipeline.
All its usage conditions were fictional validation settings, not rights evidence for real material.

The run used a temporary source directory, wiki, configuration, and fresh SQLite DB.
A temporary `claude` launcher delegated to the installed real CLI with
`--safe-mode --restricted --strict-mcp-config --mcp-config '{"mcpServers":{}}'`
and `--no-session-persistence --tools Read --permission-mode dontAsk --max-budget-usd 0.50`.
This disabled personal MCP and customizations and confined file reads to the isolated working directory.
The launcher did not supply a fake response or transform model output.

The initial build attempted outside the Go module failed with `go.mod file not found`.
Building from the repository root succeeded. Actual Cogvault commands ran from the isolated directory.
Every command used the explicit temporary `--config` path.

| Command | Observed result |
|---|---|
| `cogvault init` | Initialized the temporary wiki; exit 0 |
| `cogvault ingest` | `scanned=1 digested=1 failed=0`, all other outcome counts 0; exit 0 |
| `cogvault terms sources/aurora-cooling.md` | Stored record contained only `status: unverified`; exit 0 |
| `cogvault search AURORA-DEMO-731` | Returned `sources/aurora-cooling.md`, title `Aurora 냉각 실험 (AURORA-DEMO-731)`, type/category `source/article`; exit 0 |
| Second `cogvault ingest` | `scanned=1 digested=0 unchanged=1`, all other outcome counts 0; exit 0 |

The first ingest ran from `2026-10-20T20:22:35+09:00` to `2026-10-20T20:22:44+09:00`.
The generated page summarized the synthetic 31°C-to-26°C experiment and identified the source and usage conditions as fictional.
The stored metadata remained unverified despite a usage-conditions paragraph in the source and generated body.
The demonstration did create and use its own search DB, as needed to prove search; no existing personal DB was used.
Temporary originals, wiki, DB, configuration, launcher, and binary were removed after the successful run.
No source-code changes were required for this demonstration. Human acceptance and screen-recording status remain unconfirmed.

## Requested repository document: project-background.md

The user next requested ingestion of the actual `docs/context/project-background.md`.
This run used that repository document, not a synthetic replacement.
No fictional usage conditions were attached to it.
The file was copied byte-for-byte into a fresh isolated source directory so only the requested document was scanned.

- Input size: 36,575 bytes.
- Input SHA-256 before and after: `ecff3bdf1419c9670ffb6a4957833d38089276e11fea20c3965dfdbecdb7100d`.
- Generated page SHA-256: `8613ac0ff227402c24e2f3148295784dcde8acf81c7de90a8bfa3ad0b6f1c560`.
- Real backend: installed Claude CLI, model alias `sonnet`, with the same temporary isolation launcher described above; budget cap USD 0.75.
- Ingest timestamps: `2026-10-20T20:25:38+09:00` to `2026-10-20T20:26:05+09:00`.

| Check | Observed result |
|---|---|
| `cogvault init --config <temporary-config>` | Exit 0 |
| `cogvault ingest --config <temporary-config>` | `scanned=1 digested=1 failed=0`; all other outcome counts 0; exit 0 |
| Generated page | `sources/project-background.md`; title `Project Background (cogvault 배경·역사 문서)`; `type: source`, `category: reference` |
| `cogvault terms sources/project-background.md --config <temporary-config>` | Effective `unverified`; stored record contained only `status: unverified`; exit 0 |
| `cogvault search trigram --config <temporary-config>` | Found the generated page with a relevant search excerpt; exit 0 |
| `cogvault search Go --config <temporary-config>` | Found the generated page and `_schema.md`; exit 0 |
| `cogvault search passthrough --config <temporary-config>` | Found the generated page with a relevant search excerpt; exit 0 |
| Repeated ingest | `scanned=1 digested=0 unchanged=1`; all other outcome counts 0; exit 0 |
| Original preservation | Original bytes unchanged; SHA-256 identical |

The generated summary identified the source as non-canonical background.
It distinguished historical Obsidian/v1 passthrough context from the v2 single wiki root and batch ingest pipeline.
It retained Go selection reasons, trigram search rationale, and pointers to canonical documentation.
These are checks against the source document, not independent validation of its historical claims or linked sources.

Search quality caveat: the two-character query `Go` also matched `_schema.md`, and its generated-page excerpt showed frontmatter rather than the Go rationale.
The pipeline and lookup worked, but this run does not establish precise short-query ranking or ideal excerpts.
No unrelated search changes were made.

The assistant displayed the full generated page in the session.
The temporary source copy, wiki, DB, config, launcher, and binary were removed after verification.
The original repository document was not changed, and no personal wiki, originals, DB, or MCP were used.

## Actual MIT-license evidence on the selected review input

The user requested a test with `docs/research/llm-wiki-for-scientists-review.md` carrying the project's license as usage terms.
The assistant asked which project. The user selected Cogvault, not the reviewed `llm-wiki-for-scientists` project.
No separate reason was supplied.

The assistant read the actual Cogvault `LICENSE`: MIT, copyright 2026 Jaehoon You.
It identifies software and associated documentation, requires copyright and permission notices in copies or substantial portions, and includes an AS IS disclaimer.
The test recorded those terms for the Cogvault-authored review only, not as permission for third-party originals or the reviewed project.
These are transcribed repository-license terms, not fictional license settings or an automatic legal clearance.

The original review remained unchanged. Its exact body was copied after new frontmatter in a temporary input.
That frontmatter recorded purpose, conditions, a LICENSE reference with SHA-256, and the relevant permission/notice quotation.
Status was explicitly unverified; no completed human review or reviewer identity was fabricated.
An adjacent `.txt` copy of LICENSE supplied actual evidence within the isolated directory.
Only Markdown was ingestible.

- Review SHA-256 before and after: `f2dc906bb435e9ac584556561833ebdc12465e4cd886368edaf164ac228255b1`.
- LICENSE SHA-256 before and after: `b9e9c95880ad0293c968412e677b51786507eb684c5a8c5e97bd1ce085023c58`.
- Generated page SHA-256: `270835f44a5813464be920fea5f614e28ff59cce052700211b65cab3c06c770d`.
- Real backend: isolated installed Claude CLI, `sonnet`, USD 0.75 budget cap.
- Ingest timestamps: `2026-10-20T20:29:28+09:00` to `2026-10-20T20:29:41+09:00`.

| Check | Observed result |
|---|---|
| Actual ingest | `scanned=2 digested=1 failed=0 skipped=1`, all other outcome counts 0; exit 0 |
| Evidence file | `.txt` skipped as a disallowed ingest type; no second generated page |
| Generated review | `sources/llm-wiki-for-scientists-review.md`; exit 0 |
| `terms` | Effective `unverified`; stored mapping contained only `status: unverified`; exit 0 |
| Input purpose, conditions, evidence in generated mapping | All absent |
| MIT mention in generated body | Present, qualified to Cogvault's review and summary, not third-party rights |
| Copyright/permission-notice requirement quotation in generated body | Absent |
| `search MIT` | Returned the generated review and `_schema.md`; exit 0 |
| `search copyright` | `No results found.`; exit 0 |
| Originals | Review and LICENSE byte-for-byte unchanged |

**Finding:** execution and conservative status handling passed, but structured evidence retention did not.
The current approved implementation replaces the entire mapping, not just its status.
In this actual output, the model also omitted the notice obligation and evidence quotation from the body.
Consequently, the generated page alone cannot recover the full supplied usage conditions through `terms`.
The MIT search response is not proof of precise license retrieval: its excerpt hit the temporary `mit-input` source path, and the schema was also returned.

This is a limitation of the current design relative to the broader goal, not a claim that the license is unknown or that MIT disallows the intended use.
No unrequested implementation or contract change was applied.
The temporary input, license evidence copy, wiki, DB, configuration, launcher, and binary were removed.

## Correction: preserve declarations and reset review only

The user rejected the reset-all result: explicit usage-permission conditions must be recorded regardless of the write path.
The user's reason was the loss of known terms, not a request for automatic legal judgment.
The corrected contract is `docs/specs/2026-10-20-usage-terms-preservation-amendment.md`.

### Implementation

- `internal/terms` parses/encodes metadata and keeps dates/timestamps as their original text.
- Automatic ingest deterministically preserves structured input records before considering existing wiki records or model candidates.
- Purpose, conditions, scope, and evidence survive; only review status resets to unverified and reviewer/date assertions are removed.
- A missing source record during regeneration retains the previous declaration.
- A valid input declaration overrides omitted, contradictory, or malformed model declarations.
- Storage rejects overwrites dropping or emptying parseable existing purpose, conditions, scope, or evidence fields before file/index mutation.
- URL capture copies explicit declarations into an unverified header, removes remote review claims from the copy, and keeps the raw response body unchanged.
- MCP writes and generated-output callers use the same storage guard. Link-only derived pages do not inherit blanket permission.
- Malformed existing metadata can be repaired; malformed supplied ingest records fail rather than hiding evidence.

### Before/after regression evidence

Observed RED failures included source-record loss, MCP overwrite loss, fetch burying supplied metadata, fetch overwrite loss,
non-text replacements bypassing the retention check, and YAML date conversion changing evidence text.
The original guard also prevented malformed-page repairs; a failing repair regression exposed that issue before correction.
A model's malformed declaration initially blocked a valid source record; a failing precedence regression exposed that before correction.

Final `go test ./...` passed for all tested packages, including the new `internal/terms` package.
Tests cover source/model precedence, text extraction, regeneration, direct storage, real MCP handler with storage/index,
actual capture command execution against loopback HTTP, typed-value bypasses, incomplete evidence, malformed-page repair, and date text.
Two brittle quote-style assertions were removed; parsed source-URL correctness remains covered.
One integration run also exposed an unused test import, which was removed before the passing final suite.

### Real MIT input replay

The same selected review and actual Cogvault MIT evidence were used, with the third-party scope limitation retained.
A temporary loopback HTTP server served only that prepared input. The actual CLI ran `fetch`, then actual Claude `sonnet` ingest.
The server and all data paths were isolated; no personal MCP or existing DB was used.

| Check | Observed result |
|---|---|
| `fetch` of the license-bearing Markdown input | Saved a usable structured record; exit 0 |
| First actual ingest | `scanned=1 digested=1 failed=0`, all other outcome counts 0; exit 0 |
| `terms` after first ingest | Full purpose, conditions, evidence.reference, and exact quotation matched the prepared input |
| Effective status | `unverified`; reasons named pending review only, not missing conditions/evidence |
| `search copyright` | Found the generated review with `Preserve the copyright notice` in its excerpt |
| Actual regeneration after removing the input record | `digested=1 failed=0`; previous conditions and evidence still matched exactly |
| Fresh isolated MCP `wiki_write` without the record | `isError: true`, message named a dropped usage_terms field |
| Page bytes after rejected MCP write | Unchanged |
| Search after rejected MCP write | Still found the notice obligation |

The first ingest ran from `2026-10-20T20:46:02+09:00` to `2026-10-20T20:46:21+09:00`.
Regeneration ran from `2026-10-20T20:47:19+09:00` to `2026-10-20T20:47:34+09:00`.
The isolated MCP was launched directly from the temporary binary/config, not through any personal MCP configuration.
It initialized successfully, rejected the losing overwrite, and exited 0 after its input closed.

Final generated-page SHA-256: `2137cd9e7163dd1f2714599525089604764b229168388c7c4d5b112edc369f33`.
The review and LICENSE remained byte-identical to the input hashes recorded above.
The local HTTP server, test MCP process, temporary originals, wiki, DB, config, launcher, and binary were removed.

### Interpretation and limits

This replay proves exact structured-record retention through the exercised capture, ingest, regeneration, read/search, and MCP write paths.
It does not certify legal permission, evidence authenticity, reviewer identity, or third-party rights.
Text only present in prose still depends on extraction; deterministic carry-through is for the structured terms record.
YAML layout may change, but the inspected values and original date/timestamp text are preserved.
The earlier unquoted-date limitation is superseded: the shared decoder now preserves date text without requiring quotes.
No bulk migration, existing personal-data use, automatic rights inference, or unrelated search-ranking change was added.

### Independent review follow-through

Two bounded read-only reviews examined ingest retention and write/capture boundaries.
They identified two reproducible cases, both corrected with failing-before/passing-after regressions:

1. A filename such as `😀.md` yields a content-hash-derived slug. Changed content produced a new destination and bypassed previous-record retention.
   The ledger now resolves the previous successful/superseded page by source identity, not the new hash-derived path.
   The prior declaration is also checked across destination changes before publishing a new page.
2. A UTF-8 byte-order mark before frontmatter hid the declaration from the shared parser.
   The parser now detects frontmatter consistently with the Markdown adapters, so the overwrite guard and input preservation see the record.

A third static concern about numeric YAML keys breaking inspection JSON did not reproduce.
The exact malformed-conditions case returned successful unverified JSON before any workaround.
The reviewer retracted that concern; no speculative JSON conversion was added, and the boundary regression remains.

The final `go test ./...` after these corrections passed for all tested packages.
These additional tests use synthetic declarations only; they do not assert rights to real works.

### Final capture compatibility check

A final regression exposed a capture compatibility problem: parsing every remote body could reject raw text beginning with malformed YAML,
or remove its original frontmatter from the raw body. The capture now always retains the complete raw response bytes after its header.
Only a parseable supplied terms declaration is also exposed in the capture header, as unverified data.

`TestFetchKeepsRawResponseAndExactSourceURL` failed before this correction and passed afterward.
It also verifies the exact source URL with a space before `#fragment`, without asserting a particular YAML quote style.
The storage contract was clarified: a new declaration must retain populated protected fields or provide non-empty replacements.
Optional `scope` is now documented rather than being an undocumented protected field.

The final compiled CLI fetched two synthetic loopback responses: malformed-YAML text and a declared-terms Markdown document.
Both exited 0 and retained byte-identical raw bodies.
The declared record remained visible as unverified, with remote reviewer claims removed; no search DB was created.
The local server and temporary files were cleaned up. These synthetic conditions are not rights evidence for real material.
The complete `go test ./...` run on this implementation passed for all tested packages.

## CLI and MCP search summaries

The user accepted the existing-structure approach on the condition that both CLI
and MCP search expose the information. The delivered change adds the same
summary to CLI `search` and every MCP `wiki_search` result.

- Applicability: `scope`.
- Audience/destination: `where`.
- Permitted portion or transformation: `level`, as text rather than an ordinal grade.
- Conditions: `conditions`, including restrictions and obligations.
- Evidence location: `evidence.reference`.
- Record review: `status` and `reasons`; never a shareability verdict.

Unknown summary values use `unverified` in MCP and `미확인` in CLI. Known
conditions survive partial records. An unreadable page discards any partial
read bytes and returns an entirely unverified summary. Full quotations,
reviewer, and date remain available through detailed inspection.

Implementation reuses `internal/terms` for inspection and projection. The CLI
and MCP handlers read each matched page through storage rather than adding DB
columns. Query matching, ranking, limits, and existing result fields remain
unchanged. The overwrite guard now also protects supplied `where` and `level`.
The MCP tool description and SPEC, DESIGN, README, and usage guide describe the contract.

### Verification

Before implementation, CLI/MCP regression cases failed because search omitted
the summary. A separate regression failed because overwrites could drop Where
or Level. These cases passed after the change. Additional cases cover missing
values, malformed metadata, no inference from license/access labels, and
discarding partial metadata bytes returned with a read error.

The assistant built and ran the actual CLI with a fresh temporary wiki, DB,
and explicit config. A new owned stdio MCP server used that same isolated
state. Neither process used the personal wiki, search DB, or personal MCP.
Only three authored fictional fixtures were copied into the temporary wiki:

| Fixture | Observed CLI and MCP summary |
|---|---|
| `ready.md` | Exact declared scope, audience, portion, conditions, and evidence reference; `recorded` review state |
| `partial.md` | Unknown scope/Where/Level; existing ambiguous condition and evidence reference retained; `unverified` |
| `unknown.md` | All sharing-summary fields unknown; `unverified` with missing-record reasons |

CLI `search sharing-search-live` and MCP `wiki_search` returned the same three
paths in the same order. Assertions compared each path's scope, Where, Level,
conditions, reference, and status across both actual outputs. Page bytes stayed
unchanged. The MCP process exited successfully; its temporary wiki, DB, config,
and binary were removed.

Two read-only reviewers found no actionable defects in their assigned CLI/shared
logic and MCP/permission scopes. They did not run tests. The assistant's final
`go test ./...` passed for every tested package after the tool-description change.

### Acceptance boundary

This verifies that both search interfaces expose declared sharing conditions
without a separate detail lookup. All example conditions and reviewer identities
are fictional validation settings, not rights evidence for real material.
No LLM call or legal classification was used in this phase.
The user reviewed the fictional CLI search output and stated:
“일단 가상의 검색 기록만 보면 확실하게 구분이 됩니다.”
This records the user's confirmation of distinguishability on that output.
The user clarified that real-material sharing permission is outside this task's
acceptance criteria, not an outstanding completion requirement.
Screen-recording status remains unverified.
Per-result file reads add I/O; no latency benchmark is claimed.

