# Roadmap — cogvault

Status: non-canonical forward-looking index
Last updated: 2026-11-05

`SPEC.md`, `DESIGN.md`, and accepted decisions own behavior, architecture, and
boundaries. This file records only what remains selectable; it never creates
scope by itself.

---

## v2 — capture → digest → consume (complete)

The v2 refounding is complete. Its Phase 1 success criteria are met, and the
post-v2 tracker F1–F18 is closed. The delivered product includes:

- one read-write `wiki_dir` plus read-only external `sources[]`;
- scheduled batch ingest: scan → digest → validate → write → index → ledger;
- Claude Code, Ollama, and loopback OpenAI-compatible LLM backends, with classified failures;
- local PDF text extraction and OCR for the OpenAI-compatible backend;
- MCP and CLI retrieval, lint, synthesis, graph, fetch, embedding, and similar
  search workflows;
- remote HTTP/SSE access with bearer or OAuth authorization; and
- opt-in Git history safety for wiki mutations, including serialized commits
  and Git-control-path write protection.

Evidence and owners:

- v2 decision: [0021](docs/decisions/0021-v2-refounding.md)
- Phase 1 measurements and all F1–F18 outcomes:
  [v2-follow-ups.md](docs/research/v2-follow-ups.md)
- shipped behavior: [SPEC.md](SPEC.md), [DESIGN.md](DESIGN.md)
- recent safety-net closure: [0024](docs/decisions/0024-wiki-git-safety-net.md)

Completed ideas no longer appear as future work. Their rationale remains in
[project background](docs/context/project-background.md) and their contracts
remain with the canonical owners above.

---

## Next phase — no committed scope

No next feature has an approved spec. Select work from measured user friction,
operational evidence, or an evidence-backed maintenance finding; write and
approve a spec before implementation.

The following candidates are not implementation commitments. Each requires
evidence and an approved scope before implementation.

| Candidate | Next action | Selection or completion evidence |
|---|---|---|
| Retrieval quality evaluation | Select a ranking change only against the recorded baseline. | Baseline recorded on 2026-11-05 in [retrieval-quality-baseline](docs/research/2026-11-05-retrieval-quality-baseline.md): 44 labeled real queries, MRR 0.292, recall@10 0.304, identical across 3 runs. Labels are implicit (search followed by read), so treat the score as a regression baseline, not absolute quality. Consider reciprocal-rank fusion only when another independent signal adds measured value. |
| Inbox consume-and-archive | Record user friction with the current synced-folder workflow. | Select only if that workflow proves insufficient. Resolve source ownership and archive semantics against the read-only source boundary first. Checked 2026-11-05: no friction is recorded in `docs/` or the ingest logs, and every logged inbox file was digested; remains unselected. |
| Digest disclaimer retention | Decide whether to pursue the measured gap; a loop needs many more samples per document. | Measured 2026-11-04 on main `069b889` with the current source-page guidance: PDF digests kept the source's material disclaimer in 4 of 27 runs (9 local PDFs × 3; private corpus, not committed). Run-to-run spread was 75% of the mean. No no-guidance control was run, so the guidance's effect is unmeasured. |

The two F16 follow-up checks from the
[F16 retrospective](docs/retros/2026-08-21-f16-bound-cli-capture-retro.md)
closed on 2026-11-05 under the approved
[capture separation spec](docs/specs/2026-09-09-bounded-capture-error-separation-design.md):
`TestRunClaudeCodeStdoutOverflowExhaustsPermanentAttempts` drives the real Claude
Code adapter through ingest and proves three permanent attempts, then exhaustion
with no fourth CLI call. The capture boundary review accepts whole crossing-chunk
loss; `DESIGN.md` now records it, and capacity rejection is separate from pipe
draining. Full-capacity prefix retention remains out of scope.

Existing ingest preflight already checks extraction tools, language data, and
model readiness. A separate `doctor` command needs evidence of an unmet need.
See [SPEC §9.4](SPEC.md#94-ingest).

---

## Operational evidence and shared workflow follow-ups

These records retain ownership and status separately from product candidates.
The [latest retrospective](docs/retros/2026-09-02-pdf-extraction-openai-adapter-retro.md)
registered four process carry-forwards. All four are accounted for below.

| Item | Owner | Status and next action |
|---|---|---|
| Consolidate temporary access-check evidence | cogvault operational records | Complete on 2026-09-09. The [new ceremony](docs/research/2026-09-09-access-check-live-evidence.md) records two successful runs, operator-confirmed no-popup observation, and independent cleanup checks for one label. The [historical U4 record](docs/research/2026-09-09-access-check-u4-operational-record.md) retains its original label mismatch. |
| Local review fallback for stuck webhook reviews | compound-loop shipping | Transferred for implementation to [issue #35](https://github.com/teslamint/compound-loop/issues/35). Registration does not mean the workflow fix is complete. |
| Run required probes before dependent implementation | compound-loop planning and implementing | Transferred for implementation to [issue #36](https://github.com/teslamint/compound-loop/issues/36). Registration does not mean the phase gate is complete. |
| Project-owned decision policy guidance | Each consuming project owns policy; EntireContext provides optional guidance | Completed in [EntireContext PR #251](https://github.com/teslamint/entirecontext/pull/251) on 2026-09-08. Cogvault policy adoption remains an independent project choice. See the [ownership record](docs/research/2026-09-05-decision-verification-policy-ownership.md). |

---

## Not planned without a new decision

These are product boundaries, not deferred backlog items.

| Item | Current boundary | Owner |
|---|---|---|
| Watch mode / resident daemon | Batch + launchd remains the chosen ingestion model; revisit only if schedule latency proves unacceptable. | [0021](docs/decisions/0021-v2-refounding.md) D2 |
| AAAK-style compressed representations | Would replace the Markdown-visible abstraction. | [0014](docs/decisions/0014-roadmap-adoption-boundaries.md) D2 |
| Vector search replacing FTS | Retrieval extensions must complement, not silently replace, the retrieval core. | [0014](docs/decisions/0014-roadmap-adoption-boundaries.md) D2/D3 |
| Temporal knowledge graph as required core | Adds a reasoning model beyond the current wiki builder. | [0014](docs/decisions/0014-roadmap-adoption-boundaries.md) D2 |
| Conversation mining as primary mode | Changes the product’s input model. | [0014](docs/decisions/0014-roadmap-adoption-boundaries.md) D2 |
| Direct Anthropic API fallback | The local LLM backend is the intended escape hatch. | [0021](docs/decisions/0021-v2-refounding.md) D3 |

---

## Related records

- [SPEC.md](SPEC.md) — public behavior and scope
- [DESIGN.md](DESIGN.md) — architecture and package boundaries
- [v2-follow-ups.md](docs/research/v2-follow-ups.md) — closed operational tracker
- [project-background.md](docs/context/project-background.md) — history and broader context
- [0014](docs/decisions/0014-roadmap-adoption-boundaries.md) — adoption boundaries
- [0021](docs/decisions/0021-v2-refounding.md) — v2 refounding decision
