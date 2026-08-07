# llm-wiki-for-scientists Review

Status: reference
Date: 2026-08-07
Source: https://github.com/chaek-union/llm-wiki-for-scientists

## What it is

A 30-chapter methodology book (GitBook) by 안준용 (고려대) applying
Karpathy's LLM-Wiki pattern to academic research. Based on real usage
with 16,000+ PDFs and 18,000+ wiki pages. Not a software project.

## Ideas adopted into the cogvault roadmap

| Idea | Roadmap section | Priority |
|------|----------------|----------|
| Synthesis layer (cross-document relationship pages auto-created after ingest) | Knowledge synthesis | High |
| Question → wiki feedback loop (Q&A results written back to wiki) | Knowledge synthesis | Medium |
| Orphan audit (link graph to detect isolated pages) | Lint item in Consume/tooling | Medium |
| Supplementary file types (xlsx/csv/tsv) | Capture expansion | Low |
| Batch report sum verification | Digest expansion | Low |

## Ideas already covered by cogvault

- Raw/wiki separation (3-layer): `sources[]` read-only / `wiki_dir` / `_schema.md`
- Source page per document: `sources/<slug>.md` with required frontmatter
- Automatic ingest pipeline: scan → filter → settle → hash → digest → validate → write
- Hash-based deduplication: sha256 content hash + ledger
- Schema delivered to LLM: `_schema.md` → MCP server instructions + buildPrompt
- Scheduled zero-touch ingest: launchd + `--scheduled` flag
- Obsidian compatibility: `adapter: "obsidian"` default
- Provenance markers: `[TODO: source needed]`, `[UNCERTAIN]`
- Single-instance guarantee: flock
- Failure classification: transient/permanent/infra/refused

## Ideas not applicable

- External system integration (Gmail, Calendar, Slack, Notion, HWP via MCP):
  cogvault serves wiki only; external integration is the agent host's job.
- PDF extractor comparison: cogvault uses headless PDF reading (0021 D6).
- Style gate checker: cogvault is software, not a book.
- Agent permission mode guide: documentation-level, not a feature.
