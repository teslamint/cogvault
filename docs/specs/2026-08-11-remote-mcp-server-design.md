---
title: Remote MCP Server for Claude and ChatGPT Apps
status: draft
date: 2026-08-11
schema: spec/v1
---

# Remote MCP Server for Claude and ChatGPT Apps Design

_Created 2026-08-11._

## Overview

cogvault serves its seven wiki tools over MCP stdio, plus a deprecated,
unauthenticated SSE transport bound to localhost. Neither reaches the Claude
apps (claude.ai, Desktop, mobile) or ChatGPT, because both products connect
from their own cloud infrastructure and require a credential-gated, publicly
reachable HTTPS endpoint. This feature adds a Streamable HTTP transport and an
OAuth 2.1 resource-server authorization layer so the wiki becomes usable from
those apps, while the server keeps running on the user's Mac behind an HTTPS
tunnel and the capture→digest pipeline stays local.

## User Scenarios

### S1: Ask the wiki from the claude.ai app

The user runs `cogvault serve --transport http` on their Mac, exposes it
through an HTTPS tunnel, and adds the tunnel URL as a custom connector at
claude.ai. Claude discovers the authorization server from the server's
Protected Resource Metadata, the user completes an authorization-code login at
their identity provider, and Claude can then call `wiki_search` and `wiki_read`
against the wiki from the phone or the web app.

### S2: Use the wiki from ChatGPT Developer Mode

The user adds the same tunnel URL as a custom connector in ChatGPT Developer
Mode, selecting OAuth. ChatGPT performs the same discovery and login. Because
`wiki_read`, `wiki_list`, `wiki_search`, `wiki_scan`, and `wiki_parse` carry
`annotations.readOnlyHint: true`, ChatGPT runs them without a per-call
confirmation prompt, while `wiki_write` and `wiki_delete` are framed as write
actions requiring confirmation.

### S3: Attach Claude Code to the remote server with a static token

From a second machine the user runs:

```bash
claude mcp add --transport http cogvault https://wiki.example.ts.net/mcp \
  --header "Authorization: Bearer $COGVAULT_BEARER_TOKEN"
```

The server is started with `auth.mode: bearer`, validates the constant-time
token match, and serves the full tool set. This path needs no identity
provider.

### S4: Local stdio use is unchanged

The user's existing local MCP client configuration keeps invoking
`cogvault serve` with no flags. The default transport stays stdio, no
authorization layer is applied, and every current tool behaves exactly as
before.

### S5: A misconfigured public server refuses to start

The user runs `cogvault serve --transport http --addr 0.0.0.0:8080` while
`auth.mode` is `none`. The server refuses to start and prints an error naming
the conflict, because that combination would expose `wiki_write` and
`wiki_delete` to anyone who can reach the port.

### S6: An expired token triggers re-authorization

A client calls a tool with an expired access token. The server returns `401`
with a `WWW-Authenticate: Bearer` challenge carrying a `resource_metadata`
pointer. The client rediscovers the authorization server and re-runs the
authorization-code flow without the user editing any configuration.

## Scope

### In

- `--transport http` on `cogvault serve`, using mcp-go's
  `server.NewStreamableHTTPServer`, served at a configurable endpoint path
  (default `/mcp`).
- A new `internal/httpauth` package: an `http.Handler` middleware implementing
  three authorization modes — `none`, `bearer`, `oauth`.
- OAuth 2.1 **resource server** role only: Protected Resource Metadata
  discovery endpoint (RFC 9728), `401` + `WWW-Authenticate` challenges, and
  JWT access-token validation against the issuer's JWKS.
- Identity-provider-agnostic OAuth configuration: `issuer`, `audience`, and
  optional `required_scopes`. No vendor is hardcoded.
- A startup guard rejecting a non-loopback bind address when `auth.mode` is
  `none`.
- MCP tool annotations (`readOnlyHint`, `destructiveHint`) on all seven tools.
- Config schema additions under a new `auth:` block, with the bearer token read
  from an environment variable and never from the config file.
- Deployment documentation for exposing the server through an HTTPS tunnel.
- Canonical documentation updates: `SPEC.md` §3.1, §8, §9; `DESIGN.md` §2.8,
  §2.9, and a new component section; `CONCEPTS.md`.

### Out

- **Acting as an OAuth authorization server.** cogvault never issues, refreshes,
  or revokes tokens. The user brings an identity provider.
- **Opaque access tokens / RFC 7662 introspection.** Only JWT access tokens are
  validated. An identity provider that issues opaque tokens is unsupported in
  this release.
