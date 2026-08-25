# Retro: ingest attention report

- Date: 2026-08-25
- Source: PR #33
- Spec: docs/specs/2026-08-24-ingest-attention-report-design.md
- Plan: docs/plans/2026-08-25-001-feat-ingest-attention-report-plan.md

## Release data

| Metric | Value |
|---|---|
| **Changed non-test lines** | 1160 (added + removed) |
| Commits | 17 |
| Review rounds (unit / final / standalone) | 11 (8 / 1 / 2) |
| Fix rounds | 4 |
| Internal findings (fixed / deferred) | 10 / 1 |
| Pull request comments (fixed / deferred) | 3 / 0 |
| Count completeness | exact |
| CI failures | 0 |
| Duration (first spec commit → merge) | 0.8 days |
| Units planned / completed | 5 / 5 |

## Success criteria: measured vs declared

| # | Declared criterion | Measurement (command / rubric) | Measured result | Verdict |
|---|---|---|---|---|
| 1 | SC1: status lists current-model exhausted and refused files | `go test ./cmd/cogvault -run TestStatusHumanOutput -count=1` | verified: PASS | Met |
| 2 | SC2: status JSON is parseable with the correct attention array | `go test ./cmd/cogvault -run TestStatusJSONOutput -count=1` | verified: PASS | Met |
| 3 | SC3: a newly exhausted file triggers a notification | `go test ./internal/ingest -run TestRunNewlyExhaustedNotifiesOnce -count=1` | verified: PASS | Met |
| 4 | SC4: an already exhausted file does not notify | `go test ./internal/ingest -run TestRunAlreadyExhaustedDoesNotNotify -count=1` | verified: PASS | Met |
| 5 | SC5: a newer success excludes a resolved source | `go test ./internal/ingest -run TestAttentionRowsExported -count=1` | verified: PASS | Met |
| 6 | SC6: only scheduled ingest reaches the notification gate | `go test ./cmd/cogvault -run 'TestRunIngestPassesNotificationGateInputs/(scheduled|interactive)' -count=1` | verified: PASS | Met |
| 7 | SC7: SPEC documents the status command | `rg -n 'cogvault status' SPEC.md` | verified: match at line 783 | Met |

## Carry-forward from previous retro

| Item | Status | Evidence |
|---|---|---|
| Operational verification: F17 topology gate | Done | PR #33 shipping log; `docs/research/v2-follow-ups.md` F17 (T3) |
| Operational verification: F18 external-review gate | Done | PR #33 review artifacts; `docs/research/v2-follow-ups.md` F18 (T3) |

- Reconciliation: registered 0, accounted for 2 — degraded: previous retro has no registration table
- Previous doc shape: pre-schema, exempt

## Interview Transcript

- Independence level: same-model fresh-context
- Rounds used: 1 (max 5)

| ID | Round | Phase | Probe | Answer | Evidence | Verdict (verbatim) |
|---|---|---|---|---|---|---|
| T1 | 1 | 3 | Were all success criteria measured again? | SC1–SC7 were measured fresh and passed. | Fresh targeted tests and `rg` in this retro | `accepted` |
| T2 | 1 | 5 | Was independent review completed? | Three CodeRabbit threads were fixed and resolved. Ten internal findings were fixed. One P3 was deferred. | PR #33 review data; `.release-loop/progress.md` | `accepted` |
| T3 | 1 | 5 | Did the gates execute under their failure conditions? | PR #33 exercised F17 diverged-base and F18 artifact-free review paths. | PR #33 shipping log; `docs/research/v2-follow-ups.md` | `accepted` |

## Findings

### What worked well

- **What happened**: The final branch review found missing command wiring and timeout execution coverage before merge.
  **Why**: The standalone review compared the production seams with the plan scenarios.
  **How to apply**: Re-review external-process wrappers through their public entry point.
  **Cites**: T1 / T2

### What to improve

- **What happened**: One P3 commit-message format defect remains deferred.
  **Why**: Rewriting published history would require a force push without changing runtime behavior.
  **How to apply**: Validate Lore trailers and line lengths before the first push.
  **Cites**: T2

### Process observations

- **What happened**: PR #33 exercised both prior shipping gates in real failure states.
  **Why**: Local base divergence and an artifact-free CodeRabbit status occurred during normal shipping.
  **How to apply**: Mark an operational gate complete only after a real run records its transition evidence.
  **Cites**: T3

## Carry-forward items registered

| Item | Type | Priority | Tracked at |
|---|---|---|---|

## Lessons

- Gate completion needs live transition evidence, resolved review threads, and fresh success-criterion measurements.

## Compounding

- compound invocation: `Documentation complete — docs/solutions/workflow-issues/external-review-status-does-not-prove-review-completion.md`
