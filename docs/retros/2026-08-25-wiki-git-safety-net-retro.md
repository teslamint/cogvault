# Retro: wiki git safety net (0024)

- Date: 2026-08-25
- Source: PR #34, #35, #36, #37
- Spec: docs/decisions/0024-wiki-git-safety-net.md
- Plan: none

## Release data

| Metric | Value |
|---|---|
| **Changed non-test lines** | 77 (added + removed, `config.go` + `tools.go` + `ingest.go` production code) |
| Commits | 4 (one squash-merge commit per PR) |
| Review rounds | 0 human/bot review threads; 1 local `coderabbit review --committed`/`--uncommitted` CLI pass per PR (4 total), plus 1 CLI mutation-testing round found by the user's own advisories |
| Comments (fixed / deferred) | 0 / 0 (webhook CodeRabbit skipped on all four PRs — repo has fewer than 10 stars) |
| CI failures | 0 (`check`/GitGuardian green on every PR; the one failure observed was a local `go test -race -count=1 ./...` flake on the merged `main` tree between PR #34 and #35, not a CI run) |
| Duration (first commit → final merge) | 9h15m (PR #34 opened 14:20:20Z → PR #37 merged 23:35:21Z) |
| Units planned / completed | 1 / 1 (decision 0024: opt-in `git.auto_commit`), plus 3 unplanned fix-forward units (#35/#36/#37) surfaced by post-merge verification and user advisories |

## Success criteria: measured vs declared

Decision 0024 has no `## Success Criteria` section — it pre-dates the
`designing` skill's spec template and states a Status/deliverable list
instead. Per this template's rule ("do not reconstruct criteria after the
fact"), this section is skipped rather than mapping that deliverable list
onto criteria it was never written to satisfy.

**Fresh verification (informational only, not a declared-criteria
substitute)** — re-run during this retro, on `main` HEAD `d1100c7`:

| Command | Result |
|---|---|
| `go test ./internal/config/... -run TestGitAutoCommit -v -count=1` | PASS (3 subtests) |
| `go test ./internal/mcp/... -run TestGitAutoCommit -v -count=1` | PASS (6 subtests) |
| `go test ./cmd/cogvault/... -run TestIngestGitCommit -v -count=1` | PASS (7 subtests) |
| `gofmt -l cmd internal && go vet ./... && go build ./... && go test -race -count=1 ./...` | PASS, 3x back-to-back |
| Both `..._SlowAddDoesNotStarveCommitTimeout` tests under synthetic 16-core CPU-spin load | PASS, 3x each |

## Carry-forward from previous retro

| Item | Status | Evidence |
|---|---|---|
| (none registered) | — | — |

- Reconciliation: registered 0, accounted for 0 — previous retro (`2026-08-25-ingest-attention-report-retro.md`) registered zero carry-forward items
- Previous doc shape: conformant (Interview Transcript section present, `same-model fresh-context`, 1 round)

## Interview Transcript

- Independence level: heterogeneous
- Rounds used: 1 (max 5)

| ID | Round | Phase | Probe | Answer | Evidence | Verdict (verbatim) |
|---|---|---|---|---|---|---|
| T1 | 1 | 5 | What took longer than planned? | No repository artifact records an original estimate or elapsed duration for #34–#37. | codex facilitator search of repo artifacts | `no evidenced answer` |
| T2 | 1 | 5 | What almost went wrong but did not? | The timing-margin fix required a second correction: #35 widened margins, #36 widened again to 3x measured worst-case overhead after the first widening was found insufficient. | PR #35, #36, `docs/decisions/0024-wiki-git-safety-net.md` Status section | `accepted` |
| T3 | 1 | 5 | What would you do differently? | Stop widening after the documented redesign threshold rather than create a fourth widening PR; #37 records that cap. | PR #37, `docs/decisions/0024-wiki-git-safety-net.md` | `accepted` |

## Findings

### What worked well

- **What happened**: The user's advisories caught two real defects the agent's own mutation-testing did not initially surface: `git -C wikiDir add -A` (no pathspec) staging files outside `wikiDir` when nested in a larger repo, and `gitAutoCommit`/`postIngestGitCommit` sharing one `context.WithTimeout` across `add` and `commit`.
  **Why**: Both were reproduced empirically in `/tmp` scratch repos and via `exec.CommandContext` timing experiments before being accepted as real, per each advisory's instruction to "verify against the CLI, not against the claim."
  **How to apply**: When a reviewer (human or advisory) names a specific failure mode, reproduce it in isolation before trusting or dismissing it — both fixes in this session came from doing exactly that.
  **Cites**: T2, PR #34 commit `c2ac8fa` (add/commit timeout split), PR #34 commit (`-- .` pathspec fix)

- **What happened**: GitHub's webhook CodeRabbit review stalled "in progress" for over 7 hours on PR #34 with zero artifacts; switching to the local `coderabbit` CLI (`coderabbit review --committed --base main --agent`) produced a real review in under 2 minutes and found 3 legitimate findings on the first PR.
  **Why**: The repo's F18 external-review gate anticipates artifact-free webhook status but had no documented fallback for "genuinely stuck, not skipped."
  **How to apply**: When a webhook-based review integration is stuck (not skipped, not failed — just silent), the CLI equivalent is a faster and more reliable substitute; this is worth documenting as a standing fallback rather than rediscovering per-session.
  **Cites**: T2, `internal/mcp` CLI-found findings (shared-timeout bug, wiki_delete-always-commits doc claim)

### What to improve

- **What happened**: PR #35's first timing-margin widening (250ms→500ms budget, 50ms→200ms margin) was itself insufficient and required PR #36 to widen again to 1500ms/600ms margin, after the user's advisory pointed out the 200ms margin was within noise of measured fork/exec overhead (68-206ms under synthetic load).
  **Why**: PR #35's margin choice was based on "5 back-to-back passes," which is weak statistical evidence for a timing-sensitive test — it never measured the actual overhead it needed to exceed.
  **How to apply**: For any wall-clock-discriminated test, measure the mechanism's overhead directly (e.g. `exec.CommandContext` fork/exec cost under the worst realistic contention) before choosing a margin, rather than picking a number and validating by repeated runs. Repeated-run confidence and a measured worst-case bound answer different questions.
  **Cites**: T2, T3, PR #35 vs PR #36 diff

- **What happened**: A fourth PR (#37) was opened for a comment-only, zero-behavioral-change diff (documenting the widening history and adding a "do not widen a fourth time" guard), running the full CI + external-review-gate ceremony for a docs-only change.
  **Why**: The session defaulted to the established fix-forward-PR pattern from #35/#36 without re-evaluating whether a comment-only diff qualified for the repo's documented direct-to-main lane (used by the 0024 draft commit itself).
  **How to apply**: Before opening a PR, ask "does this diff change anything a test can fail on?" — if no, weigh a direct-to-main commit (per this repo's demonstrated docs-commit-direct convention) against PR ceremony explicitly, rather than defaulting to the pattern already in motion. Codified in `cogvault-improvement-scan`'s new §5.
  **Cites**: T3, PR #37 (32 additions / 12 deletions, 0 behavioral change)

### Process observations

- **What happened**: The session ran 4 sequential PRs (#34→#35→#36→#37) across roughly 9 hours, each triggered by either a post-merge verification failure or a user advisory, rather than being planned upfront.
  **Why**: Decision 0024 had no plan document and no declared success criteria beyond its "Status" section's deliverable list, so there was no upfront scope boundary to catch "the timing margin needs empirical measurement, not iteration" before the second widening.
  **How to apply**: A decision doc that authorizes runtime-code implementation work (not just a durable rationale) benefits from a companion plan or at least a stated verification bound, even when the `designing`/`planning` skills' full spec template feels heavyweight for a single-feature change.
  **Cites**: T1, T2, T3

## Carry-forward items registered

| Item | Type | Priority | Tracked at |
|---|---|---|---|
| Document a standing fallback for "webhook review stuck in progress with zero artifacts, not skipped" distinct from the already-documented "skipped" case | process | P3 | `docs/solutions/workflow-issues/external-review-status-does-not-prove-review-completion.md` (extend) |
| Consider whether decision docs authorizing nontrivial runtime-code implementation should require a companion plan or stated verification bound | process | P4 | ROADMAP.md or a future `docs/decisions/` entry on decision-doc scope |

## Lessons

- A timing-margin test's safety must be established by measuring the mechanism's worst-case overhead directly, not by counting how many times it happened to pass — PR #35's "5 passes" was accepted evidence for a margin that was still within noise of the real overhead, and only failed again under the user's insistence on a measured bound (PR #36).

## Compounding

- compound invocation: `Documentation complete — docs/solutions/best-practices/measure-timing-margins-not-repeated-runs.md`
