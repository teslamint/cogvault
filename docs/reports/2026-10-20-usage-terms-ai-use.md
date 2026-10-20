# AI-use record: usage terms workflow

## Task and boundary

The user requested a three-hour improvement and validation exercise for recording usage terms and evidence.
The problem was unknown reuse conditions, not finding original source locations.
The assistant used repository code and newly authored fictional examples only.
No existing personal wiki, original material, search DB, or personal MCP service was used.
The pre-existing untracked `config.yaml` and `testdata/fixtures/usage-terms/` were not opened or modified.

All example terms and reviewers are fictional validation settings, not evidence for rights to real material.
This exercise does not make legal determinations or classify an existing collection.

## AI role and tools

The assistant inspected source and canonical documentation, proposed alternatives, authored code and examples, and ran validation.
The session reported model `openai-codex/gpt-6.1-sol`.
Tools included repository search/read/edit/write, shell commands for Go and Python execution, and a user-choice tool.
Two bounded subagents handled common ingest normalization and an independent design review.
The parent inspected the delegated implementation, removed a duplicate parse, and ran integration checks.
No browser, desktop control, personal MCP, external evidence lookup, or real LLM ingestion was used.
The first Go CLI smoke command downloaded the declared `golang.org/x/sys v0.48.0` build dependency.
No new application dependency was added.

## Proposal and decision log

| Choice | Proposal and tradeoff | Human decision and reason | Verification |
|---|---|---|---|
| Workflow scope | A: template/manual review; B: template/explicit inspector; C: generation integration | User chose the template and explicit inspector, and required automatic ingest to remain unverified. No separate reason was supplied. | CLI examples and ingest regressions passed. |
| State meaning | Assistant proposed `unverified` and `recorded`; recorded means completeness, not permission. | Assistant implementation choice within the selected scope; no separate human adoption/rejection reason recorded. | Missing or malformed records remain unverified; complete but explicitly unverified records stay unverified. |
| Isolation | Assistant proposed config plus storage only, without search DB or evidence fetching. | Assistant implementation choice under the user's privacy constraint. | Real CLI demo preserved wiki bytes and created no DB. |
| Automatic ingest | Replace the entire model-produced usage record at the common write boundary. Regeneration resets the human record too. | User explicitly required unverified automatic ingest; the reset consequence was disclosed before implementation. No separate reason supplied. | Synthetic model claims and absent records both became unverified in storage and the index. |
| Schema instructions | Keep concise safety instructions and put the full field contract in SPEC.md. | Assistant correction; no human decision requested. | Initial full suite exposed a schema-length failure; concise instructions restored the existing bound. |

The independent design reviewer found no blocking issue. It confirmed storage-based isolation and the common ingest enforcement point against current source.
That review was not human acceptance or legal review.

## AI corrections and limits

- The first CLI regression failed because `terms` did not exist; implementation followed this observed RED result.
- The first ingest regressions exposed retained model claims and absent status; common normalization corrected both.
- An anchored test edit used a wrong line endpoint. The assistant read the damaged region and repaired it before compilation.
- One exploratory regression expected a specific diagnostic for numeric YAML keys. Actual output was already safely unverified; the incidental diagnostic assertion was removed.
- The first full suite failed because the added schema instructions exceeded the existing 2,000-rune bound. The assistant shortened only the new instructions.
- Final `go test ./...` passed. The isolated CLI demo passed five scenarios.

The inspector validates structure, not evidence truth, authenticity, legal meaning, or applicability.
It does not establish that a named reviewer actually performed a review.
Ingest enforcement covers the metadata mapping, not every legal statement that a model could put in prose.

## Human-controlled evidence

Full-screen recording was not started or observed by the assistant.
The session did not independently measure the complete three-hour window.
The user has not yet supplied final acceptance/rejection or reviewed the example outputs in this record.
Do not describe any of these as completed.

For human review, compare the outputs from `python3 -B scripts/demo_usage_terms.py` with `testdata/terms-review-demo/wiki/fictional-policy.md`.

## Additional user-requested live LLM demonstration

After the initial report, the user requested the complete ingest-to-search process.
The earlier statement that no real LLM ingestion was used describes the initial implementation verification only.
For this additional run, the assistant used the actual installed Claude CLI with model alias `sonnet`.
The request sent only a newly authored fictional article and repository-provided schema instructions.
No existing personal originals, wiki, search DB, or personal MCP were used.