- **Dynamic Client Registration and Client ID Metadata Documents.** These are
  authorization-server responsibilities, not resource-server ones.
- **ChatGPT Apps SDK UI widgets** (`_meta.ui.resourceUri`, `text/html;profile=mcp-app`
  resources, the `postMessage` bridge). A separable feature with its own design.
- **ChatGPT deep-research `search`/`fetch` tools.** Required only for the deep
  research and company-knowledge surfaces, which are not targeted here;
  Developer Mode accepts cogvault's existing tool names.
- **Removing the SSE transport.** It stays, unchanged and still accepted by both
  vendors, so existing setups do not break.
- **Tool-result size capping.** See Open Decisions D3.
- **Automated tunnel provisioning.** Documentation only; cogvault does not
  manage cloudflared or Tailscale.
- **Per-user authorization or multi-tenancy.** A valid token grants the full
  tool set. This follows the approved decision that trust concentrates in the
  authorization layer.

## Assumptions and Preconditions

| Claim | Command | Observed at | Observed result | Evidence source |
|---|---|---|---|---|
| mcp-go v0.47.0 exposes a Streamable HTTP server that is an `http.Handler`, so auth can wrap it without forking | `grep -n "func (s \*StreamableHTTPServer) ServeHTTP\|func NewStreamableHTTPServer" $(go env GOMODCACHE)/github.com/mark3labs/mcp-go@v0.47.0/server/streamable_http.go` | 2026-08-11T02:23:00Z | `NewStreamableHTTPServer` at line 212; `ServeHTTP` at line 246 | Module cache, `mcp-go@v0.47.0` |
| mcp-go ships **no** server-side OAuth support, so the resource-server role is hand-rolled | `grep -rln "oauth\|OAuth\|WWW-Authenticate\|protected-resource" $(go env GOMODCACHE)/github.com/mark3labs/mcp-go@v0.47.0/server/` | 2026-08-11T02:30:40Z | Zero matches under `server/`; all 11 matches are under `client/` and `client/transport/` | Module cache, `mcp-go@v0.47.0` |
| mcp-go negotiates protocol revisions up to `2025-11-25`, not the current `2026-07-28` revision, so that revision's session and GET-stream removals do not apply | `sed -n '138,148p' $(go env GOMODCACHE)/github.com/mark3labs/mcp-go@v0.47.0/mcp/types.go` | 2026-08-11T02:23:00Z | `LATEST_PROTOCOL_VERSION = "2025-11-25"`; valid list also has `2025-06-18`, `2025-03-26`, `2024-11-05` | Module cache, `mcp-go@v0.47.0` |
| No MCP tool currently declares annotations, so `readOnlyHint` is additive rather than a change of existing behavior | `grep -n "ReadOnly\|Annotation\|Destructive\|Idempotent" internal/mcp/tools.go` | 2026-08-11T02:26:53Z | Zero matches | Working tree at `d6bd457` |
| Claude apps require public reachability and cannot reach a loopback address, which is why the tunnel is mandatory rather than optional | Live documentation fetch by a research subagent, reconfirmed by an independent verifier | 2026-08-11T02:41:23Z | "Servers hosted on a private corporate network, behind a VPN, or blocked by a firewall won't connect, even if you can reach them from your own machine." | `support.claude.com/en/articles/11175166-get-started-with-custom-connectors-using-remote-mcp` |
| Anthropic's connections originate from a fixed egress range, so a firewall allowlist is possible as defence in depth | Live documentation fetch by an independent verifier | 2026-08-11T02:41:23Z | "Anthropic's outbound traffic to your server originates from `160.79.104.0/21`." | `claude.com/docs/connectors/building/authentication`, "Network reference" section |
| ChatGPT cannot present a static credential, which is why OAuth is required rather than optional for ChatGPT support | Live documentation fetch by a research subagent | 2026-08-11T02:29:26Z | "ChatGPT does not support machine-to-machine OAuth grants ... nor can it present custom API keys or customer-provided mTLS certificates" | `developers.openai.com/plugins/build/auth` |
| Anthropic's static-header auth is an organization-administrator beta, so it is not a reliable path for this user's personal account | Live documentation fetch by a research subagent, reconfirmed by an independent verifier | 2026-08-11T02:41:23Z | `static_headers` availability is "Beta"; the credential is "entered by an organization administrator" and "shared by the organization rather than pasted per user". Only `oauth_dcr` and `oauth_cimd` are marked "Supported out of the box". The verifier could **not** confirm an early-access contact instruction attached to the `static_headers` row specifically, so no such claim is relied on here. | `claude.com/docs/connectors/building/authentication` |

