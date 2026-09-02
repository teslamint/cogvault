---
title: PDF text extraction and OpenAI-compatible local provider adapter
status: approved
date: 2026-09-01
schema: spec/v1
---

# PDF text extraction and OpenAI-compatible local provider adapter Design

_Created 2026-09-01._

## Overview

Scheduled ingest currently gives `claudecode` a source path. Local HTTP providers cannot read that path, and all three installed providers rejected a PDF attachment over their documented request surface. Cogvault will own PDF extraction, including Korean-and-English OCR for image-only PDFs, then send extracted text plus the source metadata required for page provenance to a new OpenAI-compatible HTTP adapter. The adapter only calls an already-ready local endpoint; it never starts a provider or loads a model.

## User Scenarios

### S1: Digest a text-layer PDF through a local endpoint

An operator configures `llm.backend: openai`, a loopback OpenAI-compatible `llm.base_url`, a model ID, and `llm.max_input_chars`. A scheduled ingest extracts the complete text layer from a source PDF and creates a schema-conforming source page through Unsloth, MTPLX, or Ollama's OpenAI-compatible endpoint. No provider is instructed to access the source path.

### S2: Digest a Korean scanned PDF

A scanned Korean-and-English PDF has no usable text layer. Ingest renders its pages, OCRs them with Tesseract `eng+kor`, sends the complete OCR result to the configured endpoint, and writes the resulting source page. The user performs no per-file conversion.

### S3: Explain an unsupported or oversized source

A PDF has no recoverable text after OCR, or its complete extracted text exceeds `llm.max_input_chars`. Ingest records a permanent per-file failure that names the extraction cause. It does not create a partial page, truncate the source, or silently substitute a summary of only some pages.

### S4: Recover from an unavailable provider

The configured endpoint is down, returns 408/429/5xx, or does not have the configured model ready. Ingest records the failure as transient and retries on a later scheduled run. Cogvault does not start a server or load a model.

### S5: Preserve Claude Code behavior

An operator keeps `llm.backend: claudecode`. Ingest continues to pass the original source path to Claude Code and does not require Poppler, Tesseract, or `tesseract-lang` for that backend.

## Scope

### In

- A new `openai` LLM backend using `POST /v1/chat/completions` against a configured local OpenAI-compatible endpoint.
- A provider input-mode contract so ingest extracts source text only for adapters that require text rather than a provider-readable path.
- PDF text extraction using `pdftotext`; image-only fallback through `pdftoppm` and Tesseract with `eng+kor`.
- Overflow-aware, complete-only text capture controlled by a required positive `llm.max_input_chars` for `openai`.
- Startup validation of required utilities and Tesseract language data for the `openai` backend; no automatic package installation.
- The `openai` backend accepts `.pdf` sources only in this release. Configuration validation rejects an `openai` configuration whose `sources[].types` contains any other extension; those formats remain available through existing path-mode backends.
- Exact transient/permanent classification, configuration validation, tests, README setup, the launchd template PATH, and updates to SPEC.md and DESIGN.md.

### Out

- Automatic installation, launch, model load, warm-up, shutdown, or monitoring of Ollama, Unsloth, or MTPLX.
- Sending raw PDFs, page images, or a filesystem path as provider content to read.
- PDF text chunking, map-reduce summarization, truncation, or partial source-page generation.
- OCR for languages other than English and Korean.
- OCR or conversion support for non-PDF source formats.
- Direct text ingestion for non-PDF source formats through the `openai` backend.
- Changing the existing `claudecode` or legacy `ollama` backend behavior.

## Assumptions and Preconditions

| Claim | Command | Observed at | Observed result | Evidence source |
|---|---|---|---|---|
| Poppler text extraction and page rendering utilities are available on the maintainer machine. | `command -v pdftotext && pdftotext -v`; `command -v pdftoppm && pdftoppm -v` | 2026-09-01T08:27:00+09:00 | Both resolve under `/opt/homebrew/bin`; Poppler 26.08.0. | Local benchmark, sanitized transcript |
| Tesseract is available but Korean data is not yet installed. | `tesseract --version`; `tesseract --list-langs`; `brew info tesseract-lang` | 2026-09-01T08:27:00+09:00 | Tesseract 5.5.3; installed languages are `eng`, `osd`, `snum`; Homebrew `tesseract-lang` is available but not installed. | Local benchmark, sanitized transcript |
| A local OpenAI-compatible provider cannot consume a PDF as an image attachment. | OpenAI-compatible `POST /v1/chat/completions` with a `data:application/pdf;base64,...` image URL | 2026-09-01T08:20:00+09:00 | Unsloth and MTPLX returned HTTP 400; Ollama's native image input returned HTTP 400. | Local benchmark, sanitized transcript |
| Existing ingestion has no source-text field and the existing Ollama adapter sends only `buildPrompt(req)`. | `go test ./...` and inspection of `internal/llm/llm.go`, `internal/llm/ollama.go`, and `internal/ingest/ingest.go` | 2026-09-01T08:28:44+09:00 | Baseline passed 14 packages; `DigestRequest` has path/schema/slug/extension only, while Ollama sends a prompt containing the path. | Feature worktree at `d00c179` |

