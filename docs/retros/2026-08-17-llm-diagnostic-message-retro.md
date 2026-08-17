# Retro: llm-diagnostic-message

- Date: 2026-08-17
- Source: PR #29
- Spec: `docs/specs/2026-08-17-llm-diagnostic-message-design.md`
- Plan: `docs/plans/2026-08-17-001-fix-llm-diagnostic-message-plan.md`

## Release data

| Metric | Value |
|---|---|
| **Changed non-test lines** | 1,150 (1,099 added + 51 removed) |
| Commits | 17 |
| Review rounds | 1 |
| Comments (fixed / deferred) | 0 / 0 |
| CI failures | 0 |
| Duration (first spec commit → merge) | 0.16 days (3h 49m 29s) |
| Units planned / completed | 3 / 3 |

## Success criteria: measured vs declared

| # | Declared criterion | Measurement (command / rubric) | Measured result | Verdict |
|---|---|---|---|---|
| 1 | The observed weekly-limit JSON reports its reset message while remaining transient and not refused; generic API, rate-limit, and authentication errors follow the same class. | `go test ./internal/llm/ -run 'TestDigestStructuredTransientDiagnostic|TestDigestGenericAPIErrorNotRefused' -v` | verified: exit 0; both named tests and persistable/classification-only subtests passed on merged `main` | Met |
| 2 | Only the final error-eligible result can supply or classify structured output; stale non-final text, completed content containing refusal phrases, and malformed JSON-looking stdout cannot override the final result or stderr. | `go test ./internal/llm/ -run TestDigestDiagnosticEventEligibility -v` | verified: exit 0; all four event-eligibility subtests passed on merged `main` | Met |
| 3 | Plain and structured policy-specific refusals remain `ErrRefused` on exit-zero, nonzero, and mixed-stream paths, while `connection refused` remains transient. | `go test ./internal/llm/ -run TestDigestRefusal -v` | verified: exit 0; refusal precedence, eligibility, canonicalization, mutation negatives, and exit-zero/nonzero cases passed | Met |
| 4 | Fallbacks preserve stderr, then non-JSON stdout, then the process error; normalization replaces terminal controls and enforces the exact 2,000-rune inclusive bound. | `go test ./internal/llm/ -run 'TestDigestNonzeroExitDiagnosticFallbacks|TestNormalizeCLIDiagnostic' -v` | verified: exit 0; all fallback cases and ASCII/Korean 1,999/2,000/2,001-rune boundaries passed | Met |
| 5 | The ingest report and ledger `last_error` contain the same normalized weekly-limit reason, with status `failed` and attempts `0`. | `go test ./cmd/cogvault/ -run TestIngestNonzeroExitDiagnostic -v` | verified: exit 0; weekly-limit, scheduled, stderr, safety-shaping, false-envelope retry, and same-hash replacement subtests passed | Met |
| 6 | The complete repository remains regression-free. | `go test -race ./...` | verified: exit 0; all 13 packages passed on merged `main` | Met |

## Carry-forward from previous retro

| Item | Status | Evidence |
|---|---|---|
| Real remote MCP interoperability smoke with the chosen IdP and tunnel, from both Claude and ChatGPT | In progress | `docs/research/v2-follow-ups.md` F15 remains Open; PR #29 contains no hosted two-client round-trip artifact (T-01) |

- Reconciliation: registered 1, accounted for 1
- Previous doc shape: conformant

## Interview Transcript

- Independence level: same-model fresh-context
- Rounds used: 2 (max 5)