Environment invariants that still apply: the wiki and its SQLite index stay on
the user's Mac; `wiki_dir` remains the sole read-write root per decision 0021
D1; and the ingest single-writer lock is unaffected because `serve` does not
write to the ledger.

## Architecture

Three layers, each independently testable:

```
cmd/cogvault/serve.go
  ├─ selects transport: stdio (default) | sse | http
  ├─ enforces the bind-address guard (S5)
  └─ for http: httpauth.Middleware(cfg.Auth) wraps StreamableHTTPServer

internal/httpauth/           (new package, no MCP dependency)
  ├─ Middleware(Config) func(http.Handler) http.Handler
  ├─ ProtectedResourceMetadataHandler(Config) http.Handler
  ├─ bearer mode: constant-time comparison against the env-supplied token
  └─ oauth mode: Validator — OIDC discovery, JWKS cache, JWT verification

internal/mcp/tools.go
  └─ tool definitions gain readOnlyHint / destructiveHint annotations
```

`internal/httpauth` deliberately knows nothing about MCP. It takes an
`http.Handler` and returns one, so it is exercised with `net/http/httptest`
alone. `internal/mcp` stays transport-agnostic; it never learns that an HTTP
transport exists.

Request flow in `oauth` mode:

1. Request arrives at the mux. `/.well-known/oauth-protected-resource` (and its
   RFC 9728 path-suffixed form) is served unauthenticated.
2. Any other path enters the middleware. A missing or malformed
   `Authorization: Bearer <jwt>` header yields `401` with
   `WWW-Authenticate: Bearer resource_metadata="<public-url>/.well-known/oauth-protected-resource"`.
3. The validator verifies the JWT signature against a cached JWKS, then checks
   `iss` against the configured issuer, `aud` against the configured audience,
   and `exp`/`nbf` against the clock with a 60-second leeway. A `kid` absent
   from the cache triggers one rate-limited JWKS refetch before rejection.
4. Failures return `401` with an `error="invalid_token"` challenge. A valid
   token whose scopes miss `required_scopes` returns `403` with
   `error="insufficient_scope"`.
5. On success the request passes to the Streamable HTTP handler unchanged.

## Interface

### CLI

```
cogvault serve [--transport stdio|sse|http] [--addr host:port]
               [--endpoint-path /mcp] [--public-url https://wiki.example.com]
```

- `--transport` gains the `http` value. The default stays `stdio`.
- `--addr` default stays `localhost:8080`.
- `--endpoint-path` (new, default `/mcp`) sets the MCP endpoint path.
- `--public-url` (new) is the externally visible base URL the tunnel maps to.
  It is required in `oauth` mode because the Protected Resource Metadata
  document and the `WWW-Authenticate` pointer must carry the URL the client
  reached, not the loopback bind address.

Unknown `--transport` values are rejected with an error naming the accepted
set. Today they silently fall through to stdio; that is fixed here.

### Config

New top-level `auth:` block in the config file:

```yaml
auth:
  mode: string            # "none" | "bearer" | "oauth". Default: "none".
  oauth:
    issuer: string        # OIDC issuer URL. Required when mode is "oauth".
    audience: string      # Expected `aud` claim. Required when mode is "oauth".
    required_scopes: []   # Optional. Empty means any valid token is accepted.
```

The bearer token is **never** a config field. It is read from
`COGVAULT_BEARER_TOKEN`, and `mode: bearer` with an unset or empty variable is
a startup error. Rationale: config files are routinely committed, and this
project's config already lives beside a git-tracked wiki.

Validation, following the existing `field: bad value; expected form` convention
in `internal/config`:

- `auth.mode` outside the accepted set → error.
- `mode: oauth` with an empty `issuer` or `audience` → error.
- `issuer` that is not an absolute `https://` URL → error.
- `mode: none` with a non-loopback `--addr` → startup error (S5).
- `mode: oauth` with no `--public-url` → startup error.

### Protected Resource Metadata response

```json
{
  "resource": "<public-url>/mcp",
  "authorization_servers": ["<issuer>"],
  "scopes_supported": ["<required_scopes...>"],
  "bearer_methods_supported": ["header"]
}
```

### Tool annotations

| Tool | readOnlyHint | destructiveHint |
|---|---|---|
| `wiki_read`, `wiki_list`, `wiki_search`, `wiki_scan`, `wiki_parse` | `true` | `false` |
| `wiki_write` | `false` | `true` |
| `wiki_delete` | `false` | `true` |

