# Retro: pdf-extraction-openai-adapter

- Date: 2026-09-02
- Source: PR #40 (`feat/pdf-extraction-openai-adapter`)
- Spec: docs/specs/2026-09-01-pdf-extraction-openai-adapter-design.md
- Plan: docs/plans/2026-09-01-001-feat-pdf-extraction-openai-adapter-plan.md

## Release data

| Metric | Value |
|---|---|
| **Changed non-test lines** | ~634 added (production Go + config + deploy) |
| Test lines added | ~1100 (extract 511, openai 188, ingest 390+, config 42) |
| Commits | 11 |
| Review rounds (unit / final / standalone) | 1 (0 / 1 / 0) |
| Fix rounds | 1 (resolved 2 P1 + 2 P2 in one commit) |
| Internal findings (fixed / deferred) | 4 / 0 |
| Pull request comments (fixed / deferred) | 0 / 0 |
| Count completeness | exact |
| CI failures | 0 (local validation; no remote CI) |
| Duration (first spec commit → PR ready) | ~31h (2026-09-01T10:18+09:00 → 2026-09-02T17:08+09:00) |
| Units planned / completed | 5 / 5 |

## Success criteria: measured vs declared

| # | Declared criterion | Measurement | Measured result | Verdict |
|---|---|---|---|---|
| 1 | Text-layer PDF reaches endpoint as complete extracted text, never raw bytes/images/path. | `go test ./internal/extract -run TestExtract` + `go test ./internal/llm -run TestOpenAI` | Tests assert SourceText in request body, reject missing SourceText, no path-read prompt. | Met |
| 2 | Korean scanned PDF falls back to ordered Tesseract `eng+kor` OCR and yields a source page. | `go test ./internal/ingest -run TestRunTextInputExtractsPDFAndSharesContext` + U5 launchd evidence | Integration test with fake renderer/OCR/HTTP; U5 live `kor_scanned.pdf` → valid wiki page with Korean title. | Met |
| 3 | Over-limit extraction creates no page, consumes one attempt; profile change retries. | `go test ./internal/ingest -run TestRunProfileChangeRetries` + extract overflow tests | No HTTP call on overflow; ledger attempts incremented; profile-change retry proven. | Met |
| 4 | OCR rejects 50-page/16MP/32MiB limits, cleans artifacts, no page, one permanent attempt. | `go test ./internal/extract -run 'PageLimit\|Oversize\|LaterOversize\|OversizeRendered'` | Each bound tested with fake commands; cleanup verified; no HTTP call. Render cap uses post-write size check per deviation `2026-09-02-render-singlefile-size-check.md`. | Met (with documented deviation) |
| 5 | Missing prerequisite fails before ledger mutation. | `go test ./cmd/cogvault -run 'MissingPdftotext\|MissingTesseract\|MissingKorData'` | Command exits before ledger opens; temporary DB unchanged. | Met |
| 6 | Transient HTTP/preflight consumes no attempt; malformed consumes one; shared deadline. | `go test ./internal/llm -run TestOpenAI` + `go test ./internal/ingest -run DigestExtraction` | 408/429/5xx/timeout/loaded:false → ErrTransient; malformed → permanent; deadline seam test passes without wall-clock sleep. | Met |
| 7 | max_input_chars rejects >1M; letterless over-cap text reaches OCR. | `go test ./internal/config -run MaxInputChars` + `go test ./internal/extract -run Letterless` | Config rejects 0, negative, >1M; letterless candidate drains and falls back to OCR. | Met |
| 8 | Claude Code ingestion works without Poppler/Tesseract. | `go test -race ./...` (full suite) + `go test ./cmd/cogvault -run ClaudeCode` | All 15 packages pass; Claude path-mode tests have no extract prerequisite. | Met |
| 9 | Controlled launchd one-shot completes with valid page. | U5 evidence (`.release-loop/runs/.../evidence/U5/local-ingest-report.md`) | Temporary launchd job: exit 0, 2 PDFs digested, valid wiki pages, ledger success with digest_profile. No AppData prompt observed for that exact job. | Met |

## Carry-forward from previous retro

