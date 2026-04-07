# 0011-step4-review-convergence

Status: accepted
Date: 2026-04-07

## Context

Step 4 (`internal/index/`) went through multiple review rounds after the initial implementation landed.

The public contract in `SPEC.md` and the implementation structure in `DESIGN.md` were mostly stable, but three pieces of rationale were not obvious from code and existing documents alone:

1. why `CheckConsistency` is split into a best-effort scan phase and an all-or-nothing apply phase,
2. why implementation-plan files are not allowed to redefine canonical behavior after code review,
3. why the final rollback guarantee must be proven through a `CheckConsistency`-level test instead of lower-level transaction tests.

These points repeatedly surfaced in review because they were reconstruction knowledge from review history rather than facts directly encoded in a single canonical document.

## Decision

### D1: `CheckConsistency` has asymmetric failure semantics by design

CheckConsistency uses best-effort scan + all-or-nothing apply. See `docs/decisions/0010-step4-index-decisions.md` D7 for details.

### D2: Plan files are working notes, not canonical behavior

Implementation plans such as `~/.claude/plans/*.md` may describe intended direction, but once review changes the final behavior, the canonical sources remain:

- `SPEC.md` for public contract,
- `DESIGN.md` for architecture and implementation semantics,
- `docs/decisions/` for rationale that explains why the canonical behavior looks that way.

If a plan file drifts from code after review, the plan must be updated or ignored; it must not be treated as an alternate source of truth.

### D3: Review-discovered invariants must be locked at the highest meaningful level

When review establishes a behavioral invariant that is easy to accidentally regress, the test should target the highest level that actually owns that invariant.

For Step 4, rollback guarantees belong to `CheckConsistency`, not to SQLite transaction helpers in isolation. Therefore the regression test must induce failure through `CheckConsistency`'s apply path, not merely prove that `BEGIN` + `ROLLBACK` works in SQLite.

## Why

### Why best-effort scan + atomic apply

See 0010 D7 and `docs/research/step4-index-review-notes.md` 'Why all-or-nothing TX'.

### Why plan drift matters

During review, the same issue reappeared because readers compared current code to an outdated plan instead of the canonical documents. Recording this explicitly reduces future review noise and prevents re-litigating already-settled semantics.

### Why high-level rollback tests matter

A low-level rollback test only proves that SQLite transactions work. It does not prove that `CheckConsistency` aborts on the first apply failure, does not update `lastConsistency`, and does not leak partially applied rows into `wiki_fts` or `file_meta`.

The project cares about the composed behavior of the index layer, so the regression test must exercise that composed behavior.

## Alternatives Considered

### A1: Best-effort scan and best-effort apply

Rejected. This keeps the implementation simple but allows partially reconciled DB state after a failed consistency pass.

### A2: Fail-fast scan and atomic apply

Rejected. This would keep semantics simpler, but a single unreadable file would block unrelated reconciliation work and make periodic consistency much noisier in practice.

### A3: Keep this rationale only in chat or review transcripts

Rejected. The issue reappeared multiple times precisely because the rationale was not promoted into a persistent project document.

## Revisit Triggers

- `CheckConsistency` becomes a throughput bottleneck and apply batching strategy must change.
- Step 5 or later introduces upper-layer behavior that requires different guarantees around partial results.
- The index layer stops being the owner of reconciliation logic and the invariant moves into a higher engine/service layer.

## Related Files

- `SPEC.md` — index contract
- `DESIGN.md` — `CheckConsistency` algorithm and locking model
- `docs/decisions/0010-step4-index-decisions.md` — Step 4 concrete decisions
- `internal/index/sqlite.go` — `CheckConsistency` implementation
- `internal/index/sqlite_test.go` — rollback and per-file failure tests
