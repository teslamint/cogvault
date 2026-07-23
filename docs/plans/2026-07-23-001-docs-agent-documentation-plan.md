---
schema: plan/v1
title: Agent entrypoint and project documentation cleanup
type: docs
status: approved
date: 2026-07-23
execution: non-code
---

# Agent entrypoint and project documentation cleanup plan

## Goal

Make the repository's documentation hierarchy match decisions 0003 and 0012: neutral repository documents own durable context and working conventions, while `CLAUDE.md` and `AGENTS.md` lead with the non-obvious knowledge an agent needs to change this repository safely. Preserve useful historical context, remove stale section-number coupling, and leave every repository-local Markdown link resolvable.

## Architecture notes

- Keep `SPEC.md`, `DESIGN.md`, and `docs/decisions/` as the existing canonical contract, architecture, and decision surfaces. This plan does not change product behavior or reassign those responsibilities.
- Create `docs/context/project-background.md` for useful non-canonical context now embedded in `CLAUDE.md`: project origin, technology choice, prior-art analysis, rejected early alternatives, review-established principles, future directions, risk/pivot details, and historical v1 usage context. A neutral background document is preferred over retaining long context in a Claude-specific entrypoint.
- Create `docs/decisions/0022-repository-working-conventions.md` for durable repository-wide development and verification conventions currently defined only in `CLAUDE.md` section 7. This applies decision 0012 D1 instead of inventing a new agent-only rule source.
- Decision 0022 may use `Status: accepted` only after this plan is approved because it relocates already-active conventions without changing their meaning. If implementation discovers a semantic policy change, stop U1 and separate that change from this cleanup instead of silently accepting it.
- Rewrite `CLAUDE.md` as a high-signal working brief followed by a concise route map. Its first substantive section must summarize repository-specific invariants that are easy to miss: the v2 single-mode boundary (`wiki_dir` vs read-only `sources`), contract-bearing `internal/schema/default_schema.md`, ingest failure/attempt semantics, SQLite DSN and `BUSY_SNAPSHOT` constraints, the single-writer ingest lock, the no-delete safety boundary, and the canon-over-plan precedence. Every summary links to its neutral owner.
- Keep `AGENTS.md` as the pointer/delta document required by decision 0012 D4, but lead with an action-oriented “before editing” checklist that directs Codex/Gemini to the shared working brief and the exact canon for the touched boundary. It may summarize agent actions with canonical citations, but must not reproduce the full explanatory facts from `CLAUDE.md`.
- Any route to `docs/plans/` must label plans as provisional working notes that may become stale and never override `SPEC.md`, `DESIGN.md`, or accepted decisions.
- Update live cross-references that currently depend on `CLAUDE.md` section numbers. Preserve retrospective statements as historical evidence when the statement describes what happened at that time.
- Rejected: mirror the full guidance in both agent files. Decision 0012 D4 already rejects this because synchronization drift is structural.
- Rejected: delete historical material from `CLAUDE.md` without relocation. The history is useful background even though it is not canonical product state.
- Rejected: introduce a documentation generator or new dependency. Five small Markdown surfaces can remain consistent through direct links and repository checks.
- Known Pattern: decisions 0003 and 0012 already define neutral canon, `CLAUDE.md` as background/index, and `AGENTS.md` as pointer/delta. No applicable documentation-cleanup learning exists under `docs/solutions/`; that directory currently contains only a SQLite operational learning.

## Assumption Recheck

No origin spec; no approved live assumptions to recheck.

## File structure

- `docs/context/project-background.md` — neutral, non-canonical project history, future/risk context, superseded v1 background, and an auditable source-to-destination migration inventory. Create.
- `docs/decisions/0022-repository-working-conventions.md` — durable repository-wide coding, dependency, test, and documentation-maintenance conventions. Create.
- `CLAUDE.md` — high-signal shared working brief, Claude-facing route map, and current-state orientation. Rewrite.
- `AGENTS.md` — prioritized Codex/Gemini “before editing” checklist plus pointer-and-delta entrypoint with direct canonical links. Modify.
- `README.md` — project documentation map for human readers. Modify.
- `docs/specs/2026-07-22-refound-capture-pipeline-design.md` — replace the live `CLAUDE.md` section-number risk reference with its canonical decision reference. Modify.
- `docs/research/o1-headless-pdf-verification.md` — remove the live `CLAUDE.md` section-number dependency while preserving the research conclusion. Modify.

