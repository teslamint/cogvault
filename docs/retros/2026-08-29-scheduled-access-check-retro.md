# Retro: scheduled-access-check

- Date: 2026-08-29
- Source: local merge commit `fcff9ac`
- Spec: docs/specs/2026-08-29-scheduled-access-check-design.md
- Plan: docs/plans/2026-08-29-001-feat-scheduled-access-check-plan.md

## Release data

| Metric | Value |
|---|---|
| **Changed non-test lines** | 976 (921 added + 55 removed) |
| Commits | 21 |
| Review rounds (unit / final / standalone) | 8 (7 / 1 / 0) |
| Fix rounds | 0 |
| Internal findings (fixed / deferred) | 7 / 0 |
| Pull request comments (fixed / deferred) | 0 / 0 |
| Count completeness | exact |
| CI failures | 0 (local validation; no remote CI) |
| Duration (first spec commit → merge) | 8h28m (2026-08-29T07:59:55+09:00 → 2026-08-29T16:27:50+09:00) |
| Units planned / completed | 4 / 4 |

## Success criteria: measured vs declared

| # | Declared criterion | Measurement (command / rubric) | Measured result | Verdict |
|---|---|---|---|---|
| 1 | The CLI proves create, read, and delete access to `wiki_dir` and the database parent without leaving a sentinel. | `go test ./cmd/cogvault -run 'TestAccessCheck'` | verified: exit 0; access-check tests cover create, close, readback, delete, and cleanup failure paths | Met |
| 2 | The CLI enumerates every configured source root and performs a bounded read of each accepted, size-eligible top-level regular file without following symlinks. | `go test ./cmd/cogvault -run 'TestAccessCheck'` | verified: exit 0; tests cover source filtering, one-byte reads, descriptor identity, and no-follow flags | Met |
| 3 | The launchd harness invokes one temporary job twice and fails on a non-zero or timed-out run. | `bash scripts/check-scheduled-access_test.sh` | verified: exit 0; stateful harness tests cover two launches, PID timeout, exact marker, cleanup, and retained failure artifacts | Met |
| 4 | Existing behavior remains green. | `go test -race ./...` | verified: exit 0; all packages passed | Met |
| 5 | The user can distinguish configured-path checks from a complete ingest or AppData check. | Reviewer rubric applied to CLI, script output, README, SPEC, and DESIGN | verified: final review `final:2` at `7497dea` confirmed bounded claims and no TCC persistence or unconfigured-folder claim | Met |
| 6 | A configured network-volume path uses the normal surface probe without mount discovery. | Recording filesystem seam plus static check for `/Volumes` discovery | verified: access-check tests pass; implementation has no `/Volumes` discovery path and only uses configured roots | Met |

## Carry-forward from previous retro

| Item | Status | Evidence |
|---|---|---|
| Document a standing fallback for "webhook review stuck in progress with zero artifacts, not skipped" distinct from the already-documented "skipped" case | Not started | `docs/solutions/workflow-issues/external-review-status-does-not-prove-review-completion.md`; T1 |
| Consider whether decision docs authorizing nontrivial runtime-code implementation should require a companion plan or stated verification bound | Not started | No repository-wide policy record; T1 |

- Reconciliation: registered 2, accounted for 2
- Previous doc shape: conformant

## Interview Transcript

- Independence level: same-model fresh-context
- Rounds used: 1 (max 5; native fresh-context facilitator)

