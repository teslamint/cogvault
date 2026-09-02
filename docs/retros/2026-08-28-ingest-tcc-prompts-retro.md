# Retro: ingest TCC prompts

- Date: 2026-08-28
- Source: local fast-forward merge of `feat/ingest-tcc-prompts`
- Spec: docs/specs/2026-08-27-ingest-tcc-prompts-design.md
- Plan: docs/plans/2026-08-27-001-feat-ingest-tcc-prompts-plan.md

## Release data

| Metric | Value |
|---|---|
| **Changed non-test lines** | 1,166 (1,115 added + 51 removed) |
| Commits | 21 |
| Review rounds (unit / final / standalone) | unavailable — the ignored release-loop ledger was removed with the user-approved worktree cleanup |
| Fix rounds | unavailable — the source ledger is unavailable |
| Internal findings (fixed / deferred) | unavailable — the source ledger is unavailable |
| Pull request comments (fixed / deferred) | 0 / 0 (local merge; no PR) |
| Count completeness | unavailable — the source ledger is unavailable |
| CI failures | not run (local merge; no PR CI) |
| Duration (first spec commit → merge) | 9h02m (2026-08-27T14:41:20+09:00 → 2026-08-27T23:43:40+09:00) |
| Units planned / completed | 6 / 6 |

## Success criteria: measured vs declared

| # | Declared criterion | Measurement (command / rubric) | Measured result | Verdict |
|---|---|---|---|---|
| 1 | The first stable-identity launchd run completes its re-grant round for the observed AppData service. | `sqlite3 "$HOME/Library/Application Support/com.apple.TCC/TCC.db" "select service, length(csreq) from access where client='$HOME/bin/cogvault' and service='kTCCServiceSystemPolicyAppData';"` | verified: fresh query returned one `kTCCServiceSystemPolicyAppData` row with NULL `csreq`; the maintainer selected Allow during the release ceremony | Met |
| 2 | A rebuild after the grants are re-established does not invalidate them. | Prompt-sensitive five-step signed-install and `launchctl kickstart` ceremony from the spec | unverified: re-running would change TCC state and may raise consent prompts, so the exact sequence was not repeated in this retrospective | Partially met — the release ceremony observed no second prompt and zero new rows, but this retrospective has no fresh execution of that sequence |
| 3 | A build without any signing certificate still succeeds. | `make clean && make build CODESIGN_IDENTITY=- && ./cogvault --help` | verified: exited 0; build printed `identity=-` and help rendered | Met |
| 4 | A denied source read is reported as a permission problem. | `go test ./internal/ingest -run 'TestRunSourcePermissionDenied|TestRunSourceDirReadError'` | verified: exit 0 | Met |
| 5 | The full test suite stays green. | `go test -race ./...` | verified: exit 0; all 14 packages passed | Met |
| 6 | The documentation lets an operator perform the grant and the cleanup without asking a question. | Reviewer rubric applied to `README.md` §5 and `docs/solutions/build-errors/macos-sigkill-rebuilt-go-binary.md` | verified: both documents contain copy-pasteable commands, use placeholders rather than personal identifiers, state grant coverage and the `go build` caveat, and warn that `tccutil reset` is broad | Met |

## Carry-forward from previous retro

| Item | Status | Evidence |
|---|---|---|
| Document a standing fallback for "webhook review stuck in progress with zero artifacts, not skipped" distinct from the already-documented "skipped" case | Not started | `docs/solutions/workflow-issues/external-review-status-does-not-prove-review-completion.md` lacks the local CLI fallback; T1 |
| Consider whether decision docs authorizing nontrivial runtime-code implementation should require a companion plan or stated verification bound | Not started | No policy record in `ROADMAP.md`, `docs/decisions/`, `docs/solutions/`, or `docs/research/`; T1 |

- Reconciliation: registered 2, accounted for 2
- Previous doc shape: conformant

