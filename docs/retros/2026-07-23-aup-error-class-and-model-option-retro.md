# Retro: AUP-refusal error class + llm.model option (F6)

- Date: 2026-07-23
- Source: PR #8
- Spec: docs/specs/2026-07-23-aup-error-class-and-model-option-design.md
- Plan: docs/plans/2026-07-23-001-feat-aup-error-class-and-model-option-plan.md

## Release data

| Metric | Value |
|---|---|
| **Changed non-test lines** | 119 (102 added + 17 removed) |
| Commits | 8 (5 code + spec/plan draft+approve; +merge) |
| Review rounds | 6 (5 unit task-reviews + 1 final branch review); 1 fix round (final only) |
| Comments (fixed / deferred) | 4 fixed (final I1 + F6-U5-m1 + M1, and the earlier spec C1/M1/M2) / 12 minors deferred to follow-up |
| CI failures | 0 (no CI; local `go test -race` is the gate) |
| Duration (first spec commit → merge) | ~1h10m (2026-07-23 01:44 → 02:52 KST) |
| Units planned / completed | 5 / 5 |

## Success criteria: measured vs declared

Measured fresh during this retro (enforces: P3).

| # | Declared criterion | Measurement | Measured result | Verdict |
|---|---|---|---|---|
| 1 | AUP refusals terminal, not infinitely retried | `go test -run TestRunRefusedTerminalNoAttempt` + `TestE2ERefusalNotRetried` | both ok (ingest 0.42s, cmd 0.85s) | Met |
| 2 | `llm.model` reaches the CLI | `go test -run TestDigestModelPassthrough\|TestDigestNoModelOmitsFlag ./internal/llm/` | ok 0.38s | Met |
| 3 | Switching models re-attempts refused files | `go test -run TestE2EModelChangeRecoversRefused ./cmd/...` | ok 0.37s | Met |
| 4 | Report distinguishes refused from failed | `go test -run TestE2ERefusalTerminal ./cmd/...` | ok 0.36s (asserts refused=1 failed=0) | Met |
| 5 | No regression | `go test -race ./...` | 11 packages ok, 0 FAIL | Met |

## Carry-forward from previous retro

Previous retro: docs/retros/2026-07-23-refound-capture-pipeline-retro.md (the v2 Phase 1 retro, same day). Its carry-forward items F1-F8 live in docs/research/v2-follow-ups.md:

| Item | Status | Evidence |
|---|---|---|
| F6 (AUP refusal class + model option) | Done | this PR #8 (main f8a5b13) |
| F1 (SC1/SC2 real-world) | Done for SC1/SC2, SC3/SC4 pending | prior retro + docs/research/v2-follow-ups.md |
| F2/F3/F4/F5/F7/F8 | Not started | v2-follow-ups.md (unchanged this cycle) |

- Previous doc shape: conformant (has Interview Transcript with a valid independence level and cited findings)

## Interview Transcript

- Independence level: self-checklist
- Rounds used: 1 (max 5)

| ID | Round | Phase | Probe | Answer | Evidence | Verdict (verbatim) |
|---|---|---|---|---|---|---|
| T1 | 1 | 5 | What did the plan fail to see that a reviewer caught? | Two things, both by the same independent reviewer. Design review caught that the ledger's `CREATE TABLE IF NOT EXISTS` makes a new column silently absent on the existing 63-row DB — reading it in `lookup` would runtime-error; the fix was an explicit `PRAGMA table_info` + `ALTER TABLE ADD COLUMN`. Final branch review caught that no test asserted the refused row's `llm_model` was threaded, so a regression could revive the exact infinite-retry bug F6 exists to fix and still pass every test. | spec review C1; final review I1 → commit 47741e1 | self-attested |
| T2 | 1 | 5 | What almost went wrong but didn't, and what caught it? | The integration of the two independently-scoped parts. Part 1 (classify refusals terminal) and Part 2 (model knob) look separable, but a terminal `refused` row would permanently block retry even after switching models — so Part 2 couldn't recover Part 1's casualties without a bridge. The design-phase integration check surfaced this, adding the per-row `llm_model` column and model-change re-attempt as the necessary bridge, not gold-plating. | spec S3, plan U3 model-gated retry; design integration-check note | self-attested |
| T3 | 1 | 5 | Which criterion's measurement surprised you? | None surprised — all five are test-measured and were designed to be runnable at retro time (unlike the parent v2 retro, whose SC1-SC4 needed a week of real use). The deliberate choice to make F6's success criteria test-backed rather than usage-backed is why this retro could measure Met/Met/Met/Met/Met immediately. | success-criteria section, all Met | self-attested |
| T4 | 1 | 5 | What took longer or diverged from plan? | Nothing diverged materially — 5/5 units, 1 fix round, ~70min. The one honest note: reviewer subagents again went idle without delivering verdicts (f6-review-u4, f6-final-review), requiring re-pings and one orchestrator direct-evidence acceptance. Same reviewer-liveness pattern the parent retro flagged; still unresolved at the harness level. | progress ledger re-ping entries; parent retro "What to improve" | self-attested |

