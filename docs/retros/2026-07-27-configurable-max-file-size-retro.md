# Retro: Configurable max_file_size_mb (F9)

- Date: 2026-07-27
- Source: PR #10
- Spec: docs/specs/2026-07-27-configurable-max-file-size-design.md
- Plan: skipped (atomic feature, all decisions in spec)

## Release data

| Metric | Value |
|---|---|
| **Changed non-test lines** | 8 (config.go +7, ingest.go +1/-2) |
| Commits | 7 (1 impl + 1 review fix + 3 spec + 1 docs + 1 tracker) |
| Review rounds | 2 (1 spec review + 1 branch review); 2 Important findings fixed each round |
| Comments (fixed / deferred) | 4 Important fixed / 3 Minor deferred |
| CI failures | 0 (no CI; local `go test -race` gate) |
| Duration (first spec commit → merge) | ~54min (12:27 → 13:21 UTC) |
| Units planned / completed | plan skipped; 3 logical units (config, wiring, docs) / 3 |

## Success criteria: measured vs declared

Measured fresh during this retro (enforces: P3).

| # | Declared criterion | Measurement | Measured result | Verdict |
|---|---|---|---|---|
| SC1a | `New` produces Runner with `maxFileSize == int64(cfg.MaxFileSizeMB)<<20`; synthetic file above cap skips, below digests | `go test -run TestRunConfigMaxFileSize ./internal/ingest/` | verified: PASS (0.01s); test creates Runner via `New` with `MaxFileSizeMB:1`, asserts `runner.maxFileSize == 1<<20`, then runs with 1MB+1 byte file (skipped=1) and small file (digested=1) | Met |
| SC1b | With `max_file_size_mb: 64`, the NYT 42MB PDF digests instead of appearing in `skipped` | `cogvault ingest` with `max_file_size_mb: 64` in real config | verified: `digested=2 skipped=0`; wiki page `nyt.com-who-is-satoshi-nakamoto-my-quest-to-unmask-bitcoins-creator.md` exists in wiki sources/ | Met |
| SC2 | Config with field omitted or set to 0 behaves identically to today (32MB default) | `go test -run TestLoadDefaults ./internal/config/` | verified: PASS; `MaxFileSizeMB == 32` asserted | Met |
| SC3 | Negative value rejected at config load | `go test -run TestValidationErrors/max_file_size_mb_negative ./internal/config/` | verified: PASS; error contains "max_file_size_mb" and "positive" | Met |
| SC4 | DESIGN.md and SPEC.md reflect configurable cap | `grep -n max_file_size_mb DESIGN.md SPEC.md` | verified: DESIGN §2.2 (line 56), §2.7 (line 161-162); SPEC §3.1 (line 111), §3.3 (line 138), §10.1 (line 473), §10.7 (lines 540-545) | Met |

## Carry-forward from previous retro

Previous retro: docs/retros/2026-07-23-aup-error-class-and-model-option-retro.md (F6). Its carry-forward items:

| Item | Status | Evidence |
|---|---|---|
| Set llm.model=opus in the real config | Done | `~/.config/cogvault/config.yaml` already has `model: opus` (set during F6 or after) |
| Deferred F6 minors (refused-skip PerFile, duplicate ModelUnchanged test, fake argv) | Not started | docs/research/v2-follow-ups.md F2 (unchanged) |
| F1 SC3/SC4 (1-week validation) | In progress | docs/research/v2-follow-ups.md F1 (unchanged) |
| F2-F5, F7, F8 | Not started | docs/research/v2-follow-ups.md (unchanged) |

- Previous doc shape: conformant (Interview Transcript with `self-checklist` independence level, all findings cite T-IDs)

## Interview Transcript

- Independence level: self-checklist
- Rounds used: 1 (max 5)

