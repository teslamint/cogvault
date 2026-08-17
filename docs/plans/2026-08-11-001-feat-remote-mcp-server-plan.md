---
schema: plan/v1
title: Remote MCP server — Streamable HTTP transport and OAuth 2.1 resource server
type: feat
status: done
date: 2026-08-11
execution: code
origin: docs/specs/2026-08-11-remote-mcp-server-design.md
body_seal: f426704f55e73e175b9e9b23fc2c52ec29f64feebd6d465bd2bb66f2e39876de
completed_by: ae5c39e9ee78f0e237c4be9f02e2d8ff8a2c7e8a
---

# Remote MCP server — Streamable HTTP transport and OAuth 2.1 resource server

## Goal

Make cogvault's seven MCP tools reachable from the Claude apps and ChatGPT by
adding a Streamable HTTP transport and an OAuth 2.1 resource-server
authorization layer, with the server still running on the user's Mac behind an
HTTPS tunnel. Close the unauthenticated-SSE hole the same layer exposes.

## Architecture notes

**The authorization layer is a plain `http.Handler` middleware in a new
`internal/httpauth` package with no MCP dependency.** Both transports it guards
are already `http.Handler`s — `server.StreamableHTTPServer.ServeHTTP`
(`streamable_http.go:246`) and `server.SSEServer.ServeHTTP` (`sse.go:783`) — so
guarding them is composition, not a fork. The package is testable with
`net/http/httptest` alone, which is why it holds no reference to
`internal/mcp`.

**cogvault is a resource server, never an authorization server.** It validates
tokens and publishes Protected Resource Metadata; a user-supplied identity
provider issues them. This is vendor-agnostic by construction: config carries
an OIDC `issuer` and an `audience`, and the JWKS location comes from the
issuer's `/.well-known/openid-configuration`.

**mcp-go supplies nothing server-side for OAuth.** Verified: zero matches for
`oauth|OAuth|WWW-Authenticate|protected-resource` under `server/` in
`mcp-go@v0.47.0`; all matches live under `client/`. Every piece of the resource
-server role is ours.

**JWT validation uses `github.com/golang-jwt/jwt/v5` (latest `v5.3.1`), a new
direct dependency.** Verified absent from `go.mod`, `go.sum`, and the full
`go list -m all` transitive graph, so adding it is a clean `go get`. Parser
construction is done once at startup and reused per request:

```
jwt.NewParser(
  jwt.WithValidMethods([]string{"RS256", "ES256"}),
  jwt.WithExpirationRequired(),
  jwt.WithIssuer(cfg.Issuer),
  jwt.WithAudience(cfg.Audience),
  jwt.WithLeeway(60 * time.Second),
)
```

Library semantics confirmed against the `v5.3.1` source, and they shape the
units:

- `WithExpirationRequired()` exists and is required. Without it the library
  validates `exp` only when the claim is present, so an `exp`-less token would
  be an eternal credential.
- `WithAudience(...)` is a **contains-any** check over the token's `aud`, which
  is exactly the spec's requirement. `aud` unmarshals into `ClaimStrings`
  (`[]string`) whether the JSON is a string or an array, so no branch is needed
  for the two shapes — but both still get a test, because the requirement is
  about behavior, not about which code path runs.
- Setting `WithAudience`/`WithIssuer` also makes those claims mandatory; a token
  missing either is rejected.
- Sentinels (`ErrTokenExpired`, `ErrTokenInvalidAudience`, …) work with
  `errors.Is` even when several validation failures are joined.

**`typ: at+jwt` (RFC 9068) is deliberately not enforced.** The library has no
option for it, and many identity providers do not emit the header, so requiring
it would break common setups. The defense it would provide — rejecting an ID
token presented as an access token — is already supplied by the audience check,
because an ID token's `aud` is the client ID while ours must equal the resource
URL. Recorded here so a reviewer does not read the absence as an oversight.

**JWKS handling is stdlib-only.** `golang-jwt/v5` ships no JWKS support
(confirmed by file listing at `v5.3.1`), and a second dependency is not worth
it for one document shape. JWK → `crypto.PublicKey` uses `encoding/json`,
`base64.RawURLEncoding` (JWK values are unpadded base64url), `math/big`,
`crypto/rsa`, and `crypto/elliptic` + `crypto/ecdsa`.

**Vendor behavior the deployment documentation must reflect** (re-verified live
at plan time, see Assumption Recheck):

- The PRM `resource` must match the server URL "exactly as the user enters it
  in Claude, including any path component" — which is why `resource` derives
  from `--endpoint-path` rather than a hardcoded `/mcp`.