## Content preservation map

| Current `CLAUDE.md` section | Destination after cleanup |
|---|---|
| 0. Canonical Context | `CLAUDE.md` working context/document map; canon/plan/agent-file ownership backed by decisions 0003 and 0012; the “update README and convention references after a completed step” obligation owned by decision 0022 |
| 1. 프로젝트 기원 | `docs/context/project-background.md` |
| 2. 왜 Go인가 | `docs/context/project-background.md` |
| 3. 선행 구현 분석 | `docs/context/project-background.md` |
| 4. 기각된 대안과 이유 | `docs/context/project-background.md`, with links to accepted decisions where they now own the outcome |
| 5. 리뷰에서 확립된 원칙 | concise working-context items in `CLAUDE.md`; full rationale in `docs/context/project-background.md`; durable rules linked to existing decisions or decision 0022 |
| 6. Roadmap (v2) | current-state summary in `CLAUDE.md`; full future-direction list in `docs/context/project-background.md`; accepted boundaries linked to decisions 0014 and 0021 plus `docs/research/v2-follow-ups.md` |
| 7. 코딩 컨벤션 | decision 0022, linking the existing specialized decisions |
| 8. Known risks and pivot paths | highest-impact operational traps in the `CLAUDE.md` working context; complete risk/pivot table in `docs/context/project-background.md` with canonical references |
| 9. 에이전트 사용 시나리오 | `docs/context/project-background.md`, explicitly labeled v1 historical behavior |
| 10. 프로젝트명 | `docs/context/project-background.md`, explicitly labeled historical decision context |
| 11. 구현 시작 체크리스트 | `docs/context/project-background.md`, explicitly labeled completed/superseded v1 setup history |

Implementation must inventory every heading, table row, checklist item, and unique bullet in the current file against this map. The final `docs/context/project-background.md` must retain that item-level inventory as a migration appendix with each source item, final destination, and disposition (`moved`, `summarized with canonical link`, or `retained in agent briefing`). Text already owned verbatim by canon may become a link, but no unique rule, rationale, risk, future direction, or historical fact may disappear without being listed under Deferred to Follow-Up Work.

## Scenario coverage map

No origin spec exists and therefore there are no User Scenario IDs to map. Observable verification is defined in each non-code unit's acceptance criteria.

## Implementation Units

## U1: Move durable and historical content to neutral documents
Files:
  Create/Modify: `docs/context/project-background.md`, `docs/decisions/0022-repository-working-conventions.md`
Steps:
  1. Inventory every heading, table row, checklist item, and unique bullet in the current `CLAUDE.md` against the plan's Content preservation map before rewriting the source file.
  2. Write `docs/context/project-background.md` with the still-useful origin, Go choice, prior-art analysis, rejected early alternatives, review-established principles, future directions, complete risk/pivot details, and explicitly labeled v1 historical context from the current `CLAUDE.md`; add a migration-inventory appendix recording every inventoried source item, final destination, and disposition.
  3. After this plan is approved, write accepted decision 0022 with the repository-wide coding, dependency, verification, and documentation-maintenance conventions currently carried by `CLAUDE.md` section 7, linking existing specialized decisions rather than duplicating their full contents; stop and separate the work if relocation would change policy meaning.
  4. Self-review both documents against decisions 0003 and 0012; confirm background is labeled non-canonical, the section 0 documentation-maintenance obligation is owned by decision 0022, every durable rule has a neutral owner, and every inventoried source item appears in the migration appendix with a destination and disposition.
  5. Commit: `docs(context): give project history and working rules neutral owners`
Acceptance: `docs/context/project-background.md` labels superseded v1 material as historical and contains every unique context item assigned to it by the preservation map; its migration appendix has one auditable entry for every current heading, table row, checklist item, and unique bullet with zero blank destinations or dispositions; decision 0022 is `Status: accepted`, owns the section 0 documentation-maintenance obligation, and maps every current `CLAUDE.md` section 7 convention to either its own text or an existing specialized decision without adding a new policy.

