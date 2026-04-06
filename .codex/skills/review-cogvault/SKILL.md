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
  - Goal: compare implementation against plan and canon, then check whether tests actually lock the intended behavior.

If the user does not specify a target, identify it from the request first.

## Workflow

1. Identify the review target and mode.
2. Narrow the relevant canon in `SPEC.md`, `DESIGN.md`, and `docs/decisions/`.
3. For implementation review, inspect changed files, tests, and fixture data together.
4. Prefer contract and security findings over style commentary.
5. Run tests when the review depends on behavior claims.
6. Report only concrete findings. If no findings remain, say so explicitly.

## Review Priorities

Always prioritize these checks:
- Contract mismatch with `SPEC.md`
- Architecture mismatch with `DESIGN.md`
- Security boundary drift: traversal, symlink, excluded paths, permission semantics
- Missing or misleading tests
- Hidden coupling between layers
- Data-shape drift: path normalization, source type, links, attachments, tags

## Evidence Rules

- Cite file references for every material finding.
- Treat passing tests as supporting evidence, not proof of correctness.
- If behavior is only partially verified, say exactly what remains unproven.

## Checklists

Read [references/checklists.md](references/checklists.md) and use the relevant section:
- `Plan Review Checklist`
- `Implementation Review Checklist`

## Output Rules

- Findings must be the first section.
- Severity labels should be explicit: `high`, `medium`, `low`.
- If there are no findings, say `없음` and still include the role-based assessment.
- Keep summaries short. Do not turn the answer into a changelog.