| Item | Status | Evidence |
|---|---|---|
| Document a standing fallback for stuck external review with zero artifacts | Not started | No change this cycle |
| Decide whether nontrivial decision documents require a companion plan | Not started | No policy record changed |
| Combine temporary access-check prompt observation into one canonical transcript | Not started | No change this cycle |

- Reconciliation: registered 3, accounted for 3
- Previous doc shape: conformant

## Findings

### What worked well

- **What happened**: The U5 controlled launchd proof caught two real bugs (ranged `pdfinfo` parse and `pdftoppm -singlefile` stdout empty) that no unit test could have found because they depend on the host Poppler build's actual behavior.
  **Why**: The plan mandated a real-binary launchd proof precisely because prior cycles showed unit tests cannot prove tool-chain compatibility.
  **How to apply**: Any feature that shells out to host tools needs at least one controlled proof with the real binaries, not just fake-command unit tests.
  **Cites**: U5 evidence; commits `9ab985b`, `744972b`

- **What happened**: The final review's P1 legacy-retry inversion was caught before merge despite passing all existing tests.
  **Why**: The existing test seeded the wrong fixture (legacy row) for the "already exhausted, skip" assertion. The feature's skip condition reproduced pre-feature behavior for legacy rows, which happened to match the test's expectation but violated the spec's "retryable once" promise.
  **How to apply**: When adding a new dimension to a skip/retry gate, write a Run-loop test that exercises the full legacy→retry→skip lifecycle, not just the skip predicate in isolation.
  **Cites**: Final review P1 `digest_profile-legacy-retry-inverted`; `TestRunLegacyExhaustedRowRetriedOnceAndThenSkipped`

### What to improve

- **What happened**: The approved spec's streaming-cap language described stdout piping that was impossible on the host Poppler. The deviation was discovered during U5 (late), requiring a spec/plan amendment and a deviation addendum.
  **Why**: The Implementation-time note in the plan correctly flagged "verify Poppler stdout flags in a disposable fixture before U2", but U2 initially implemented the stdout approach and only discovered the zero-byte problem during U5's real-binary proof.
  **How to apply**: Execute implementation-time probes at their named phase (U2), not defer them to the proof phase (U5). A 30-second `pdftoppm -png file.pdf -` probe at the start of U2 would have caught this before writing the stdout-based code.
  **Cites**: Plan implementation-time note line 188; deviation `2026-09-02-render-singlefile-size-check.md`

- **What happened**: The dead `usable()` function survived through U2 implementation and all review rounds until the final review flagged it as P2.
  **Why**: It was a package-level function with no callers; Go does not error on unused package-level functions, and the review focus was on correctness, not dead code.
  **How to apply**: After refactoring a function into a method (here: `streamCollector.Usable()`), immediately delete the superseded free function rather than leaving it for cleanup.
  **Cites**: Final review P2 `dead-package-level-usable-function`

### Process observations

- **What happened**: All three previous carry-forward items remain open after this merge.
  **Why**: This feature cycle did not touch the process-policy or workflow-evidence areas they address.
  **How to apply**: Keep all rows in the next retro until their named durable artifacts change.

## Carry-forward items registered

| Item | Type | Priority | Tracked at |
|---|---|---|---|
| Document a standing fallback for stuck external review with zero artifacts | process | P3 | `docs/solutions/workflow-issues/external-review-status-does-not-prove-review-completion.md` |
| Decide whether nontrivial decision documents require a companion plan | process | P4 | future `docs/decisions/` entry |
| Combine temporary access-check prompt observation into one canonical transcript | process | P3 | previous retro carry-forward |
| Execute implementation-time probes at their named plan phase, not defer to proof | process | P3 | this retro |

## Lessons

- When adding a new dimension to a retry/skip gate (e.g. digest_profile), a test that only seeds the new-dimension value proves the happy path; a test that seeds the ABSENT value (legacy row) proves the migration contract. Both are needed.
- A spec promising "streamed stdout cap" for a subprocess that writes to a file is unfalsifiable by unit tests with fake commands. The implementation-time probe must run the real binary before the unit is coded.
- After extracting a method from a free function, delete the free function in the same commit; Go's unused-function tolerance means it will survive indefinitely otherwise.

Retrospective complete — docs/retros/2026-09-02-pdf-extraction-openai-adapter-retro.md
