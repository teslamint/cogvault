# v2 Phase 1 Follow-ups (durable tracker)

Registered by the 2026-07-23 retro (PR #3). Status updates belong here, not in PR comments.

| # | Item | Type | Priority | Status |
|---|---|---|---|---|
| F1 | Run spec SC1-SC4 validation: real 66-file backlog ingest (`cogvault ingest --dry-run`, then `--limit` batches; ≥90% success via ledger query), launchd zero-touch inflow (one test PDF, `run_origin=scheduled` row), 1-week re-find rubric (≥4/5 hits log) | feature/process | P1 | Not started |
| F2 | Deferred review minors batch: U2-m1..m5, U3-m1/m2, U4-m1..m3, U5-m1..m5, U6-m1/m3..m7, U7-m1/m2/m5, U9-m1/m3/m4 (full text in the release-loop ledger log; representative: dry-run needs claude on PATH, mtime TZ-offset gate weakening, supersede hash-binding assertion) | process | P3 | Not started |
| F3 | FTS write-write `SQLITE_BUSY_SNAPSHOT` limitation — revisit if scheduled ingest vs serve write collisions appear in real use (`source-errors`/infra failures in reports); see docs/deviations/2026-07-22-busy-timeout-fts-write-write.md | architecture | P3 | Not started |
| F4 | Spec self-contradiction: Testing section's "dedup including renamed files" is unimplementable under the (path,hash) ledger key the same spec mandates — a renamed source re-digests. Decide: accept (document in SPEC) or add content-hash-first lookup | edge-case | P3 | Not started |
| F5 | Cleanup: dead `contentHash()` in internal/ingest, v1-shaped `.cogvault.yaml` fixtures under testdata/fixtures/, MCP missing-schema fallback tells agents to `wiki_read("_schema.md")` which 404s | process | P4 | Not started |
