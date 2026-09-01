---
schema: plan/v1
title: PDF extraction and OpenAI-compatible local adapter
type: feat
status: approved
date: 2026-09-01
execution: code
origin: docs/specs/2026-09-01-pdf-extraction-openai-adapter-design.md
body_seal: a5d2589ae0f6f99327dd0841a51ab93657432b8d3461cfbdb0e6f003583c81ad
---

# Goal

Implement complete-only local PDF ingestion: Cogvault extracts PDF text, falls back to bounded Korean-and-English OCR, and sends text plus provenance to an already-ready loopback OpenAI-compatible endpoint. Preserve Claude Code path-mode ingestion.

## Architecture notes

- `llm.Adapter` gains `InputMode()`: `PathInput` preserves Claude Code; `TextInput` selects the new extraction route. `DigestRequest` gains `SourceText`; only `TextInput` adapters require it.
- `internal/extract` owns Poppler/Tesseract subprocesses. It validates UTF-8, trims while streaming, treats a text layer as usable at 80 non-whitespace runes plus one letter/number, and never submits a retained prefix. `pdftotext` drains an over-cap candidate to decide whether an unusable layer must fall back to OCR. One aggregate collector bounds all OCR-page output.
- OCR preflights total page count and each page's dimensions. A one-page `pdftoppm` stdout stream is bounded before it reaches an owned temporary file; Tesseract processes that file, which is removed before the next page.
- `openai` uses loopback `http` only, a canonicalized API-root URL, redirect-disabled endpoint-pinned HTTP clients, `GET /models` readiness preflight, then `POST /chat/completions`. It never starts, loads, warms, or stops a provider.
- Failed/refused retry identity becomes `digest_profile`: backend, model, canonical base URL, `max_input_chars`, and extraction-contract version. `llm_model` stays provenance. Additive SQLite migration keeps legacy rows retryable once, and every terminal attention/notification/status comparison uses the same current profile rather than model alone.
- Rejected alternatives: native PDF/image attachments (all local providers rejected them); source truncation/map-reduce (violates complete-only decision); provider lifecycle ownership (provider-specific operational surface); macOS Vision (unnecessary platform bridge while Tesseract is selected).

## Assumption Recheck

| Approved claim | Fresh evidence | Outcome |
|---|---|---|
| Poppler text extraction and rendering utilities are available. | `command -v pdftotext && pdftotext -v && command -v pdftoppm && pdftoppm -v` at 2026-09-01T02:27:07Z resolved both under `/opt/homebrew/bin`, Poppler 26.08.0. | match |
| Tesseract is installed but Korean data is absent; `tesseract-lang` is available. | `tesseract --list-langs && brew info tesseract-lang` at 2026-09-01T02:27:07Z listed `eng`, `osd`, `snum`; formula available and not installed. | match |
| Local providers reject PDF attachments. | Recheck unavailable: reproducing the prior direct attachment probe requires loading/changing the user's local model runtime. The implementation does not depend on attachment capability; its request contract sends extracted text. | unavailable |
| Local OpenAI-compatible readiness and chat envelopes are observable without lifecycle mutation. | At 2026-09-01T02:30Z, `/v1/models` listed MTPLX's ready model, Unsloth models with `loaded:false`, and Ollama models; MTPLX default text chat returned `200` with `choices[0].message.content="READY"`; Unsloth returned `400` with `error.message="No model loaded. Call POST /inference/load first."`. | match |
| Current ingestion lacks source text and Ollama sends a path-only prompt. | `go test ./...` at 2026-08-31T23:28:44Z passed 14 packages; live inspection of `internal/llm/llm.go`, `ollama.go`, `ingest.go` confirms the current request shape. | match |

The unavailable attachment recheck is not a planning blocker because no active unit relies on attachment capability; U1 tests the text-only request contract directly.

## File structure

