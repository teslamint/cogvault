# Retro: Makefile with codesign (F10)

- Date: 2026-07-27
- Source: PR #11
- Spec: docs/specs/2026-07-27-makefile-codesign-design.md
- Plan: skipped (atomic feature, all decisions in spec)

## Release data

| Metric | Value |
|---|---|
| **Changed non-test lines** | 19 (Makefile) + 1 (DESIGN.md) + 10 (README.md) = 30 |
| Commits | 3 (1 spec + 1 impl + 1 review fix) |
| Review rounds | 2 (1 spec review + 1 branch review); 3I+5M spec, 2I+3M branch |
| Comments (fixed / deferred) | 5 Important fixed / 8 Minor (5 deferred, 3 fixed) |
| CI failures | 0 (no CI; local `make test` gate) |
| Duration (first spec commit to merge) | ~30min |
| Units planned / completed | plan skipped; 3 logical units (Makefile, DESIGN.md, README.md) / 3 |

## Success criteria: measured vs declared

Measured fresh during this retro (enforces: P3).

| # | Declared criterion | Measurement | Measured result | Verdict |
|---|---|---|---|---|
| SC1 | `make build` produces `./cogvault` with proper adhoc signature (not linker-signed) | `make build && codesign -dv ./cogvault 2>&1` grep checks | verified: linker-signed count = 0, Signature=adhoc count = 1 | Met |
| SC2 | `make install` copies signed binary to `~/bin/cogvault` and it runs without SIGKILL | `make install && ~/bin/cogvault --help; echo $?` | verified: exit 0 (not 137) | Met |
| SC3 | `make test` runs `go test -race ./...` and exits 0 | `make test` | verified: all packages ok, exit 0 | Met |
| SC4 | `make clean` removes `./cogvault` | `make clean && test ! -f ./cogvault` | verified: PASS | Met |
| SC5 | DESIGN.md §5 mentions the Makefile | `grep -c Makefile DESIGN.md` | verified: 1 | Met |
| SC6 | README.md build instructions reference `make` targets | `grep -c 'make build\|make install' README.md` | verified: 3 | Met |

## Carry-forward from previous retro

Previous retro: docs/retros/2026-07-27-configurable-max-file-size-retro.md (F9). Its carry-forward items:

| Item | Status | Evidence |
|---|---|---|
| Add `codesign --force --sign -` to Makefile build target | Done | PR #11 (this release); Makefile committed |
| Update F9 tracker row to Done after retro merge | Done | docs/research/v2-follow-ups.md F9 row reads "Done (PR #10, main 3b3659f)" |
| Set llm.model=opus in the real config | Done (from F6) | `~/.config/cogvault/config.yaml` has `model: opus` |
| Deferred F6 minors | Not started | docs/research/v2-follow-ups.md F2 (unchanged) |
| F1 SC3/SC4 (1-week validation) | In progress | docs/research/v2-follow-ups.md F1 (unchanged) |
| F2-F5, F7, F8 | Not started | docs/research/v2-follow-ups.md (unchanged) |

- Previous doc shape: conformant (Interview Transcript with `self-checklist` independence level, all findings cite T-IDs)

## Interview Transcript

- Independence level: self-checklist
- Rounds used: 1 (max 5)

| ID | Round | Phase | Probe | Answer | Evidence | Verdict (verbatim) |
|---|---|---|---|---|---|---|
| T1 | 1 | 5 | What did the spec and branch reviews catch that self-review missed? | Spec review caught 3 Important: SC1 measurement was provably useless (grep passed without codesign), no runtime smoke test SC, and README would keep documenting the SIGKILL-reproducing build path. Branch review caught 2 Important: solution doc didn't capture the install-path re-sign finding, and spec itself contradicted the implementation on where codesign runs. All 5 were real defects — the SC1 finding is particularly notable because it means the spec's primary criterion would have passed even if the feature's core mechanism was absent. | spec review (fable), branch review (fable); commits 799f02a, 87c3c1a | self-attested |
| T2 | 1 | 5 | What was the most important implementation discovery? | Sign-then-copy is insufficient. The initial Makefile signed `./cogvault` during build and copied to `~/bin/`. SC2 failed with exit 137. Adding `codesign --force --sign -` at the install destination fixed it. This contradicted the solution doc's model (sign once, at the build location) and the spec's S1 narrative. The reviewer's open question — whether TCC matches by path hash or vnode cache — is unresolved but doesn't affect the fix. | `make install` exit 137 → exit 0 after adding destination codesign | self-attested |
| T3 | 1 | 5 | Was the plan skip justified? | Yes. Same criteria as F9: atomic (3 files, no KTDs, no scope boundaries, no traceability needed). The spec's Affected files table served as the plan. The install-path re-sign discovery was an implementation finding, not a design gap — a plan wouldn't have caught it either. | planning SKILL.md skip conditions; spec Affected files table | self-attested |

## Findings

### What worked well

- **What happened**: Spec review's SC1 finding caught a measurement that was provably useless — `grep 'Signature=adhoc'` passes on both unsigned and signed binaries, so the primary success criterion would have claimed the feature works even without the codesign step.
  **Why**: the reviewer actually built a test binary and compared `codesign -dv` output before/after re-signing, rather than desk-checking the grep.
  **How to apply**: SC measurements for build/signing features should be verified empirically, not desk-checked — adhoc signatures have surprising properties.
  **Cites**: T1

### What to improve

- **What happened**: The spec assumed sign-then-copy was sufficient (S1: "built, adhoc-signed, and copied"), and implementation discovered it wasn't — SC2 failed with exit 137 on the first run.
  **Why**: the solution doc from F9 only documented signing at the build location because the F9 fix built directly to `~/bin/`. When the Makefile separated build and install paths, the TCC hash mismatch surfaced.
  **How to apply**: when automating a manual fix into a build system, test the exact sequence the build system uses (build → copy → run), not just the manual sequence.
  **Cites**: T2

### Process observations

- **What happened**: Plan was skipped for the second consecutive release (F9 and F10). Both were atomic, single-commit-boundary features. Neither skip missed a design decision — the implementation discovery in F10 was an empirical finding, not a planning gap.
  **Why**: the skip criteria (atomic, no KTDs, no scope boundaries, no traceability) are working as intended for small features.
  **How to apply**: continue using the skip criteria; the spec review remains the safety net.
  **Cites**: T3

## Carry-forward items registered

| Item | Type | Priority | Tracked at |
|---|---|---|---|
| Update F10 tracker row to Done after retro merge | process | P4 | docs/research/v2-follow-ups.md F10 |

## Lessons

- Sign-then-copy does not preserve macOS TCC validity: re-sign at the install destination, not just the build location.

## Compounding

- compound invocation: not attempted — the destination re-sign finding is already captured in `docs/solutions/build-errors/macos-sigkill-rebuilt-go-binary.md` (updated in this PR's review fix commit), which is the canonical location for this knowledge.

Retrospective complete — docs/retros/2026-07-27-makefile-codesign-retro.md