The assistant chose a temporary CLI launcher to add safe/restricted operation, empty strict MCP configuration,
Read-only tools, no session persistence, and a USD 0.50 maximum budget.
The launcher executed the real model and did not fake or transform its response.
This was an assistant isolation choice for the user's explicit live-demonstration request; no separate human reason was supplied.

Actual CLI commands initialized a fresh wiki, digested one fictional source, inspected unverified terms,
found the generated page through search, and skipped unchanged input on the second ingest.
The first build failed because it ran outside the Go module; the assistant built from the repository root instead.
All Cogvault runtime commands used the isolated working directory and explicit temporary configuration.
The assistant removed the temporary source, wiki, DB, configuration, launcher, and binary afterward.
Exact observed outcomes and ingest timestamps are retained in the work-result report's additional demonstration section.
The user has not yet recorded final human acceptance or rejection.

## User-requested actual repository document

The user explicitly selected `docs/context/project-background.md` for real ingestion.
This extended the demonstration from authored fictional examples to the named repository document, not to any personal collection.
The assistant copied the exact 36,575-byte file into a new isolated source directory and verified its SHA-256 before and after.
No fictional usage terms were added or inferred for this document.

The actual Claude CLI (`sonnet`) digested the document with the prior safe/restricted launcher and a USD 0.75 budget cap.
The assistant inspected the generated page, checked `terms`, searched `trigram`, `Go`, and `passthrough`, and reran ingest.
All commands exited 0; ingest processed one document, terms remained unverified, and the repeated ingest reported unchanged.
Source-based review confirmed historical-v1/current-v2 separation, not the truth of linked external historical claims.
The short `Go` query also returned the schema and a poor frontmatter excerpt; this limitation is recorded without unrelated code changes.
The assistant verified that the original file was unchanged and removed the temporary runtime files.
No personal wiki, existing source collection, existing search DB, or personal MCP was used.

## License-bearing review input and observed retention gap

The user selected Cogvault's license after the assistant distinguished it from the reviewed project's license.
The assistant read actual LICENSE terms and added a scoped MIT record to a temporary copy of the selected review.
The original review body was retained exactly after new frontmatter. The repository review and LICENSE were not modified.
The input remained explicitly unverified; no human review completion or legal permission was asserted.

The real isolated Claude run digested the Markdown review and skipped the adjacent `.txt` license evidence as configured.
The assistant checked the generated page, `terms`, `search MIT`, and `search copyright`.
The automatic mapping reset removed input purpose, conditions, and evidence. The body kept only a qualified MIT mention and omitted the notice-obligation quotation.
This exposed a gap: preserving unverified status is not equivalent to preserving supplied evidence for later review.
The assistant reported that gap rather than describing all requested outcomes as successful.

The current replacement rule had been disclosed and recorded before implementation.
No new human choice between retaining evidence while resetting status and replacing the entire mapping was obtained in this test.
The assistant did not infer such approval or modify code.
Original checksums were unchanged and the temporary runtime files were removed.

## User rejection and corrected preservation rule

The user rejected the reset-all behavior and stated that explicit usage-permission conditions must be recorded regardless of the path.
This corrected the assistant's interpretation: pending human review is a status, not a reason to delete evidence.
The user did not request automatic legal clearance or a broader private-data classification.

| Proposal or finding | Decision and stated reason | Observed verification |
|---|---|---|
| Reset the entire record during automatic ingest | Rejected by the user because explicit permission conditions must remain recorded across paths | The previous MIT test showed actual condition/evidence loss |
| Preserve declarations while resetting review status only | Implemented under the user's direct correction; no separate approval or invented reason | Exact MIT input record matched `terms` after ingest and regeneration |
| Protect write routes with a shared loss check | Assistant implementation choice for path-independent retention | Before-change MCP/fetch overwrites lost evidence; corrected paths retained or rejected the losing overwrite |
| Record explicit remote declarations as unverified data | Assistant implementation choice; this is not inference from a URL or acceptance of a remote review | Capture regression retains the declaration, forces unverified, and removes remote reviewer/date claims |
| Block all rewrites of malformed existing pages | Rejected during integration because it prevents repair without protecting a parseable declaration | Failing repair regression followed by a passing final suite |
| Source identity instead of generated filename for previous records | Independent review found a concrete hash-slug bypass; corrected | Emoji-filename content-change regression failed before and passed after |
| Normalize numeric YAML mappings for JSON | Not adopted; the claimed failure did not reproduce | Actual malformed-conditions inspection returned unverified JSON; reviewer retracted the finding |

