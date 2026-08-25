---
schema: spec/v1
title: "Ingest attention report: status command and push notification"
type: feature
status: approved
date: 2026-08-24
---

# Ingest attention report: status command and push notification

## Problem

When `cogvault ingest --scheduled` runs via launchd, files that permanently fail
(exhausted after 3 attempts) or are refused by the LLM are recorded only in
stdout logs. The user has no way to learn about these failures without manually
reading `/Users/*/Library/Logs/cogvault/ingest.out.log`.

Consequence: the user drops a PDF into a source folder, assumes it will be
digested, and never discovers it failed. The file sits indefinitely in an
unrecoverable state with no signal.

## Stop condition

Two complementary channels surface attention-worthy ingest items:

1. **Pull**: `cogvault status` prints every file currently in an exhausted or
   refused state for the active LLM model, excluding files that have a newer
   successful digest.
2. **Push**: when a scheduled ingest run causes a file to *newly* reach
   exhausted or refused state, a macOS notification fires. Already-known
   failures do not re-notify. Push fires only on `--scheduled` runs; interactive
   runs already print the report to stdout.

## User scenarios

**S1 — Scheduled run, new failure, push notification.**
launchd runs `cogvault ingest --scheduled`. A PDF exhausts its 3 permanent-failure
attempts for the first time. macOS notification center shows:
Title: `cogvault ingest`
Body: `1건 주의 필요 — filename.pdf (page missing frontmatter or title)`

**S2 — Known failure, no repeat notification.**
Next hourly run: the same file is still exhausted. The ingest report marks it
`exhausted` (skipped). No notification fires because the file was already
exhausted before this run started.

**S3 — Manual status check, pull.**
User runs `cogvault status`. Output:

```
주의 필요: 2건
  exhausted  hankookilbo-com-...pdf   validate: page missing frontmatter or title  (2026-08-24 16:53)
  refused    rotcee-github.io-...pdf  claude policy refusal                        (2026-08-24 15:53)
```

**S4 — Problem resolved.**
User replaces the broken PDF with a corrected version. Next ingest hashes the
new content and digests successfully. The attention query uses latest-row-per-
source-path semantics: when the latest row for a source path is `success`, all
older exhausted/refused rows for that path are excluded. `cogvault status` no
longer lists the file.

**S5 — Clean state.**
`cogvault status` with no attention items prints:
`주의 필요 항목 없음.`

**S6 — Machine-readable output.**
`cogvault status --json` outputs a JSON object with an `attention` array,
each element containing `path`, `status`, `error`, `last_attempt`,
`llm_model`, and `attempts`.

**S7 — Source file deleted while exhausted.**
User removes a broken PDF from the source directory instead of fixing it. The
exhausted ledger row remains because `sweepOrphans` handles only `success` rows.
`cogvault status` continues to list the file. This is acceptable: the user can
see it in `status` and knows it was never digested. A future enhancement could
add a `cogvault status --dismiss <path>` to clear stale rows.

**S8 — Interactive run, no push notification.**
User runs `cogvault ingest` (without `--scheduled`). A file exhausts. The report
is printed to stdout as today. No macOS notification fires — interactive users
already see the output.

## Architecture

### Pull path: `cogvault status`

A new CLI subcommand that queries the ledger for attention-worthy rows.

```
cogvault status [--config path] [--json]
```

The `status` command opens the ledger database. Because `openLedger` executes
DDL (`CREATE TABLE IF NOT EXISTS` and the `llm_model` migration), it is not a
pure read-only open. If the database file does not exist, `status` prints
`주의 필요 항목 없음.` and exits 0 without creating the database. When the
database exists, `openLedger` runs as normal (the DDL is idempotent).

The status command does not run ingest and does not acquire the single-writer
lock. It loads config only — it does not call `bootstrap()` (which also opens
the index SQLite DB and may create files). A dedicated config-only path
ensures `status` creates no side-effect files beyond the ledger DDL.

The attention query uses **latest-row-per-source-path** semantics:

1. For each `source_path`, find the row with the most recent `digested_at`.
2. If that latest row has `status = 'success'` or `status = 'superseded'`,
   the path is not attention-worthy regardless of older rows.
3. Otherwise, the latest row is attention-worthy if:
   - `status = 'failed' AND attempts >= maxAttempts AND llm_model = <current>`
     (exhausted)
   - `status = 'refused' AND llm_model = <current>` (refused)