| ID | Round | Phase | Probe | Answer | Evidence | Verdict (verbatim) |
|---|---|---|---|---|---|---|
| T-01 | 1→2 | 4 | Did PR #29 fire or satisfy F15, and what exact evidence closes it? | PR #29 did not fire or satisfy F15. No artifact proves both hosted Claude and ChatGPT completed `initialize` plus `tools/call` through one chosen IdP and stable HTTPS tunnel. Owner remains the user/operator after choosing the IdP; F15 remains Open until both hosted-client artifacts exist. | `docs/research/v2-follow-ups.md` F15; previous retro registration; PR #29 artifact set | accepted |
| T-02 | 1→2 | 5 | How did the greedy provider-envelope regex survive earlier gates, and what now prevents recurrence? | The mutation set covered generic quoted, negated, suffix, and stale cases but omitted leading prose inside the possessive provider envelope. Plan scenario completeness should have required a counterexample for each grammar branch. `4828f61` narrowed the accepted envelopes and `46ea4a6` added the two exact adapter and same-hash ingest recovery mutations. | `675a217`; `4828f61`; `46ea4a6`; `internal/llm/claudecode_test.go:363-376`; `cmd/cogvault/ingest_integration_test.go:170-218` | accepted |
| T-03 | 1→2 | ship | Why did squash merging make local `main` unable to fast-forward, and how was recovery kept safe? | The branch started from local `main` at `afe2048`, two commits ahead of remote `cb9aab2`, so PR #29 included those commits. Squash created new topology at `df5da99`. After typed authorization, recovery created `backup/main-before-pr29-reconcile-20260817`, reset local `main` to the exact merge SHA, proved equality, and reran the full race suite. A pre-push remote-base topology check would have exposed the divergence. | Progress ledger; PR commit list; backup branch at `afe2048`; synchronized `main` and `origin/main` at `df5da99`; fresh `go test -race -count=1 ./...` | accepted |
| T-04 | 1→2 | review / ship | What sequencing ambiguity let merge finish before a requested external review, and what gate separates internal review from external review? | The ledger's one review round was internal, while GitHub had zero reviews and threads. A green CodeRabbit status was treated like completion even though its comment required manual review. The remote merge completed before the later wait request; interruption could not cancel it. Shipping must record external review as required or waived and, when required, fetch completed review artifacts rather than accept a status context. | Progress ledger; PR #29 timestamps and review objects; CodeRabbit closed-PR response; `docs/solutions/workflow-issues/external-review-status-does-not-prove-review-completion.md` | accepted |

## Findings

### What worked well
- **What happened**: The final branch review constructed two provider-envelope counterexamples that earlier unit reviews and green success-criteria tests missed, and the approved fix added both adapter classification and same-hash ledger recovery proof.
  **Why**: The final reviewer inspected the whole grammar and state consequence rather than accepting generic negation fixtures as coverage of every branch.
  **How to apply**: For every anchored policy grammar branch, require a matching positive and a leading-prose/negation mutation that proves terminal state cannot be entered accidentally.
  **Cites**: T-02 / Phase 3 data

### What to improve
- **What happened**: The feature branch inherited two local-only `main` commits, so the squash PR included unrelated completed work and left the base checkout unable to fast-forward after merge.
  **Why**: Worktree creation validated the local base but shipping did not compare that base with its remote-tracking branch before push.
  **How to apply**: Add the F17 remote-base topology gate before PR creation and stop on unexpected left/right commits.
  **Cites**: T-03 / PR #29 release data
- **What happened**: Merge completed with zero GitHub review objects or threads; the later CodeRabbit request failed because the PR was already closed.
  **Why**: A green integration status, internal review, and external review artifact were treated as interchangeable signals.
  **How to apply**: Add the F18 required-versus-waived external-review gate and fetch reviews, comments, and threads separately immediately before merge approval.
  **Cites**: T-04 / PR #29 remote review data

### Process observations
- **What happened**: F15 remained Open without being silently credited by the new local integration tests.
  **Why**: The retrospective compared the deployment-layer stop condition with actual hosted-client artifacts instead of substituting lower-layer protocol evidence.
  **How to apply**: Preserve claim layers when reconciling carry-forward work; local tests cannot close a hosted-deployment criterion.
  **Cites**: T-01 / Phase 4 reconciliation

## Carry-forward items registered

| Item | Type | Priority | Tracked at |
|---|---|---|---|
| Real remote MCP interoperability smoke with the chosen IdP and tunnel, from both Claude and ChatGPT | feature | P2 | `docs/research/v2-follow-ups.md` F15 |
| Bound Claude CLI stdout/stderr before buffering without truncating legitimate generated pages | performance | P3 | `docs/research/v2-follow-ups.md` F16 |
| Detect local-base versus remote-base topology divergence before PR creation | process | P2 | `docs/research/v2-follow-ups.md` F17 |
| Require external-review artifacts or an explicit waiver before merge approval | process | P2 | `docs/research/v2-follow-ups.md` F18 |

## Lessons

- A negation test for one grammar branch does not protect another; every terminal-classification envelope needs its own adversarial mutation and state-retry proof.
- A clean local base is not a clean PR base; compare it with the remote-tracking branch before push or squash will preserve content while severing fast-forward topology.
- A green reviewer status proves only that the integration finished; required external review needs reviewer-authored artifacts or an explicit waiver before merge.

## Compounding

- compound invocation: `Documentation complete — docs/solutions/workflow-issues/external-review-status-does-not-prove-review-completion.md`
