---
title: Makefile with codesign
status: approved
date: 2026-07-27
schema: spec/v1
---

# Makefile with codesign Design

_Created 2026-07-27._

## Overview

Add a Makefile to cogvault with build, install, test, and clean targets. The build target includes `codesign --force --sign -` to prevent macOS SIGKILL after every rebuild (F10 from the follow-ups tracker, surfaced during F9 deployment).

## User Scenarios

### S1: Developer rebuilds and installs after code change

Developer edits Go source, runs `make install`. The binary is built, adhoc-signed, and copied to `~/bin/cogvault` (the launchd plist path). The next `launchctl start` uses the new binary without SIGKILL.

### S2: Developer runs tests

Developer runs `make test`. This executes `go test -race ./...` — no difference from today, but now discoverable via the Makefile itself.

### S3: Developer builds without installing

Developer runs `make build` or just `make`. The binary is produced in the project root as `./cogvault` (already in `.gitignore`) and adhoc-signed, but not copied to `~/bin/`.

## Scope

### In

- `Makefile` in project root with targets: `build` (default), `install`, `test`, `clean`
- `codesign --force --sign -` in the build target
- DESIGN.md update: mention Makefile in §5 File responsibilities
- README.md update: point build instructions at `make build` / `make install`

### Out

- CI/CD integration (no CI exists)
- Cross-platform build support (cogvault uses launchd; macOS-only)
- Version embedding via `-ldflags`
- Release automation

## Assumptions and Preconditions

_No live assumptions were retained for this spec. The codesign fix was validated during F9 deployment (see `docs/solutions/build-errors/macos-sigkill-rebuilt-go-binary.md`)._

## Architecture

No architectural change. One new file (`Makefile`) at the project root.

### Affected files

| File | Change |
|---|---|
| `Makefile` | New: build/install/test/clean targets with codesign |
| `DESIGN.md` | Update: mention Makefile in §5 File responsibilities |
| `README.md` | Update: point build/install instructions at Makefile targets |

## Config

| Variable | Default | Purpose |
|---|---|---|
| `BINARY` | `cogvault` | Output binary name |
| `INSTALL_DIR` | `$(HOME)/bin` | Install destination (matches launchd plist) |

## Testing

No automated tests — this is a build script. Verification is manual:

1. `make build` produces `./cogvault` with adhoc signature
2. `make install` copies to `~/bin/cogvault`
3. `make test` runs the test suite
4. `make clean` removes `./cogvault`

## Risks

| Risk | Mitigation |
|---|---|
| `codesign` unavailable on non-macOS | Out of scope — cogvault is macOS-only (launchd) |
| User has different install path | `INSTALL_DIR` variable is overridable: `make install INSTALL_DIR=/usr/local/bin` |
| `~/bin` does not exist on a fresh machine | `install` target creates the directory with `mkdir -p` |

## Success Criteria

1. `make build` produces `./cogvault` with a proper adhoc signature (not linker-signed)
   - **Measured by**: `make build && codesign -dv ./cogvault 2>&1 | grep -c 'linker-signed'` returns 0 AND `codesign -dv ./cogvault 2>&1 | grep -c 'Signature=adhoc'` returns 1
2. `make install` copies the signed binary to `~/bin/cogvault` and it runs without SIGKILL
   - **Measured by**: `make install && ~/bin/cogvault --help; test $? -eq 0` (exit 137 was the SIGKILL failure signature)
3. `make test` runs `go test -race ./...` and exits 0
   - **Measured by**: `make test` exits 0
4. `make clean` removes `./cogvault`
   - **Measured by**: `make clean && test ! -f ./cogvault`
5. DESIGN.md §5 mentions the Makefile
   - **Measured by**: `grep -c Makefile DESIGN.md` returns at least 1
6. README.md build instructions reference `make` targets
   - **Measured by**: `grep -c 'make build\|make install' README.md` returns at least 1

## Open Decisions

None.
