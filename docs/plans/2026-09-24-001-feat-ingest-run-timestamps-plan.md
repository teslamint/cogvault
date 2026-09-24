---
schema: plan/v1
title: Ingest run timestamp lines
type: feat
status: done
completed_by: 5e05184b2821ce8addfceac3816d3d8a6c23dc15
date: 2026-09-24
execution: code
origin: docs/specs/2026-09-24-ingest-run-timestamps-design.md
body_seal: 7f4a0e6386c333b2167625ca4a1a38967b5c6f0cde0c92987455c066c2465c87
---

## Goal

Make every `cogvault ingest` invocation that reaches `runIngest` write one
timestamped start line and one timestamped end line to stderr, so an operator can
date failure streaks and confirm post-change runs from the launchd stderr log
alone.

## Architecture notes

Decision 1 — one bracket function owns both lines. `cmd/cogvault/ingest.go` gains
`bracketIngestRun(w io.Writer, origin string, body func() (*ingest.Report, error)) (err error)`.
It writes the start line, calls `body`, and writes the end line from a `defer`.
The `defer` calls `recover()`. On a panic it writes `result=panic` and re-panics
with the same value. Otherwise it writes `result=ok` for a nil error and
`result=error` for a non-nil error. Rationale: `runIngest` has 9 return paths
(spec assumption row 7). One deferred emitter covers all of them and any path
added later. Per-path calls were rejected in the spec's trade-off table.

Decision 2 — `runIngest` becomes a thin wrapper. It computes `origin` from the
`--scheduled` flag first. The flag is parsed before `RunE`, so this is safe. Then
it returns `bracketIngestRun(cmd.ErrOrStderr(), origin, func() (*ingest.Report, error) { ... })`.
The closure holds the current body unchanged except that each `return err`
becomes `return nil, err` (or `return report, err` after `runner.Run`). The
existing `origin` computation at `ingest.go:105-109` moves above the closure.

Decision 3 — clock seam. Add `var ingestNow = time.Now` beside the existing
`ingestLookPath` and `ingestCheckOpenAIReady` seams at `ingest.go:24-25`. Both
lines format with `ingestNow().Format(time.RFC3339)`. `time.RFC3339` prints the
value's own zone offset; `time.Now()` returns local time, so production prints
the local offset (for example `+09:00`).

Decision 4 — summary counts come from `ingest.Report`. Add
`func (r *Report) Summary() string` in `internal/ingest/report.go`. It returns
the current first line of `String()` without the trailing newline: the nine
`key=value` counts, plus ` not-examined=N` when `NotExamined > 0`. `String()`
calls `Summary()` so the two cannot drift. The end line appends
`" " + report.Summary()` only when the report is non-nil.