Two implementation slices owned the shared terms/storage boundary and ingest preservation.
The parent integrated capture and inspection, examined their code, corrected typed-value bypasses and date conversion,
and captured additional RED regressions before applying corrections.
Two independent read-only reviewers then checked the integrated result.

The real replay used a fresh loopback HTTP server, actual `fetch`, actual isolated Claude `sonnet` ingest,
`terms`, search, a second real ingest with the source record removed, and a newly launched isolated stdio MCP server.
The MCP test was not a call to a personal MCP instance. Its explicit temporary configuration pointed only to test data.
The losing `wiki_write` request returned `isError: true`; page bytes and searchable evidence stayed unchanged.
Both model calls used the previously documented safe/restricted launcher and USD 0.75 per-call cap.

All temporary servers/processes and runtime files were cleaned up. The selected review and LICENSE stayed byte-identical.
Final `go test ./...` passed, including source/model precedence, regeneration, direct and MCP writes, capture, BOMs, and date text.
Human acceptance, evidence authenticity, reviewer identity, third-party rights, and screen-recording status remain unconfirmed.
Structured records are preserved deterministically; plain-prose extraction is not presented as a guaranteed legal-text detector.

### Capture compatibility follow-through

The assistant reproduced a capture regression involving malformed-YAML text and raw frontmatter bytes before correcting it.
The final capture preserves the entire response body and exposes only a copied explicit terms record in its header.
Remote review claims are removed from the copied record; the raw source is retained as untrusted evidence, not certified permission.
A fragment-bearing source URL is checked by parsed value rather than quote-style assertions.
The assistant clarified that replacement declarations must retain populated protected fields or provide non-empty replacements.
Actual compiled CLI calls against synthetic loopback responses verified raw-body equality and unverified declaration visibility.
The final full suite passed; temporary server/files were removed and no DB or external LLM was used for this compatibility check.

## Search-time sharing inspection

- User decision: the existing-structure approach is suitable if both CLI and MCP
  search expose the sharing information. After reviewing the fictional CLI output,
  the user stated: “일단 가상의 검색 기록만 보면 확실하게 구분이 됩니다.”
  Record this as confirmation that the user could distinguish the examples.
  The user clarified that proving real-material sharing permission is outside
  this task's acceptance criteria, not an outstanding completion requirement.
- Assistant implementation choice: retain the index and DB schema, then enrich
  each result through permission-checked page reads. This avoids a migration at
  the cost of one additional storage read per result.
- Assistant representation choice: reuse applicability, conditions, evidence,
  and review state; add optional text `where` and `level`. Level is not a grade.
  MCP uses `unverified` for unknown fields; CLI renders `미확인`.
- Safety boundary: do not infer permission from MIT, URLs, free access,
  subscriptions, or `recorded`. Preserve known partial declarations. Discard
  partial bytes when page reads fail. Protect Where and Level from silent loss.
- Verification: capture failing CLI/MCP summary and overwrite-retention cases,
  implement shared inspection/projection, then pass the full Go suite.
- Live proof: compile the CLI, initialize a fresh temporary wiki/DB containing
  three fictional examples, and query it through CLI and a newly owned stdio
  MCP server. Compare the six summary values per result across both interfaces.
  All comparisons passed; page bytes remained unchanged; runtime files and the
  owned MCP process were cleaned up.
- Review: two read-only reviewers checked the CLI/shared logic and MCP/permission
  paths. Neither reported an actionable defect; neither ran checks.
- AI contribution: code, regression cases, documentation, and execution analysis.
  No content-generation LLM or personal MCP was used for this phase.
- Human review scope: the recorded confirmation concerns the displayed fictional
  CLI output. It does not assert a separate human review of an MCP view.
  Screen-recording status remains unverified.