- `internal/llm/llm.go`, `openai.go`, `openai_test.go`, existing adapter tests: adapter input modes, text request, readiness, and HTTP classification.
- `internal/config/config.go`, `config_test.go`: `openai` configuration, bounded input limit, local endpoint and PDF-only source-type validation.
- `internal/extract/pdf.go`, `pdf_test.go`: bounded text-layer extraction and per-page OCR fallback.
- `internal/ingest/ingest.go`, `ledger.go`, `attention.go`, `ingest_test.go`, `ledger_test.go`, `attention_test.go`: one shared deadline, extractor integration, additive profile migration, retry gate, notification latch, and current-profile attention filtering.
- `cmd/cogvault/ingest.go`, `cmd/cogvault/status.go`, `cli_test.go`: command prerequisite/readiness validation, OpenAI construction, and current-profile status filtering.
- `deploy/com.teslamint.cogvault.ingest.plist`, `README.md`, `SPEC.md`, `DESIGN.md`: Homebrew PATH, install/reload instructions, public configuration/behavior, and architecture contracts.

## Scenario coverage map

| Scenario | Unit chain | Walking evidence |
|---|---|---|
| S1 text-layer PDF | U1 → U2 → U3 → U4 | U3 integration test with text-layer fake extractor and fake OpenAI HTTP server. Covers S1. |
| S2 Korean scanned PDF | U2 → U3 → U4 → U5 | U2 OCR ordered-page test; U3 ingest integration; U5 controlled launchd observation. Covers S2. |
| S3 unsupported/oversized PDF | U2 → U3 | U2 boundary tests plus U3 ledger-attempt integration. Covers S3. |
| S4 unavailable provider | U1 → U4 → U3 | U1 readiness/HTTP classification tests and U4 command no-ledger test. Covers S4. |
| S5 Claude compatibility | U1 → U3 → U4 | Existing and extended path-mode CLI/ingest tests with extraction tools absent. Covers S5. |

## U1: Define local-provider configuration and adapter contract
Execution note: test-first
Files:
  Create: internal/llm/openai.go, internal/llm/openai_test.go
  Modify: internal/llm/llm.go, internal/llm/claudecode.go, internal/llm/ollama.go, internal/llm/claudecode_test.go, internal/llm/ollama_test.go, internal/config/config.go, internal/config/config_test.go
  Test: internal/llm/openai_test.go, internal/config/config_test.go
Interfaces:
  Consumes: `DigestRequest`, `Adapter`, `LLMConfig`, existing `WithTimeout` option.
  Produces: `type InputMode uint8`; `PathInput`, `TextInput`; `Adapter.InputMode() InputMode`; `DigestRequest.SourceText string`; `NewOpenAI(baseURL, model string, opts ...Option) *OpenAI`; `CheckOpenAIReady(ctx context.Context, baseURL, model string) error`.
Test scenarios:
  happy: OpenAI request posts the canonical `/chat/completions` URL with schema, provenance, and complete source text. Covers S1.
  edge: canonical API roots with and without a trailing slash yield one endpoint; maximum 1,000,000 input characters is accepted.
  error: missing/non-loopback base URL, missing model, invalid source type, invalid input limit, absent listed model, malformed response, and loopback 307/308 redirects fail deterministically without a follow-on request; `loaded:false`, preflight deadline, transport errors, 408/429/5xx, and the exact observed Unsloth unloaded-model message wrap `ErrTransient`.
  integration: n/a — adapter/config boundary.
Steps:
  1. Add failing config and adapter tests for all accepted/rejected backend fields and exact HTTP payload/classification cases.
  2. Add input-mode and `SourceText` contracts; update Claude/Ollama implementations and mocks to retain path mode.
  3. Implement canonical loopback URL validation plus redirect-disabled, endpoint-pinned clients for capped 10-second readiness and chat completion; parse the observed generic model-list, MTPLX completion, and Unsloth unloaded-model envelopes without guessing unobserved variants.
  4. Run `go test ./internal/config ./internal/llm`; inspect request bodies for absent path-read instructions.
  5. Commit: `feat(llm): add local OpenAI-compatible adapter`.
