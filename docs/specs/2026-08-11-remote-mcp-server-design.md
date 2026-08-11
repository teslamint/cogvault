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
`wiki_delete` to anyone who can reach the port. The identical guard fires for
`--transport sse`, which is unauthenticated today.

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
- The same middleware and the same bind guard applied to the **existing SSE
  transport**, not only to the new HTTP one. `server.SSEServer` is already an
  `http.Handler` (`ServeHTTP` at `sse.go:783`), so this is wiring, not a
  rewrite. Leaving SSE ungated would ship a feature whose stated purpose is
  safe remote exposure while keeping an unauthenticated remote door open.
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
- Deployment documentation for exposing the server through an HTTPS tunnel,
  including an explicit statement that cogvault provides no recovery from a
  compromised credential and that backups are the operator's responsibility.
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
- **Removing the SSE transport.** It stays, still accepted by both vendors, so
  existing setups do not break. It does gain the authorization layer and the
  bind guard (see In), which is a behavior change under non-default config but
  not a removal.
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
  ├─ enforces the bind-address guard (S5) for both network transports
  └─ for sse and http alike:
       httpauth.Middleware(cfg.Auth) wraps the transport's http.Handler

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

1. Request arrives at the mux. Exactly two paths are served unauthenticated:
   `/.well-known/oauth-protected-resource` and its RFC 9728 path-suffixed form
   `/.well-known/oauth-protected-resource<endpoint-path>`. The match is
   **exact-string, never prefix** — a near-miss such as
   `/.well-known/oauth-protected-resource-x` or
   `/.well-known/oauth-protected-resource/../mcp` enters the middleware like
   any other path. A prefix match here would be an authorization bypass, so a
   negative test covers it.
2. Any other path enters the middleware. A missing or malformed
   `Authorization: Bearer <jwt>` header yields `401` with
   `WWW-Authenticate: Bearer resource_metadata="<public-url>/.well-known/oauth-protected-resource"`.
3. The validator verifies the JWT signature against a cached JWKS, then checks
   `iss` against the configured issuer, `aud` against the configured audience,
   and `exp`/`nbf` against the clock with a 60-second leeway. A `kid` absent
   from the cache triggers one rate-limited JWKS refetch before rejection.
   **An `exp` claim is mandatory**: a token without one is rejected, not
   treated as non-expiring. `golang-jwt/v5` validates `exp` only when the claim
   is present unless the parser is constructed with `jwt.WithExpirationRequired()`,
   so a provider or misconfiguration that mints an `exp`-less access token
   would otherwise produce an eternal credential and silently defeat both the
   S6 re-authorization flow and the stream bound above. The exact option name
   is confirmed against the library at implementation time; the requirement is
   the behavior, not the spelling.
4. Failures return `401` with an `error="invalid_token"` challenge. A valid
   token whose scopes miss `required_scopes` returns `403` with
   `error="insufficient_scope"`.
5. On success the request passes to the Streamable HTTP handler unchanged.

Four properties of this flow are requirements, not incidental:

- **Authorization is per-request, never per-session** — for request/response
  traffic. mcp-go's stateful mode issues an `Mcp-Session-Id`, but the
  middleware sits in front of every POST including those carrying an
  established session id. Possession of a session id is never itself a
  credential.
- **Long-lived streams are bounded by token expiry.** The above is *not*
  automatically true of the persistent `text/event-stream` connection opened by
  a GET: the middleware authorizes it once at establishment, and mcp-go then
  holds it open indefinitely with heartbeats, deliberately not revalidating —
  `handleGet` states outright that "the MCP specification doesn't require
  validating session ID for GET requests" (`server/streamable_http.go:586-588`).
  Without a bound, a token that has since expired keeps receiving on any stream
  opened before expiry. So in `oauth` mode the middleware derives a deadline
  from the token's `exp` and cancels the request context at that instant,
  closing the stream; the client reconnects and re-authorizes. In `bearer` mode,
  where there is no expiry, streams are bounded by `auth.max_stream_seconds`
  (default 3600) instead.
- **`aud` may be a string or an array.** The configured audience must be
  present in the array form too, and an `aud` that omits it is rejected. Without
  this, any token the same issuer minted for a different resource server would
  be accepted here.
- **Request bodies are bounded.** mcp-go applies no limit — `MaxBytesReader`
  appears nowhere in `server/streamable_http.go` — so the middleware wraps
  `r.Body` in `http.MaxBytesReader` with a configurable cap (default 4 MiB)
  before the handler runs.