- Claude probes `/.well-known/oauth-protected-resource/<mcp-path>` first, then
  the bare path, so both are served.
- A `WWW-Authenticate` header on a `200` is ignored; the `401` status is
  required.
- The authorization server itself must also be reachable from Anthropic's
  egress range `160.79.104.0/21`, not only the MCP server.

**Known Pattern — `docs/solutions/database-issues/sqlite-pool-pragma-and-busy-snapshot.md`.**
Serving does not write to the ingest ledger, so the single-writer lock and
`SQLITE_BUSY_SNAPSHOT` handling are untouched by this work. Recorded so a
reviewer does not expect concurrency changes.

**Config conventions this plan follows**, read from `internal/config/config.go`:
nested config is a named struct embedded by value (`LLMConfig` at line 19);
defaults are applied by `applyDefaults()` using zero-value checks (line 122,
`if c.MaxFileSizeMB == 0 { c.MaxFileSizeMB = 32 }`); validation lives in
`(*Config).validate()` (line 152) and every message follows
`field: problem; expected form` — for example
`"max_file_size_mb: must be positive; expected a value in megabytes"` (line
201) and `"adapter: %q not supported; use \"obsidian\" or \"markdown\""` (line
204).

**CLI conventions**, read from `cmd/cogvault/wire.go` and `cli_test.go`:
commands obtain dependencies from `bootstrap(configPath)`, which returns
`(*config.Config, *storage.FSStorage, *index.SQLiteIndex, adapter.Adapter, error)`;
tests drive cobra in-process through `executeCommand(args ...string) (stdout, stderr string, err error)`
and build fixtures with `testVault(t) (configPath, wikiDir, dbPath string)`.
Tests use the standard library only, with no assertion helper library.

## Assumption Recheck

The origin spec retains seven live assumptions. Every one was rerun at plan
time; all seven are `match`, with no contradictions and nothing unavailable.

| Approved claim | Fresh evidence | Outcome |
|---|---|---|
| mcp-go's Streamable HTTP server is an `http.Handler` | `grep -n` on `streamable_http.go` → `NewStreamableHTTPServer` line 212, `ServeHTTP` line 246 | match |
| mcp-go ships no server-side OAuth | `grep -rln "oauth\|OAuth\|WWW-Authenticate\|protected-resource"` under `server/` → 0 files | match |
| mcp-go negotiates only up to protocol `2025-11-25` | `sed -n '139p;142,148p' mcp/types.go` → `LATEST_PROTOCOL_VERSION = "2025-11-25"`, list `2025-06-18`, `2025-03-26`, `2024-11-05` | match |
| No MCP tool declares annotations today | `grep -c "ReadOnly\|Annotation\|Destructive\|Idempotent" internal/mcp/tools.go` → `0` | match |
| Claude apps cannot reach a loopback address | Live fetch of the Anthropic connector docs → firewall/VPN sentence unchanged; egress `160.79.104.0/21` restated verbatim under "Network reference" | match |
| ChatGPT cannot present a static credential | Live fetch of `developers.openai.com/plugins/build/auth` → "nor can it present custom API keys or customer-provided mTLS certificates" verbatim | match |
| `static_headers` is an organization-administrator beta | Live fetch → row reads "entered by an organization administrator", "shared by the organization rather than pasted per user", availability `Beta`; only `oauth_dcr` and `oauth_cimd` read "Supported out of the box" | match |

The Anthropic fetch additionally **strengthened** an approved requirement
rather than contradicting it: the PRM `resource` "must match your MCP server
URL exactly as the user enters it in Claude, including any path component."
That is vendor support for the spec's `resource`-from-`--endpoint-path` rule,
so no addendum is required.

## File structure

Create:

- `internal/httpauth/auth.go` — `Config`, `Middleware`, mode dispatch, bearer
  comparison, body cap, `Origin` check, challenge writers.
- `internal/httpauth/jwks.go` — JWKS cache, OIDC discovery, JWK decoding.
- `internal/httpauth/oauth.go` — parser construction and token validation.
- `internal/httpauth/metadata.go` — PRM document and its handler.
- `internal/httpauth/auth_test.go`, `jwks_test.go`, `oauth_test.go`,
  `metadata_test.go` — one test file per source file.
- `docs/deployment/remote-mcp.md` — tunnel, identity provider, and the
  no-recovery statement.

Modify:

- `internal/config/config.go` — `AuthConfig`, `OAuthConfig`, defaults,
  validation.
- `internal/config/config_test.go` — validation cases.
- `cmd/cogvault/serve.go` — `http` transport, middleware on both network
  transports, bind guard, stream deadline.