Acceptance: U1 tests pass; no OpenAI request carries a raw PDF, page image, or source-path read instruction.

## U2: Build bounded PDF extraction and OCR fallback
Execution note: test-first
Files:
  Create: internal/extract/pdf.go, internal/extract/pdf_test.go
  Modify: none
  Test: internal/extract/pdf_test.go
Interfaces:
  Consumes: `context.Context`, PDF path, integer input-character ceiling, command-runner seam.
  Produces: `type PDFExtractor`; `func NewPDFExtractor(maxInputChars int, commands Commands) *PDFExtractor`; `func (*PDFExtractor) Extract(ctx context.Context, path string) (string, error)`; exported transient sentinel for deadline/process-start failure.
Test scenarios:
  happy: usable text-layer output returns unchanged complete trimmed text and invokes neither pdfinfo nor OCR. Covers S1.
  edge: 79/80 rune predicate, invalid UTF-8, leading/trailing whitespace, page ordering, 50-page limit, later oversize page, maximum image stream, and aggregate multi-page OCR cap.
  error: a usable over-cap text candidate, invalid text encoding, conversion failure, unusable OCR, renderer overflow, and deadline return the planned permanent/transient classifications. Covers S3.
  integration: fake commands prove scanned Korean-and-English pages concatenate in page order. Covers S2.
Steps:
  1. Create fake executable seams that record argv/context and emit controlled stdout/stderr without host tools.
  2. Write failing tests for trim-aware candidate collection, UTF-8 validity, text-layer/OCR routing, and no-prefix overflow behavior.
  3. Implement bounded `pdftotext`, per-page `pdfinfo`, one-page `pdftoppm` stdout-to-capped-file, and `tesseract -l eng+kor stdout` execution with cleanup.
  4. Add limit, mixed-page, aggregate-overflow, and cancellation controls; verify all temporary paths disappear on each result.
  5. Run `go test ./internal/extract`; commit: `feat(ingest): extract complete PDF text locally`.
Acceptance: U2 tests prove no provider-facing text exists after any incomplete capture and no renderer writes above the image cap.

## U3: Integrate extraction deadlines and retry profiles into ingest
Execution note: test-first
Files:
  Modify: internal/ingest/ingest.go, internal/ingest/ledger.go, internal/ingest/attention.go, internal/ingest/ingest_test.go, internal/ingest/ledger_test.go, internal/ingest/attention_test.go, cmd/cogvault/status.go, cmd/cogvault/cli_test.go
  Test: internal/ingest/ingest_test.go, internal/ingest/ledger_test.go, internal/ingest/attention_test.go, cmd/cogvault/cli_test.go
Interfaces:
  Consumes: `llm.Adapter.InputMode`, `extract.PDFExtractor`, `DigestRequest.SourceText`, `LLMConfig.TimeoutSeconds`.
  Produces: `digest_profile` ledger column; Runner-owned `TextExtractor` seam; one derived per-file context shared by extraction and text-mode adapter call; profile-based terminal attention/status filtering.
Test scenarios:
  happy: text-mode ingest writes and indexes a valid page; path-mode ingest does not invoke the extractor. Covers S1 and S5.
  edge: successful legacy row stays unchanged; a legacy failed row retries once; profile changes for limit/base URL retry exhausted failures and clear only the old profile's terminal attention.
  error: extraction permanent failure increments attempts; extraction/provider transient failure does not; invalid page remains permanent.
  integration: a fake scanned PDF route emits source text to a fake text-mode adapter, writes a page, and records profile. Covers S2 and S3.