## Interview Transcript

- Independence level: same-model fresh-context
- Rounds used: 1 (max 5; native fresh-context facilitator)

| ID | Round | Phase | Probe | Answer | Evidence | Verdict (verbatim) |
|---|---:|---|---|---|---|---|
| T1 | 1 | 4 | The previous retro registered two carry-forward items. What artifact proves each item’s current status? | Both remain Not started. The external-review solution lacks the stuck-webhook to local-CLI fallback, and no tracker or policy record resolves the decision-doc requirement question. This feature used an approved spec and plan, so it did not trigger the decision-only policy question. | Previous retro; external-review solution; current repository search | accepted |
| T2 | 1 | 5 | What almost went wrong in the grant ceremony, and what artifact shows the correction occurred before implementation? | The initial S3 instructed a terminal ingest run and late launchd load. That would attribute consent to the terminal and leave the schedule prompting. | `ef77050` spec; `2b0afc0`; current spec S3 | accepted |
| T3 | 1 | 5 | Which verification assumptions failed when the maintainer-machine observations met the approved plan? | CDHash change and non-empty `csreq` were invalid proxy checks. The corrected contract uses CDHash only diagnostically and measures AppData-row existence, prompt recurrence, and post-rebuild row mutation. | `a407081`, `791d590`, `5f64f49`; current spec Success Criteria 1–2 | accepted |
| T4 | 1 | 5 | What did post-approval review catch that the plan’s earlier reviews missed, and what concrete pre-approval check would have exposed it? | The U2 shell recipe did not preserve a realistic Developer ID value as one argument. A dry run with representative whitespace exposed the defect before implementation. | `70e027c` plan; `1aef320`; `4a30ff6`; current `Makefile` | accepted |

## Findings

### What worked well

- **What happened**: The design review replaced a terminal-driven grant ceremony with a launchd-driven ceremony before implementation.
  **Why**: macOS TCC attributes protected access to the responsible process, so the launcher is part of the permission contract.
  **How to apply**: Reproduce the production launcher when documenting a grant procedure.
  **Cites**: T2

- **What happened**: Maintainer-machine observations invalidated the CDHash-change and non-empty-`csreq` proxy checks before shipment.
  **Why**: neither representation value is guaranteed by a correct stable-identity grant.
  **How to apply**: Measure prompt recurrence and relevant TCC row mutation as outcomes; retain representation values only as diagnostics.
  **Cites**: T3

### What to improve

- **What happened**: The approved plan contained an unquoted signing-identity expansion even though its representative value contained spaces and parentheses.
  **Why**: the review inspected variable flow without rendering the exact documented example.
  **How to apply**: Dry-run every shell recipe with representative whitespace and metacharacters before approval.
  **Cites**: T4

### Process observations

- **What happened**: Both carry-forward process items from the previous retro remain unstarted.
  **Why**: neither item received an implemented tracker outcome in this feature.
  **How to apply**: Preserve both items in the next cycle until their named tracker records a concrete disposition.
  **Cites**: T1

## Carry-forward items registered

| Item | Type | Priority | Tracked at |
|---|---|---|---|
| Document a standing fallback for "webhook review stuck in progress with zero artifacts, not skipped" distinct from the already-documented "skipped" case | process | P3 | `docs/solutions/workflow-issues/external-review-status-does-not-prove-review-completion.md` (extend) |
| Consider whether decision docs authorizing nontrivial runtime-code implementation should require a companion plan or stated verification bound | process | P4 | `ROADMAP.md` or a future `docs/decisions/` entry on decision-doc scope |

## Lessons

- macOS TCC verification should assert prompt recurrence and TCC row mutation, not CDHash difference or AppData `csreq` shape; both representation values can vary independently of grant correctness.

## Compounding

- compound invocation: `Documentation complete — docs/solutions/build-errors/macos-sigkill-rebuilt-go-binary.md`
