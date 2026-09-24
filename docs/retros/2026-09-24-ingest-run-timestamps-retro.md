# Retro: ingest-run-timestamps

- Date: 2026-09-24
- Source: PR #51
- Spec: docs/specs/2026-09-24-ingest-run-timestamps-design.md
- Plan: docs/plans/2026-09-24-001-feat-ingest-run-timestamps-plan.md

## Release data

| Metric | Value |
|---|---|
| **Changed non-test lines** | 603 (571 added + 32 removed; 124 in Go code, the rest spec, plan, SPEC.md, DESIGN.md) |
| Commits | 9 (squash-merged as `5e05184`) |
| Review rounds (unit / final / standalone) | 6 (3 / 2 / 1) |
| Fix rounds | 1 |
| Internal findings (fixed / deferred) | 2 / 1 |
| Pull request comments (fixed / deferred) | 0 / 0 |
| Count completeness | exact |
| CI failures | 2 (SonarCloud Code Analysis on both attempts; the `check` job passed both) |
| Duration (first spec commit → merge) | 0 days (2026-09-24T02:55:59Z → 2026-09-24T04:34:20Z, 1.6 hours) |
| Units planned / completed | 3 / 3 |

## Success criteria: measured vs declared

All measurements ran fresh at `5e05184` on 2026-09-24.

| # | Declared criterion | Measurement (command / rubric) | Measured result | Verdict |
|---|---|---|---|---|
| 1 | A successful run writes exactly one start line and one `result=ok` end line whose counts equal the stdout summary. | `go test -race -count=1 -v ./cmd/cogvault -run 'TestIngestRunTimestamps\|TestBracketIngestRun'`; controls: hard-coded counts, end line removed | verified: 8/8 PASS; hard-coded counts fails `TestIngestRunTimestampsDryRunSuccess`; removing the end line fails 8 tests | Met |
| 2 | Every error return writes a `result=error` end line, including a return before bootstrap. | Same command (missing-config, missing-claude, lock-held, run-error-with-report cases); control: start line removed | verified: all 4 error cases PASS; removing the start line fails 6 tests | Met |
| 3 | A panic writes `result=panic` and still propagates. | Same command (`TestBracketIngestRunPanic`); control: `recover()` removed | verified: PASS; removing `recover()` fails `TestBracketIngestRunPanic` | Met |
| 4 | Stdout output of `cogvault ingest` is unchanged. | `go test -race -count=1 ./cmd/cogvault ./internal/ingest`; `git diff c7a5dfc -- cmd/cogvault/cli_test.go`; control: bracket passed `cmd.OutOrStdout()` | verified: both packages ok; diff 0 lines; the stdout control fails 6 tests | Met |
| 5 | The installed scheduled job writes both lines in production. | `rg -c '^\d{4}-\d{2}-\d{2}T\S+ ingest start origin=scheduled$'` and `rg -c '^\d{4}-\d{2}-\d{2}T\S+ ingest end origin=scheduled result=(ok\|error\|panic)'` on the launchd stderr log after install at 2026-09-24T13:40:14+09:00 | verified: start 1, end 1 (`2026-09-24T13:54:49+09:00 ingest end origin=scheduled result=ok scanned=312 digested=3 failed=2 ...`, equal to the stdout summary); control on the 501 pre-change lines: 0. The run was the launchd restart that `scripts/install-signed.sh` issues, not an interval-timer run | Met |
| 6 | `SPEC.md` §9.4 and the `DESIGN.md` `cmd/cogvault` row describe the lines. | Reviewer rubric: formats, three `result` values, stderr stream, flag-parse exclusion; clock seam and deferred emitter | verified: rubric applied to `SPEC.md:719-733` and `DESIGN.md:683`; every rubric item present | Met |

Goal coverage beyond the per-criterion checks. The goal is to date failure streaks and confirm post-change runs from the stderr log alone. The population is every exit of `runIngest`: 9 return paths plus a panic, a launchd SIGKILL, and a flag-parse error.

