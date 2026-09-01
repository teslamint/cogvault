# Roadmap — cogvault

Status: non-canonical forward-looking index
Last updated: 2026-08-27

`SPEC.md`, `DESIGN.md`, and accepted decisions own behavior, architecture, and
boundaries. This file records only what remains selectable; it never creates
scope by itself.

---

## v2 — capture → digest → consume (complete)

The v2 refounding is complete. Its Phase 1 success criteria are met, and the
post-v2 tracker F1–F18 is closed. The delivered product includes:

- one read-write `wiki_dir` plus read-only external `sources[]`;
- scheduled batch ingest: scan → digest → validate → write → index → ledger;
- Claude Code and local Ollama LLM backends, with classified failures;
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

Potential discovery themes, not commitments:

- **Capture friction:** revisit a dedicated share-sheet target or inbox
  consume-and-archive semantics only if the configured synced-folder pattern
  proves insufficient.
- **Retrieval quality:** consider full reciprocal-rank fusion only after a
  second independent ranking signal produces measurable value beyond current
  embedding + FTS fallback behavior.
- **Operational hardening:** prioritize reproducible defects or uncovered
  contracts on live paths over coverage percentage alone.

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
