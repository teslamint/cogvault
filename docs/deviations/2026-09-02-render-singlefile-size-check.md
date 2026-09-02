# Render singlefile size check deviation

## Original contract

The approved design spec states:

> It invokes `pdftoppm` for exactly one page with PNG bytes on stdout and
> streams that output through a 32-MiB-plus-sentinel writer into one owned
> temporary PNG, terminating the renderer on overflow.

Source: `docs/specs/2026-09-01-pdf-extraction-openai-adapter-design.md`,
Architecture section (original paragraph).

The approved plan states:

> A one-page `pdftoppm` stdout stream is bounded before it reaches an owned
> temporary file.

Source: `docs/plans/2026-09-01-001-feat-pdf-extraction-openai-adapter-plan.md`,
line 20.

## Discovered contradiction

During U5 implementation (controlled launchd proof), Poppler 26.08.0's
`pdftoppm -png <file> -` stdout form wrote zero bytes. The `-singlefile`
flag writes the PNG directly to `<prefix>.png` and is the only reliable
render mode on this Poppler build. Because `-singlefile` is a file write
internal to `pdftoppm`, the process cannot be terminated mid-write via a
capped stdout pipe.

## Why documentation alone cannot fix it

The original streaming-cap design assumed `pdftoppm` emits PNG bytes to
stdout. Since it does not on the host Poppler, the streamed-cap pipe is
impossible without patching Poppler itself. The rendered file must be
size-checked after completion.

## New observable behavior

`render()` invokes `pdftoppm -singlefile` to write the PNG to an owned
temporary directory. After the process exits, it checks `os.Stat` on the
file. If the file exceeds `maxImageBytes` (32 MiB), it is deleted and a
permanent error is returned — no OCR or provider call occurs. If the file is
zero bytes, a permanent error is returned.

The pixel preflight (16 megapixels at 200 DPI) still prevents most
legitimate pages from exceeding the byte cap. The difference from the
original contract is that a noisy or high-entropy page above the byte cap is
fully written to disk before deletion, rather than terminated mid-stream.
The temporary file is in an owned `os.MkdirTemp` directory removed on every
outcome, and only one page image exists at a time.

## Traceability

- Approved design spec:
  `docs/specs/2026-09-01-pdf-extraction-openai-adapter-design.md`
  Architecture section, paragraph on renderer.
- Approved plan:
  `docs/plans/2026-09-01-001-feat-pdf-extraction-openai-adapter-plan.md`
  line 20 and U2 step 3.
- U5 evidence:
  `.release-loop/runs/pdf-extraction-openai-adapter/evidence/U5/local-ingest-report.md`
- Implementation owner: `internal/extract/pdf.go` `render()`.
- Test: `TestExtractOversizeRenderedImageRejected` in `internal/extract/pdf_test.go`.
- Updated spec/plan paragraphs reference this deviation file.
