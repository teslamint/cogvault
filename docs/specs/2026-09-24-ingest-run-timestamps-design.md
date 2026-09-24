---
title: Ingest Run Timestamps
status: approved
date: 2026-09-24
schema: spec/v1
---

# Ingest Run Timestamps Design

_Created 2026-09-24._

## Overview

`cogvault ingest` writes one timestamped start line and one timestamped end line
to stderr for every invocation that reaches `runIngest`. The end line states the
run result and the summary counts. An operator can then read the launchd stderr
log alone and answer two questions: when did scheduled runs start failing, and
did a run after a given change succeed.

Motivation: on 2026-09-24 the scheduled job had failed on every run for an
unknown period with `storage.Read _schema.md: ... resource deadlock avoided`.
Neither log file carried a timestamp, and the ledger holds no run-level row, so
the start of the failure and the first good run after the fix could not be dated.

## User Scenarios

### S1: Date a failure streak from the scheduled log

An operator opens the launchd stderr log after a silent outage. Each run is
bracketed:

```
2026-09-24T01:09:26+09:00 ingest start origin=scheduled
2026-09-24T01:09:26+09:00 ingest end origin=scheduled result=error scanned=0 digested=0 failed=0 refused=0 skipped=0 deferred=0 unchanged=0 archived=0 source-errors=0
ingest.Run: storage.Read _schema.md: read ...: resource deadlock avoided
```

The first `result=error` end line after the last `result=ok` end line dates the
start of the streak.

### S2: Confirm that a run after an operational change succeeded

After an operator changes the environment (for example, pins the wiki folder in
iCloud), the operator reads the first end line whose timestamp is after the
change. `result=ok` confirms that run returned without error.

### S3: Interactive run

An operator runs `cogvault ingest --config <path>` in a terminal. The same two
lines appear on stderr with `origin=interactive`. Stdout is unchanged.

### S4: Failure before the index opens

The config file is missing or invalid, or the `claude` CLI is not on PATH. The
start line and a `result=error` end line with no counts still appear, followed
by the existing error line.

### S5: Crash inside a run

A panic escapes `runIngest`. The end line says `result=panic`, then the panic
continues and the process exits abnormally. A crash never logs as `result=ok`.

## Scope

### In

- Start and end lines on stderr for every `cogvault ingest` invocation that
  reaches `runIngest`: success, `--dry-run`, lock-held, and every error return.
- RFC 3339 timestamps in the process's local time zone with a numeric offset.
- End-line summary counts in the same `key=value` form as the stdout summary
  line when a report exists.
- An injectable clock for deterministic tests, following the existing
  `ingestLookPath` package-variable seam in `cmd/cogvault/ingest.go`.
- `SPEC.md` §9.4 output contract and the `DESIGN.md` `cmd/cogvault` row.

### Out (non-goals)

- Flag-parse errors that cobra rejects before `RunE` (for example `--limit x`).
  Those invocations print only the existing `main.go` error line.
- No change to stdout. The summary line and per-file list stay byte-identical.
- No change to other subcommands or to the shared error print in
  `cmd/cogvault/main.go`.
- No run-level ledger table and no `cogvault status` change.
- No change to the `slog` default handler or its timestamp format.
- No log rotation or retention policy.
- No elapsed-time field; the two timestamps already give duration.

## Assumptions and Preconditions

