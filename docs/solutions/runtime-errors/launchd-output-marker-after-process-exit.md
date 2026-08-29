---
module: launchd access-check harness
date: 2026-08-29
problem_type: runtime_error
component: shell-harness
severity: medium
symptoms:
  - "A launchd command exits successfully, but its redirected success marker is not visible immediately."
  - "A harness reports a false timeout or missing-marker failure after the child has exited."
root_cause: launchd can flush redirected stdout after the responsible process exits
resolution_type: code_fix
related_components:
  - launchd
  - macos-tcc
tags:
  - launchd
  - stdout
  - timeout
  - race
---

# Launchd output marker after process exit

## Problem

A launchd job can return exit status zero before its redirected stdout contains
the command-owned success marker. A harness that checks the marker immediately
after the PID exits can report a false failure.

## Symptoms

The child exits successfully, but the marker file is still empty or incomplete.
The marker appears shortly afterward when the redirect stream flushes.

## What did not work

Checking the marker only once after `kill -0` stops reporting the PID. Extending
the timeout after process exit hides the race and changes the declared time bound.

## Solution

Keep the original timeout start time. After the PID exits, poll the command-owned
marker until it appears as an exact full line. Fail when the shared timeout
expires, and retain the stdout and stderr artifacts for diagnosis.

## Why this works

The PID loop proves responsible-process completion. The marker loop proves that
the process result reached the redirected output. Sharing one start time keeps
the harness bound honest while allowing normal stream flushing.

## Prevention

Add delayed-marker and decoy-marker fixtures. Require an exact-line match rather
than a substring so diagnostic text cannot satisfy the success condition.

Evidence: commits `5c6ddb5` and `0323684`; `bash scripts/check-scheduled-access_test.sh`.
