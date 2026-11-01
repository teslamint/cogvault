# 0025: Versioning and Container Release

- Status: accepted
- Date: 2026-11-01
- Context owner: this decision; `docs/deployment/nas.md` describes the deployment it enables.

## Problem

The repository had no release tags, and the version string `0.1.0` was
hard-coded in `internal/mcp/server.go`. An operator could not tell which build
runs on a host, and no artifact other than a locally signed macOS binary
existed.

## Decision

1. Versions follow SemVer 2.0.0 and are tagged `vMAJOR.MINOR.PATCH` on `main`.
   While MAJOR is 0, a MINOR bump may change the public contract below; PATCH
   never does.
2. The public contract is: config file keys and validation, CLI commands and
   flags, MCP tool names and argument/result shapes, and the SQLite index
   schema version (`SPEC.md` §6.4). A change that breaks any of these needs a
   MINOR bump (MAJOR from 1.0.0 on) and an entry in the release notes.
3. The single version source is the git tag. `internal/version.String()`
   returns the `-ldflags -X` value, else the module version from build info,
   else `dev`. `cogvault --version` and the MCP server `initialize` response
   report it.
4. Pushing a `v*` tag runs `.github/workflows/container.yml`. It builds a
   `linux/amd64` + `linux/arm64` image and publishes it to
   `ghcr.io/teslamint/cogvault` with tags `X.Y.Z`, `X.Y`, and `sha-<short>`.
   Pushes to `main` publish only `edge` and `sha-<short>`. `latest` is not
   published; deployments pin an explicit version tag.
5. The first release is `v0.2.0`.

## Why

A tag-derived version leaves one place to edit and lets CI and the Makefile
agree. Pinning deployments to explicit tags makes a NAS rollback a one-line
change.

## Alternatives Considered

- A `VERSION` file: a second source that can drift from the tag.
- Publishing `latest`: hides which version runs and makes rollback ambiguous.
- Automated release tooling (release-please, goreleaser): more machinery than a
  single-maintainer repository needs now.

## Revisit Triggers

- A second maintainer or external consumers appear.
- A release needs binaries besides the container image.

## Related Files

- `internal/version/version.go`
- `Makefile`
- `Dockerfile`
- `.github/workflows/container.yml`
