# Remote MCP deployment

cogvault's MCP server can serve the wiki tools to the hosted Claude apps
(claude.ai, Desktop, mobile) and to ChatGPT Developer Mode, in addition to a
local `stdio` client. This document covers the `sse` and `http` (Streamable
HTTP) transports: how to expose them safely over the internet, and — because
a valid credential grants full read/write/delete access — what the server
does and does not protect you from.

See `SPEC.md` §8.1/§9.3 for the tool/CLI contract this implements and
`DESIGN.md` §2.10 for the `internal/httpauth` package that enforces it.

If you only ever run `cogvault serve` (stdio, the default) for a local MCP
client such as Claude Code on the same machine, none of this applies — stdio
has no network boundary.

## 1. What this is for

cogvault runs on hardware you control — your own machine, not a hosted
service. The Claude apps and ChatGPT connect to your server from vendor cloud
infrastructure, not from your browser or local network, so the server's
endpoint must be reachable over the public internet at an `https://` URL. A
`localhost`/loopback address only accepts connections from the same machine
and **cannot** work for a hosted client — that is the entire reason a tunnel
(§2) is necessary.

## 2. Tunnel setup

Expose the local listener (`--addr`, default `localhost:8080`) through an
`https://` tunnel. cogvault does not terminate TLS itself. Concrete options,
without endorsing one:

- [cloudflared](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/)
  (Cloudflare Tunnel)