- `cmd/cogvault/cli_test.go` — startup-guard and transport cases.
- `internal/mcp/tools.go` — tool annotations.
- `internal/mcp/tools_test.go` — annotation assertions.
- `go.mod`, `go.sum` — `github.com/golang-jwt/jwt/v5`.
- `SPEC.md`, `DESIGN.md`, `CLAUDE.md`, `ROADMAP.md`, `README.md` — canon.

## Scenario coverage map

| S-ID | Unit chain | Scenario evidence |
|---|---|---|
| S1 — claude.ai app via OAuth | U1 → U3 → U4 → U5 → U6 | `internal/mcp/integration_test.go::TestStreamableHTTP` (`Covers S1`): full `initialize` then `tools/call` over Streamable HTTP through the middleware with a signed token, plus `internal/httpauth/oauth_test.go::TestOAuthRoundTrip` for the unauthenticated → `401` → PRM → token discovery leg |
| S2 — ChatGPT Developer Mode | U1 → U3 → U4 → U5 → U6 → U7 | Same round trip as S1 plus `internal/mcp/tools_test.go::TestToolAnnotations` (`Covers S2`), since ChatGPT's confirmation framing depends on `readOnlyHint` |
| S3 — Claude Code with a static token | U1 → U2 → U6 | `internal/httpauth/auth_test.go::TestBearerMode` (`Covers S3`): correct token passes, near-miss rejected |
| S4 — local stdio unchanged | U6 | `cmd/cogvault/cli_test.go::TestServeStdioUnchanged` (`Covers S4`): default transport still stdio, no middleware applied |
| S5 — misconfigured public server refuses to start | U1 → U6 | `cmd/cogvault/cli_test.go::TestServeBindGuard` (`Covers S5`): `mode: none` with a non-loopback address fails for both `http` and `sse` |
| S6 — expired token triggers re-authorization | U3 → U4 → U5 | `internal/httpauth/oauth_test.go::TestExpiredTokenChallenge` (`Covers S6`): expired token → `401` carrying a `WWW-Authenticate` challenge whose `resource_metadata` is the configured URL |

## Implementation Units

## U1: Config `auth:` block, defaults, and validation
Execution note: test-first
Files:
  Modify: internal/config/config.go
  Test: internal/config/config_test.go
Interfaces:
  Consumes: existing `(*Config).applyDefaults()` and `(*Config).validate()`
  Produces:
    `type AuthConfig struct { Mode string; MaxBodyMB int; MaxStreamSeconds int; OAuth OAuthConfig }`
    `type OAuthConfig struct { Issuer string; Audience string; RequiredScopes []string; JWKSTTLSeconds int }`
    field `Auth AuthConfig \`yaml:"auth"\`` on `Config`
Test scenarios:
  happy: a config with `auth.mode: oauth`, an `https://` issuer, and a public URL loads and validates
  edge: an omitted `auth:` block yields `mode: "none"`, `max_body_mb: 4`, `max_stream_seconds: 3600`, `jwks_ttl_seconds: 900`
  error: `mode: "sometimes"` is rejected; `mode: oauth` with an empty `issuer` is rejected; an `http://` issuer is rejected; zero or negative `max_body_mb`, `max_stream_seconds`, or `jwks_ttl_seconds` are each rejected
  integration: n/a — leaf unit
Steps:
  1. Write failing table-driven test `internal/config/config_test.go::TestAuthConfigValidation` covering every case above, asserting on the returned error string.
  2. Run `go test ./internal/config/`; confirm failure because `AuthConfig` does not exist.
  3. Add `AuthConfig` and `OAuthConfig` structs and the `Auth AuthConfig` field with yaml tags to `internal/config/config.go`.
  4. Extend `applyDefaults()` with zero-value checks in the existing style: empty `Auth.Mode` becomes `"none"`, zero `Auth.MaxBodyMB` becomes `4`, zero `Auth.MaxStreamSeconds` becomes `3600`, zero `Auth.OAuth.JWKSTTLSeconds` becomes `900`.
  5. Extend `validate()` with messages in the existing `field: problem; expected form` shape: `auth.mode: %q not supported; use "none", "bearer", or "oauth"`; `auth.oauth.issuer: must not be empty when auth.mode is "oauth"`; `auth.oauth.issuer: %q is not an absolute https:// URL; expected a scheme-qualified issuer URL`; `auth.max_body_mb: must be positive; expected a value in megabytes`; `auth.max_stream_seconds: must be positive; expected a value in seconds`; `auth.oauth.jwks_ttl_seconds: must be positive; expected a value in seconds`.
  6. Run `go test ./internal/config/`; confirm pass and no regressions.
  7. Commit: "feat(config): add auth block for the remote MCP transports"