- Tests pin the end line on 6 of the 10 handler exits (9 return paths plus panic). The openai-prerequisite, openai-readiness, and `ingest.New` exits have no test. The single deferred emitter covers all 10 by construction (U2 unit review, `reviews/events/U2-unit-1.txt`).
- A SIGKILL writes a start line with no end line. Flag-parse errors write neither line. The launchd plist passes fixed arguments, so scheduled runs cannot hit a flag-parse error.
- The production sample is 1 run, all `result=ok`. No production error run has happened since install, so `TestIngestRunTimestampsRunErrorWithReport` is the only evidence for the motivating failure class.
- The 501 pre-change error lines stay undated. Goal (1) holds only for failures after `5e05184` is installed.
- The spec declared no coverage floor over this population. That gap is recorded as a finding below.

## Carry-forward from previous retro

| Item | Status | Evidence |
|---|---|---|
| Document a standing fallback for stuck external review with zero artifacts | Not started | `docs/solutions/workflow-issues/external-review-status-does-not-prove-review-completion.md` last changed in `7cc8e96` (2026-08-25); PR #51 changed no file under `docs/solutions/workflow-issues/` (T1) |
| Decide whether nontrivial decision documents require a companion plan | Not started | No `docs/decisions/` file in `git diff --stat c7a5dfc 5e05184` |
| Combine temporary access-check prompt observation into one canonical transcript | Not started | No access-check evidence file in `git diff --stat c7a5dfc 5e05184` |
| Execute implementation-time probes at their named plan phase, not defer to proof | Done | Plan U2 step 6 (`docs/plans/2026-09-24-001-feat-ingest-run-timestamps-plan.md:157`) ran its three mutations inside U2 (commit `3625fc8`), before U3 started (T2) |

- Reconciliation: registered 4, accounted for 4

- Previous doc shape: violations recorded as findings

## Interview Transcript

- Independence level: heterogeneous
- Rounds used: 2 (max 5); facilitator `codex exec` (codex-cli 0.156.1); rounds published at `.release-loop/runs/ingest-run-timestamps/reviews/facilitator/round-1.md` (sha256 `5a62f9df…`) and `round-2.md` (sha256 `a53ba412…`)