Repository invariants: only `wiki_dir` is writable/MCP-addressable; sources remain direct read-only ingest inputs. Per-file permanent failures consume attempts, while transient and infrastructure failures do not.

## Architecture

```text
source PDF
  └─ ingest Runner ── adapter input mode: text ──► PDF extractor
       ├─ pdftotext (complete text layer)
       └─ if no usable text: pdftoppm → Tesseract eng+kor (ordered pages)
  └─ DigestRequest.SourceText ──► OpenAI-compatible Adapter
       └─ POST <base_url>/chat/completions ──► page validation, write, index, ledger
```

`internal/llm.Adapter` gains a small input-mode declaration. `ClaudeCode` keeps path mode. The new `OpenAI` adapter declares text mode; `Runner.digestOne` invokes the PDF extractor only in that mode and passes the resulting text in `DigestRequest.SourceText`. This keeps source access in Cogvault's existing ingest trust boundary and prevents the HTTP provider from needing filesystem permissions.

Extracted bytes must be valid UTF-8. The streaming collector validates UTF-8 across chunk boundaries; invalid input is a permanent extraction-encoding failure and is never lossy-normalized. It discards leading whitespace, retains a trailing-whitespace run only as bounded counters until a later non-whitespace rune decides whether it is internal, and discards that run at EOF. `llm.max_input_chars` therefore applies exactly to the resulting `strings.TrimSpace` text. Text is usable only when it contains at least 80 non-whitespace Unicode runes and at least one Unicode letter or number.

`pdftotext` uses a bounded candidate collector plus unbounded scalar state only: rune count, letter-or-number presence, and overflow flag. It drains the command to EOF without retaining bytes beyond the cap. After EOF, an unusable candidate is discarded and triggers OCR even if it overflowed; a usable candidate with overflow is the permanent complete-input over-limit failure. Thus an oversized punctuation-only or letterless text layer cannot suppress OCR.

The extractor is an `internal` package with a context-aware command seam. Its trim-aware retained-text cap is `4 * llm.max_input_chars + 1` bytes; valid UTF-8 admits every representation of the configured code-point limit plus an overflow sentinel. One aggregate collector persists across all per-page Tesseract output and page separators. OCR overflow terminates the current subprocess and fails permanently before `Adapter.Digest`, since no later fallback exists. No retained prefix is ever sent.


Before OCR, the extractor calls `pdfinfo` to obtain and reject a document above 50 pages. Before rendering each individual page, it performs that page's own dimension query (parsing both the whole-document `Page size:` line and the ranged `Page N size:` forms, since some Poppler builds emit only the ranged form) and rejects it above 16 megapixels at 200 DPI; a later oversized page therefore never reaches the renderer. It invokes `pdftoppm -singlefile` for exactly one page, writing a PNG file to an owned temporary directory (Poppler's stdout form writes zero bytes on some builds — verified empty on Poppler 26.08.0). After the render completes, the file size is checked and the image is deleted if it exceeds the 32 MiB cap; the bounded PNG is then OCRed and removed before the next page. This limits workspace use to one bounded page image rather than the whole document. A page-count, pixel, or image-byte breach terminates processing, removes the owned directory, and returns a permanent source-too-large/complex error. See `docs/deviations/2026-09-02-render-singlefile-size-check.md`.

The extractor runs each subprocess under the caller's deadline and removes its owned temporary directory on every outcome. It preserves page order. If `pdftotext` emits usable text, `pdfinfo`, rendering, and OCR are not invoked. A deadline or process-start failure is transient; a source-specific conversion failure, unusable OCR result, capture overflow, and render-resource breach are permanent because retrying cannot recover the source or make a complete input fit.

`Runner.digestOne` derives one per-file context with the effective `llm.timeout_seconds` before extraction, passes that exact context to both extractor and `Adapter.Digest`, and cancels it after the latter returns. Thus rendering/OCR consumes the same deadline budget as the HTTP call; adapters must not derive a fresh independent timeout for text-mode requests.