- **Credentials never reach the logs.** No log line may contain an
  `Authorization` header value, a bearer token, or a raw JWT. Rejections log
  the reason class and the remote address only.
- **`Origin` is validated when present.** The MCP Streamable HTTP transport
  requires servers to check `Origin` as a DNS-rebinding defense. A request
  carrying an `Origin` that is neither the configured `--public-url` nor a
  loopback origin is rejected with `403`. A request with no `Origin` header —
  the normal shape for the server-to-server calls Claude and ChatGPT make — is
  unaffected.

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
  max_body_mb: int        # request body cap for network transports. Default: 4.
  max_stream_seconds: int # hard lifetime cap on a GET event stream. Default: 3600.
                          # In oauth mode the token's exp wins when it is sooner.
  oauth:
    issuer: string        # OIDC issuer URL. Required when mode is "oauth".
    audience: string      # Expected `aud` claim. Required when mode is "oauth".
    required_scopes: []   # Optional. Empty means any valid token is accepted.
    jwks_ttl_seconds: int # JWKS cache lifetime. Default: 900.
```

The bearer token is **never** a config field. It is read from
`COGVAULT_BEARER_TOKEN`, and `mode: bearer` with an unset or empty variable is
a startup error. Rationale: config files are routinely committed, and this
project's config already lives beside a git-tracked wiki.

Validation, following the existing `field: bad value; expected form` convention
in `internal/config`:

- `auth.mode` outside the accepted set → error.
- `auth.max_body_mb`, `auth.max_stream_seconds`, or
  `auth.oauth.jwks_ttl_seconds` zero or negative → error, matching the existing
  `max_file_size_mb` rejection style in `internal/config`.
- `mode: oauth` with an empty `issuer` → error.
- `issuer` that is not an absolute `https://` URL → error.
- `jwks_uri` from OIDC discovery that is not `https://` → error. The issuer is
  trusted config, but its discovery document is fetched content; an `http://`
  `jwks_uri` would let a network attacker supply signing keys and forge tokens.
- `--public-url` that is not an absolute `https://` URL, or that carries a
  trailing slash, query, or fragment → error. This value flows verbatim into
  both the `WWW-Authenticate` challenge and the PRM `resource`, so it gets the
  same rigor as `issuer`.
- `audience` explicitly set to something other than `<public-url><endpoint-path>`
  → error (see above).
- `mode: none` with a non-loopback `--addr` → startup error (S5).
- `mode: oauth` with no `--public-url` → startup error.
- `mode: bearer` with a `COGVAULT_BEARER_TOKEN` shorter than 32 bytes →
  startup error. The comparison is constant-time, but nothing else bounds an
  online guessing attack across a public tunnel, and this release ships no rate
  limiting (Open Decision D5).

### Protected Resource Metadata response

```json
{
  "resource": "<public-url><endpoint-path>",
  "authorization_servers": ["<issuer>"],
  "scopes_supported": ["<required_scopes...>"],
  "bearer_methods_supported": ["header"]
}
```

The `resource` value is built from `--endpoint-path`, not a hardcoded `/mcp`.

