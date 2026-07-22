# U6 · T4 — Lock exclusion + cancellation

- Plan: docs/plans/2026-07-22-001-feat-v2-capture-pipeline-plan.md, row T4
- Source commit: a60fbae
- Fixtures: TestRunAlreadyRunningLock (lock), TestRunContextCanceled (pre-run cancellation), TestRunCancelAfterFirstFile (mid-run cancellation)
- Timestamp: 2026-07-22T14:25:38Z
- Isolation: t.TempDir() dirs, mock llm. No real claude, no real user data.

## Lock exclusion
- Pre: `ingest.lock` at dir(dbPath); one source file staged.
- Action: test opens the same lock file on an independent fd and holds `unix.Flock(LOCK_EX|LOCK_NB)`; then calls `Run`. flock is per open-file-description, so a second open within the same process contends.
- Post: `Run` acquires the flock inside Run (not New), sees EWOULDBLOCK, returns the exported sentinel `ErrAlreadyRunning` immediately (err == ErrAlreadyRunning). Real lock released on Run exit via defer (LOCK_UN + close).

## Cancellation

### Pre-run cancellation (TestRunContextCanceled)
- Pre: one source file staged.
- Action: pass an already-canceled context to `Run`.
- Post: lock acquired then released via defer; ctx.Err() checked before the first file → `Run` returns non-nil partial Report plus `fmt.Errorf("ingest.Run: %w", ctx.Err())` (error contains "context canceled"); mock llm never invoked.

### Mid-run cancellation (TestRunCancelAfterFirstFile)
- Pre: two source files staged (`a-first.md`, `b-second.md`; scan sorts by absPath).
- Action: mock llm adapter cancels the shared context after completing the FIRST file's digest (on its first call), then returns success; the loop's top-of-iteration `ctx.Err()` check trips before the second file.
- Post assertions:
  - `Run` returns a wrapped context error — `errors.Is(err, context.Canceled)` holds.
  - `Report.Digested == 1` (only the first file digested; llm called exactly once).
  - First file's ledger row: status `success`.
  - Second file: NO ledger row (never reached).
  - Lock released on abort: a subsequent `Run` with a fresh context acquires the flock cleanly and finishes the backlog (digested=1 for the second file, unchanged=1 for the first).
