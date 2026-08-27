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

Use the project's install target with the same stable identity for every
rebuild:

```bash
make install CODESIGN_IDENTITY="Developer ID Application: <your name> (<team>)"
```

The default `-` identity remains available for contributors without a
certificate. Do not use it for a TCC-protected installed binary. An ad-hoc
signature whose `cdhash` changes cannot match its prior grant.

The target signs both the build artifact and the installed copy with the same
identity and identifier:

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

Go binaries are ad-hoc-signed by the linker (`linker-signed` in `codesign -dv`
output). In the affected local TCC rows, the ad-hoc designated requirement was
only a `cdhash`; a changed rebuild cannot satisfy it. A Developer ID signature
with a fixed identifier has a certificate-based designated requirement without
that `cdhash` term, so a changed binary signed with the same identity can
satisfy the prior requirement.

TCC storage is service-specific. The observed AppData row exists after Allow
but stores a NULL `csreq`; that row does not itself prove the code requirement.
Inspect the installed binary with `codesign -d -r-` when checking its signature.

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
- An ad-hoc identity creates a `cdhash`-only designated requirement. A rebuild
  that changes its `cdhash` invalidates matching grants. A stable certificate
  identity with a fixed identifier creates a certificate-based requirement with
  no `cdhash` term. Existing grants can survive only when every rebuild uses
  that same identity. See the README's [Schedule zero-touch ingest](../../../README.md#5-schedule-zero-touch-ingest-launchd)
  section for the one-time grant ceremony.

## Certificate rotation

Developer ID code signed while its certificate is valid can remain valid after
that certificate expires when it has a secure timestamp. A valid identity is
still required to sign future rebuilds. Moving to a new identity changes the
code requirement, so repeat the one-time ceremony after the new install and
use the README's stale-grant inspection procedure for earlier rows. The local
effect of certificate revocation on TCC matching has not been verified.