The command entrypoint validates `pdftotext`, `pdfinfo`, `pdftoppm`, `tesseract`, and Tesseract's `eng` plus `kor` language data before constructing the openai adapter. It then queries `<base_url>/models` with a 10-second context and a 1-MiB response cap: an absent configured model is a configuration error before ledger mutation; an exact model entry with `loaded: false`, preflight deadline, or transport failure is a transient run-start failure before ledger mutation. Missing tools or language data similarly stop the command before any ledger mutation and name the required Homebrew setup. This implementation changes `deploy/com.teslamint.cogvault.ingest.plist` to add `/opt/homebrew/bin` while retaining existing PATH entries, and updates README's template-copy/reload procedure; the launchd proof uses that updated shipped configuration. Cogvault does not load the model.

## Interface and Configuration

`llm.backend` accepts `openai` in addition to current values. For this backend:

```yaml
llm:
  backend: openai
  base_url: http://127.0.0.1:8888/v1
  model: unsloth/Qwen3.8-27B-GGUF
  max_input_chars: 200000
  timeout_seconds: 300
```

- `base_url` is required, uses `http`, and must name a loopback API root (`localhost`, `127.0.0.1`, or `::1`); the adapter appends `/chat/completions` exactly once.
- `model` is required and sent unchanged in the request body.
- `max_input_chars` is required, positive, at most 1,000,000, and applies to Unicode code points after trim-aware extraction. The collector rejects an over-limit source; it never slices text. This ceiling makes the retained UTF-8 bound at most 4,000,001 bytes and avoids multiplication overflow. Its effective value is part of the text-mode digest profile.
- `timeout_seconds` defines the one per-file deadline that `Runner.digestOne` derives before extraction. Provider calls receive only its remaining context budget.
- No API key, server lifecycle setting, or model-loading setting is introduced. A provider needing authorization or a manually loaded model is an operator responsibility outside Cogvault.

The OpenAI request contains a system instruction carrying the wiki schema and a user message carrying source metadata plus extracted text. It requests a markdown source page only. Response parsing accepts the first non-empty assistant text choice and strips an optional markdown fence using the existing normalizer.

HTTP transport failures, 408, 429, 5xx, and a caller deadline wrap `llm.ErrTransient`; other HTTP failures, malformed JSON, no usable choice, and empty output are permanent. The only transient 4xx is a JSON `error.message` containing case-insensitive `no model loaded` or `model is not loaded`, which is the declared fallback for a provider that omits `loaded` from `/models` yet rejects an unloaded listed model at chat time. Extractor errors retain the classification specified above, and the existing ingest page validator records invalid final pages as permanent. The ingest ledger remains the sole authority for retry accounting.

The ledger retains `llm_model` for provenance and adds a `digest_profile` value for failed/refused retry gating. The text-mode value contains the backend, model, canonicalized loopback `base_url`, `max_input_chars`, and extraction-contract version; changing any part makes a prior failed row retryable even when its source hash is unchanged. A legacy row with no profile is retryable once. Successful rows retain the existing hash-based unchanged behavior.

## Testing

- `internal/extract` unit tests use fake `pdftotext`, `pdfinfo`, `pdftoppm`, and `tesseract` executables to prove text-layer preference, ordered OCR fallback, `eng+kor` invocation, cleanup, deadline propagation, empty-output rejection, and over-limit rejection without requiring host tools. Boundary fixtures cover invalid UTF-8 (including invalid bytes followed by otherwise within-limit text), over-cap leading/trailing whitespace with within-limit trimmed text, 79 vs. 80 non-whitespace runes, text without a letter or number, a usable prefix followed by capture overflow, an over-cap letterless text layer that falls back to OCR, and multi-page aggregate OCR overflow; overflow fixtures prove no HTTP request or page and one permanent attempt.
- `internal/llm` HTTP tests assert OpenAI request URL/body, schema and source text placement, fence removal, response-shape errors, each retry classification, `/models` absent-model configuration failure, `loaded: false` transient run-start failure, a hanging preflight that expires within its injected 10-second deadline without ledger mutation, and the two exact unloaded-model 4xx messages.
- `internal/ingest` tests prove extraction occurs only for text-mode adapters; extractor permanent failures consume an attempt, unavailable-provider errors do not, and exhausted rows become retryable after `max_input_chars` or canonical `base_url` changes.
- The per-file timeout test uses injected deadline derivation and command/HTTP seams. It records the deadline passed to extraction and the deadline received by the adapter, advances a bookkeeping clock during extraction, and proves the adapter receives the remaining shared budget without wall-clock sleeps.
- `internal/config` and `cmd/cogvault` tests cover required OpenAI fields, rejection of zero, negative, over-1,000,000, and arithmetic-overflow-adjacent `max_input_chars` values, rejection of non-PDF `sources[].types`, backend selection, and every missing external prerequisite. Extractor tests prove page-count, pixel, and rendered-image-byte limit breaches remove the temp directory, make no provider call, and consume one permanent attempt; a mixed-dimension fixture proves a later oversize page is never rendered, and an overflowing renderer stdout never writes more than the configured image cap. Existing `claudecode` tests prove no extractor prerequisite leaks into its path.
- A controlled launchd one-shot, using a preloaded local provider and a single known PDF, proves: a valid source page is written; report/ledger record success; no AppData prompt is observed for that exact scheduled job. This observation is scoped to that execution and does not claim TCC persistence or coverage of unconfigured paths.