Acceptance: `go test ./internal/config/ -run TestAuthConfigValidation -v` passes.

## U2: `internal/httpauth` middleware — none and bearer modes
Execution note: test-first
Files:
  Create: internal/httpauth/auth.go, internal/httpauth/auth_test.go
Interfaces:
  Consumes: `net/http`, `crypto/subtle`, `log/slog`
  Produces:
    `type Config struct { Mode string; BearerToken string; PublicURL string; EndpointPath string; MaxBodyBytes int64; MaxStreamSeconds int; Validator TokenValidator }`
    `type TokenValidator interface { Validate(ctx context.Context, token string) (expiresAt time.Time, err error) }`
    `func Middleware(cfg Config) func(http.Handler) http.Handler`
Test scenarios:
  happy: `mode: "none"` passes every request through untouched
  edge: a request body larger than `MaxBodyBytes` is rejected rather than buffered; an absent `Origin` header passes
  error: `mode: "bearer"` rejects a missing header, a wrong token, and a token that is a prefix or extension of the correct one, each with `401`; a foreign `Origin` yields `403`
  integration: no rejection path writes the token or the `Authorization` header value into a captured `slog` handler; a handler that blocks on `r.Context().Done()` is released after `MaxStreamSeconds`, proving the stream deadline is applied by the middleware rather than by a caller
Steps:
  1. Write failing tests in `internal/httpauth/auth_test.go` using `httptest.NewRecorder` and a sentinel next-handler that records whether it ran, covering every scenario above. For the logging test, install a `slog.New(slog.NewTextHandler(buf, nil))` and assert the token string never appears in `buf`.
  2. Run `go test ./internal/httpauth/`; confirm failure because the package does not exist.
  3. Create `internal/httpauth/auth.go` with `Config`, the `TokenValidator` interface, and `Middleware`. Compare bearer tokens with `subtle.ConstantTimeCompare` over equal-length byte slices, guarding length separately so the comparison cannot short-circuit on length alone. Wrap `r.Body` with `http.MaxBytesReader` before calling through. Reject an `Origin` that is neither `PublicURL` nor a loopback origin with `403`; treat an absent `Origin` as acceptable.
  4. Write the `401` challenge as `WWW-Authenticate: Bearer resource_metadata="<PublicURL>/.well-known/oauth-protected-resource"`, and log rejections with the reason class and remote address only.
  5. Apply the stream deadline inside the middleware, not in any caller: replace the request context with one whose deadline is `MaxStreamSeconds` from now, and — once a `Validator` is present — the earlier of that and the `expiresAt` the validator returned. The middleware is the only place that holds both values, which is why the deadline lives here.
  6. Run `go test ./internal/httpauth/`; confirm pass.
  7. Commit: "feat(httpauth): add authorization middleware with none and bearer modes"
Acceptance: `go test ./internal/httpauth/ -run 'TestNoneMode|TestBearerMode|TestBodyCap|TestOrigin|TestNoCredentialLogging|TestStreamDeadline' -v` passes.

## U3: JWKS cache with OIDC discovery
Execution note: test-first
Files:
  Create: internal/httpauth/jwks.go, internal/httpauth/jwks_test.go
Interfaces:
  Consumes: `encoding/json`, `encoding/base64`, `math/big`, `crypto/rsa`, `crypto/ecdsa`, `crypto/elliptic`, `net/http`, `sync`
  Produces:
    `type JWKSCache struct { ... }`
    `func NewJWKSCache(issuer string, ttl time.Duration, client *http.Client) *JWKSCache`
    `func (c *JWKSCache) KeyFor(ctx context.Context, kid string) (crypto.PublicKey, error)`
Test scenarios:
  happy: an RSA JWK served by a stub JWKS endpoint resolves to an `*rsa.PublicKey` whose modulus matches the generated key; an EC P-256 JWK resolves to an `*ecdsa.PublicKey`
  edge: a second `KeyFor` call within the TTL performs no additional HTTP request; an unknown `kid` triggers exactly one refetch and, when still unknown, returns an error
  error: a discovery document whose `jwks_uri` is `http://` is rejected without fetching it; malformed base64 in `n` returns an error rather than panicking
  integration: 50 concurrent `KeyFor` calls for an unknown `kid` produce exactly one JWKS fetch (assert on a request counter in the stub server) and the test passes under `-race`