| ID | Round | Phase | Probe | Answer | Evidence | Verdict (verbatim) |
|---|---|---|---|---|---|---|
| T1 | 1→2 | 4 | 외부 검토가 무산됐을 때의 상시 대체 절차가 이번에도 문서화되지 않았다는 `Not started` 상태를 뒷받침할 영구 산출물과, CodeRabbit CLI 검토를 임시 적용으로만 본 근거를 제시해 주세요. | The named doc last changed in `7cc8e96` (2026-08-25). PR #51 touched 8 files, none under `docs/solutions/`. The CLI fallback ran from an agent skill outside the repo and is recorded only in the gitignored ledger. | `git log -- docs/solutions/workflow-issues/external-review-status-does-not-prove-review-completion.md`; `git diff --stat c7a5dfc 5e05184`; ledger line `external-review — reviewer=CodeRabbit CLI ... decision=satisfied` | accepted |
| T2 | 1→2 | 4 | 구현 단계의 mutation probe를 계획된 U2 단계에서 실행했다는 `Done` 상태를 증명할 계획 단계, U2 보고서, 실행 결과를 연결해 설명해 주세요. | Plan line 157 names three mutations in U2. The U2 report records each failing a named test. U2 completed at 03:36:55Z and U3 at 03:46:22Z, so the probes ran in U2. | Plan line 157; `reports/U2-report.md`; commit `3625fc8`; ledger unit-completion lines | accepted |
| T3 | 1→2 | 4 | `time.RFC3339`가 UTC에서 숫자 오프셋 대신 `Z`를 출력한다는 계획 오류가 승인 전 검증에서 놓친 이유와, 이를 막을 수 있었던 구체적 검증을 설명해 주세요. | Plan Decision 3 (lines 37-41) was true only for non-zero offsets. Every fixture used `time.FixedZone("KST", 9*60*60)`, and the discrimination table varied only the instant, never the zone. A UTC fixture would have caught it; the fix added exactly that test. | Plan lines 37-41; `reviews/events/final-branch-1.txt` (`rfc3339-utc-prints-z-not-numeric-offset`); commit `6825be7` `TestBracketIngestRunUTCPrintsNumericOffset` RED `2026-09-24T01:00:00Z` | accepted |
| T4 | 1→2 | 4 | 이번 변경에서 거의 배포될 뻔했던 결함 하나와 그것을 발견한 정확한 검토·mutation 증거를 설명해 주세요. | Mutation M1 (`return report, err` → `return nil, err` on the Run-error path) passed the full `./cmd/cogvault` suite at `33cb862`. That path is the motivating `_schema.md` failure. The final reviewer's integrity-attack instruction found it, not the unit tests. | `final-branch-1.txt` (`counts-on-run-error-untested`); commit `6825be7` `TestIngestRunTimestampsRunErrorWithReport`; `final-branch-2.txt` M1 re-fails | accepted |
| T5 | 1→2 | 5 | 계획보다 오래 걸린 작업을 시간 구간과 원인별로 설명하고, 다음 계획에서 어떻게 측정 가능하게 줄일지 제시해 주세요. | Design 24 min, plan 10 min, implement 46 min, ship 33 min. The ship time had no plan item. CI attempt 1 failed SonarCloud, which added refactor `db32860`, a standalone review, and a second CI run. Planning never consulted the PR quality gates. | Ledger timestamps 02:41, 03:05, 03:15, 04:01, 04:06:53 (CI 1), 04:11:26 (CI 2), 04:34 (merge) | accepted |
| T6 | 1→2 | 5 | lock fixture 복사를 지시한 계획이 SonarCloud 중복 실패와 기존 테스트 파일 수정 금지의 충돌을 만들었다는 점에서 계획 결함이었는지, 당시 이용 가능한 대안과 함께 평가해 주세요. | Yes. The plan said "copy the lock fixture" and required an empty `cli_test.go` diff. That forced an 18-line duplicate (4.5%, then 4.2%, against a 3% gate). The spec only forbade edits to existing assertions; a shared setup helper was allowed. | Plan U2 scenarios and line 159; SonarCloud duplication `ingest_run_timestamps_test.go:135-152` vs `cli_test.go:436-454`; spec criterion 4 wording | accepted |

## Findings

### What worked well

- **What happened**: The final branch review's integrity attack found mutation M1, which dropped the end-line counts on the exact Run-error path of the motivating `_schema.md` failure. The full unit suite passed with M1 applied.
  **Why**: The reviewer prompt required the cheapest conforming counterexample for each guarantee, not a check of the happy path.
  **How to apply**: Keep that instruction in every final-review prompt, and name the motivating incident's code path as a mandatory attack target.
  **Cites**: T4; criterion 2 measurement

- **What happened**: The three U2 mutation probes ran inside U2, before U3 started, and every one failed a named test.
  **Why**: The plan placed the probes in the owning unit's steps, which applied the previous retro's carry-forward item.
  **How to apply**: Write mutation probes as numbered steps of the unit that owns the guarded code.
  **Cites**: T2; criteria 1, 3, 4 controls

### What to improve

- **What happened**: The plan stated that `time.RFC3339` prints the value's own zone offset. On a UTC host it prints `Z`, which breaks the spec's numeric-offset clause. No test failed until the final review.
  **Why**: Every clock fixture used one zone (KST), and the discrimination table varied only the instant.
  **How to apply**: When a contract names a format property (offset, zone, locale), give the fixture a changed-axis case on that property.
  **Cites**: T3

- **What happened**: The plan told U2 to copy the lock fixture and also required an empty `cli_test.go` diff. The result was an 18-line duplicate and a SonarCloud duplication failure (4.5%, then 4.2%) that the user had to accept at the ship gate.
  **Why**: The plan tightened the spec's "no edits to existing assertions" into "no edits at all", which ruled out a shared setup helper.
  **How to apply**: Copy test constraints from the spec at the spec's strength. Prefer a shared helper to a copied fixture when a duplication gate runs on the PR.
  **Cites**: T6; Phase 2 CI data

