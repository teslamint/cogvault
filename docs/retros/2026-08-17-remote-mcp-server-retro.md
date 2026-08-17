# Retro: remote MCP server

- Date: 2026-08-17
- Source: PR #22
- Spec: docs/specs/2026-08-11-remote-mcp-server-design.md
- Plan: docs/plans/2026-08-11-001-feat-remote-mcp-server-plan.md

## Release data

| Metric | Value |
|---|---|
| **Changed non-test lines** | 3,177 (3,146 added + 31 removed) |
| Commits | 45 |
| Review rounds | 1 formal pre-merge multi-lane round |
| Comments (fixed / deferred) | 0 / 0 GitHub comments |
| CI failures | 0 |
| Duration (first spec commit → merge) | 6h 7m (0.26 days) |
| Units planned / completed | 8 / 8 |

## Success criteria: measured vs declared

| # | Declared criterion | Measurement (command / rubric) | Measured result | Verdict |
|---|---|---|---|---|
| 1 | Streamable HTTP completes initialize and `tools/call`. | `go test ./internal/mcp/ -run TestStreamableHTTP -v` | verified: PASS after rerunning outside the sandbox's loopback-bind restriction | Met |
| 2 | Authorization rejection paths return the correct status and challenge. | `go test ./internal/httpauth/ -v` | verified: PASS, including distinct `401`, `403`, and challenge cases | Met |
| 3 | `auth.mode: none` refuses non-loopback HTTP and SSE binds. | `go test ./cmd/cogvault/ -run TestServeBindGuard -v` | verified: PASS for both transports and loopback allowance | Met |
| 4 | SSE uses the same authorization layer as HTTP. | `go test ./cmd/cogvault/ -run TestSSERequiresAuth -v` | verified: PASS; an uncredentialed bearer-mode request returned `401` | Met |
| 5 | All seven tools declare annotations; only write/delete are non-read-only. | `go test ./internal/mcp/ -run TestToolAnnotations -v` | verified: PASS over the registered tool set | Met |
| 6 | Protected Resource Metadata is unauthenticated and names the issuer. | Disposable oauth-mode server plus `curl -fsS "$PUBLIC_URL/.well-known/oauth-protected-resource" \| jq -e '.authorization_servers[0]'` | verified: exit 0 and printed `https://issuer.example.com`; metadata advertised the expected resource | Met |
| 7 | Existing stdio/default-loopback SSE behavior remains working. | `go test ./...` | verified: PASS across all 13 packages | Met |
| 8 | Canonical documentation matches shipped transports, auth, tool, config, and follow-up behavior. | Reviewer rubric over `SPEC.md`, `DESIGN.md`, `docs/research/v2-follow-ups.md`, and source | verified: rubric applied; the named sections and source cross-checks are present and consistent | Met |
| 9 | A reader can stand up the remote server from documentation alone. | Reviewer rubric over `docs/deployment/remote-mcp.md` | verified: rubric applied; tunnel, nested config, `--public-url`, JWT/issuer/audience prerequisites, and credential risks are explicit | Met |
| 10 | Documentation states there is no credential-compromise recovery and requires backups. | Reviewer rubric over `docs/deployment/remote-mcp.md` §7 | verified: rubric applied; overwrite/delete limits and two concrete backup commands are explicit | Met |
| 11 | Near-miss well-known paths are not an auth bypass. | `go test ./internal/httpauth/ -run TestWellKnownExactMatch -v` | verified: PASS for exact, suffixed, near-miss, method, and unusable-config cases | Met |
| 12 | Tokens require `exp`; streams cannot outlive their bound. | `go test ./internal/httpauth/ -run 'TestExpRequired\|TestStreamDeadline' -v` | verified: PASS, including the real-transport stream-deadline test selected by the pattern | Met |

## Carry-forward from previous retro

| Item | Status | Evidence |
|---|---|---|

- Reconciliation: registered 0, accounted for 0 — degraded: previous retro has no registration table
- Previous doc shape: pre-schema, exempt

## Interview Transcript

- Independence level: same-model fresh-context
- Rounds used: 2 (max 5)