| Claim | Command | Observed at | Observed result | Evidence source |
|---|---|---|---|---|
| The shared error print writes the raw error with no timestamp. | `rg -n 'Fprintln\(os.Stderr, err\)' cmd/cogvault/main.go` | `2026-09-24T11:51:03+09:00` | 1 match at line 38 | Worktree at `c7a5dfc`; re-run by independent reviewer |
| The ledger has no run-level table; only per-file rows exist. | `rg -n 'CREATE TABLE' internal/ingest/*.go` (excluding tests) | `2026-09-24T11:51:03+09:00` | 1 table: `ingest_ledger` | Worktree at `c7a5dfc`; re-run by reviewer |
| The stdout summary line prints even when a run fails early. | `rg -c '^scanned=0 ' <launchd StandardOutPath log>` | `2026-09-24T11:51:03+09:00` | 501 lines, matching the 501 identical stderr error lines | Local launchd logs (count only; path omitted) |
| A package-variable test seam already exists for ingest. | `rg -n '^var ingest' cmd/cogvault/ingest.go` | `2026-09-24T11:51:03+09:00` | 2 seams: `ingestLookPath`, `ingestCheckOpenAIReady` | Worktree at `c7a5dfc`; re-run by reviewer |
| No existing ingest CLI test asserts on stderr. | `rg -n 'executeCommand\("ingest"' cmd/cogvault/*_test.go` plus the `executeCommand(args...)` callers | `2026-09-24T11:51:26+09:00` | 40 ingest invocations (38 literal, 2 via `args...`); 0 capture stderr by name | Worktree at `c7a5dfc`; count corrected by reviewer |
| Every existing ingest stdout assertion is a substring check, not exact equality. | Reviewer search for `stdout ==` on ingest tests | `2026-09-24T11:51:26+09:00` | 0 exact checks; all use `strings.Contains` | Reviewer report |
| `runIngest` has 9 return paths. | Read of `cmd/cogvault/ingest.go:61-135` | `2026-09-24T11:51:26+09:00` | resolveConfigPath, bootstrap, openai prerequisites, openai readiness, claude lookup, `ingest.New`, lock-held, other Run error, nil | Reviewer report |
| The shared error print covers every subcommand. | `rg -c 'cmd.AddCommand' cmd/cogvault/main.go` | `2026-09-24T11:51:26+09:00` | 14 subcommands | Worktree at `c7a5dfc`; re-run by reviewer |
| `executeCommand` routes `cmd.ErrOrStderr()` to a test buffer; production falls back to `os.Stderr`. | Read of `cmd/cogvault/cli_test.go:25-34` and `cmd/cogvault/main.go:32` | `2026-09-24T11:51:26+09:00` | `root.SetErr(errBuf)` in tests; `main.go` sets only `SetOut` | Worktree at `c7a5dfc`; confirmed by reviewer |

`[INFERENCE]` The `slog` lines emitted by the sweep, post-ingest embed, and git
commit steps go to `os.Stderr` through the default handler, not to the cobra
buffer. In production they appear between the start and end lines. This spec
does not rely on their format.

## Trade-offs

| Option | Metric it improves | Metric it degrades | Measured cost |
|---|---|---|---|
| Selected: A. Ingest-only start/end lines on stderr, always | Every run that reaches `runIngest`, success or failure, is dated in one file | Interactive stderr gains lines | 2 lines per run; 0 of 40 existing ingest test calls assert on stderr |
| Rejected: B. Timestamp on the shared error line in `main.go` | Change in 1 place | Successful runs stay undated, so S2 is not answerable; output contract changes for all commands | 14 subcommands affected; 0 successful runs dated |
| Rejected: C. Run-level ledger table shown by `cogvault status` | Survives log deletion; queryable | Needs schema change and migration; failures before the DB opens leave no row | 1 new table; S4 uncovered |
| Rejected: A limited to `--scheduled` | Interactive output unchanged | Manual recovery runs from a terminal are not dated | 0 lines for interactive runs; user chose "always" |
| Selected: one deferred emitter with `recover()` | Covers all 9 return paths and panics with one code site | A deferred `recover()` is slightly more code than per-path calls | 1 emitter instead of 9 call sites |
| Rejected: explicit end-line call at each return | No `defer` or `recover()` | A missed return path silently drops the end line | 9 call sites to keep in sync |

B loses on S2: a successful run writes no stderr line, so its time is still
unknown. C loses on S4 and costs a migration for a diagnostic need that a log line
meets. Per-path calls lose on coverage: a later return path added without a call
would drop the end line with no test failing unless every path is tested.

## Architecture

`runIngest` in `cmd/cogvault/ingest.go` computes `origin` from the `--scheduled`
flag first, writes the start line, and only then resolves the config path. The
`origin` computation currently sits at `ingest.go:105-109`; it moves to the top.

One deferred emitter writes the end line for every exit. It reads a named return
error and the report variable. When `recover()` returns a value, the emitter
writes `result=panic` and re-panics with the same value. Otherwise it writes
`result=ok` for a nil error and `result=error` for a non-nil error.

The end line precedes the error line that `main.go` prints after `Execute`
returns (`SilenceErrors: true`). The next run's start line bounds that error
line, so each error stays attributable to one run.

Both lines go to `cmd.ErrOrStderr()`. The clock is a package variable
(`ingestNow`) that tests replace.

## Interface

Start line:

```
<RFC3339 local> ingest start origin=<scheduled|interactive>
```

End line:

```
<RFC3339 local> ingest end origin=<scheduled|interactive> result=<ok|error|panic>[ <summary counts>]
```

- `result=ok` when `runIngest` returns nil; `result=error` when it returns a
  non-nil error; `result=panic` when a panic escapes.