Line formats (from the spec's Interface section):

```
<RFC3339> ingest start origin=<scheduled|interactive>
<RFC3339> ingest end origin=<scheduled|interactive> result=<ok|error|panic>[ <Summary()>]
```

Order in production: the end line is written before `runIngest` returns;
`main.go:37-38` prints the error after `Execute` returns (`SilenceErrors: true`).
The `slog` lines from the sweep, embed, and git steps go to `os.Stderr` and land
between the two bracket lines in production. Tests do not see them because
`executeCommand` captures only the cobra writers.

Known pattern: package-variable seams with `t.Cleanup` restore
(`cmd/cogvault/cli_test.go:1155-1157`). Test vault helpers: `newIngestVault`,
`fakeClaudeOnPath`, and the aged-source fixture in `TestIngestDryRunListsPending`
(`cli_test.go:409-429`). Lock fixture: `TestIngestLockHeldFails`
(`cli_test.go:435-462`).

Post-merge production check (spec criterion 5) is not an implementation unit.
It needs the merged binary installed with `make install-signed`, which restarts
the production ingest job. The release-loop Retro phase owns that step and asks
the user before running it.

## Assumption Recheck

Rerun time: `2026-09-24T03:06:47Z`.

| Approved claim | Fresh evidence | Outcome |
|---|---|---|
| The shared error print writes the raw error with no timestamp. | `rg -n 'Fprintln\(os.Stderr, err\)' cmd/cogvault/main.go` → line 38. | match |
| The ledger has no run-level table. | `rg -n 'CREATE TABLE' internal/ingest/*.go` excluding tests → 1 table. | match |
| The stdout summary prints even when a run fails early. | `rg -c '^scanned=0 '` on the launchd stdout log → 501. | match |
| A package-variable test seam exists for ingest. | `rg -n '^var ingest' cmd/cogvault/ingest.go` → 2 lines. | match |
| No ingest CLI test asserts on stderr (40 invocations). | 38 literal plus 2 `executeCommand(args...)` callers; 0 named `stderr` captures on ingest calls. | match |
| Ingest stdout assertions are substring checks. | `rg -c 'stdout =='` over `cmd/cogvault/*_test.go` → 1 hit, in `TestInitCreatesFiles` (an emptiness check for `init`). 0 for ingest. | match |
| `runIngest` has 9 return paths. | `sed -n '61,135p' cmd/cogvault/ingest.go \| rg -c '^\s*return'` → 9. | match |
| The shared error print covers 14 subcommands. | `rg -c 'cmd.AddCommand' cmd/cogvault/main.go` → 14. | match |
| Tests route `ErrOrStderr` to a buffer; production falls back to `os.Stderr`. | `main.go:32` sets only `SetOut`; `cli_test.go:30` sets `SetErr`. | match |

## File structure

| File | Responsibility | Unit |
|---|---|---|
| `internal/ingest/report.go` | `Report.Summary()`; `String()` reuses it | U1 |
| `internal/ingest/report_test.go` (new) | Summary/String invariance tests | U1 |
| `cmd/cogvault/ingest.go` | `ingestNow` seam, `bracketIngestRun`, `runIngest` wrapper | U2 |
| `cmd/cogvault/ingest_run_timestamps_test.go` (new) | bracket line contract across exit paths and panic | U2 |
| `SPEC.md` §9.4 | stderr bracket-line contract | U3 |
| `DESIGN.md` `cmd/cogvault/*` row | clock seam and deferred emitter | U3 |

## Scenario coverage map

| Scenario | Unit chain | Evidence |
|---|---|---|
| S1 — date a failure streak | U1 → U2 → U3 | `TestIngestRunTimestampsLockHeld` and `TestIngestRunTimestampsMissingClaude` assert `result=error` end lines. Covers S1. |
| S2 — confirm a post-change run succeeded | U1 → U2 → U3 | `TestIngestRunTimestampsDryRunSuccess` asserts `result=ok` with counts equal to the stdout summary. Covers S2. Production evidence is spec criterion 5, measured in Retro. |
| S3 — interactive run | U2 | `TestIngestRunTimestampsDryRunSuccess` runs without `--scheduled` and asserts `origin=interactive` and unchanged stdout. `TestIngestRunTimestampsScheduledOrigin` asserts `origin=scheduled`. Covers S3. |
| S4 — failure before the index opens | U2 | `TestIngestRunTimestampsMissingConfig` asserts both lines, `result=error`, no counts. `TestIngestRunTimestampsMissingClaude` covers a non-bootstrap early return. Covers S4. |
| S5 — crash inside a run | U2 | `TestBracketIngestRunPanic` asserts `result=panic` and that the panic value propagates. Covers S5. |

## Implementation Units

## U1: Report summary line

Execution note: test-first
Files:
  Create: internal/ingest/report_test.go
  Modify: internal/ingest/report.go
  Test: internal/ingest/report_test.go
Interfaces:
  Consumes: `type Report struct` in `internal/ingest/report.go` (fields `Scanned`, `Digested`, `Failed`, `Refused`, `Skipped`, `Deferred`, `Unchanged`, `Archived`, `SourceErrors`, `NotExamined`, `SumMismatch`, `PerFile`)
  Produces: `func (r *Report) Summary() string`
Test scenarios:
  happy: A report with `Scanned=5 Digested=2 Failed=1 Refused=0 Skipped=1 Deferred=0 Unchanged=1 Archived=3 SourceErrors=0` gives `Summary() == "scanned=5 digested=2 failed=1 refused=0 skipped=1 deferred=0 unchanged=1 archived=3 source-errors=0"`.
  edge: The same report with `NotExamined=4` gives the same string plus `" not-examined=4"`; with `NotExamined=0` the suffix is absent.
  error: n/a — `Summary()` has no error path.
  integration: `String()` starts with `Summary() + "\n"` for a report that also has `SumMismatch` and two `PerFile` rows, and the remainder is byte-identical to the pre-change output for that report (hard-code the expected full string in the test).
Steps:
  1. Create `internal/ingest/report_test.go` in `package ingest` with `TestReportSummary` (happy and edge cases above) and `TestReportStringStartsWithSummary` (integration case, full expected string literal).
  2. Run `go test ./internal/ingest -run 'TestReportSummary|TestReportStringStartsWithSummary'`; confirm it fails to compile because `Summary` is undefined.
  3. In `internal/ingest/report.go`, add `Summary()` that builds the counts line with the existing `fmt.Fprintf` format string and the `not-examined` suffix, with no trailing newline. Change `String()` to write `r.Summary()` then `'\n'`, then keep the existing `SumMismatch` and per-file code unchanged.
  4. Run `go test -race ./internal/ingest`; confirm all pass.
  5. Commit: `feat(ingest): Expose report summary line`.
Acceptance: `go test -race ./internal/ingest` passes. Mutation check: drop the `not-examined` suffix from `Summary()` → the edge case fails; restore.

## U2: Timestamped run bracket for ingest

Execution note: test-first
Files:
  Create: cmd/cogvault/ingest_run_timestamps_test.go
  Modify: cmd/cogvault/ingest.go
  Test: cmd/cogvault/ingest_run_timestamps_test.go
Interfaces:
  Consumes: `func (r *Report) Summary() string` from U1; `executeCommand(args ...string) (stdout, stderr string, err error)` in `cmd/cogvault/cli_test.go:25`; `newIngestVault(t)`, `fakeClaudeOnPath(t)` test helpers in `cmd/cogvault`; the `runIngestNotify` seam at `cmd/cogvault/ingest.go:47`.
  Produces: `var ingestNow = time.Now`; `func bracketIngestRun(w io.Writer, origin string, body func() (*ingest.Report, error)) (err error)`.
Test scenarios:
  happy: Dry-run with one aged pending source (copy the fixture from `TestIngestDryRunListsPending`) and a fixed clock returning `2026-09-24T01:00:00+09:00` then `2026-09-24T01:00:05+09:00`. Stderr equals exactly `"2026-09-24T01:00:00+09:00 ingest start origin=interactive\n2026-09-24T01:00:05+09:00 ingest end origin=interactive result=ok " + <first stdout line> + "\n"`. The first stdout line contains `digested=1`. Stdout contains neither `" ingest start "` nor `" ingest end "`. Covers S2, S3.
  edge: The same dry-run with `--scheduled` gives `origin=scheduled` in both lines. The test replaces `runIngestNotify` with a no-op and restores it with `t.Cleanup`, because a scheduled run with a nil error otherwise sends a real notification. Covers S3.
  error: (a) Missing config: `--config <tempdir>/absent.yaml` → non-nil error; stderr is exactly two lines: the start line and `2026-09-24T01:00:05+09:00 ingest end origin=interactive result=error` with nothing after `result=error`. Covers S4. (b) `claude` absent (`t.Setenv("PATH", "")`, as in `TestIngestMissingClaudeBinary`) → end line has `result=error`. Covers S4. (c) Lock held (copy the lock fixture from `TestIngestLockHeldFails`) → end line has `result=error`. Covers S1.
  integration: `TestBracketIngestRunPanic` calls `bracketIngestRun` with a `bytes.Buffer` and a body that panics with `"boom"`; a deferred `recover()` in the test captures `"boom"`, and the buffer's last line ends with `result=panic`. Covers S5.
Steps:
  1. Create `cmd/cogvault/ingest_run_timestamps_test.go` with a helper `fixedIngestClock(t, times ...time.Time)` that replaces `ingestNow` with a function returning the given times in order and restores it with `t.Cleanup`. Use `time.FixedZone("KST", 9*60*60)` for the zone. Add tests `TestIngestRunTimestampsDryRunSuccess`, `TestIngestRunTimestampsScheduledOrigin`, `TestIngestRunTimestampsMissingConfig`, `TestIngestRunTimestampsMissingClaude`, `TestIngestRunTimestampsLockHeld`, and `TestBracketIngestRunPanic` for the scenarios above.
  2. Run `go test ./cmd/cogvault -run 'TestIngestRunTimestamps|TestBracketIngestRun'`; confirm it fails to compile because `ingestNow` and `bracketIngestRun` are undefined.
  3. In `cmd/cogvault/ingest.go`: add `var ingestNow = time.Now` after line 25. Add `bracketIngestRun` per Architecture Decision 1: write `fmt.Fprintf(w, "%s ingest start origin=%s\n", ingestNow().Format(time.RFC3339), origin)`; declare `var report *ingest.Report`; `defer` a function that computes `result` (`panic` if `recover()` returned non-nil, else `ok`/`error` from the named `err`), writes `"%s ingest end origin=%s result=%s"` plus `" " + report.Summary()` when `report != nil` plus `"\n"`, and re-panics with the recovered value when it was non-nil; then `report, err = body()` and `return err`.
  4. Rewrite `runIngest`: read `scheduled` and set `origin` at the top (move lines 105-109 up); `return bracketIngestRun(cmd.ErrOrStderr(), origin, func() (*ingest.Report, error) { ... })`. Inside the closure keep the existing body. Change each early `return err` / `return fmt.Errorf(...)` to `return nil, <same error>`. Change `report, err := runner.Run(...)` so the closure returns that `report` on every later path: `return report, fmt.Errorf("ingest already running (lock held)")`, `return report, err`, and the final `return report, nil`.
  5. Run `go test -race ./cmd/cogvault`; confirm the new tests and all existing tests pass with no edits to existing test files.
  6. Mutation checks, each run then reverted: (a) pass `cmd.OutOrStdout()` instead of `cmd.ErrOrStderr()` into `bracketIngestRun` → `TestIngestRunTimestampsDryRunSuccess` fails on the stdout assertion; (b) remove `recover()` from the `defer` → `TestBracketIngestRunPanic` fails (line says `result=ok` or no line); (c) replace `report.Summary()` with the literal `scanned=0 digested=0 failed=0 refused=0 skipped=0 deferred=0 unchanged=0 archived=0 source-errors=0` → the dry-run test fails because the fixture has `digested=1`.
  7. Commit: `feat(ingest): Write timestamped run start and end lines`.
Acceptance: `go test -race ./cmd/cogvault` passes; the three step-6 mutations each fail at least one new test; `git diff c7a5dfc -- cmd/cogvault/cli_test.go` is empty.

## U3: Canon update for the bracket lines

Execution note: skip-test-first
Files:
  Modify: SPEC.md, DESIGN.md
  Test: none — documentation only
Interfaces:
  Consumes: the line formats and `result` values from U2
  Produces: none
Test scenarios:
  happy: n/a — documentation.
  edge: n/a — documentation.
  error: n/a — documentation.
  integration: n/a — leaf unit.
Steps:
  1. In `SPEC.md` §9.4, after the `--scheduled` bullet, add a bullet stating: every invocation that reaches the ingest handler writes `<RFC3339 local> ingest start origin=<scheduled|interactive>` and `<RFC3339 local> ingest end origin=<...> result=<ok|error|panic>[ <summary counts>]` to stderr; the counts are the stdout summary line and appear only when a report exists; the end line precedes the command error line; flag-parse errors rejected before the handler write neither line; stdout and exit codes are unchanged.
  2. In `DESIGN.md`, extend the `cmd/cogvault/*` row (line 683) with: `ingest brackets each run with timestamped stderr lines via one deferred emitter (recover → result=panic); clock seam ingestNow`.
  3. Self-review both edits against spec Success Criterion 6 (formats, three `result` values, stderr stream, flag-parse exclusion, clock seam, deferred emitter).
  4. Commit: `docs(canon): Document ingest run timestamp lines`.
Acceptance: `rg -n 'ingest start origin=' SPEC.md` and `rg -n 'result=<ok\|error\|panic>' SPEC.md` each return 1 line in §9.4; `rg -n 'ingestNow' DESIGN.md` returns 1 line.

## Test discrimination checks

| Boundary | Invariance fixture | Changed-axis fixture | Effect-bearing signal |
|---|---|---|---|
| Clock seam | Same fixed clock, same dry-run twice → identical stderr. | Only the second clock value changes → only the end-line timestamp differs. | End-line timestamp field. |
| Origin | Two runs without `--scheduled` → both `origin=interactive`. | Add `--scheduled` only → both lines say `origin=scheduled`; nothing else differs. | `origin=` field on both lines. |
| Stdout guard | Correct build: stdout lacks `" ingest start "` → pass. | Mutation (a) in U2 step 6 → fail. | Presence of bracket text on stdout. |
| Panic guard | Body returns `(nil, nil)` → `result=ok`, no panic. | Body panics → `result=panic` and panic propagates; mutation (b) fails it. | `result=` value and recovered panic value. |
| Counts guard | Dry-run fixture with one pending file → end counts equal stdout line 1. | Mutation (c) hard-codes zero counts → mismatch on `digested=1`. | End-line counts vs stdout summary. |

## Mutation/failure-state matrix

No stateful ceremony in the deliverable; no mutation/failure-state matrix required.

## Carry-forward trigger audit

Tracker examined: `ROADMAP.md` at `c7a5dfc` (no open backlog rows; "Next phase — no committed scope") and the carry-forward table in `docs/retros/2026-09-02-pdf-extraction-openai-adapter-retro.md` (4 open rows).

| Tracker row | Trigger class | What fired it | Disposition |
|---|---|---|---|
| Execute implementation-time probes at their named plan phase | event-based | This plan's U2 step 6 names mutation probes inside the unit that owns them. | Folded in: probes run in U2 step 6, not deferred to review. |

Unobservable drift-based rows: none.

Not fired: "Document a standing fallback for stuck external review" (event-based; no external review planned), "Decide whether nontrivial decision documents require a companion plan" (event-based; no decision document in this plan), "Combine temporary access-check prompt observation into one transcript" (edit-based on access-check evidence; not in the file list).

Attestation: all 4 open carry-forward rows and the ROADMAP candidate themes were classified against the file list above.

## Deferred to Follow-Up Work

- Log rotation for `~/Library/Logs/cogvault/*.log` — spec non-goal; about 17,520 extra lines per year.
- Timestamps for `slog` lines or other subcommands — spec non-goal.

## Open unknowns

Planning-time: none.

Implementation-time:
- Whether `runner.Run` returns a non-nil report together with `ErrAlreadyRunning`. The lock-held test asserts only `result=error`, so either outcome passes.
- The exact lock-fixture helper shape if the `TestIngestLockHeldFails` setup is extracted instead of copied.