Steps:
  1. Extend harness mocks with `InputMode`, a text-extractor seam, and deterministic deadline derivation above both stages.
  2. Write additive SQLite migration tests from 9- and 10-column historical schemas plus two concurrent openers of one temporary database; both a status-like opener and an ingest-like opener must succeed with one preserved profile column.
  3. Implement concurrency-safe idempotent `digest_profile` migration/query/upsert: re-inspect schema after duplicate-column contention and preserve `llm_model` provenance/legacy retry semantics.
  4. Replace model-only retry, `wasAlreadyExhausted`/`wasAlreadyRefused`, notification, and status attention checks with one current-profile calculation; test a changed base URL or input cap produces exactly one new terminal notification.
  5. Derive one per-file deadline before extraction, map extractor errors, populate `SourceText`, and pass its remaining context to text-mode adapters.
  6. Run `go test ./internal/ingest -race ./cmd/cogvault`; commit: `feat(ingest): route local PDF text through retry profiles`.
Acceptance: deterministic tests prove extraction consumes the adapter's remaining shared budget, redirects never escape loopback, concurrent migration succeeds, and profile changes re-enable only failed/refused rows while status/attention reflect the current profile.

## U4: Wire CLI prerequisites and deployment documentation
Execution note: test-first
Files:
  Modify: cmd/cogvault/ingest.go, cmd/cogvault/cli_test.go, deploy/com.teslamint.cogvault.ingest.plist, README.md, SPEC.md, DESIGN.md
  Test: cmd/cogvault/cli_test.go, scripts/make_install_test.sh
Interfaces:
  Consumes: `config.LLMConfig`, `llm.NewOpenAI`, `llm.CheckOpenAIReady`, `extract` prerequisite validator.
  Produces: openai command wiring; fail-before-ledger diagnostics for missing Poppler/Tesseract/language data; launchd PATH including `/opt/homebrew/bin`.
Test scenarios:
  happy: configured OpenAI backend constructs the adapter only after tools and readiness succeed. Covers S1.
  edge: the template retains standard and Claude CLI PATH entries while adding Homebrew.
  error: missing `pdftotext`, `pdfinfo`, `pdftoppm`, `tesseract`, `eng`, `kor`, unavailable readiness, and missing binary fail before Runner/ledger construction. Covers S4.
  integration: path-mode Claude command with scrubbed extraction PATH still constructs normally. Covers S5.
Steps:
  1. Add PATH-seam command tests for every prerequisite and ready/not-ready model outcome.
  2. Wire `openai` adapter setup and preflight before `ingest.New`; preserve dry-run and Claude behavior.
  3. Update template comments/PATH, README install/reload and `brew install tesseract-lang` instructions, then update SPEC/DESIGN configuration and ingest diagrams.
  4. Run `go test ./cmd/cogvault ./internal/config ./internal/llm` and `bash scripts/make_install_test.sh`.
  5. Commit: `feat(cli): prepare local PDF ingestion runtime`.
Acceptance: no failing local-provider prerequisite writes a ledger row; the deployed template can resolve Homebrew tools without dropping current PATH entries.

## U5: Validate installed OCR and scheduled local ingest
Execution note: test-first
Files:
  Create: .release-loop/runs/pdf-extraction-openai-adapter/evidence/U5/local-ingest-report.md
  Modify: none
  Test: no committed test file; execute the accepted operational proof.
Interfaces:
  Consumes: signed installed binary, approved feature configuration, Homebrew `tesseract-lang`, ready loopback provider, copied updated launchd template.
  Produces: disposable evidence naming the temporary launchd label, configured test fixture, provider endpoint/model, report/ledger outcome, exact template PATH, and popup observation.
Test scenarios:
  happy: one configured text-layer PDF produces a valid source page under the temporary launchd job. Covers S1.
  edge: one Korean scanned fixture exercises OCR when available. Covers S2.
  error: provider stopped or model marked unavailable exits transiently without a ledger attempt. Covers S4.
  integration: launchd PID completion plus exact stdout success marker are both observed within one timeout, following `scripts/check-scheduled-access.sh`. Covers S1 and S2.