## U2: Prioritize non-obvious working context in agent entrypoints
Files:
  Create/Modify: `CLAUDE.md`, `AGENTS.md`, `docs/specs/2026-07-22-refound-capture-pipeline-design.md`, `docs/research/o1-headless-pdf-verification.md`
Steps:
  1. Rewrite `CLAUDE.md` so its first substantive section is a compact “working context” briefing covering the v2 path/trust boundary, runtime schema asset, ingest error/attempt semantics, SQLite concurrency traps, ingest lock, no-delete safety rule, and canon-over-plan precedence with a canonical reference beside every item.
  2. Follow the working context with a route map to `SPEC.md`, `DESIGN.md`, `CONCEPTS.md`, decisions, research, solutions, plans, the v2 design, project background, and repository working conventions; label plans as non-canonical working notes that may become stale, and retain only current-state orientation and actual Claude-specific deltas.
  3. Update `AGENTS.md` so its first substantive section tells Codex/Gemini to read the shared working context before editing and gives an action-oriented checklist for choosing the contract owner, checking the runtime schema asset on contract changes, and applying the concurrency/failure-semantic references when those boundaries are touched; keep the agent-difference section explicit without copying the full `CLAUDE.md` briefing or referring to numbered sections.
  4. Replace live `CLAUDE.md` section-number references in the approved v2 design and O1 research note with canonical decision references or self-contained wording. Leave retrospective prose unchanged when it records a historical action.
  5. Self-review against decision 0012 D3-D4 and verify that each non-obvious briefing item has a neutral owner, the action checklist is not a duplicated handbook, and neither agent file becomes a second product canon.
  6. Commit: `docs(agents): prioritize repository-specific working context`
Acceptance: `CLAUDE.md` is at most 140 lines and presents all seven named non-obvious invariants before the general documentation map; `AGENTS.md` is at most 60 lines and presents the before-editing checklist before general pointers; both identify `docs/plans/` as non-canonical working notes; `rg -n 'CLAUDE\.md (Section|section|§)|CLAUDE\.md와의' CLAUDE.md AGENTS.md docs/specs docs/research` reports no live section-number coupling outside explicitly historical records; no repository-wide working convention remains solely owned by an agent-specific file.

## U3: Publish the documentation map and verify the repository
Files:
  Create/Modify: `README.md`
Steps:
  1. Expand README's Project docs section into a compact responsibility map covering canonical contracts, neutral background, decisions, concepts, and agent entrypoints.
  2. Run a repository-wide check over every tracked Markdown file: ignore external URLs and in-page anchors, resolve each repository-relative link from its source file, and require zero missing file targets; inspect the final diff for duplicated guidance and accidental product-contract changes.
  3. Run the docs-specific ownership, line-count, stale-section-reference, and repository-wide link checks before running `go test -race ./...` as a secondary regression gate for embedded Markdown assets and repository behavior.
  4. Commit: `docs(readme): expose the documentation ownership map`
Acceptance: the repository-wide Markdown link check reports zero missing file targets; `rg -n '^## (프로젝트 기원|왜 Go인가|선행 구현 분석|코딩 컨벤션|에이전트 사용 시나리오)' CLAUDE.md` returns no matches; every row in the Content preservation map is satisfied and the migration appendix has zero source items without a final destination/disposition; the U1/U2 ownership and line-count checks pass; `go test -race ./...` passes after the documentation checks are green.

## Mutation/failure-state matrix

No stateful ceremony in the deliverable; no mutation/failure-state matrix required.

## Deferred to Follow-Up Work

- Translating all historical Korean prose to English; language normalization is independent of documentation ownership.
- Reorganizing `docs/research/`, `docs/decisions/`, or the product canon beyond links required by this cleanup.
- Adding generated documentation, a Markdown linter, or a link-checking dependency.
- Updating historical retrospective wording that accurately records the repository state at the time.

## Open unknowns

### Planning-time

None.

### Implementation-time

- Exact final line counts below the acceptance ceilings depend on removing duplication during the rewrite.
- Historical paragraphs that repeat an accepted decision will be shortened or linked during U1/U2 self-review while preserving unique rationale.