| ID | Round | Phase | Probe | Answer | Evidence | Verdict (verbatim) |
|---|---|---|---|---|---|---|
| T-01 | 1→2 | 4 | Reconcile D1–D6, F11, and the prior section-validation idea without silently dropping survivors. | D1/D2/D3/D4/D5 remain trigger-owned deferrals; D6 was resolved as a separate feature and replaced here by honest recovery documentation. F11 is done. The old section-validation bullet was never registered under the current schema and remains an informal idea. F15 now records the one concrete survivor: real two-client interoperability. | Spec Open Decisions; `docs/research/v2-follow-ups.md` F11/F15; PR #24 merge `72b6d9` | accepted |
| T-02 | 1→2 | ship / retro | Explain why the ledger still said waiting-user and `merged: false` after PR #22 merged, then name the prevention. | The last surviving write preceded the merge by 35 minutes. The merge happened outside the surviving orchestrator, which never persisted Step 8 or entered Retro. Atomic merge-result persistence plus live `gh` verification on resume prevents trusting that stale state. | `.release-loop/progress.md.corrupt-20260817T040707Z`; `gh pr view 22`; merge `ae5c39e` | accepted |
| T-03 | 1→2 | implement / review | Reconstruct the shared-checkout collision around `a41c410` and identify a discriminating control. | The auth-mode revert commit also carried 51 lines of session-sweeper implementation from concurrent work, hiding a P1 fix under a false message and losing its tests. `6080ca8` restored the TTL assertion; `7a3c96b` added a wiring test that fails if `WithSessionIdleTTL` is removed. Isolated worktrees and file ownership would have prevented the collision. | `git show --stat a41c410`; commits `6080ca8`, `7a3c96b`; `TestSessionSweeperEvictsIdleSession` | accepted |
| T-04 | 1→2 | review | Cluster the post-unit security/reliability fixes and identify earlier discriminating gates. | Auth semantics needed scenario/rejection-matrix mutations; OIDC/JWKS inputs needed hostile protocol fixtures; deadlines/sweeping needed real-transport and wiring-deletion probes; deployment prose needed a composed threat-flow smoke test. These clusters explain why nominal unit completion was insufficient. | PR #22 fix commits `1439544`, `248864e`, `0c66d3b`, `a88e7b8`, `4afac21`; Phase 3 measurements | accepted |
| T-05 | 1→2 | design / verification | Separate repository-proven success from deployment-unproven interoperability. | All declared commands and rubrics pass, including a disposable local metadata curl. A real IdP, stable tunnel, hosted Claude app, and ChatGPT session remain unproven; F15 stops only when both clients retain artifact-backed initialize and tool-call round trips. | Phase 3 measurements; `docs/research/v2-follow-ups.md` F15 | accepted |

## Findings

### What worked well
- **What happened**: Independent design and pre-merge review turned multiple authorization, OIDC, deadline, and deployment-document gaps into fixes before merge; all twelve declared criteria pass freshly today.
  **Why**: Reviews attacked failure direction and then required discriminating tests rather than accepting intent or generic green suites.
  **How to apply**: Keep protocol-boundary reviews separate from ordinary unit review, and pair every accepted finding with a test or rubric that would fail if the fix disappeared.
  **Cites**: T-04 / Phase 2–3 data

### What to improve
- **What happened**: Concurrent writers in one checkout hid the session-sweeper implementation inside an unrelated revert and lost its first tests; the final Git history required follow-up commits to make the behavior legible and defended.
  **Why**: Shared-path edits had no worktree isolation or file-level ownership boundary, so commit contents and commit messages diverged.
  **How to apply**: Use isolated worktrees for concurrent writers; when that is impossible, reject commits containing unowned files until the diff and message are reconciled.
  **Cites**: T-03
- **What happened**: The release-loop ledger remained at `waiting-user` and `merged: false` after the PR merged, leaving no contemporaneous merged-result verification or Retro transition.
  **Why**: Merge completion and durable state transition were separated; the orchestrator stopped before the post-merge write.
  **How to apply**: Treat `final_action: executed`, `merged: true`, merge SHA evidence, and `phase: retro` as one atomic post-merge state update; every resume checks `gh` before trusting a waiting gate.
  **Cites**: T-02

### Process observations
- **What happened**: All repository success criteria are Met, but they do not prove hosted Claude and ChatGPT interoperability through the user's eventual IdP and tunnel.
  **Why**: The design deliberately remained IdP-agnostic, so real vendor round trips could not be produced before the deployment choice.
  **How to apply**: Report local protocol proof and deployed interoperability separately; close F15 only with artifacts from both hosted clients.
  **Cites**: T-05 / Phase 3 data

## Carry-forward items registered

| Item | Type | Priority | Tracked at |
|---|---|---|---|
| Real remote MCP interoperability smoke with the chosen IdP and tunnel, from both Claude and ChatGPT | feature | P2 | `docs/research/v2-follow-ups.md` F15 |

## Lessons

- A loopback guard and a tunnel guide can compose into a public unauthenticated endpoint even when each component is correct; test operational instructions as one threat flow.
- A synthetic deadline test can stay green while the real transport option is absent; mutation must remove the actual wiring, not merely exercise the helper.
- Post-merge state persistence is shipping evidence, not bookkeeping; if it is not atomic with merge verification, the next session must reconstruct authority.

## Compounding

- compound invocation: `Documentation complete — docs/solutions/security-issues/loopback-bind-guards-do-not-protect-tunneled-endpoints.md`
