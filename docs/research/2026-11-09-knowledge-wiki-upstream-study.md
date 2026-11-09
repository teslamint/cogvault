# Upstream study: Anthropic knowledge-wiki quickstart (2026-11-09)

Status: research note. It is not canon. `SPEC.md` §11 and `DESIGN.md` §2.9 own the shipped behavior.

## Upstream, pinned

- Repository: `anthropics/claude-quickstarts`, commit `4a4224bdf0e9cfb3332167b69c90457403813625` (main on 2026-11-06), license MIT.
- Folder: [`managed-agents/knowledge-wiki`](https://github.com/anthropics/claude-quickstarts/tree/4a4224bdf0e9cfb3332167b69c90457403813625/managed-agents/knowledge-wiki).
- Files read: `README.md`, `skill.md`, `CLAUDE.md`, `prompts/{lead,analyzer,organizer,baseline}_system.md`, `utilities.py` (`wiki_lint`), the notebook's markdown cells, schema string and steering string.
- The upstream text is treated as data. No upstream code or prompt text is copied; the ideas below are re-expressed for Cogvault. The runtime is Anthropic Managed Agents (a paid API); Cogvault adds no dependency on it.

## What upstream does

It builds a wiki once from a document set with a multi-agent workflow (extractors, resolver, analyzers, organizer). Agents then answer from the wiki read-only. The notebook reports that a small model on the wiki answers within about a point of a larger model on raw documents. Those figures come from upstream's benchmark of 21 questions on one corpus; this study did not reproduce them.

## Comparison with Cogvault

| Upstream idea | Cogvault today | Decision |
|---|---|---|
| Every fact carries `[source \| as-of]`; superseded facts are closed (`valid_to`), never deleted; conflicts stay conflicts; figures on different bases are never merged (schema, extractor rules in the notebook) | Source pages carry `source_path`/`ingested_at`. `research_evidence` records per-claim checks and gaps (ADR 0026). No guidance on conflicting or superseded claims across pages. | **Adopted** as advisory guidance (below). |
| "Not in this document", never a global absence claim | Digest guidance covers scope qualifications on source pages only. | **Adopted** for agent-written pages. |
| A document is data, not instructions | Already in force (ingest and MCP boundaries). | No change. |
| Structural lint of the wiki as a set (`wiki_lint`: dangling links, name/alias clashes, not-indexed, oversized files) | `cogvault lint` reports broken links, orphans, frontmatter, evidence. `_index.md` is a generated view and nothing reads it back (SPEC §2.3). | Not adopted: no new lint API; overlap is already covered. |
| "Where to start" map, "Current picture" block on large nodes, digest atop the escalation list | Retrieval is FTS5 plus `wiki_list`. | Not adopted: layout rules need an eval, and would enter the ingest prompt. |
| Build pipeline: batch ≤120k characters per agent, resolver, analyzers, organizer | Single-pass digest per source. | Out of scope: architecture change. |
| Accepted shared-page line loss (README, "Shared pages": about 5% of lines on the busiest page lose content) | `wiki_write` replaces the whole file. | **Not adopted.** See "Limits". |

## Change

`internal/mcp/server.go` gains a second server-owned guidance constant, `conflictInstructions`, appended to MCP initialize instructions after `evidenceInstructions`. Rules:

1. Keep conflicting or superseded claims; mark `CONFLICT` (same measure, sources disagree) or `SUPERSEDED` (replaced by a later claim); do not average, overwrite or delete one merely to resolve the disagreement (a user-requested removal is not that). Figures on different bases are separate claims, recorded with their basis.
2. Attribute each claim to its own source (a wiki page, or the external source or the user when the claim is not from a page; a user's own observation is its own source), as-of date ("date unknown" if none is stated), and basis.
3. A `sources/` page is a wiki record; report how it was created only as far as its provenance or the user establishes it, otherwise unknown; do not assume it is the original or that the original bytes are archived. `source_path` is only a path value recorded on the page, not an origin and not evidence that the path was collected or is the original or a copy; a URL there is a recorded URL, not verified as the origin. Cite `url`, `source_url` or `canonical_url` as a recorded origin only when the page presents it as the source; cite an origin named in the text as recorded, not verified, a user-supplied origin as user-provided, not verified, and one the agent checked with what was checked; say unrecorded only when no origin exists. Ingest strips model-emitted `url`, `source_url`, `canonical_url` and `author` only for PDFs, so a recorded origin on other pages can be model-written.
4. Scope an absence to the sources checked.
5. `wiki_write` replaces the whole file and has no compare-and-swap: read right before editing, apply the requested changes, keep all other content verbatim, re-read afterwards to detect a lost edit. This narrows the race window and detects some losses; it prevents none.

Placement follows ADR 0026: server-owned guidance reaches every MCP client regardless of a wiki's editable `_schema.md`. It does not enter the ingest digest prompt, so ingest output is unchanged and needs no ingest eval. `default_schema.md` is unchanged because ingest embeds it (`internal/ingest/ingest.go`, `SchemaText`).

Contract test: `TestInitializeInstructionsIncludeServerGuidance` asserts both blocks appear with the default schema and with a schema long enough to be truncated. It does not test the ingest boundary; that rests on the guidance living only in `server.go`.

## Pilot (synthetic, small)

Fixture: three synthetic source pages for one fictional company: a reported FY2025 figure (with `source_url`), a later restatement of the same basis (with `source_url`), and a third-party estimate on a different basis (no recorded origin). Task: answer a two-part question and save a synthesis page. Each run was a fresh subagent on the same model family as this session, given the schema text plus guidance text and its own copy of the fixture. The input was the raw `default_schema.md`, not the exact MCP payload (the server rewrites one sentence), and tool descriptions were absent.

Arms, three runs each: **A** schema plus evidence guidance (before); **B** plus the first wording of the new block; **C** plus the reworded block after a review comment (B said "local copy" for `sources/` pages). The committed wording differs from C in its conflict definition, origin hedge and last bullet; it was checked separately in the next section. The fixture titles say "(local copy)" in all arms, and the fixture targets the behaviors the guidance names. Behavior checks were fixed before the runs. Counts are runs out of 3:

| Check | A | B | C |
|---|---:|---:|---:|
| All three figures kept, none blended | 3 | 3 | 3 |
| Supersession stated | 3 | 3 | 3 |
| No invented origin for the page without one | 3 | 3 | 3 |
| Each figure's source and date on the figure's own line | 1 | 3 | 2 |
| Uses `CONFLICT`/`SUPERSEDED` marker | 0 | 3 | 3 |
| Origin URL on the figure's own line (strict) | 0 | 0 | 0 |
| Scoped absence (grader regex) | 0 | 1 | 0 |

Reading the outputs: the grader's absence regex mis-scores title and question lines, so that row is unreliable. By manual reading, every run in all three arms scoped the absence to the sources checked. Origin URLs appeared outside the figure lines in one B run and one C run. Two C runs described the page as an ingest summary rather than an archived copy. The marker row is mostly explained by the block naming the literal strings, which arm A never saw; the case-sensitive regex also scores a negation ("CONFLICT 아님") as a use and lowercase "superseded" in arm A as none. Arms B and C disagreed on whether the third-party figure is a `CONFLICT` (one run called it one, one said "in the loose sense", one said it was not), which is why the final wording defines the terms.

What the pilot supports: marker words appeared when the block named them, and the per-claim line format counts were 1, 3 and 2 of 3, which is no reliable difference at this size. The baseline already kept all figures and stated supersession, and the origin rule showed no effect (0 of 9 on figure lines; the "do not invent an origin" half already held in arm A). It does not show a quality gain and does not test real wiki content. It has n=3 per arm, one fixture, one model family, and a grader partly keyed to the block's own marker words. The pilot kit lived in a temporary directory and is not committed.

## Fresh-session check of the committed payload

Added to satisfy live verification of an instruction change. It uses the exact payload of the committed server, not a reconstruction: a binary built from this commit was started over stdio on a synthetic wiki (`cogvault init`, then the three fixture pages), and the `initialize` instructions (3,828 characters, including the new block) and the seven `tools/list` descriptions were saved verbatim. Three fresh subagents each received only that payload and a private copy of the wiki, with plain file tools standing in for the MCP tools; no live MCP transport was used. The wiki also held a page by "another agent" with two sections that had to survive. Task: answer the same two-part question, then add the finding to that existing page.

Observations, three runs, no statistics:

- Another agent's two sections were present and unchanged in all three outputs.
- All three kept the $120M, $118M and $132M figures, marked the $120M as `SUPERSEDED`, and scoped the segment-margin absence to the three pages checked.
- No run labelled the $132M different-basis figure a `CONFLICT`; two wrote that it is not one, which matches the new definition.
- No run cited a recorded origin URL or mentioned the origin question, although two fixture pages record `source_url`. The origin rule had no visible effect here, as in the earlier arms.
- Two of three runs said they re-read the page after writing. The third did not mention it.
- The real `cogvault lint` reported one `orphan` per output (the notes page has no incoming links, as in the seeded page) and nothing else.
- Limits: one fixture, one model family, plain file tools instead of MCP, no concurrent writers, and no check of the real wiki. The agents were told to follow the payload, so the result shows the text is followable, not that unprompted clients will follow it.

## Limits

- The guidance is advisory text. No tool enforces it; lint and `wiki_write` do not check for conflict marks.
- `wiki_write` overwrites the whole file and has no compare-and-swap. Reading right before writing narrows the race window and re-reading afterwards detects some losses; neither prevents a lost concurrent edit, and the pilot did not test concurrency. A real guarantee needs a storage-level check (for example a content-hash precondition on write). That is a separate change to a public tool contract and is not part of this one.
- Upstream accepts roughly 5% line loss on shared pages. Cogvault does not adopt that tolerance, and this change does not solve the problem.
- Whether the guidance changes agent behavior on the real wiki is unmeasured.
- After the pilots, CodeRabbit review changed rules 3 and 5 (user-supplied origins; requested edits may remove content), which grew the block to 1,343 characters. Those two bullets were not re-piloted. Two other CodeRabbit suggestions were declined: asserting the guidance's detail wording in tests (incidental-wording pin) and copying the block into three tool descriptions (about 1.3k characters added to every tool listing, and it moves the guidance out of the server-owned placement of ADR 0026).
- Targeted check of the revised bullets (one fresh subagent, the real `initialize` payload of a binary built from the working tree, synthetic fixture): the user supplied an origin URL and asked to remove a private placeholder line. Observed: the line and its now-empty section were removed; the user's URL was recorded as user-provided and not verified, without treating the page's `source_path` as an origin; the new figure cites its source page and date, with the earlier figure kept as `SUPERSEDED`; an unrelated section and an open question stayed verbatim. One run, so it shows the text is followable, not how often.
- Payload versions: the fresh-session check (3 runs) used the feature-commit payload; the targeted check used the payload with the removal clarification and the user-supplied-origin rule. Neither used the later as-of date ("date unknown") and user-as-source wording, which was not piloted; it rests on review only. `TestInitializeInstructionsIncludeServerGuidance` checks the guidance block's marker and its position after the schema, not the wording of these rules.