- **What happened**: The Ship phase took 33 minutes. The plan had no item for it, and CI attempt 1 failed SonarCloud (cognitive complexity 25, duplication 4.5%). That failure added a refactor commit, a standalone review, and a second CI run.
  **Why**: Planning did not consult the quality gates that run on every PR. Moving the command body into a closure also added one nesting level to every branch.
  **How to apply**: During planning, record the SonarCloud thresholds (cognitive complexity 15, new-code duplication 3%), and add a `gocognit -over 15` check on touched functions to unit acceptance.
  **Cites**: T5; Phase 2 CI data

- **What happened**: The spec's six criteria each had a proving command and a control, but none set a coverage floor over the population of `runIngest` exits. Tests pin 6 of 10 exits, and the production sample holds no error run.
  **Why**: The criteria measured what the design does, not how much of the goal's population it covers.
  **How to apply**: For a goal stated over a class of events, define the class in the spec and add one criterion with a coverage floor and a sample.
  **Cites**: Phase 3 goal-coverage measurement

### Process observations

- **What happened**: The CodeRabbit GitHub bot skipped review again ("excluded by label configuration"). A CodeRabbit CLI review in a detached worktree ran as the fallback (findings 0, 8 of 8 files), but only after a user decision. The repo still documents no standing fallback.
  **Why**: The fallback exists only as an agent skill outside the repo.
  **How to apply**: Carry the item forward. Move the CLI procedure into `docs/solutions/workflow-issues/` when it is next touched.
  **Cites**: T1

- **What happened**: The previous retro (`docs/retros/2026-09-02-pdf-extraction-openai-adapter-retro.md`) has no Interview Transcript section. One of its five findings (the Process observations entry) has no Cites line.
  **Why**: That retro was written without the facilitator protocol that earlier retros in this repo used.
  **How to apply**: Run the pre-commit transcript check from the retrospective protocol before every retro commit.
  **Cites**: Phase 4 backward check (`rg 'Interview Transcript'` returns no match in that file; 5 `What happened` entries, 4 `Cites` lines)

## Carry-forward items registered

| Item | Type | Priority | Tracked at |
|---|---|---|---|
| Document a standing fallback for stuck external review with zero artifacts | process | P3 | `docs/solutions/workflow-issues/external-review-status-does-not-prove-review-completion.md` |
| Decide whether nontrivial decision documents require a companion plan | process | P4 | future `docs/decisions/` entry |
| Combine temporary access-check prompt observation into one canonical transcript | process | P3 | previous retro carry-forward |
| Check PR quality-gate thresholds (cognitive complexity 15, new-code duplication 3%) during planning and unit acceptance | process | P3 | this retro |
| Pin the ingest end line on the openai-prerequisite, openai-readiness, and `ingest.New` exits | edge-case | P4 | this retro |

## Lessons

- Every clock fixture used KST, so `time.RFC3339` printing `Z` on UTC hosts passed all tests. A format contract needs a fixture that changes the property the contract names.
- Mutation M1 removed the counts from the exact failure path that started this work, and the full unit suite still passed. Point the final review's counterexample search at the motivating incident.
- A plan that says "copy the fixture" and "do not touch that test file" has already decided the SonarCloud duplication result before any code exists.

## Compounding

- compound invocation: `Documentation complete — docs/solutions/best-practices/rfc3339-utc-prints-z-not-numeric-offset.md`
- Provenance: the headless compound worker returned a structured result (written path above, `validate-frontmatter.py` exit 0, overlap Low, `CONCEPTS.md` entry "Discrimination axis", `CLAUDE.md` invariant 8) instead of the literal signal line; the line above restates that result. The worker's `AGENTS.md` pointer edit was reverted as redundant with `CLAUDE.md` invariant 8.