Steps:
  1. Write failing tests in `internal/httpauth/jwks_test.go`. Generate an RSA key with `rsa.GenerateKey` and a P-256 key with `ecdsa.GenerateKey`, serialize their public parts as JWKs, and serve both a `/.well-known/openid-configuration` document and a JWKS document from one `httptest.Server`, counting requests to each.
  2. Run `go test ./internal/httpauth/`; confirm failure because `NewJWKSCache` does not exist.
  3. Create `internal/httpauth/jwks.go`. Fetch `<issuer>/.well-known/openid-configuration`, read `jwks_uri`, and reject it unless its scheme is `https`. Decode JWK members with `base64.RawURLEncoding` because JWK values are unpadded. Build `*rsa.PublicKey` from `n` and `e`, and `*ecdsa.PublicKey` from `crv`, `x`, and `y`, mapping `P-256`, `P-384`, and `P-521` to their `elliptic` curves and rejecting other curves.
  4. Hold keys in a `map[string]crypto.PublicKey` behind a `sync.Mutex`, with a fetch timestamp for the TTL, an in-flight marker so concurrent misses collapse onto one fetch, and a minimum 60-second interval between forced refetches.
  5. Run `go test ./internal/httpauth/ -race`; confirm pass.
  6. Commit: "feat(httpauth): add JWKS cache with OIDC discovery"
Acceptance: `go test ./internal/httpauth/ -run TestJWKS -race -v` passes and the concurrent case reports exactly one fetch.

## U4: OAuth token validator
Execution note: test-first
Files:
  Create: internal/httpauth/oauth.go, internal/httpauth/oauth_test.go
  Modify: go.mod, go.sum
Interfaces:
  Consumes: `github.com/golang-jwt/jwt/v5`, `(*JWKSCache).KeyFor`
  Produces:
    `type OAuthValidator struct { ... }`
    `func NewOAuthValidator(issuer, audience string, requiredScopes []string, keys *JWKSCache) *OAuthValidator`
    `func (v *OAuthValidator) Validate(ctx context.Context, token string) (time.Time, error)`
    `var ErrInsufficientScope = errors.New("insufficient scope")`
Test scenarios:
  happy: a token signed with the stub JWKS key, carrying the configured issuer and audience and a future `exp`, validates and returns that `exp`
  edge: a token whose `aud` is a JSON array containing the configured audience validates; a token whose `aud` is a bare string containing it also validates
  error: expired, not-yet-valid, wrong `aud`, `aud` array omitting the configured audience, wrong `iss`, bad signature, unknown `kid`, malformed JWT, `alg: none`, and **a token with no `exp` claim** each fail; a valid token missing a required scope fails with `ErrInsufficientScope`
  integration: `internal/httpauth/oauth_test.go::TestExpiredTokenChallenge` (`Covers S6`) drives the middleware from U2 with an expired token and asserts `401` plus a `WWW-Authenticate` header whose `resource_metadata` is the configured URL
Steps:
  1. Run `go get github.com/golang-jwt/jwt/v5@latest` and confirm `go.mod` records `v5.3.1` or newer.
  2. Write failing tests in `internal/httpauth/oauth_test.go`, minting tokens with the key generated in the U3-style stub so signatures verify.
  3. Run `go test ./internal/httpauth/`; confirm failure because `NewOAuthValidator` does not exist.
  4. Create `internal/httpauth/oauth.go`. Build the parser once in `NewOAuthValidator` with `jwt.WithValidMethods([]string{"RS256", "ES256"})`, `jwt.WithExpirationRequired()`, `jwt.WithIssuer(issuer)`, `jwt.WithAudience(audience)`, and `jwt.WithLeeway(60 * time.Second)`.
  5. Implement `Validate` by calling `parser.ParseWithClaims` with a `jwt.Keyfunc` that reads `t.Header["kid"]` as a string, returns an error when it is absent, and otherwise delegates to `KeyFor`. Return the claims' `ExpiresAt.Time` on success.
  6. Check `RequiredScopes` against the token's `scope` claim, splitting it on spaces, and return `ErrInsufficientScope` when any required scope is missing.
  7. Run `go test ./internal/httpauth/`; confirm pass.
  8. Commit: "feat(httpauth): add OAuth token validation with mandatory exp"
Acceptance: `go test ./internal/httpauth/ -run 'TestOAuth|TestExpRequired|TestExpiredTokenChallenge' -v` passes.

## U5: Protected Resource Metadata handler and exact-match routing
Execution note: test-first
Files:
  Create: internal/httpauth/metadata.go, internal/httpauth/metadata_test.go
  Modify: internal/httpauth/auth.go