This ensures that S4 (resolved files) and duplicate rows from multiple failed
hashes do not produce false positives.

### Push path: macOS notification on new terminal failures

Push fires only when `origin == "scheduled"`. Interactive runs print the report
directly and do not notify.

After `Runner.Run` completes, the report's `NewAttention` slice contains files
that newly reached a terminal state during this run. The caller (`runIngest`)
passes this slice to a platform-specific notifier.

**"Newly terminal" definition**: `recordFailure` populates `NewAttention` when:

- **Exhausted**: `class == classPermanent` and the post-increment `attempts >=
  maxAttempts`. Uses `>=` (not `==`) to cover the carryover case where a model
  change causes attempts to exceed `maxAttempts` on a single permanent failure.
  Additionally, `recordFailure` checks whether `prev` was already terminal for
  the current model (i.e., `prev != nil && prev.llmModel == cfg.LLM.Model &&
  prev.attempts >= maxAttempts`) — if so, the file was already exhausted and
  does not re-notify.
- **Refused**: `class == classRefused` and no prior row existed for this
  (path, hash) with `status = 'refused'` and `llm_model == current`. The
  `prev` parameter already carries this information: if `prev` is non-nil with
  `status == "refused"` and matching model, it is not new.

**Notification gap (accepted)**: a file that becomes silently terminal through
a transient/infra failure after a model change (attempts carried over at
`maxAttempts`, transient failure rewrites the row with the new model without
incrementing, next run skips as exhausted) will not trigger a push notification.
The pull path (`cogvault status`) catches these. This gap is accepted because
the alternative (comparing prior terminal state inside `recordFailure` for
non-permanent classes) adds complexity disproportionate to the single-user
use case. The gap is documented here so users know to run `cogvault status`
periodically.

**Perpetual transient/infra failures** (attempts never reach `maxAttempts`)
are invisible to both channels. This is by design: transient failures are
expected to self-resolve, and infra failures indicate a system problem that
manifests as ingest errors in logs.

The notification mechanism is platform-specific:
- **darwin**: `osascript -e 'display notification ...'` via `os/exec`.
- **other**: no-op; the report is already printed to stdout.

The notifier is an injectable function field on `Runner` so tests can capture
calls without executing `osascript`. Notification ownership lives in `Runner`
(not the caller): `Runner.Run` populates `NewAttention` on the report, and
a post-run method `Runner.Notify(report)` sends the notification. The caller
decides whether to call `Notify` based on the `--scheduled` flag.

### Boundary: what is NOT in scope

- Notification click actions or deep links.
- Notification history or deduplication state beyond the ledger.
- Email, Slack, or any remote notification channel.
- Modifying the launchd plist.
- A `cogvault status --watch` mode.
- Dismissing stale attention rows for deleted source files (future enhancement).
- Push notification for the transient/infra → silent exhaustion edge case.

## Interface

### `cogvault status`

```
Usage:
  cogvault status [flags]

Flags:
      --config string   path to config file
      --json            output as JSON
```

Human-readable output format:

```
주의 필요: <N>건
  <status>  <filename>  <error>  (<timestamp>)
  ...
```

Or when clean:

```
주의 필요 항목 없음.
```

JSON output format:

```json
{
  "attention": [
    {
      "path": "/Users/.../file.pdf",
      "status": "exhausted",
      "error": "validate: page missing frontmatter or title",
      "last_attempt": "2026-08-24T07:53:00Z",
      "llm_model": "claude-sonnet-4-20250514",
      "attempts": 3
    }
  ],
  "model": "claude-sonnet-4-20250514"
}
```

### Push notification format

Title: `cogvault ingest`
Body: `<N>건 주의 필요 — <first-filename> (<error-prefix>)`

`<error-prefix>` is the substring of `last_error` after the first colon
(trimmed), or the full string if no colon is present, truncated to 60
characters. The stage prefix before the colon (e.g. `validate:`, `digest:`)
is uninformative on its own — the detail after the colon carries the
actionable message.

When multiple files are newly terminal, the body lists the first file and
appends `외 <N-1>건`.

Push fires only on `--scheduled` runs.

## Data model

No schema changes. The existing `ingest_ledger` table contains all necessary
fields.

### Attention query (pull)

The query uses latest-row-per-source-path semantics to avoid false positives
from stale rows:

```sql
WITH latest AS (
  SELECT source_path,
         MAX(digested_at) AS max_at
  FROM ingest_ledger
  GROUP BY source_path
)
SELECT l.source_path, l.content_hash, l.digested_at, l.last_error,
       l.attempts, l.llm_model, l.status
FROM ingest_ledger l
JOIN latest lt ON l.source_path = lt.source_path
             AND l.digested_at = lt.max_at
WHERE l.llm_model = ?
  AND (
    (l.status = 'failed' AND l.attempts >= 3)
    OR l.status = 'refused'
  );
```

The hardcoded `3` mirrors `maxAttempts` in `ingest.go`. If `maxAttempts`
becomes configurable, the query must accept it as a parameter.

**Timestamp ordering caveat**: `digested_at` is RFC3339Nano text, which strips
trailing fractional-second zeros (`…05.1Z` sorts above `…05.11Z`
lexicographically). Real runs write same-path rows hours apart, so this is
production-safe. For seeded test rows, use second-precision RFC3339
(fixed-width, lexicographically sortable). To handle potential ties, add a
deterministic tiebreak (`MAX(rowid)` secondary) or `GROUP BY` in the outer
select to emit at most one row per `source_path`.

### "Newly terminal" detection (push)

`Report` gains a `NewAttention []FileResult` field. `recordFailure` appends
to it when the result is terminal (exhausted or refused) and was not already
terminal for the current model before this run. The detection logic:

```
on recordFailure(entry, prev, class):
  if class == classPermanent:
    newAttempts = attemptsOf(prev) + 1
    if newAttempts >= maxAttempts:
      wasAlreadyExhausted = prev != nil
        && prev.llmModel == currentModel
        && prev.attempts >= maxAttempts
      if not wasAlreadyExhausted:
        append to NewAttention
  if class == classRefused:
    wasAlreadyRefused = prev != nil
      && prev.status == "refused"
      && prev.llmModel == currentModel
    if not wasAlreadyRefused:
      append to NewAttention
```

When `Runner.Run` returns an error mid-run (context cancellation), `runIngest`
does NOT call `Notify` — partial reports may contain incomplete `NewAttention`
data. Notification fires only on successful (nil-error) completion.

If `recordFailure`'s ledger upsert fails (logged only, does not propagate),
the "no repeat" property depends on that write succeeding for the next run's
skip branch to work. This is an existing ledger-write-failure property, not
introduced by this feature.

## Integration

### New files

| File | Purpose |
|---|---|
| `cmd/cogvault/status.go` | `cogvault status` subcommand |
| `cmd/cogvault/status_test.go` | Status command tests |
| `internal/ingest/notify_darwin.go` | macOS notification via osascript (build tag) |
| `internal/ingest/notify_other.go` | No-op for non-darwin platforms |

### Modified files

| File | Change |
|---|---|
| `cmd/cogvault/main.go` | Register `newStatusCmd()` |
| `cmd/cogvault/ingest.go` | Call `runner.Notify(report)` when `--scheduled` and no error |
| `internal/ingest/ledger.go` | Add `attentionRows(model string)` method with latest-row CTE |
| `internal/ingest/report.go` | Add `NewAttention []FileResult` field to `Report` |
| `internal/ingest/ingest.go` | Populate `NewAttention` in `recordFailure`; add `Notify` method |
| `SPEC.md` | Document `cogvault status` in §9 CLI commands |

## Testing strategy

- **`ledger.attentionRows`**: unit test with seeded ledger rows covering
  exhausted, refused, failed-but-not-exhausted, success, superseded, and
  different-model rows. Include a row where an older hash is exhausted but a
  newer hash is successful for the same `source_path` — verify the path is
  excluded (S4 coverage).
- **`Report.NewAttention` population**: unit test `recordFailure` paths:
  (a) permanent failure reaching `maxAttempts` → in `NewAttention`;
  (b) permanent failure with prev already exhausted for current model → not in
  `NewAttention`; (c) refused with no prior refused row → in `NewAttention`;
  (d) refused with prior refused row for current model → not in `NewAttention`;
  (e) permanent failure with attempts exceeding `maxAttempts` after model
  change → in `NewAttention` (carryover case).
  Note: the `wasAlreadyExhausted`/`wasAlreadyRefused` prior-state checks in
  `recordFailure` are unreachable through `Runner.Run` (the skip branches at
  ingest.go:146-160 prevent terminal-for-current-model rows from reaching
  `digestOne`). Tests (b) and (d) must call `recordFailure` directly.