| ID | Round | Phase | Probe | Answer | Evidence | Verdict (verbatim) |
|---|---:|---|---|---|---|---|
| T1 | 1 | 4 | 각 이전 carry-forward 항목의 현재 상태를 증명하는 구체적 artifact와 측정은 무엇인가? | 현재 ledger의 retro는 null이다. merged main `fcff9ac` tree에는 실행 ledger와 reports/evidence가 포함되지 않는다. 마지막 명시 상태는 이전 retro의 두 항목 Not started이다. | `docs/retros/2026-08-28-ingest-tcc-prompts-retro.md:36-42`; progress ledger; `git ls-tree -r --name-only fcff9ac` | 완료로 승격할 근거가 없다. 두 항목을 open/not-started로 carry forward한다. |
| T2 | 1 | 4 | 결정 문서에 companion plan 또는 verification bound가 필요하다는 이전 항목이 해결됐다는 policy artifact를 제시하라. | 이번 feature에는 approved spec과 sealed plan이 있지만 repository-wide policy artifact는 없다. | plan; previous retro; progress ledger | 이번 feature의 사례는 정책 채택 증거가 아니다. policy record 없이는 resolved로 인정하지 않는다. |
| T3 | 1 | 5 | 가장 놀라운 실행 실패와 수정의 인과를 commit, observable failure, regression measurement로 제시하라. | U4 첫 ceremony는 프로세스가 종료된 뒤 stdout marker가 기록되어 harness가 너무 일찍 확인한 false-negative였다. `5c6ddb5`가 동일 timeout 안에서 marker polling을 추가했고 delayed-marker regression은 수정 전 실패·수정 후 통과했다. | U4 real-ceremony evidence; U2 report; `git log --oneline fcff9ac` | 재현 가능한 timing race와 regression이 확인됐다. PID 종료와 command-owned output 관찰을 별도 완료 조건으로 모델링한다. |
| T4 | 1 | 5 | 두 번의 access-check 성공과 production ingest no-popup 관찰의 증명 범위를 분리하라. | U4는 한 temporary GUI LaunchAgent, unchanged signed binary, 두 runs, job/private-directory cleanup만 증명한다. production ingest의 no-popup은 별도 operator observation이며 TCC persistence나 complete-runtime coverage를 증명하지 않는다. | U4 real-ceremony and prompt-observation evidence; scheduled-access spec | 실행 맥락과 claim boundary가 분리됐다. no-popup을 영구 grant나 complete-ingest proof로 승격하지 않는다. |
| T5 | 1 | 5 | U2의 Remaining concern이 U4에서 닫혔다는 exact observation을 제시하라. | U4 evidence는 installed binary, Apple Development identity, unchanged identity, exit 0, run_count 2, temporary job absent, private directory absent를 기록한다. 후속 access-check artifact가 두 실행의 no-popup을 기록한다. | U2 report; U4 evidence | 실행 경계는 닫혔고 prompt 증거도 임시 ceremony에 귀속됐다. 다음 cycle에서는 canonical U4 transcript로 결합한다. |

## Findings

### What worked well

- **What happened**: U2의 delayed stdout marker false-negative를 `5c6ddb5`에서 재현하고 수정했으며, exact-line marker 회귀를 `0323684`에서 추가로 차단했다.
  **Why**: launchd 프로세스 종료와 redirected stdout flush가 서로 다른 시점에 완료될 수 있다.
  **How to apply**: 프로세스 종료와 command-owned output marker를 별도 관찰 조건으로 모델링하고 하나의 timeout window를 공유한다.
  **Cites**: T3; `go test -race ./...`; `bash scripts/check-scheduled-access_test.sh`

- **What happened**: U4 evidence distinguished temporary access-check prompts from production ingest observations.
  **Why**: preflight success cannot prove subprocess, AppData, or TCC database behavior.
  **How to apply**: Record each execution context and claim boundary in separate evidence artifacts.
  **Cites**: T4, T5; U4 evidence

### What to improve

- **What happened**: The first prompt-observation supplement attributed a production-ingest observation to the temporary ceremony and required a P1 review correction.
  **Why**: evidence scope was not encoded in the artifact identity before review.
  **How to apply**: Name the exact launch label and execution context in every runtime observation before using it to supersede another artifact.
  **Cites**: T4, T5; final review `U4-PROMPT-OBSERVATION-WRONG-EXECUTION-SCOPE`

### Process observations

- **What happened**: Both previous carry-forward items remain open after this merge.
  **Why**: no tracker or policy record changed during this feature.
  **How to apply**: Keep both rows in the next retro until their named durable artifacts change.
  **Cites**: T1, T2

## Carry-forward items registered

| Item | Type | Priority | Tracked at |
|---|---|---|---|
| Document a standing fallback for stuck external review with zero artifacts, distinct from skipped | process | P3 | `docs/solutions/workflow-issues/external-review-status-does-not-prove-review-completion.md` |
| Decide and record whether nontrivial decision documents require a companion plan or explicit verification bound | process | P4 | `ROADMAP.md` or a future `docs/decisions/` entry |
| Combine temporary access-check prompt observation, label, and cleanup into one canonical U4 transcript artifact | process | P3 | this retro's carry-forward table |

## Lessons

- launchd success requires two independent observations: the PID has exited and the command-owned output marker has been flushed; one timeout window must govern both.
- A no-popup observation is valid only when its evidence names the exact responsible-process label; production-ingest and temporary-preflight observations cannot supersede each other.

## Compounding

- compound invocation: `Documentation complete — docs/solutions/runtime-errors/launchd-output-marker-after-process-exit.md`

Retrospective complete — docs/retros/2026-08-29-scheduled-access-check-retro.md