`wiki_write` is marked destructive because it overwrites unconditionally:
`SPEC.md` §5.4 states existing files are overwritten, and `FSStorage.Write`
calls `os.WriteFile` with no existence check, no `O_EXCL`, and no conflict
token. Annotations are advertised metadata, not enforcement — the
handlers keep their own path and permission checks unchanged.

## Data model

No SQLite schema change and no index migration. The JWKS cache is in-memory
only: a `map[kid]crypto.PublicKey` with a fetch timestamp, a configurable TTL
(default 15 minutes), and a minimum 60-second interval between forced refetches
so an attacker cannot drive unbounded outbound requests with unknown `kid`
values.

## Integration

- **Canon updates required before this work is complete**, per `CLAUDE.md`:
  - `SPEC.md` §8.1: the "Transport: stdio" line is now wrong (SSE already
    shipped) and must name all three transports plus the authorization model.
  - `SPEC.md` §8: `wiki_delete` has no subsection. §8 runs 8.1–8.7 and covers
    only the other six tools. Its sole appearance anywhere in `SPEC.md` is
    line 48, inside §1.3 "Out of scope (Phase 1)", which still asserts it is
    not built. Both are fixed here: §8 gains a `wiki_delete` subsection, and
    the `wiki_delete`/auto-commit entry leaves §1.3. This is pre-existing
    drift, but documenting the tool in §8 while §1.3 calls it out of scope
    would be a contradiction **this change itself creates**, so it is in scope.
    - Deliberately **not** fixed here: §1.3 also still lists vector search,
      ontology graph, `ResolveLink`, periodic `cogvault digest`, phone capture,
      URL/web extraction, and the local LLM backend, all of which `ROADMAP.md`
      records as delivered. That is unrelated roadmap-clearance drift; folding
      it in would silently widen this feature. Tracked as a follow-up in
      `docs/research/v2-follow-ups.md`.
  - `SPEC.md` §8.5: the note claiming mcp-go does not set
    `additionalProperties:false` is stale; `internal/mcp/server.go` now applies
    `noExtra` to every tool. Fixed here for the same reason.
  - `SPEC.md` §3.1: add the `auth:` block. §9: add the new `serve` flags.
  - `DESIGN.md` §2.8 (mcp) and §2.9 (cmd), plus a new component section for
    `internal/httpauth`.
  - `CONCEPTS.md`: add "Resource server" and "Protected Resource Metadata".
- `ROADMAP.md` lists SSE transport as done under "Consume / tooling expansion";
  a remote-with-auth row is added there.
- No interaction with `internal/ingest`, `internal/llm`, or the launchd job.

## Testing

`DESIGN.md` §7 holds a per-package test-target table; the rows below extend it
for the new package. Unit tests reach no network — the JWKS endpoint is a local
`httptest` server.

- `internal/httpauth` unit tests with a locally generated RSA key pair and a
  stub JWKS server via `httptest`:
  - valid token passes; expired, not-yet-valid, wrong `aud`, wrong `iss`,
    bad signature, unknown `kid`, and malformed JWT each return `401`
  - valid token missing a required scope returns `403` with
    `error="insufficient_scope"`
  - missing `Authorization` header returns `401` carrying a
    `WWW-Authenticate` header whose `resource_metadata` is the configured URL
  - bearer mode accepts the exact token and rejects near-misses; the comparison
    is constant-time
  - `none` mode passes every request through
  - JWKS refetch on unknown `kid` happens at most once per minimum interval
- Protected Resource Metadata handler returns the documented JSON at both the
  bare and path-suffixed well-known paths, and stays reachable without a token.
- `cmd/cogvault` tests: `--transport http` starts and serves; an unknown
  transport value errors; the `mode: none` plus non-loopback address
  combination refuses to start; `mode: oauth` without `--public-url` refuses to
  start; `mode: bearer` with an unset `COGVAULT_BEARER_TOKEN` refuses to start.
- `internal/mcp` test asserting every registered tool carries annotations and
  that exactly `wiki_write` and `wiki_delete` are non-read-only — this fails if
  a future tool is added without a deliberate annotation choice.
- Integration test in `internal/mcp/integration_test.go` style: a full
  initialize-then-`tools/call` round trip over Streamable HTTP through the
  middleware with a signed token.

## Risks