## Risks and Mitigations

| Risk | Mitigation |
|---|---|
| OCR rendering expands a small or malicious PDF into excessive temporary data. | Preflight page count and each page's dimensions, render one 200-DPI page at a time to stdout, cap the streamed PNG at 32 MiB, then remove it before continuing. |
| A document exceeds model context or an operator raises the input limit. | Reject complete-only over-limit input, persist the text-mode digest profile, and retry failed rows when that profile changes. |
| The endpoint is cold, unavailable, or stalls during readiness. | Do not own lifecycle; use a capped 10-second `/models` preflight, reject an absent configured model before ledger mutation, treat `loaded: false`, preflight expiry, or the two declared unloaded-model messages as transient, and retry on the next schedule. |
| Raw PDF content or a path-based read request reaches a provider. | The text-mode adapter receives extracted text plus provenance metadata required by source-page frontmatter, but emits no raw PDF bytes, page images, or instruction to read a path. |
| launchd cannot find Homebrew-installed extraction tools. | Ship `/opt/homebrew/bin` in the ingest template PATH, document reload after template installation, and prove the controlled one-shot with that exact template. |

## Success Criteria

1. A text-layer PDF reaches an OpenAI-compatible endpoint as complete extracted text plus required provenance metadata, never as raw PDF bytes, page images, or a path-based read instruction.
   - **Measured by**: extractor and adapter unit tests asserting request content, rejecting missing `SourceText`, and rejecting the legacy path-read prompt.
2. A Korean scanned PDF falls back to ordered Tesseract `eng+kor` OCR and yields a source page when the fake provider returns valid markdown.
   - **Measured by**: deterministic ingest integration test with fake renderer, OCR executable, and HTTP provider.
3. An extracted result over `llm.max_input_chars`, including a writer-overflowed usable prefix or aggregate multi-page OCR overflow, creates no page and consumes one permanent failure attempt; after the effective input limit or endpoint changes, the same source hash is retried rather than remaining exhausted.
   - **Measured by**: deterministic ingest test inspecting no HTTP call, report action, ledger attempts, and profile-change retry behavior.
4. OCR rejects a source exceeding the 50-page, 16-megapixel-per-page, or 32-MiB-rendered-image limit, cleans every owned temporary artifact, creates no page, and consumes one permanent failure attempt. A later oversized page is rejected before rendering, and renderer stdout cannot write more than the image cap.
   - **Measured by**: fake `pdfinfo`/renderer integration tests that exceed each bound and assert cleanup, capped output, and no HTTP request.
5. A missing Poppler/Tesseract prerequisite fails `cogvault ingest` before any ledger mutation.
   - **Measured by**: command test with PATH seams and an unchanged temporary ledger.
6. A 408, 429, 5xx, transport timeout, capped 10-second `/models` preflight failure, `/models` `loaded: false`, or either declared unloaded-model response consumes no attempt; malformed provider output consumes one; extraction and HTTP share one deterministic per-file deadline.
   - **Measured by**: OpenAI adapter plus ingest ledger tests, including a hanging preflight seam, and a deadline-derivation seam test with no wall-clock sleep.
7. `llm.max_input_chars` rejects values above 1,000,000 before extraction, and a letterless over-cap text layer still reaches OCR rather than becoming a partial text-layer failure.
   - **Measured by**: config bounds tests and extractor fake-command test that observes OCR after the over-cap letterless candidate.
8. Existing Claude Code ingestion remains operational without Poppler or Tesseract available.
   - **Measured by**: existing and extended `cmd/cogvault`/`internal/llm` tests with a scrubbed PATH, plus `go test -race ./...`.
9. A configured launchd one-shot using the shipped template and a ready local provider completes with a valid page and no observed AppData consent prompt for that exact job execution.
   - **Measured by**: operator-run launchd procedure that records job label, source fixture, report, ledger result, template PATH, and popup observation; it makes no persistence claim.
None. The user selected OCR in scope, Homebrew `tesseract-lang` for Korean data, provider lifecycle outside Cogvault, and complete-only handling for over-limit text.