**The `resource` and `audience` values must be identical.** RFC 8707 clients
request a token for the advertised `resource`; the validator checks the
returned `aud`. If the two disagree, every token is rejected as wrong-audience
and the failure is indistinguishable from an expired token at the client.
`auth.oauth.audience` therefore **defaults to** `<public-url><endpoint-path>`,
and an explicitly configured value that differs from it is a startup error
rather than a silent runtime rejection. Keeping the audience resource-specific
is also the only defense against a confused deputy: a broad IdP-default
audience would let a token minted for an unrelated application authorize wiki
writes here.

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
only: a `map[kid]crypto.PublicKey` with a fetch timestamp, a TTL from
`auth.oauth.jwks_ttl_seconds` (default 900), and a minimum 60-second interval
between forced refetches so an attacker cannot drive unbounded outbound
requests with unknown `kid` values. Concurrent requests that miss the cache
collapse onto a single in-flight fetch via a `sync.Mutex`-guarded in-flight
marker — named explicitly because "at most once per interval" without a
primitive invites N concurrent refetches under a burst of unknown-`kid`
requests.

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
  - `SPEC.md` §1.2 says "MCP stdio server: six tools (read, write, list,
    search, scan, parse)". Both halves are now wrong — three transports, seven
    tools. Fixed here.
  - `CLAUDE.md` Working Context invariant 6 reads "Deletion stays unsafe
    without auto-commit, so there is no `wiki_delete`". The tool exists and
    auto-commits (`internal/mcp/tools.go:119`). This is a briefing that
    actively misleads any agent reading it, so it is corrected here rather
    than deferred — and the correction must state what the P0 risk analysis
    established: the auto-commit does not make the wiki recoverable.
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
  - a token with a valid signature, issuer, and audience but **no `exp` claim**
    returns `401` rather than being accepted as non-expiring
  - a GET stream authorized by a token expiring in N seconds is closed at N
    seconds, not held open; in `bearer` mode it closes at
    `auth.max_stream_seconds`
  - valid token missing a required scope returns `403` with
    `error="insufficient_scope"`
  - missing `Authorization` header returns `401` carrying a
    `WWW-Authenticate` header whose `resource_metadata` is the configured URL
  - a token whose `aud` is an array **not** containing the configured audience
    returns `401`; the array form containing it passes
  - bearer mode accepts the exact token and rejects near-misses; the comparison
    is constant-time
  - `none` mode passes every request through
  - JWKS refetch on unknown `kid` happens at most once per minimum interval
  - a body exceeding the configured cap is rejected rather than buffered
  - no rejection path writes the token or the `Authorization` header to the log
    sink (asserted against a captured `slog` handler)
- Protected Resource Metadata handler returns the documented JSON at both the
  bare and path-suffixed well-known paths, and stays reachable without a token.
  Its `resource` reflects a non-default `--endpoint-path`.
- Negative routing test: near-miss well-known paths
  (`/.well-known/oauth-protected-resource-x`,
  `/.well-known/oauth-protected-resource/../mcp`) are **not** served
  unauthenticated and return `401` under `mode: bearer`.
- `Origin` handling: a foreign `Origin` returns `403`; an absent `Origin`
  passes; the configured `--public-url` origin passes.
- Startup validation table test: short bearer token, `http://` public URL,
  public URL with a trailing slash, an `audience` that disagrees with the
  advertised `resource`, and a non-`https` `jwks_uri` each refuse to start.
- `cmd/cogvault` tests: `--transport http` starts and serves; an unknown
  transport value errors; the `mode: none` plus non-loopback address
  combination refuses to start **for both `http` and `sse`**; `mode: oauth`
  without `--public-url` refuses to start; `mode: bearer` with an unset
  `COGVAULT_BEARER_TOKEN` refuses to start.
- An SSE-specific test asserting the middleware is actually mounted on that
  transport — a request without a credential to the SSE endpoint under
  `mode: bearer` returns `401`. This is the regression test for the hole this
  design closes.
- `internal/mcp` test asserting every registered tool carries annotations and
  that exactly `wiki_write` and `wiki_delete` are non-read-only — this fails if
  a future tool is added without a deliberate annotation choice.
- Integration test in `internal/mcp/integration_test.go` style: a full
  initialize-then-`tools/call` round trip over Streamable HTTP through the
  middleware with a signed token.

## Risks

| Risk | Mitigation |
|---|---|
| **A leaked credential can destroy the wiki irrecoverably.** | Partially mitigated, and the residue is accepted rather than hidden. The S5 startup guard blocks the worst misconfiguration on both network transports, and the credential itself is the access boundary. But there is **no recovery path in cogvault**: `wiki_write` overwrites unconditionally and does not commit, and the cheapest total-destruction path for an attacker is overwriting every page rather than deleting it. The existing `gitAutoCommit` (`internal/mcp/tools.go:129`) fires **only** from `handleWikiDelete`, and nothing anywhere in `internal/` or `cmd/` commits on write or on ingest — so a delete-commit typically records the removal of content that was never tracked, recovering nothing. Backups are therefore an operator precondition, stated as such in the deployment section, not a property of this feature. See Open Decision D6. |
| Bearer tokens are guessable under sustained brute force, and the server has no rate limiting | The token is expected to be high-entropy and machine-generated; the deployment docs say so and give a generation command. Rate limiting is not implemented — recorded as Open Decision D5 rather than silently omitted. |
| Claude Code stores the bearer token as plaintext config in `~/.claude.json`, unlike OAuth client secrets which it puts in the keychain | Out of cogvault's control. Documented in the deployment section so the user chooses bearer mode knowingly; OAuth mode avoids it. |
| A long-lived event stream outliving its credential | The middleware authorizes a GET stream once at establishment and mcp-go never revalidates it, so the stream is bounded by the token's `exp` (or `auth.max_stream_seconds` in bearer mode) and closed at that instant. Residual: a stream can persist up to the leeway window past nominal expiry. Accepted. |
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
3. A server in `auth.mode: none` cannot be started on a non-loopback address,
   on either network transport.
   - **Measured by**: `go test ./cmd/cogvault/ -run TestServeBindGuard -v`,
     which covers `--transport http` and `--transport sse`.