- [Tailscale Funnel](https://tailscale.com/kb/1223/funnel)

Use a **stable, named tunnel**, not an ephemeral one-off URL. The advertised
`resource` value (§3) must stay fixed: a Protected Resource Metadata document,
or a client's already-configured connector, that points at a URL which
changes on every restart breaks every client that already connected.
cloudflared's quick/ephemeral tunnels generate a new random hostname on each
run — use a named tunnel instead.

## 3. `--public-url`

`cogvault serve` never infers its externally visible address — pass it
explicitly via `--public-url`, e.g. `--public-url https://cogvault.example.com`.
This is a **vendor requirement**, not a cogvault preference: the value must
**exactly** match the URL the user types into the Claude/ChatGPT connector
setup, byte for byte, including any path component. A trailing slash, a
different subdomain, or `http://` instead of `https://` produces a
working-looking server that no hosted client can actually reach or
authenticate against. `--public-url` must be an absolute `https://` URL with
no userinfo (`user:pass@host`), trailing slash, query, or fragment (a path
component, for a tunnel mounted under a subpath, is allowed as long as it
doesn't end in `/`).

**The advertised `resource`, the expected token `aud` (in `oauth` mode), and
the path the server actually answers on are all the same value**:
`<public-url><endpoint-path>` (`--endpoint-path` defaults to `/mcp`).

Endpoint shapes differ by transport:

- **`http`** serves only at the normalized `--endpoint-path`; any other path
  returns `404`.
- **`sse`** keeps mcp-go's fixed `/sse` and `/message` paths —
  `--endpoint-path` deliberately does not apply to it. The message endpoint a
  client receives is `https://<public-url>/message?sessionId=<id>` when
  `--public-url` is set, and `http://<addr>/message?sessionId=<id>`
  otherwise. **This applies in every auth mode, including `bearer`** — not
  only `oauth`, where `--public-url` is already a hard startup requirement
  (§6). A `bearer`-mode SSE deployment that sets `COGVAULT_BEARER_TOKEN` but
  omits `--public-url` starts without error, and the client's initial
  `GET /sse` connection appears to establish; it only fails on the first
  message `POST`, once the client tries to reach the loopback address it was
  handed. If a remote SSE client connects but every message silently fails,
  check `--public-url` first.
- **Protected Resource Metadata** (`oauth` mode) is served, unauthenticated,
  at `/.well-known/oauth-protected-resource` and at that path suffixed with
  `--endpoint-path`; Claude probes the suffixed form first.

**In `oauth` mode, `--transport http` is required — `--transport sse` refuses
to start** (§6): this is a limitation of the `oauth`+`sse` combination
specifically (not a general `sse` warning) — `sse`'s fixed `/sse` path can
never match the advertised `resource` (`<public-url><endpoint-path>`), so a
conformant client discovering the Protected Resource Metadata document
through the `WWW-Authenticate` challenge's `resource_metadata` pointer (RFC
9728 §3.3, which requires the returned `resource` to be identical to the URL
the client requested) is required to discard it, and the OAuth flow can never
complete. No amount of documentation works around a combination no conformant
client can complete.

**In `oauth` mode, prefer a `--public-url` with no path component** (e.g.
`https://cogvault.example.com`, not `https://cogvault.example.com/sub`).
RFC 9728 permits a client to *derive* the metadata URL itself, by inserting
`/.well-known/oauth-protected-resource` between the host and the full
resource path, instead of following the `resource_metadata` pointer in the
`401` challenge. For a subpath `--public-url`, that derivation lands on
`https://<host>/.well-known/oauth-protected-resource/sub/mcp` — a path
cogvault does not serve; it only serves the well-known path bare and suffixed
with `--endpoint-path` alone (not the public URL's own subpath), per the
bullet above. This is a **caveat, not a breakage**: clients that discover the
document through the `401` challenge's `resource_metadata` pointer — the route
the MCP specification defines — are unaffected. Do not count on the bare
well-known fallback here: a subpath deployment only works behind a
path-stripping proxy, and a probe to `https://<host>/.well-known/…` sits
outside the forwarded `/sub/*` prefix, so it never reaches cogvault. Use a
path-less `--public-url` in `oauth` mode unless you have a specific reason for
a subpath deployment.

## 4. Identity provider prerequisites (`oauth` mode)

`auth.mode: oauth` makes cogvault an OAuth 2.1 **resource server**: it
validates tokens, it does not issue them. The dotted names below are nested
keys in `~/.config/cogvault/config.yaml`; the minimum runnable form is:

```yaml
auth:
  mode: oauth
  oauth:
    issuer: https://issuer.example.com
```

Confirm all of the following before turning it on — cogvault cannot detect a
failure of these until the first real request:

- **The provider must issue JWT access tokens.** Opaque tokens are
  **unsupported in this release** — `internal/httpauth/oauth.go` parses and
  cryptographically verifies a JWT; there is no token-introspection fallback.
  If your provider issues opaque tokens, `oauth` mode will not work with it.
- **You supply the issuer URL** (`auth.oauth.issuer`): an absolute `https://`
  URL with no query, fragment, or userinfo component. cogvault discovers the
  JWKS endpoint from `<issuer>/.well-known/openid-configuration` and only
  ever follows an `https://` `jwks_uri`.
- **The audience must equal the advertised resource.** cogvault defaults
  `auth.oauth.audience` to `<public-url><endpoint-path>` automatically — leave
  it unset unless your provider needs the audience configured explicitly on
  its own side to match. A configured value that disagrees with the resource
  is a **startup error**, not a silent runtime mismatch.
- **The authorization server itself must be reachable from the vendor's
  egress range, not only your MCP server.** Anthropic documents its outbound
  connections as originating from `160.79.104.0/21`
  (`claude.com/docs/connectors/building/authentication`, "Network reference"
  section — this claim is sourced from the approved design spec's evidence
  table, not independently re-verified here). If your identity provider sits
  behind a firewall allowlist, that range needs to reach it too, in addition
  to reaching your `--public-url`.
- **Leaving `auth.oauth.required_scopes` unset — the documented default — is
  not a "safer default", it is a narrower check.** With no required scopes
  configured, authorization reduces to issuer plus audience: any token the
  provider issued for this resource is accepted, with no per-tool gate on
  `wiki_write` or `wiki_delete`. `oauth` mode is not inherently safer than
  `bearer` mode on the write/delete surface unless you also configure
  `required_scopes` and have your identity provider actually gate scope
  issuance.
- **The scope claim name is hardcoded to `scope`** (a space-delimited string
  or a JSON array, both accepted). If your identity provider emits scopes
  under a different claim — Azure AD uses `scp` or `roles`, not `scope` — set
  `required_scopes` and every token will be rejected as
  `error="insufficient_scope"`: fail-closed, but a silent lockout with no
  runtime hint pointing at the claim name. Confirm your provider's scope
  claim name before setting `required_scopes` against it.
- **The signing-algorithm allowlist is `RS256` and `ES256` only,
  deliberately.** This is a hardening choice against algorithm-confusion
  attacks, not an oversight, and it is not planned to widen: neither OAuth
  2.1 nor RFC 9068 mandates a specific algorithm, so a conformant provider
  signing with `PS256` or `EdDSA` will have every token rejected. Confirm
  your provider signs with `RS256` or `ES256` before enabling `oauth` mode.
- **Immediately after an identity-provider key rotation, a token carrying the
  new `kid` can be rejected for up to 60 seconds**, even though the provider
  has already published the new key. `KeyFor`'s forced-refetch floor
  (`minForcedRefetchInterval`, `internal/httpauth/jwks.go`) bounds how often
  an unseen `kid` can trigger a fresh JWKS fetch, so a burst of tokens
  carrying the new key can still hit the stale cache for up to a minute after
  rotation. This is intentional — a hammering guard, not a bug — but expect a
  short window of `401`s across a rotation.
- **Size `auth.oauth.jwks_ttl_seconds` (default 900) against how long your
  identity provider can be down.** The freshness gate applies to the whole
  cache, not per-key: once the TTL lapses during a provider outage, *every*
  `oauth` token starts failing, not only ones carrying a `kid` the cache
  hasn't seen. It self-heals as soon as the provider answers again, but a
  short TTL turns a brief provider outage into an availability incident for
  every client. Raise `jwks_ttl_seconds` if your provider's availability
  history warrants it; there is no mechanism here that serves a stale key
  past its TTL.

## 5. `bearer` mode

For Claude Code and other local/single-operator use, `auth.mode: bearer` is
simpler than running an identity provider: a single static shared secret.

Select the mode in `~/.config/cogvault/config.yaml`. The key is nested, and the
server refuses to start with `--public-url` under the default `auth.mode: none`
(see §6), so this edit is required, not optional:

```yaml
auth:
  mode: bearer
```

Generate a high-entropy token and export it as `COGVAULT_BEARER_TOKEN` before
starting the server — cogvault reads it **only** from that environment
variable, never from a flag or the config file, and requires at least 32 raw
bytes:

```bash
export COGVAULT_BEARER_TOKEN=$(head -c 32 /dev/urandom | base64)
cogvault serve --transport http --public-url https://cogvault.example.com
```

Configure the client with an `Authorization: Bearer <token>` header carrying
the same value.

Be aware of where the client stores it: **Claude Code stores a configured
bearer header value as ordinary configuration text in `~/.claude.json`** —
plaintext on disk — unlike OAuth client secrets, which Claude Code places in
the system keychain. That storage behavior is outside cogvault's control (this
claim describes Claude Code's own behavior, not cogvault's, and is not
verifiable against this repository's code). Choose `bearer` mode knowing this;
`oauth` mode avoids it.

## 6. What the server refuses to start on

`cogvault serve` fails fast, before it ever listens, on any of the following
(`cmd/cogvault/serve.go`, `buildServeHandler` and its helpers):

| Condition | Why |
|---|---|
| `auth.mode: none` and `--addr` is not a loopback address (`localhost`, `127.0.0.1`, `::1` — matched as literal strings, not resolved through DNS) | `none` mode applies no credential check, so it may only *bind* where only the local machine can reach it — a guarantee about the bind address, not about what else forwards traffic to it. See the warning below the table. |
| `auth.mode: none` and `--public-url` is set | A public URL has no legitimate function in `none` mode; its presence signals unauthenticated tunnel-exposure intent the code cannot see the tunnel for, but can see the contradictory config for. |
| `auth.mode: oauth` and `--public-url` is unset | The audience/resource cannot be computed. |
| `auth.mode: oauth` and `--transport sse` | `sse`'s fixed `/sse`/`/message` paths can never match the advertised `resource`; no conformant OAuth client can complete the flow (§3, RFC 9728 §3.3). |
| `--public-url` is set but not an absolute `https://` URL, or carries userinfo, a trailing slash, a query, or a fragment | Guards the exact-match requirement in §3; userinfo would otherwise leak into the advertised resource, the expected token `aud`, and the `WWW-Authenticate` challenge. |
| `auth.mode: bearer` and `COGVAULT_BEARER_TOKEN` is unset or under 32 bytes | Refuses a missing or weak credential. |
| `auth.oauth.audience` is set and disagrees with `<public-url><endpoint-path>` | A confused-deputy misconfiguration is caught at startup instead of silently rejecting every token later. |
| `--endpoint-path` normalizes to empty (e.g. `/` or `""`) | There would be no meaningful path to serve or advertise. |
| `--transport` is not one of `stdio`, `sse`, `http` | Fails closed on a typo rather than falling through. |

An error here means the server never bound a socket — there is nothing
running to shut down.

**A loopback bind is not a security boundary once something is forwarding
traffic to it.** The `auth.mode: none` guard above only proves the process
bound a loopback address; it cannot see, and does not guard against, a
tunnel (§2) pointed at that same loopback address from another terminal. The
`none`+`--public-url` guard above catches the common mistake of pairing
`none` mode with `--public-url`, but a tunnel needs no cogvault flag at all.
`--public-url` controls what the metadata document advertises, what the
`WWW-Authenticate` challenges point at, and which non-loopback `Origin` is
accepted; none of those decide whether the listener answers a request that
arrives over a tunnel. If you tunnel a `none`-mode server (§2, §3), you have built
an unauthenticated `wiki_write`/`wiki_delete` endpoint open to anyone who
finds the tunnel URL, guard or no guard. `auth.mode: none` — the default — is
for `stdio`-equivalent local/loopback use only; use `bearer` or `oauth`
(§4/§5) for anything reachable through a tunnel.

**Troubleshooting note**, and it differs by mode. In `oauth` mode a `401
Unauthorized` on every request can mean the credential is wrong, but it can
*also* mean `--endpoint-path` is wrong: the expected audience is
`<public-url><endpoint-path>`, so serving a different path than the one the
token was issued for rejects an otherwise valid token. In `bearer` mode the
credential check is a constant-time comparison with no path dependency, so a
correct token sent to the wrong path passes the middleware and reaches
cogvault's path-matching wrapper — that is a `404`, not a `401`. Persistent
`401`s in `bearer` mode mean the token, not the path.

## 7. Security posture

Read this before exposing the server to the internet.

A valid credential — a matching bearer token, or a valid `oauth` token
carrying the required scopes — grants the **full tool set**, including
`wiki_write` and `wiki_delete`. There is no read-only credential tier and no
per-tool scope split narrower than whatever scopes you configure in `oauth`
mode.

**cogvault provides no recovery from a compromised credential by default.**
With the default `git.auto_commit: off` (SPEC §3.1):

- `wiki_write` overwrites the target file unconditionally and does **not**
  commit to git.
- Nothing commits to git on write, and nothing commits on `cogvault ingest`
  either.
- `wiki_delete` does auto-commit its own deletion — but because nothing else
  ever committed, that commit typically records the removal of content that
  was **never tracked** in the first place. It does not hand you a prior
  version to restore.

In short: with `git.auto_commit: off`, git inside this wiki is not a safety
net for anything `wiki_write` touched, and it is only incidentally a record —
never a restore path — for what `wiki_delete` removed. An attacker (or a bug,
or a mistake) holding a valid credential can silently overwrite or delete the
entire wiki, and cogvault has no built-in way to get it back.

**`git.auto_commit: write` or `write+ingest` (0024) narrows this, but is not
a substitute for backups.** Setting `git.auto_commit: write` commits every
successful `wiki_write` (`git add` + `git commit -m "wiki: write <path>"`);
`write+ingest` additionally commits the whole tree once after a `cogvault
ingest` run that digested at least one file (`git add -A -- .` + `git commit -m
"wiki: ingest snapshot"`). Either mode gives you real prior-version history
for the operations it covers — a compromised credential that only calls
`wiki_write`/`wiki_delete` (or an ingest run) now leaves recoverable commits
behind. It does **not** protect against:

- the same credential also running `git` itself (`rm -rf .git`, a forced
  push, history rewriting) — same-repo history is attacker-writable by
  anything that can reach the filesystem or a shell;
- a commit subprocess that fails silently (logged, not surfaced as a tool or
  command error — check the server's logs, not just tool responses, if you
  are relying on this);
- anything written directly to disk outside cogvault's own write path.

Off-repo snapshots (below) remain the real safety net regardless of this
setting.

**Backups are the operator's responsibility regardless of `git.auto_commit`.**
Back up `wiki_dir` outside of cogvault, on a schedule independent of
cogvault's own git activity (whichever mode you run) — for example:

```bash
# Option A: commit the whole tree yourself, on a schedule (cron/launchd).
# Redundant with git.auto_commit: write+ingest, but still worth doing if
# you run write-only or off mode, or want commits between ingest runs.
git -C /path/to/wiki_dir add -A && git -C /path/to/wiki_dir commit -m "snapshot $(date -u +%FT%TZ)"

# Option B: an independent copy (swap in your OS's snapshot/backup tool).
rsync -a --delete /path/to/wiki_dir/ /path/to/backup/wiki_dir-$(date +%F)/
```

Put one of these on a schedule before exposing `wiki_write`/`wiki_delete` to
any credential you would not trust with root on this machine.

## 8. Known limits

- **No rate limiting** on repeated authorization failures in this release. A
  bearer token or JWT is checked in constant time per request, but nothing
  throttles or locks out a client that fails repeatedly. If sustained
  brute-force attempts are a concern for your deployment, add throttling at
  the tunnel or network layer — cogvault does not provide it.
- **Opaque access tokens are unsupported.** `oauth` mode requires a JWT
  access token; see §4.

`auth.max_stream_seconds` (and, in `oauth` mode, an earlier token expiry) is
enforced as a **socket-level** read/write deadline (`http.ResponseController`
in `internal/httpauth`), not only a request-context cancellation a blocked
`Read`/`Write` can't observe. A client trickling a request body a few bytes
at a time, or one that opens a stream and stops reading it, is cut off at the
deadline like any other connection — this is what lets `WriteTimeout` stay
unset on the underlying `*http.Server` (`cmd/cogvault/serve.go`) without
leaving those two cases unbounded.