Interfaces:
  Consumes: `net/http`, `encoding/json`
  Produces:
    `func MetadataHandler(cfg Config) http.Handler`
    `func Mount(cfg Config, mcp http.Handler) http.Handler`
Test scenarios:
  happy: `GET /.well-known/oauth-protected-resource` returns `200` with `resource` equal to `PublicURL + EndpointPath`, `authorization_servers` naming the configured issuer, and `bearer_methods_supported` of `["header"]`
  edge: the path-suffixed form `/.well-known/oauth-protected-resource<EndpointPath>` returns the same document; a non-default `EndpointPath` such as `/wiki` is reflected in `resource`
  error: `/.well-known/oauth-protected-resource-x` and `/.well-known/oauth-protected-resource/../mcp` are **not** served unauthenticated and return `401` under `mode: bearer`
  integration: `internal/httpauth/oauth_test.go::TestOAuthRoundTrip` (`Covers S1`, `Covers S2`) walks unauthenticated request → `401` with pointer → PRM fetch → signed token → success against the mounted handler
Steps:
  1. Write failing tests in `internal/httpauth/metadata_test.go` covering every scenario, asserting the near-miss paths reach the middleware rather than the metadata handler.
  2. Run `go test ./internal/httpauth/`; confirm failure because `MetadataHandler` does not exist.
  3. Create `internal/httpauth/metadata.go` with the PRM struct and its handler, marshalling `resource` as `cfg.PublicURL + cfg.EndpointPath`.
  4. Add `Mount` to `internal/httpauth/auth.go`: build an `http.ServeMux`-free dispatcher that compares `r.URL.Path` against exactly two strings — `/.well-known/oauth-protected-resource` and `/.well-known/oauth-protected-resource` + `cfg.EndpointPath` — and routes everything else through the middleware to the wrapped handler. Use exact string equality, never `strings.HasPrefix`, because a prefix match here is an authorization bypass.
  5. Run `go test ./internal/httpauth/`; confirm pass.
  6. Commit: "feat(httpauth): serve protected resource metadata with exact-match routing"
Acceptance: `go test ./internal/httpauth/ -run 'TestMetadata|TestWellKnownExactMatch|TestOAuthRoundTrip' -v` passes.

## U6: Wire the transports in `cogvault serve`
Execution note: test-first
Files:
  Modify: cmd/cogvault/serve.go
  Test: cmd/cogvault/cli_test.go, internal/mcp/integration_test.go
Interfaces:
  Consumes: `httpauth.Config`, `httpauth.Mount`, `httpauth.NewOAuthValidator`, `httpauth.NewJWKSCache`, `server.NewStreamableHTTPServer`, `server.NewSSEServer`, `bootstrap(configPath)`
  Produces: `cogvault serve --transport stdio|sse|http [--addr host:port] [--endpoint-path /mcp] [--public-url https://host]`
Test scenarios:
  happy: `--transport http` starts and answers an MCP initialize over Streamable HTTP
  edge: `--transport stdio` and an omitted `--transport` both keep current behavior with no middleware
  error: an unknown `--transport` value errors instead of silently starting stdio; `mode: none` with a non-loopback `--addr` refuses to start for both `http` and `sse`; `mode: oauth` without `--public-url` refuses to start; `mode: bearer` with an unset `COGVAULT_BEARER_TOKEN` refuses to start; a `COGVAULT_BEARER_TOKEN` shorter than 32 bytes refuses to start
  integration: `cmd/cogvault/cli_test.go::TestSSERequiresAuth` asserts an uncredentialed request to the SSE endpoint under `mode: bearer` returns `401`; `TestServeBindGuard` (`Covers S5`) and `TestServeStdioUnchanged` (`Covers S4`) cover the guard and the no-regression path
