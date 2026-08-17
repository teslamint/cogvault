---
module: remote MCP server
date: "2026-08-17"
problem_type: security_issue
component: deployment_security_boundary
severity: critical
symptoms:
  - "auth.mode: none passes the loopback bind guard while an external tunnel forwards public traffic to the listener"
  - "The tunnel URL exposes unauthenticated wiki_write and wiki_delete operations even though the server binds only to localhost"
root_cause: "security boundary validated at the local bind layer without testing the composed deployment path through an external tunnel"
resolution_type: code_and_documentation_fix
related_components:
  - cmd/cogvault
  - docs/deployment/remote-mcp.md
  - external_https_tunnel
tags:
  - remote-mcp
  - authentication
  - loopback
  - tunnel
  - threat-flow
  - defense-in-depth
---

# Loopback bind guards do not protect tunneled endpoints

## Problem

A component-level safety check can be correct and still disappear at the
deployment boundary. A loopback bind guard proves only that a service does not
accept direct connections on non-loopback interfaces. A reverse proxy, tunnel,
port forward, or sidecar can still accept public traffic and relay it to the
allowed loopback socket.

PR #22 exposed this composition failure in cogvault. `auth.mode: none`
correctly rejected non-loopback binds, while the deployment guide told operators
to expose `localhost:8080` through an HTTPS tunnel. Read together, those
instructions created a public unauthenticated path to all MCP tools, including
`wiki_write` and `wiki_delete`.

## Symptoms

- Bind-guard tests pass, yet the documented deployment is publicly reachable.
- A public forwarder targets a loopback listener without changing the server's
  command line or setting an application-visible public flag.
- Process startup and health checks succeed while authentication remains off.

## What did not work

- Treating loopback as an end-to-end reachability boundary.
- Reviewing the startup guard and tunnel instructions independently.
- Adding a `none` plus `--public-url` rejection and treating it as complete:
  an external tunnel does not need to set `--public-url` at all.
- Relying on unit tests that never assemble the operator's full runnable path.

## Solution

Review and test deployment instructions as one executable security path:

1. Draw the complete route: untrusted client → public ingress → tunnel or
   proxy → local listener → authentication layer → privileged operation.
2. State the guarantee at each hop. "Binds only to loopback" is an
   interface-selection guarantee, not an authentication or reachability
   guarantee.
3. Execute the published setup from a clean environment with its real defaults,
   including the separate forwarding process.
4. Probe the assembled deployment anonymously against disposable data. The
   request must be rejected before a privileged handler runs.
5. Make secure ordering explicit: configure authentication, start the
   authenticated service, expose it, then verify both rejection and authorized
   paths.
6. Re-run the composed threat-flow test whenever guards, defaults, quickstarts,
   proxy examples, or tunnel instructions change.

For cogvault, commit `cc849b5` corrected the deployment guide to say that the
guard proves only the bind address and changed the quickstart to configure
bearer mode before exposure. The `none` plus `--public-url` startup rejection
remains useful defense in depth, but the documentation explicitly says it
cannot see a tunnel launched by another process.

## Why this works

Security properties are not automatically preserved by composition. Testing
the entire request route evaluates the state an operator actually creates,
rather than the local invariant each component claims in isolation. An
anonymous privileged-operation probe also distinguishes a real authorization
boundary from a server that merely starts successfully.

## Prevention

- Use a threat-equivalent local forwarder in CI when a hosted tunnel would be
  flaky.
- Keep a staging smoke test using the real tunnel or proxy when vendor-specific
  behavior matters.
- Separate local protocol evidence from hosted-client interoperability evidence.
- For mutable services, include a harmless sentinel invocation of the most
  privileged operation against disposable state.

This check applies to local databases behind SSH tunnels, development servers
behind share links, loopback admin consoles behind reverse proxies, container
ports published by orchestrators, and webhook receivers exposed through
forwarding tools.

## Evidence

- [Remote MCP deployment guide](../../deployment/remote-mcp.md)
- [PR #22 retrospective](../../retros/2026-08-17-remote-mcp-server-retro.md)
- `cmd/cogvault/serve.go` startup guards
- `TestServeBindGuard` and `TestServeNoneRejectsPublicURL`, which remain
  necessary but do not substitute for a composed forwarding-path test