Steps:
  1. Install `tesseract-lang` with Homebrew only after confirming the exact package at point of execution; verify `kor` appears in `tesseract --list-langs`.
  2. Build a feature binary in the private proof root and sign that isolated copy with the current stable identity; never invoke `scripts/install-signed.sh` because it restarts the production ingest label. Record the production label's program path/state before and after and require both unchanged.
  3. Start or select a ready local provider outside Cogvault, then run one temporary launchd job using the updated template PATH and one bounded source fixture.
  4. Observe system consent UI, process exit, exact report marker, page validity, and ledger status; retain sanitized evidence under the release artifact root and clean temporary job/config/data.
  5. Commit only sanitized repository evidence if it contains no private source paths, provider credentials, or user-specific identifiers: `test(ingest): record local scheduled PDF proof`.
Acceptance: the exact temporary job produces a valid page and success ledger row with no observed AppData prompt; cleanup leaves no temporary launchd label or private fixture directory, and the production job is unchanged.

## Mutation/failure-state matrix

| Transition | Pre-state | Action | Expected post-state | Unit / evidence | Success; forced failure; rerun; compensation; headless; cancellation |
|---|---|---|---|---|---|
| `M1-ledger-profile` | Existing SQLite ledger lacks `digest_profile`; concurrent command opens are possible before the ingest flock. | Migration checks schema and adds default-empty column with duplicate-column re-inspection. | Existing rows preserved; exactly one profile column; missing profile retries once. | U3; `.release-loop/runs/pdf-extraction-openai-adapter/evidence/U3/`. | Success: two concurrent temp-DB openers, including status-like access, succeed and preserve rows. Forced failure: temp DB seam returns ALTER error before runner creation. Rerun: reopen succeeds once schema exists. Compensation: production migration is additive/irreversible; restore the operator's pre-run DB backup only under manual recovery, never auto-drop a column. Headless: unit tests use temp DB only. Cancellation: context does not reach migration; process interruption before commit leaves SQLite atomic DDL outcome verified by reopen. |
| `M2-tesseract-language` | `kor` traineddata absent. | `brew install tesseract-lang`. | `tesseract --list-langs` includes `kor`. | U5; local sanitized evidence. | Success: exact package installs and language check passes. Forced failure: use `brew install --dry-run tesseract-lang` where available; no state changes. Rerun: Homebrew reports installed/current. Compensation: do not auto-uninstall shared language data; operator may run `brew uninstall tesseract-lang` knowingly. Headless: forbidden because package installation changes the host. Cancellation: stop before package mutation or inspect Homebrew's transaction state, then rerun the exact install command. |
| `M3-temporary-launchd-proof` | No private temp job/config/wiki/DB exists; production ingest label state/path captured. | Bootstrap a unique temporary launchd label from copied updated template and private signed binary. | One job result plus evidence; then bootout and remove private files; production label state/path unchanged. | U5; `.release-loop/runs/pdf-extraction-openai-adapter/evidence/U5/`. | Success: PID and exact stdout marker observed, then temporary label/files absent and production state/path compare equal. Forced failure: fake provider returns readiness failure in the private config; report shows no ledger attempt. Rerun: create a fresh label/private root after cleanup. Compensation: `launchctl bootout gui/<uid>/<label>` then remove only the owned private root. Headless: forbidden because popup observation needs a human. Cancellation: trap bootout/retain 0600 diagnostics, print exact recovery command, and do not delete evidence until inspected. |

## Carry-forward trigger audit

Audited `ROADMAP.md` at `c6d23216063b1e14d677a1b26a3487a66c2d573c`: 0 open rows, 0 fired, 0 unobservable. The tracker has no open carry-forward row to classify; its Next phase section explicitly has no committed scope.

## Deferred to Follow-Up Work

- Provider lifecycle ownership, model warm-up, and shutdown.
- Direct text ingestion for non-PDF source types through `openai`.
- OCR languages beyond `eng+kor`.
- PDF chunking/map-reduce or partial summaries.

## Open unknowns

Planning-time: none.

Implementation-time: verify the exact Poppler stdout flags for a one-page PNG in a disposable fixture before U2 implementation; use the documented command only after this probe proves byte-bounded piping works. Confirm exact local-provider `/models` response envelope against Unsloth and MTPLX while implementing U1; unsupported envelopes remain a configuration error rather than guessed readiness.
