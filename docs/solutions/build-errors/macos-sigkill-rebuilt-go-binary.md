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
BINARY     = cogvault
INSTALL_DIR = $(HOME)/bin
CODESIGN_IDENTITY   ?= -
CODESIGN_IDENTIFIER ?= dev.tmint.cogvault

build:
	go build -o $(BINARY) ./cmd/cogvault/
	@echo "codesign: identity=$(CODESIGN_IDENTITY) identifier=$(CODESIGN_IDENTIFIER)"
	codesign --force --sign "$(CODESIGN_IDENTITY)" --identifier "$(CODESIGN_IDENTIFIER)" $(BINARY)

install: build
	mkdir -p $(INSTALL_DIR)
	cp $(BINARY) $(INSTALL_DIR)/$(BINARY)
	@echo "codesign: identity=$(CODESIGN_IDENTITY) identifier=$(CODESIGN_IDENTIFIER)"
	codesign --force --sign "$(CODESIGN_IDENTITY)" --identifier "$(CODESIGN_IDENTIFIER)" $(INSTALL_DIR)/$(BINARY)
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

- Set `CODESIGN_IDENTITY` and `CODESIGN_IDENTIFIER` in the Makefile. The
  default identity `-` keeps ad-hoc signing available; use a stable certificate
  identity with a fixed identifier when grants must survive rebuilds.
- Always sign after `go build` on macOS when the binary accesses
  TCC-protected directories, passing both variables with quoted arguments:
  `codesign --force --sign "$(CODESIGN_IDENTITY)" --identifier "$(CODESIGN_IDENTIFIER)" $(BINARY)`.
- When copying to a separate install path, re-sign at the destination — a
  sign-then-copy without destination re-sign still triggers SIGKILL. Use the
  same identity and identifier at both paths.
- Keep both signing steps in the project's `make build` and `make install`
  targets instead of relying on a manual command.
- An ad-hoc identity creates a `cdhash`-only designated requirement, so every
  rebuild invalidates every grant. A stable certificate identity with a fixed
  identifier creates a certificate-based requirement with no `cdhash` term, so
  existing grants can survive rebuilds only when every rebuild uses that same
  identity. See the README's [Schedule zero-touch ingest](../../../README.md#5-schedule-zero-touch-ingest-launchd)
  section for the one-time grant ceremony.

## Certificate rotation

Developer ID code signed while its certificate is valid can remain valid after
that certificate expires when it has a secure timestamp. A valid identity is
still required to sign future rebuilds. Moving to a new identity changes the
code requirement, so repeat the one-time ceremony after the new install and
use the README's stale-grant inspection procedure for earlier rows. The local
effect of certificate revocation on TCC matching has not been verified.
