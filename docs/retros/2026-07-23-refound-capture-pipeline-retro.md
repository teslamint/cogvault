# Retro: refound-capture-pipeline (cogvault v2 Phase 1)

- Date: 2026-07-23
- Source: PR #3
- Spec: docs/specs/2026-07-22-refound-capture-pipeline-design.md
- Plan: docs/plans/2026-07-22-001-feat-v2-capture-pipeline-plan.md

## Release data

| Metric | Value |
|---|---|
| **Changed non-test lines** | 3,867 (2,796 added + 1,071 removed) |
| Commits | 28 (incl. merge) |
| Review rounds | 11 dispatched reviews (9 unit + final branch + release-loop gate); 7 fix rounds, none exceeded the 1-round cap per unit |
| Comments (fixed / deferred) | 21 / 32 (Important-class all fixed; minors ledgered → docs/research/v2-follow-ups.md F2) |
| CI failures | 0 (no CI configured; local `go test -race` was the gate) |
| Duration (first spec commit → merge) | <1 day (2026-07-22 20:56 KST → 2026-07-23 01:00 KST, ≈4h) |
| Units planned / completed | 9 / 9 |

## Success criteria: measured vs declared

| # | Declared criterion | Measurement (command / rubric) | Measured result | Verdict |
|---|---|---|---|---|
| 1 | ≥90% of the 65 corpus PDFs have a `success` ledger row and an indexed wiki page | `sqlite3 <db> "select status, count(*) from ingest_ledger group by status"` + 5 search spot checks | Not runnable at retro time: no production config/db exists yet — backlog run is post-merge validation | Not met — validation pending (tracked F1) |
| 2 | Zero-touch inflow: new PDF gains a page with no manual command | drop test PDF, wait one interval, verify `run_origin = scheduled` success row | Not runnable: launchd job not yet installed by the user | Not met — validation pending (tracked F1) |
| 3 | Re-find beats web re-search: ≥4/5 attempts over 1 week | user judgment log per attempt | Not runnable: requires 1 week of real use | Not met — validation pending (tracked F1) |
| 4 | Zero per-item instructions during validation week | ledger rows all `run_origin = scheduled` | Not runnable: same window as #3 | Not met — validation pending (tracked F1) |
| 5 | Test suite passes | `go test -race ./...` | Run fresh this retro: 11 packages ok, 0 FAIL | Met |

## Carry-forward from previous retro

No previous retro doc exists under docs/retros/ — this is the repository's first retro; nothing to reconcile.

- Previous doc shape: pre-schema, exempt (no previous doc)

## Interview Transcript

- Independence level: self-checklist
- Rounds used: 1 (max 5)

| ID | Round | Phase | Probe | Answer | Evidence | Verdict (verbatim) |
|---|---|---|---|---|---|---|
| T1 | 1 | 5 | Criteria 1-4 are Not Met at retro time — what did the declaration get wrong? | Nothing structural: they were declared as post-merge real-world measurements and the branch ships their instrumentation (ledger status/run_origin, report counters, plist). The gap is scheduling, not design. | ledger DDL internal/ingest/ledger.go; deploy plist; F1 | self-attested |
| T2 | 1 | 5 | What did the plan fail to see? | That `internal/schema/default_schema.md` is a contract-carrying runtime asset, not passive packaging. The plan said "internal/schema as-is", so no unit owned it, and the stale v1 text (scope param, vault workflow) reached the final branch review before being caught. | final review I1; fix commit f89fc1c | self-attested |
| T3 | 1 | 5 | What almost went wrong but didn't, and what caught it? | The plan's U7 test row asserted "scope argument rejected by schema" — mcp-go performs no server-side argument validation, so the scenario was unimplementable as written. The implementer flagged it as a deviation instead of faking a passing test, and the reviewer independently verified the framework claim against mcp-go source. Recorded in 0021 D5. | U7 report deviation 2; 0021 D5 | self-attested |
| T4 | 1 | 5 | Which measurement surprised you? | That `PRAGMA busy_timeout` via `db.Exec` configures exactly one pooled connection, and that FTS5's DELETE-then-INSERT upgrades a DEFERRED tx read→write where a conflict returns `SQLITE_BUSY_SNAPSHOT` that busy_timeout never retries. Both were discovered by reviewers, not by the original implementation. | U4 review F1 → commit 23f89d0; docs/deviations/2026-07-22-busy-timeout-fts-write-write.md | self-attested |
| T5 | 1 | 5 | If you re-ran this from the spec, what would you do differently? | Give test fakes per-item behavior from the start: the run-global `CLAUDE_FAKE_MODE` silently narrowed the failure-isolation scenario to a single file across BOTH the unit and e2e suites, and only the U9 review caught that the mixed-outcome case existed nowhere. | U9 review Important; fix commit 3215e59 | self-attested |