4. The SSE transport is gated by the same authorization layer as HTTP.
   - **Measured by**: `go test ./cmd/cogvault/ -run TestSSERequiresAuth -v` — an
     uncredentialed request under `mode: bearer` returns `401`.
5. All seven MCP tools declare annotations, and exactly `wiki_write` and
   `wiki_delete` are non-read-only.
   - **Measured by**: `go test ./internal/mcp/ -run TestToolAnnotations -v`
6. The Protected Resource Metadata document is reachable without credentials and
   names the configured issuer.
   - **Measured by**: with a test server running,
     `curl -fsS "$PUBLIC_URL/.well-known/oauth-protected-resource" | jq -e '.authorization_servers[0]'`
     exits `0` and prints the configured issuer.
7. Existing deployments keep working: stdio is untouched, and SSE under the
   default `auth.mode: none` on a loopback address behaves exactly as before.
   (SSE behavior does change when authorization is enabled — that is criterion
   4, and it is the intended change, not a regression.)
   - **Measured by**: `go test ./...` passes with no edits to pre-existing stdio
     or SSE test expectations; any new SSE expectation is added under a new
     test name rather than by modifying an existing one.
8. Canonical documentation matches the shipped behavior, including the
   pre-existing `SPEC.md` drift items named in Integration.
   - **Measured by**: reviewer rubric — `SPEC.md` §8.1 names all three
     transports and the authorization model; §8 has a `wiki_delete` subsection;
     §1.3 no longer lists `wiki_delete`/auto-commit as out of scope; §8.5's
     `additionalProperties` note matches `internal/mcp/server.go`; §3.1 carries
     the `auth:` block; §9 carries the new flags; `DESIGN.md` has a component
     section for `internal/httpauth`; and the remaining stale §1.3 entries are
     recorded as a follow-up rather than left unnoticed.
9. A reader can stand the server up remotely from the documentation alone.
   - **Measured by**: reviewer rubric — the deployment section states the tunnel
     step, the `--public-url` requirement, the identity-provider prerequisites
     (JWT access tokens, issuer, audience), and the token-leak risk, with no
     step left as "configure appropriately."
10. The documentation states plainly that cogvault provides no recovery from a
    compromised credential, and names backups as an operator precondition.
    - **Measured by**: reviewer rubric — the deployment section says `wiki_write`
      overwrites without committing, says the `wiki_delete` auto-commit does not
      make the wiki recoverable, and gives a concrete backup instruction. A
      section that implies git makes the wiki recoverable fails this criterion.
11. Near-miss well-known paths are not an authorization bypass.
    - **Measured by**: `go test ./internal/httpauth/ -run TestWellKnownExactMatch -v`
12. No credential grants unbounded access: a token without `exp` is rejected,
    and an authorized event stream does not outlive its token.
    - **Measured by**: `go test ./internal/httpauth/ -run 'TestExpRequired|TestStreamDeadline' -v`

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
- **D5 — Rate limiting on authorization failures.** Not implemented in this
  release. Whether it is needed depends on whether the deployment sits at a
  stable public URL or behind a Tailscale-style private tunnel, which follows
  from the deployment choice. _Resolved by: user, after first deployment._
- **D6 — Whether cogvault should auto-commit on write, or otherwise provide a
  real recovery path.** The P0 risk analysis established that the wiki has no
  recovery from a compromised credential: `wiki_write` overwrites without
  committing, and nothing commits on ingest. Adding auto-commit on write is a
  behavior change well beyond a transport feature, and `docs/decisions/0021`
  treats deletion safety as a settled boundary, so it is not folded in here.
  This release ships the honest documentation instead. _Resolved by: user, as
  its own feature._
