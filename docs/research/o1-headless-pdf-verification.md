# O1 Spike: Headless PDF Digestion Verification

Date: 2026-07-22. Plan unit U1 of `docs/plans/2026-07-22-001-feat-v2-capture-pipeline-plan.md`; resolves spec Open Decision O1.

## Verdict

**Positive in both contexts.** `claude --print` reads a PDF by path and emits a frontmatter markdown page, both from an interactive shell and from a launchd-spawned process. The pdftotext fallback (spec Scope Out conditional) is **not activated**.

## Test subject

One Korean-language article PDF from the real corpus, 53,968 bytes, sha256 prefix `ed19521ccb29898e` (file not committed; referenced by hash per privacy rule).

## Working argv template (for the U5 claudecode backend)

```
/Users/teslamint/.local/bin/claude --print --output-format json --allowedTools "Read"
```

- Prompt goes on **stdin** (avoids ARG_MAX, per CLAUDE.md §6 guidance). The prompt names the absolute PDF path and instructs "output ONLY a markdown wiki page" with the frontmatter shape.
- `--allowedTools "Read"` is sufficient permission for the CLI to read the PDF; no `--permission-mode` flag was needed.
- Output is a JSON **array** of events; the last element has `"type":"result"`. On success: `"subtype":"success"`, `"is_error":false`, and the page text in `"result"`. Parse: take the final array element, check `subtype`/`is_error`, read `result`.

## Measured behavior

| Context | Exit | Duration | Result |
|---|---|---|---|
| Interactive shell | 0 | 38.1s | valid page, frontmatter first line |
| launchd (one-shot submit, minimal PATH) | 0 | 31.3s | valid page **wrapped in a ```markdown code fence** |

## Findings the backend must absorb

1. **Code-fence instability**: the same prompt returned bare markdown interactively and fence-wrapped markdown under launchd. The claudecode backend must strip an optional leading/trailing ``` fence (with or without a language tag) before frontmatter validation.
2. **PATH**: launchd provides `/usr/bin:/bin:/usr/sbin:/sbin`. The plist must use the absolute `claude` binary path. User-level Claude Code hooks that invoke `node` fail ("node: command not found") but do not affect the exit code or result; the shipped plist should either extend PATH with the node directory or accept these harmless hook failures.
3. **TCC**: reading `~/Downloads` from the launchd-spawned process succeeded without a prompt on this machine. If a future macOS update tightens this, the failure mode is a per-file read error (transient class), visible in the ingest report.
4. **Auth**: subscription auth resolved non-interactively under launchd (keychain accessible in the GUI session domain).
5. **One-shot `launchctl submit` re-triggered the job once after completion** (second env-check line in stderr before removal). The production plist uses `StartInterval` and an ingest-level lockfile, so duplicate concurrent digestion is already guarded; noted for awareness.
6. Reported per-call metadata: `total_cost_usd` ≈ 1.39 and ~35s for a 53KB PDF. For the 65-file backlog expect roughly 40 minutes of wall-clock LLM time; `--limit` batching remains advisable.

## Raw transcripts

Sanitized JSON/stderr transcripts under `.release-loop/evidence/U1/` (local, gitignored; contain source filenames and are therefore not committed).