Steps:
  1. Write failing tests in `cmd/cogvault/cli_test.go` using the existing `executeCommand(args ...string)` helper and `testVault(t)` fixture, adding an `auth:` block to the generated config where a case needs one. Set and unset `COGVAULT_BEARER_TOKEN` with `t.Setenv`.
  2. Run `go test ./cmd/cogvault/`; confirm failure because `--transport http` is not recognized.
  3. Replace the `switch transport` block in `cmd/cogvault/serve.go` so `stdio`, `sse`, and `http` are explicit cases and any other value returns `fmt.Errorf("--transport: %q not supported; use \"stdio\", \"sse\", or \"http\"", transport)`.
  4. Add the `--endpoint-path` flag defaulting to `/mcp` and the `--public-url` flag defaulting to empty.
  5. Before starting either network transport, refuse to start when `cfg.Auth.Mode == "none"` and the host part of `--addr` is not a loopback address, when `cfg.Auth.Mode == "oauth"` and `--public-url` is empty, and when `cfg.Auth.Mode == "bearer"` and `COGVAULT_BEARER_TOKEN` is unset or shorter than 32 bytes. Also refuse when `--public-url` is set but is not an absolute `https://` URL, or carries a trailing slash, query, or fragment.
  6. Build an `httpauth.Config` from `cfg.Auth`, the flags, and the environment token. In `oauth` mode construct a `JWKSCache` and an `OAuthValidator` and assign it to `Validator`.
  7. Wrap both network transports with `httpauth.Mount(authCfg, transportHandler)` and serve the result with an `http.Server` on `--addr`. Pass `server.WithEndpointPath(endpointPath)` to the Streamable HTTP server so its route matches the advertised `resource`. The stream deadline needs no work here — the middleware applies it.
  8. Write `internal/mcp/integration_test.go::TestStreamableHTTP`, which the spec's success criterion 1 names by that path: build the MCP server with `NewServer`, wrap it with `httpauth.Mount` in `oauth` mode against a stub JWKS, and drive a full `initialize` then `tools/call` round trip with a signed token over Streamable HTTP. This import direction is safe because `internal/httpauth` does not import `internal/mcp`.
  9. Run `go test ./cmd/cogvault/ ./internal/mcp/`; confirm pass, then `go test ./...` for regressions.
  10. Commit: "feat(cli): add http transport and gate both network transports"
Acceptance: `go test ./cmd/cogvault/ -run 'TestServe' -v` and `go test ./internal/mcp/ -run TestStreamableHTTP -v` both pass, and `go test ./...` is green.

## U7: MCP tool annotations
Execution note: test-first
Files:
  Modify: internal/mcp/tools.go
  Test: internal/mcp/tools_test.go
Interfaces:
  Consumes: `mcp.NewTool`, `mcp.WithToolAnnotation` or the equivalent annotation option in `mcp-go@v0.47.0`
  Produces: all seven tool definitions carrying `readOnlyHint` and `destructiveHint`
Test scenarios:
  happy: `wiki_read`, `wiki_list`, `wiki_search`, `wiki_scan`, and `wiki_parse` each declare `readOnlyHint: true` and `destructiveHint: false`
  edge: `wiki_write` and `wiki_delete` each declare `readOnlyHint: false` and `destructiveHint: true`
  error: the test enumerates the registered tool set and fails when any tool carries no annotation, so a future tool cannot be added without a deliberate choice
  integration: `internal/mcp/tools_test.go::TestToolAnnotations` (`Covers S2`)
Steps:
  1. Read the annotation option's exact name and shape in `mcp-go@v0.47.0` before writing code, since the tool-construction API is the constraint here.
  2. Write failing test `internal/mcp/tools_test.go::TestToolAnnotations` asserting the table above over every registered tool.
  3. Run `go test ./internal/mcp/`; confirm failure because no annotations are set.
  4. Add annotations to each of the seven `mcp.NewTool` calls in `internal/mcp/tools.go`.
  5. Run `go test ./internal/mcp/`; confirm pass.
  6. Commit: "feat(mcp): annotate tools with readOnlyHint and destructiveHint"
Acceptance: `go test ./internal/mcp/ -run TestToolAnnotations -v` passes.

## U8: Canon and deployment documentation
Files:
  Create: docs/deployment/remote-mcp.md
  Modify: SPEC.md, DESIGN.md, CLAUDE.md, ROADMAP.md, README.md
Interfaces:
  Consumes: the shipped behavior from U1–U7
  Produces: canon that matches the implementation
Test scenarios:
  happy: n/a — documentation unit
  edge: n/a — documentation unit
  error: n/a — documentation unit
  integration: n/a — documentation unit; verified by the reviewer rubrics in the spec's success criteria 8, 9, and 10
