---
name: review-cogvault
description: Review cogvault plans or implementation results against SPEC.md, DESIGN.md, decisions, and tests. Use this when the user asks for a multi-angle review of a plan, code changes, or implementation results in this repository, especially when they want findings first and role-based assessment from junior, senior, staff, and security engineer perspectives.
---

# Review Cogvault

Use this skill only for this repository.

The default output shape is:
- Findings first, ordered by severity
- Open questions or assumptions only if needed
- Role-based assessment:
  - junior
  - senior
  - staff
  - security engineer

Keep the review grounded in repository canon:
- `SPEC.md` for contracts and user-visible behavior
- `DESIGN.md` for package boundaries and implementation intent
- `docs/decisions/` for accepted constraints and deferred items
- `docs/research/` only as supporting context, never as canon

## Mode Selection

Choose one mode before doing deeper work.

- `plan-review`
  - Use when the target is a plan doc, proposal, or implementation outline.
  - Goal: detect contract drift, missing responsibilities, unsafe assumptions, and weak verification plans before coding starts.

- `implementation-review`
  - Use when the target is code already written from a plan.
  - Goal: compare implementation against plan and canon, then check whether tests actually lock the intended behavior at the level that owns the contract.

If the user does not specify a target, identify it from the request first.

## Workflow

1. Identify the review target and mode.
2. Narrow the relevant canon in `SPEC.md`, `DESIGN.md`, and `docs/decisions/`.
3. For implementation review, inspect changed files, tests, fixture data, and the implementation plan together when a plan exists.
4. In implementation review, explicitly check for 3-way drift:
   - plan vs code
   - canon vs code
   - plan vs canon
5. Review through these additional lenses when relevant:
   - performance and operational behavior
   - schema and data migration risk
   - scenario-level regression coverage
   - failure recovery and retry semantics
   - API surface and misuse resistance
6. Prefer contract and security findings over style commentary.
7. Run tests when the review depends on behavior claims.
8. Report only concrete findings. If no findings remain, say so explicitly.

## Review Priorities

Always prioritize these checks:
- Contract mismatch with `SPEC.md`
- Architecture mismatch with `DESIGN.md`
- Security boundary drift: traversal, symlink, excluded paths, permission semantics
- Missing or misleading tests
- Tests that only prove helper behavior while missing the real contract-owning path
- Hidden coupling between layers
- Data-shape drift: path normalization, source type, links, attachments, tags
- Performance and operational regressions: fallback cost, scan latency, query-time consistency overhead
- Schema and storage migration safety: table shape changes, existing DB compatibility, rebuild assumptions
- Scenario-level regressions: write-now vs reindex-later parity, user-visible flows, end-to-end contract locking
- Failure recovery semantics: partial failure handling, retry behavior, stale-data policy, eventual healing
- API usability: public methods that invite misuse, responsibilities exposed too early, unclear ownership
- Review drift: stale plan documents, outdated ADR assumptions, canon updated without matching tests

## Evidence Rules

- Cite file references for every material finding.
- Treat passing tests as supporting evidence, not proof of correctness.
- If behavior is only partially verified, say exactly what remains unproven.
- For implementation review, prefer citing the plan file when it materially differs from code or canon.

## Checklists

Read [references/checklists.md](references/checklists.md) and use the relevant section:
- `Plan Review Checklist`
- `Implementation Review Checklist`

Additionally inspect these angles when the target touches them:
- `Plan Convergence`
  - If an implementation plan exists, does it still match the final code and canon?
  - If not, is the plan stale, or did code drift from the intended design?
- `Performance & Ops`
  - Is the steady-state cost acceptable for the expected vault size?
  - Does any fallback path change complexity enough to affect user-visible latency?
  - Are concurrency and connection-pool choices justified rather than incidental?
- `Migration`
  - Does the plan account for existing on-disk state and schema drift?
  - If schema changes are proposed, is rebuild vs migration explicitly chosen?
- `Scenario Regressions`
  - Do tests lock user-visible equivalence across different code paths?
  - Is there at least one scenario that exercises the intended flow end to end?
- `Failure Recovery`
  - After partial failure, is the next recovery path explicit and testable?
  - Is stale data policy intentional, bounded, and observable to callers where needed?
- `Contract-Locking Tests`
  - Do tests exercise the highest-level path that owns the invariant, rather than only a helper or storage primitive?
  - If a review-established invariant exists, is there a regression test that would fail if the public behavior drifted?
- `API Usability`
  - Does the exposed interface make misuse easy?
  - Could a narrower surface or clearer ownership reduce future drift?

## Output Rules

- Findings must be the first section.
- Severity labels should be explicit: `high`, `medium`, `low`.
- If there are no findings, say `없음` and still include the role-based assessment.
- Keep summaries short. Do not turn the answer into a changelog.