- `<summary counts>` is the stdout summary's first-line `key=value` sequence,
  including `not-examined=` when non-zero. It appears whenever a report exists,
  including when `Run` returns both a report and an error. It is omitted when no
  report exists.
- The start line timestamp is taken before any work; the end line timestamp is
  taken when the emitter runs.
- `--dry-run` uses the same lines. Stdout is unchanged.
- Exit codes are unchanged.

## Testing

New tests in `cmd/cogvault` use `executeCommand` and replace `ingestNow` with a
fixed clock that returns distinct start and end times:

- Successful dry-run with at least one aged pending source file (as in
  `TestIngestDryRunListsPending`), so the counts are nonzero: stderr equals
  exactly the two expected lines; `result=ok`; the end-line counts equal the
  stdout summary line; stdout contains neither ` ingest start ` nor
  ` ingest end `.
- Missing config (bootstrap exit): start line, end line with `result=error` and
  no counts, non-nil error.
- Missing `claude` binary (`TestIngestMissingClaudeBinary` path): end line with
  `result=error`. This covers a non-bootstrap early return.
- Lock held (existing `already running` fixture): end line with `result=error`.
- Panic: the emitter itself is exercised with a function that panics; the test
  asserts `result=panic` and that the panic propagates.

## Risks

- A scheduled run killed by launchd (SIGKILL) writes no end line. The reverse
  does not hold: a start line with no end line can also mean the run is still
  in progress, or another abnormal exit, or lost stderr output. First check
  `launchctl print gui/<uid>/com.teslamint.cogvault.ingest` for `state = running`.
  If the job has exited and no end line exists, investigate an abnormal exit or
  missing log output; the log alone cannot identify the cause. Residual; not
  mitigated.
- Unbounded log growth: 2 extra lines per hour is about 17,520 lines per year.
  Residual; rotation is out of scope.
- A consumer that parses stderr would see new lines. None was found in the repo
  (0 stderr assertions in the 40 ingest test calls).
- `result=ok` means `runIngest` returned nil. Per-file failures inside a
  successful run (for example `failed=2`) still produce `result=ok`; the counts
  on the same line carry that detail.

## Success Criteria

1. A successful run writes exactly one start line and one `result=ok` end line whose counts equal the stdout summary.
   - **Measured by**: `go test -race ./cmd/cogvault -run 'TestIngestRunTimestamps'`.
   - **Control**: mutations that each fail the test — drop the end line; write
     the end line before the report exists; hard-code the counts (the fixture has
     nonzero `digested`).
2. Every error return writes a `result=error` end line, including a return before bootstrap.
   - **Measured by**: the missing-config, missing-`claude`, and lock-held cases
     in the test above.
   - **Control**: an implementation that writes the start line after
     `bootstrap`, or that calls the end-line writer only at the success and
     bootstrap exits, fails at least one case.
3. A panic writes `result=panic` and still propagates.
   - **Measured by**: the panic case in the test above.
   - **Control**: a deferred emitter without `recover()` logs `result=ok` and
     fails this case.
4. Stdout output of `cogvault ingest` is unchanged.
   - **Measured by**: the successful dry-run case asserts stdout contains
     neither ` ingest start ` nor ` ingest end `; `go test -race ./cmd/cogvault`
     passes; `git diff c7a5dfc -- cmd/cogvault/cli_test.go` shows no edits to
     existing assertions.
   - **Control**: writing either line to stdout fails the new stdout assertion.
5. The installed scheduled job writes both lines in production.
   - **Measured by**: after one scheduled run past install time,
     `rg -c '^\d{4}-\d{2}-\d{2}T\S+ ingest start origin=scheduled$' <launchd StandardErrorPath log>`
     returns at least 1, and
     `rg -c '^\d{4}-\d{2}-\d{2}T\S+ ingest end origin=scheduled result=(ok|error|panic)' <launchd StandardErrorPath log>`
     returns at least 1, each with a timestamp after the install time.
   - **Control**: both commands return 0 on the current log (501 lines, none
     timestamped).
6. `SPEC.md` §9.4 and the `DESIGN.md` `cmd/cogvault` row describe the lines.
   - **Measured by**: reviewer rubric: §9.4 states both line formats, the three
     `result` values, the stderr stream, and the flag-parse exclusion; DESIGN
     names the clock seam and the deferred emitter.

## Open Decisions

None blocking. Deferred to `planning`: the helper name for the summary-count
string and whether it lives on `ingest.Report`. Deferred to `implementing`: the
exact test function names.

## Stop Condition

This spec closes on approval. New measurements of log volume or format
preference reopen it only if they contradict a Success Criterion.