| Risk | Mitigation |
|---|---|
| Exposing `wiki_write`/`wiki_delete` to the internet; a token leak means wiki loss | The S5 startup guard blocks the worst misconfiguration. `wiki_delete` already auto-commits to git when the wiki is a repository, so deletions are recoverable there. Documented explicitly in the deployment section. |
| Hand-rolled JWT validation is a classic source of vulnerabilities | Use `github.com/golang-jwt/jwt/v5` with an explicit allowed-algorithms list (`RS256`, `ES256`) rather than trusting the token header; never accept `none`. Negative tests above cover each rejection path. |
| Identity providers that issue opaque rather than JWT access tokens simply will not work | Declared out of scope and documented; startup cannot detect it, so the failure surfaces as a `401` at first call. Open Decision D2 tracks introspection support. |
| Tunnel URL changes (ephemeral cloudflared URLs) break the `resource` value in the metadata document | `--public-url` is explicit rather than inferred, and the docs recommend a stable named tunnel over ephemeral URLs. |
| Claude caps tool results near 150,000 characters; a large `wiki_read` result could be truncated by the client | Wiki pages are LLM-generated digests and are far below this, but it is unmeasured. Open Decision D3. |
| mcp-go trails the current spec revision (`2025-11-25` vs `2026-07-28`) | Clients negotiate downward; no action needed now. Revisit if a vendor drops pre-2026 revisions. |
| The user's ChatGPT plan may not include Developer Mode | Documented as a precondition; Developer Mode needs Pro/Plus/Business/Enterprise/Education on web. Not verifiable from this repository. |

## Success Criteria

1. `cogvault serve --transport http` serves a working MCP endpoint that
   completes an initialize and `tools/call` round trip.
   - **Measured by**: `go test ./internal/mcp/ -run TestStreamableHTTP -v`
2. Every authorization rejection path returns the correct status and challenge
   header.
   - **Measured by**: `go test ./internal/httpauth/ -v` — all cases in the
     Testing section pass, including the `401` versus `403` distinction.
3. A server in `auth.mode: none` cannot be started on a non-loopback address.
   - **Measured by**: `go test ./cmd/cogvault/ -run TestServeBindGuard -v`
4. All seven MCP tools declare annotations, and exactly `wiki_write` and
   `wiki_delete` are non-read-only.
   - **Measured by**: `go test ./internal/mcp/ -run TestToolAnnotations -v`
5. The Protected Resource Metadata document is reachable without credentials and
   names the configured issuer.
   - **Measured by**: with a test server running,
     `curl -fsS "$PUBLIC_URL/.well-known/oauth-protected-resource" | jq -e '.authorization_servers[0]'`
     exits `0` and prints the configured issuer.
6. The existing stdio and SSE transports behave exactly as before.
   - **Measured by**: `go test ./...` passes with no changes to pre-existing
     stdio or SSE test expectations.
7. Canonical documentation matches the shipped behavior, including the
   pre-existing `SPEC.md` drift items named in Integration.
   - **Measured by**: reviewer rubric — `SPEC.md` §8.1 names all three
     transports and the authorization model; §8 has a `wiki_delete` subsection;
     §1.3 no longer lists `wiki_delete`/auto-commit as out of scope; §8.5's
     `additionalProperties` note matches `internal/mcp/server.go`; §3.1 carries
     the `auth:` block; §9 carries the new flags; `DESIGN.md` has a component
     section for `internal/httpauth`; and the remaining stale §1.3 entries are
     recorded as a follow-up rather than left unnoticed.
8. A reader can stand the server up remotely from the documentation alone.
   - **Measured by**: reviewer rubric — the deployment section states the tunnel
     step, the `--public-url` requirement, the identity-provider prerequisites
     (JWT access tokens, issuer, audience), and the token-leak risk, with no
     step left as "configure appropriately."

## Open Decisions

- **D1 — Which identity provider the user actually adopts.** The design is
  vendor-agnostic, so this does not block implementation, but it does block
  end-to-end verification of S1 and S2. _Resolved by: user, during
  implementation._
- **D2 — Opaque access tokens via RFC 7662 introspection.** Out of scope here.
  Whether to add it depends on D1: some providers issue opaque tokens by
  default. _Resolved by: user, after D1._
- **D3 — Tool-result size capping.** Claude truncates near 150,000 characters
  and Claude Code near 25,000 tokens. Whether cogvault should truncate with an
  explicit marker instead of letting clients silently cut results is unmeasured
  and deferred. _Resolved by: user, after observing real usage._
- **D4 — Whether the SSE transport should start warning about deprecation.**
  Both vendors still accept it, so removal is not on the table, but a startup
  notice may be worth adding. _Resolved by: planning._
