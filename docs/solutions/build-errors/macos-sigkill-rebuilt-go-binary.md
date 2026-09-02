---
module: cmd/cogvault
date: "2026-07-27"
problem_type: build_error
component: binary_deployment
severity: high
symptoms:
  - "Rebuilt Go binary exits immediately with signal: killed (exit code 137)"
  - "go run ./cmd/cogvault/ works but the compiled binary does not"
  - "Binary worked before rebuild; same source, different binary hash"
root_cause: "macOS FDA (Full Disk Access) tracks binaries by code directory hash; a rebuilt binary has a new hash and loses its TCC grant, causing SIGKILL on first protected-resource access"
resolution_type: code_signing
tags:
  - macos
  - go
  - fda
  - codesign
  - tcc
  - sigkill
---

## Problem

After rebuilding a Go binary (`go build -o ~/bin/cogvault ./cmd/cogvault/`), the
binary is immediately killed by macOS with SIGKILL (exit code 137). The old binary
at the same path worked fine. `go run` with the same source code works.

## Symptoms

- `cogvault --help` → exit 137, no output
- `cogvault ingest ...` → `zsh: killed`
- `go run ./cmd/cogvault/ --help` → works normally
- `go test ./...` → all pass (tests don't execute the installed binary)

## What Didn't Work

- Re-adding the binary to Full Disk Access in System Settings (macOS re-grants by
  path, but the underlying code hash check still fails on some macOS versions)
- Removing `com.apple.provenance` extended attribute (`xattr -d` — macOS
  re-applies it automatically)
- Building from a different terminal session (the provenance attribute persists
  regardless of build context)

## Solution

Re-sign the binary after every build:

```bash
go build -o ~/bin/cogvault ./cmd/cogvault/
codesign --force --sign - ~/bin/cogvault
```

The `--force` flag replaces the existing adhoc signature. The `-` identity creates
a new adhoc signature with a fresh code directory hash that macOS accepts.

When building and installing to a separate path, re-sign at the destination —
signing the build artifact alone is not enough because macOS TCC matches the
code hash at the FDA-granted path, not the build path:

```makefile
build:
	go build -o cogvault ./cmd/cogvault/
	codesign --force --sign - cogvault

install: build
	mkdir -p $(HOME)/bin
	cp cogvault $(HOME)/bin/cogvault
	codesign --force --sign - $(HOME)/bin/cogvault
```

## Why This Works

Go binaries are adhoc-signed by the linker (`linker-signed` flag in
`codesign -dv` output). macOS TCC (Transparency, Consent, and Control) grants
like Full Disk Access are keyed to the binary's code directory hash. When `go
build` produces a new binary, the hash changes, and the existing TCC grant
no longer matches. macOS kills the binary on the first system call that touches
a TCC-protected resource (iCloud Drive, Downloads, etc.) rather than showing a
permission dialog.

`codesign --force --sign -` generates a fresh adhoc signature that macOS
recognizes, allowing the TCC subsystem to match the binary against existing
grants or prompt for new ones.

## Prevention

- Always run `codesign --force --sign -` after `go build` on macOS when the
  binary accesses TCC-protected directories.
- When copying to a separate install path, re-sign at the destination — a
  sign-then-copy without destination re-sign still triggers SIGKILL.
- Add the codesign step to the project's build target (`make build` / `make
  install` in cogvault's Makefile).
- When granting FDA to a Go binary, expect to re-sign after every rebuild.