Steps:
  1. Update `SPEC.md` §8.1 to name stdio, SSE, and Streamable HTTP, and to state the three authorization modes.
  2. Add a `wiki_delete` subsection to `SPEC.md` §8 documenting `path`, the `{status:"deleted", path}` result, and the git auto-commit side effect.
  3. Remove the `wiki_delete`/auto-commit entry from `SPEC.md` §1.3, and correct §1.2's "MCP stdio server: six tools" to name three transports and seven tools.
  4. Correct `SPEC.md` §8.5's `additionalProperties` note to match `noExtra` in `internal/mcp/server.go`.
  5. Add the `auth:` block to `SPEC.md` §3.1 and the new `serve` flags to §9.
  6. Add a component section for `internal/httpauth` to `DESIGN.md` §2, and update §2.8 and §2.9 for the new transport and wiring.
  7. Rewrite `CLAUDE.md` Working Context invariant 6, which currently claims `wiki_delete` does not exist, to state that it exists, auto-commits on delete, and that the auto-commit does not make the wiki recoverable because `wiki_write` overwrites without committing.
  8. Write `docs/deployment/remote-mcp.md` covering the tunnel step, the `--public-url` requirement, identity-provider prerequisites (JWT access tokens, issuer, audience equal to the advertised resource, and reachability from `160.79.104.0/21`), a `head -c 32 /dev/urandom | base64` style token-generation command for bearer mode, the plaintext storage of that token by Claude Code, and an explicit statement that cogvault provides no recovery from a compromised credential and that backups are the operator's responsibility.
  9. Add a remote-access row to `ROADMAP.md` under "Consume / tooling expansion" and link this feature's spec.
  10. Self-review every edited section against the spec's Integration list and success criteria 8, 9, and 10.
  11. Commit: "docs: document the remote MCP transport and its authorization model"
Acceptance: a reviewer confirms each item in spec success criteria 8, 9, and 10; `SPEC.md` contains no remaining claim that cogvault serves only stdio or only six tools.

## Mutation/failure-state matrix

No stateful ceremony in the deliverable; no mutation/failure-state matrix required.

The units produce Go source, tests, and documentation committed to a feature
branch. None crosses an outward-publication boundary: no unit pushes to a
remote, publishes to a registry, creates a release, or changes repository
visibility. Pushing the branch and opening the pull request belong to
`shipping`, not to this plan.

## Carry-forward trigger audit

`Audited ROADMAP.md and docs/research/v2-follow-ups.md at d6bd457: 1 open row, 0 fired, 0 unobservable.`

`docs/research/v2-follow-ups.md` holds ten rows, F1 through F10, all with status
`Done`. `ROADMAP.md:40` still lists F4 as an open Phase 1 follow-up, which is
the one apparently-open row. Its trigger is edit-based, naming SPEC's
renamed-file re-digest semantics under the `(path, hash)` key, which
`SPEC.md` §10.2 owns. This plan's file list touches `SPEC.md` §1.2, §1.3, §3.1,
§8, and §9 only, so the trigger does not fire. F4 is also latched `Done` in the
authoritative tracker (2026-08-03, PR #14) regardless of the stale ROADMAP row.

## Deferred to Follow-Up Work

- **Stale `SPEC.md` §1.3 entries beyond `wiki_delete`.** The Phase 1
  out-of-scope list still names vector search, ontology graph, `ResolveLink`,
  periodic `cogvault digest`, phone capture, URL/web extraction, and the local
  LLM backend, all of which `ROADMAP.md` records as delivered. Only the
  `wiki_delete`/auto-commit entry is corrected here, because documenting that
  tool in §8 would otherwise create a contradiction this change itself
  introduces. The rest is roadmap-clearance drift and is a documentation task
  of its own.
- **Stale `ROADMAP.md:40` F4 row.** Same class as above: the authoritative
  tracker marks F4 done, ROADMAP does not.
- **Auto-commit on write (spec Open Decision D6).** The user decided at the
  design gate that this stays a separate feature. This release documents the
  unrecoverability instead.
- **Rate limiting on repeated authorization failures (spec Open Decision D5).**
  Deferred pending the deployment shape; the 32-byte token floor is the
  interim defense.
- **RFC 7662 token introspection for opaque access tokens (spec Open
  Decision D2).** Only JWT access tokens are supported in this release.
- **Tool-result size capping (spec Open Decision D3).** Unmeasured; deferred
  until real usage shows whether client truncation bites.

## Open unknowns

**Planning-time — none.** Every fork the spec left open has a named owner and
none blocks implementation: D1 (which identity provider) blocks only end-to-end
verification of S1 and S2, not any unit; D2, D3, D5, and D6 are deferred
above; D4 is a one-line judgment inside U6.

**Implementation-time:**

- The exact annotation option name and shape in `mcp-go@v0.47.0` — U7 step 1
  reads it from the module before writing code rather than guessing here.
- Whether `server.WithEndpointPath` alone makes the Streamable HTTP server's
  route agree with the advertised `resource`, or whether the mount point needs
  adjusting too. U6 step 7 settles it against the running server.
- The precise `scope` claim shape the chosen identity provider emits — a
  space-delimited string is assumed in U4 step 6, and a provider emitting an
  array needs one extra branch there.
- Whether cancelling the request context is sufficient to close mcp-go's event
  stream promptly, or whether the response writer must also be closed. U6
  step 8 settles it with the stream-deadline test.