| ID | Round | Phase | Probe | Answer | Evidence | Verdict (verbatim) |
|---|---|---|---|---|---|---|
| T1 | 1 | 5 | What did the independent reviewer catch that self-review missed? | Two rounds of review each caught 2 Important findings. Spec review: (I1) default owner contradiction — `applyDefaults` zero-guard vs ingest const vs validate, making SC3 unreachable if the `ConsistencyInterval` pattern (`<= 0`) was copied; (I2) `cogvault init`/`Save` forward-compat behavior undocumented. Branch review: (I1) `TestRunConfigMaxFileSize` bypassed `New()` wiring by manually setting both fields — the test would pass even if `New` hardcoded the value; (I2) SPEC §3.1 schema block missing the new field. All four were legitimate findings, not noise. | spec review commit ec0d572; branch review commit 653ba43 | self-attested |
| T2 | 1 | 5 | What took longer than expected? | The binary deployment. After merge, three separate attempts to run the new binary were killed (exit 137/SIGKILL). Root causes: (1) `which cogvault` resolved to `~/bin/cogvault` (old binary) over `/usr/local/bin/cogvault` (new binary); (2) after copying, macOS killed the binary because FDA was invalidated by the changed code hash; (3) even after re-granting FDA, `com.apple.provenance` tracking from the Claude Code sandbox marked the binary as restricted. The fix was `codesign --force --sign -` on the binary. This deployment friction consumed more time than the actual implementation. | exit 137 logs; codesign fix | self-attested |
| T3 | 1 | 5 | Was the plan skip justified? | Yes. The planning skill's skip conditions require all four: atomic (1 commit boundary), no KTDs, no scope boundaries, no traceability needed. F9 met all four — the spec's Affected files table served as the plan, all decisions were resolved in the spec, and the implementation was straightforward wiring. The one risk (default owner ambiguity) was caught by spec review, not missed by plan absence. | planning SKILL.md skip conditions; spec review I1 | self-attested |
| T4 | 1 | 5 | What was the viability check's value? | It proved the 42MB PDF could be processed before designing the feature. If claude couldn't read it, the spec's SC1b would have been unachievable and the feature would convert `skipped` into `failed` — worse than the status quo. The check cost $2.95 and 29 seconds; it also revealed the ~214K input token cost, which fed the spec's cost note. | viability test output (subtype:success, 29s, $2.95) | self-attested |

## Findings

### What worked well

- **What happened**: The viability check before spec drafting prevented designing a feature whose primary success criterion might be unachievable. The advisor suggested it; without it, the spec would have assumed claude can handle 42MB PDFs without evidence.
  **Why**: the feature's value proposition (digest a specific 42MB file) has a testable precondition (can the LLM read it?) that could have been false.
  **How to apply**: when a feature's success depends on an external system's capability at a specific scale, spike the capability before writing the spec.
  **Cites**: T4; advisor feedback

- **What happened**: Independent spec and branch reviews each caught 2 Important findings per round — all four were real defects that would have shipped (unreachable SC3, untested wiring, incomplete SPEC schema, undocumented forward-compat).
  **Why**: the fable-model reviewer was instructed to verify claims against code, not just read the text.
  **How to apply**: keep the "run the claim against the code" reviewer instruction; for small features, one spec review + one branch review is sufficient.
  **Cites**: T1; commits ec0d572, 653ba43

### What to improve

- **What happened**: Binary deployment after merge consumed more time than implementation. Three separate SIGKILL failures from: wrong PATH resolution, FDA invalidation on binary hash change, and macOS provenance tracking from the Claude Code sandbox.
  **Why**: Go binaries are adhoc-signed; macOS tracks FDA by code hash, not path; binaries built inside Claude Code inherit provenance restrictions.
  **How to apply**: after rebuilding cogvault, always run `codesign --force --sign -` on the binary. Consider adding this to a Makefile target. Build from a non-sandboxed terminal when possible.
  **Cites**: T2; exit 137 investigation

### Process observations

- **What happened**: The plan phase was skipped for the first time in this project's release-loop history. The feature was small enough that the spec's Affected files table served as the plan. No design decisions were missed by the skip — the one ambiguity (default owner) was caught by spec review.
  **Why**: the planning skill's skip criteria (atomic, no KTDs, no scope boundaries, no traceability) were genuinely met.
  **How to apply**: trust the skip criteria; the spec review is the safety net for plan-skipped features.
  **Cites**: T3

## Carry-forward items registered

| Item | Type | Priority | Tracked at |
|---|---|---|---|
| Add `codesign --force --sign -` to Makefile build target to avoid post-build SIGKILL | process | P3 | docs/research/v2-follow-ups.md (new F10) |
| Update F9 tracker row to Done after retro merge | process | P4 | docs/research/v2-follow-ups.md F9 |

## Lessons

- Spike external-system capabilities at the target scale before writing the spec: a $3 viability check prevented designing a feature around an unverified assumption about LLM capacity.
- macOS kills rebuilt Go binaries after FDA grants because FDA tracks code hash, not path; `codesign --force --sign -` after every rebuild is the fix.

## Compounding

- compound invocation: not attempted — the codesign lesson is macOS-specific deployment friction, not a reusable code pattern; the viability-check-before-spec lesson is procedural wisdom already captured in this retro's Lessons section.

Retrospective complete — docs/retros/2026-07-27-configurable-max-file-size-retro.md