## Findings

### What worked well

- **What happened**: The design-phase integration check ("combine user-stated X + Y and probe the non-obvious downstream consequence") caught that a terminal `refused` class alone would strand files that a later model switch should recover — turning two separately-requested fixes into one coherent design with the `llm_model` column as the bridge.
  **Why**: F6 bundled two improvements; the integration check exists precisely to find where bundled features interact.
  **How to apply**: when a feature request contains "A and B", always run the A×B interaction probe before writing the spec — the bridge between them is where the real design lives.
  **Cites**: T2; plan U3 model-gated retry

- **What happened**: The independent reviewer caught the ledger-migration no-op (C1) at design time and the model-threading test gap (I1) at final review — two defects that would each have shipped a broken feature (runtime error on the real DB; silent revival of the infinite-retry bug).
  **Why**: reviewers were instructed to verify claims against actual code (C1: read ledger.go's DDL; I1: trace which upsert call site the tests actually assert).
  **How to apply**: keep the "verify migration claims against the real DDL, not the intent" and "assert the load-bearing field, not just status/counts" checks in the review rubric.
  **Cites**: T1; commits 47741e1, and the spec C1 revision

### What to improve

- **What happened**: Reviewer subagents twice went idle without delivering their verdict (f6-review-u4, f6-final-review re-verify), requiring orchestrator re-pings and one direct-evidence acceptance to keep the loop moving.
  **Why**: resumed/long-running subagents are unreliable at delivering a final message in this harness — silence is indistinguishable from work-in-progress.
  **How to apply**: for re-verification of small test/comment-only fixes, default to orchestrator direct-evidence verification (grep the assertion, run the named test) rather than round-tripping the original reviewer.
  **Cites**: T4; progress ledger re-ping entries

### Process observations

- **What happened**: F6's success criteria were deliberately test-backed (all five runnable at merge), unlike the parent v2 retro whose SC1-SC4 required a week of real use and remain pending.
  **Why**: a bugfix/hardening feature has a testable contract; a habit-formation feature does not.
  **How to apply**: match criterion type to feature type — demand runnable criteria for mechanical features, accept usage-rubric criteria only when the outcome is genuinely behavioral.
  **Cites**: T3; success-criteria section

## Carry-forward items registered

| Item | Type | Priority | Tracked at |
|---|---|---|---|
| Set llm.model=opus in the real config to route future AUP-tripping files (biomedical/policy content) to a non-refusing model | process | P3 | docs/research/v2-follow-ups.md F6 (mark Done + note) |
| Deferred F6 minors (refused-skip PerFile action distinctness; duplicate ModelUnchanged test; fake argv space-join) | process | P4 | docs/research/v2-follow-ups.md F2 batch |

## Lessons

- A `CREATE TABLE IF NOT EXISTS` is not a migration: adding a column to a table that already exists needs an explicit `PRAGMA table_info` + `ALTER TABLE ADD COLUMN`, or the column silently never appears and the first `SELECT` of it runtime-errors on the existing DB.
- When a feature bundles two independent asks, the bug lives in their interaction: a terminal error class and a model-switch knob are each simple, but "does switching models recover the terminally-failed files?" is the question that actually shapes the schema.
- Test the load-bearing field, not just the status: asserting a refused row's status and attempt count while never asserting its recorded model let a model-threading regression pass every test and silently revive the exact bug the feature fixed.

## Compounding

- compound invocation: not attempted — no reusable lesson this cycle beyond the sqlite-migration point, which is close to the already-documented docs/solutions/database-issues/sqlite-pool-pragma-and-busy-snapshot.md; the migration nuance is folded into this retro's Lessons rather than a new solution doc (moderate overlap, not worth a second DB-issues doc).