- **`cogvault status` output**: integration test bootstrapping a ledger with
  known rows and verifying human-readable and JSON output. Include test for
  missing database file → clean output without DB creation.
- **Notification dispatch**: injectable notifier on `Runner`; test captures the
  call arguments and verifies the message format and the "no repeat" property.
  Verify `Notify` is not called on interactive runs or when `Run` returns error.
- **Platform build**: CI builds for darwin and linux confirm build-tag
  compilation.

## Risks and mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| `osascript` unavailable (headless macOS, SSH) | Notification silently fails | Catch exec error, log warning, do not fail ingest |
| Ledger accumulates stale exhausted rows across model changes | `status` shows outdated entries | Filter by current `llm_model`; old-model rows invisible |
| `actionRefused` ambiguity between skip and new | Wrong notification decision | Track newly-terminal items in `NewAttention` via `recordFailure` prior-state check |
| `openLedger` DDL on `status` open | Unexpected writes | Guard: skip `openLedger` when DB file does not exist |
| Stale rows for deleted source files | Perpetual false positives in `status` | Accepted for now; documented in S7; future `--dismiss` enhancement |
| Transient/infra silent exhaustion after model change | Push misses one edge case | Accepted; pull (`status`) catches it; documented in Architecture |

## Assumptions and preconditions

| Claim | Command | Observed | Result | Source |
|---|---|---|---|---|
| `actionExhausted` is set only in the skip branch, not in `recordFailure` | `rg actionExhausted internal/ingest/ingest.go` | 2026-08-24 | Lines 157: skip branch only; `recordFailure` uses `actionFailed` | `internal/ingest/ingest.go:157` |
| `actionRefused` is set in both skip branch and `recordFailure` | `rg actionRefused internal/ingest/ingest.go` | 2026-08-24 | Lines 149 (skip) and 354 (recordFailure) | `internal/ingest/ingest.go:149,354` |
| No `cogvault status` command exists today | `rg 'StatusCmd\|statusCmd\|newStatusCmd' --glob '*.go'` | 2026-08-24 | No matches | repo-wide search |
| `openLedger` executes DDL (not read-only) | Code inspection of `openLedger` | 2026-08-24 | `CREATE TABLE IF NOT EXISTS` + ALTER migration on every open | `internal/ingest/ledger.go:50-64` |
| `supersedePrevSuccess` updates only `status='success'` rows | `rg supersedePrevSuccess internal/ingest/ledger.go` | 2026-08-24 | `UPDATE ... WHERE status = 'success'`; exhausted/refused rows are not touched | `internal/ingest/ledger.go:133-142` |
| `sweepOrphans` queries only `successRows` | Code inspection | 2026-08-24 | `r.ledger.successRows()` at line 366; exhausted/refused rows for deleted files remain | `internal/ingest/ingest.go:366` |

## Success criteria

| ID | Criterion | Proving method |
|---|---|---|
| SC1 | `cogvault status` lists exhausted and refused files for the current model | Run `cogvault status` against a ledger with known exhausted/refused rows; compare output to expected |
| SC2 | `cogvault status --json` produces valid, parseable JSON with correct `attention` array | `cogvault status --json \| jq '.attention \| length'` matches expected count |
| SC3 | Newly exhausted file triggers notification (test capture) | Integration test: seed ledger at attempts=2, run ingest with a permanent failure, verify notifier called with correct path |
| SC4 | Already-exhausted file does NOT trigger notification | Integration test: seed ledger at attempts=3 for current model, run ingest, verify notifier not called |
| SC5 | Resolved file (newer success row) excluded from `status` | Seed ledger with exhausted row, then success row for same source_path; verify `cogvault status` shows 0 attention items |
| SC6 | Push fires only on `--scheduled`, not interactive | Integration test: run ingest without `--scheduled`, verify notifier not called even with new exhausted file |
| SC7 | `SPEC.md` documents the new `status` subcommand | `rg 'cogvault status' SPEC.md` matches |

## Open decisions

- **Notification click action**: initial implementation uses a plain
  notification with no click handler. If users want `cogvault status` to open
  in Terminal on click, that can be added later with `osascript` URL scheme
  support.
- **Stale row dismissal**: files deleted from source directories leave
  exhausted/refused rows visible in `status`. A `--dismiss` flag or automatic
  source-existence check could address this in a future iteration.