## Findings

### What worked well

- **What happened**: Independent spec review round 1 falsified the draft's "config/storage reused as-is" claim with file:line evidence (absolute wiki_dir rejected at config.go:138; write-prefix check at fs.go:60) before any code was written, converting a doomed plan assumption into explicit Phase 1 scope.
  **Why**: the reviewer was instructed to verify reuse claims against actual code, not prose plausibility.
  **How to apply**: every spec claim of the form "X needs no changes" gets a code-cited verification in review, not a nod.
  **Cites**: spec revision commit f03caf1 lineage; Phase 2 data (review rounds)

- **What happened**: The final branch review caught three cross-unit contract drifts that nine per-unit reviews structurally could not see: stale default_schema.md served to every consumer (I1), prompt-vs-schema frontmatter field mismatch (I2), and SPEC overclaiming the busy_timeout guarantee (I4).
  **Why**: task reviewers only ever see their own diff; contract assets that no unit modifies are invisible until someone reads the whole tree against the spec.
  **How to apply**: keep the final whole-tree review mandatory even when every unit review came back clean.
  **Cites**: T2, T4; fix commits f89fc1c/a743bc8

### What to improve

- **What happened**: The mixed-outcome failure-isolation scenario (one file fails, siblings succeed, same run) was absent from both unit and e2e suites until the U9 review, because the fake CLI's mode was process-global.
  **Why**: test-double design wasn't reviewed against the scenario matrix when the fake was introduced in U5.
  **How to apply**: when a fake's configuration is global, explicitly check which multi-item scenarios it forecloses before writing dependent tests.
  **Cites**: T5; commit 3215e59

- **What happened**: Reviewer subagents repeatedly failed to respond after mailbox re-engagement (U4, U6, U7, U8 re-reviews), forcing replacement verifiers or orchestrator file-evidence verification.
  **Why**: resumed agents are unreliable workers in this harness; silence is indistinguishable from work-in-progress.
  **How to apply**: for re-verification of mechanical fixes, prefer a fresh verifier agent or direct orchestrator evidence checks over resuming the original reviewer.
  **Cites**: progress ledger entries 2026-07-22T15:25/15:45; verify-u4-u6 dispatch

### Process observations

- **What happened**: All 9 units completed with at most one fix round each, against a 3-round cap; total wall-clock from approved spec to merge was ≈4 hours with parallel unit implementation and review.
  **Why**: briefs carried exact interfaces plus upstream review findings (e.g. U6 received the DSN-pragma correction mid-flight), so implementers rarely guessed.
  **How to apply**: keep threading reviewer findings into in-flight dependent units instead of waiting for their review round.
  **Cites**: Phase 2 data; progress ledger course-correction entry

## Carry-forward items registered

| Item | Type | Priority | Tracked at |
|---|---|---|---|
| SC1-SC4 real-world validation (backlog, launchd, 1-week re-find) | feature/process | P1 | docs/research/v2-follow-ups.md F1 |
| Deferred review minors batch (32 items) | process | P3 | docs/research/v2-follow-ups.md F2 |
| FTS write-write BUSY_SNAPSHOT revisit trigger | architecture | P3 | docs/research/v2-follow-ups.md F3 |
| Spec self-contradiction: rename-dedup vs (path,hash) ledger key | edge-case | P3 | docs/research/v2-follow-ups.md F4 |
| Dead code / v1 fixture / MCP fallback cleanup | process | P4 | docs/research/v2-follow-ups.md F5 |

## Lessons

- `PRAGMA` via `db.Exec` configures one pooled connection; contract-level SQLite pragmas belong in the DSN (`?_pragma=busy_timeout(5000)`), or every other pool connection runs with defaults.
- `busy_timeout` cannot rescue an FTS5 DELETE-then-INSERT: a DEFERRED transaction that upgrades read→write dies with `SQLITE_BUSY_SNAPSHOT`, not a lock wait — design write-write concurrency around it, don't configure it away.
- A run-global fake mode silently narrows every multi-file failure-isolation test to single-file; give test doubles per-item behavior the day they're born.
- "Reused as-is" is a spec claim like any other: ours was falsified at the first `storage.Write` by a reviewer with file:line evidence, before implementation started.

## Compounding

- compound invocation: `Documentation complete — docs/solutions/database-issues/sqlite-pool-pragma-and-busy-snapshot.md` (CONCEPTS.md seeded with the learning's area; discoverability line added to CLAUDE.md §0; frontmatter validation exit 0)
