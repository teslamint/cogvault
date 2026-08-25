package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"github.com/mark3labs/mcp-go/util"
	"github.com/spf13/cobra"
	"github.com/teslamint/cogvault/internal/config"
	"github.com/teslamint/cogvault/internal/httpauth"
	cogmcp "github.com/teslamint/cogvault/internal/mcp"
)

// bearerTokenEnvVar names the environment variable "bearer" mode reads its
// credential from. It is never read from config or a flag: an operator
// putting the token in a config file or shell history defeats the point of
// keeping it out of both.
const bearerTokenEnvVar = "COGVAULT_BEARER_TOKEN"

// minBearerTokenBytes is the minimum length, in raw bytes of the environment
// variable value (not runes, not after any decoding), "bearer" mode accepts.
// A shorter value is rejected at startup rather than accepted as a weak
// credential.
const minBearerTokenBytes = 32

// readHeaderTimeout bounds how long the http.Server will wait for a client
// to finish sending request headers. The httpauth middleware's stream
// deadline only starts once the handler runs, i.e. after headers are fully
// parsed, so nothing else bounds this phase. Without it, a slowloris-style
// client dribbling headers at this deliberately public endpoint can hold a
// connection and goroutine open indefinitely.
const readHeaderTimeout = 10 * time.Second

// idleTimeout closes keep-alive connections that sit idle between requests,
// bounding the same class of resource exhaustion as readHeaderTimeout for
// connections that did complete a request.
const idleTimeout = 2 * time.Minute

func newServeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the MCP server (stdio, sse, or http mode)",
		RunE:  runServe,
	}
	cmd.Flags().String("transport", "stdio", "transport mode: stdio, sse, or http")
	cmd.Flags().String("addr", "localhost:8080", "listen address for the sse and http transports")
	cmd.Flags().String("endpoint-path", "/mcp", "MCP endpoint path for the http transport")
	cmd.Flags().String("public-url", "", "externally visible base URL (required in oauth mode; also used for SSE message endpoints and Origin checks)")
	return cmd
}

func runServe(cmd *cobra.Command, args []string) error {
	configPath, err := resolveConfigPath(cmd)
	if err != nil {
		return err
	}

	cfg, store, idx, adpt, err := bootstrap(configPath)
	if err != nil {
		return err
	}
	defer idx.Close()

	_, _, _, ccErr := idx.CheckConsistency(store, adpt, true)
	if err := handleConsistencyResult(cmd, ccErr); err != nil {
		return err
	}

	mcpSrv := cogmcp.NewServer(cfg.WikiDir, cfg, store, idx, adpt)

	transport, _ := cmd.Flags().GetString("transport")
	addr, _ := cmd.Flags().GetString("addr")
	endpointPath, _ := cmd.Flags().GetString("endpoint-path")
	publicURL, _ := cmd.Flags().GetString("public-url")

	switch transport {
	case "stdio":
		return server.ServeStdio(mcpSrv)
	case "sse", "http":
		handler, err := buildServeHandler(cfg, mcpSrv, serveFlags{
			transport:    transport,
			addr:         addr,
			endpointPath: endpointPath,
			publicURL:    publicURL,
		})
		if err != nil {
			return err
		}
		cmd.Printf("%s server listening on %s\n", transport, addr)
		return serveUntilSignal(cmd.Context(), addr, handler)
	default:
		return fmt.Errorf("--transport: %q not supported; use \"stdio\", \"sse\", or \"http\"", transport)
	}
}

// serveUntilSignal runs the HTTP server until SIGINT or SIGTERM (or ctx is
// canceled), then drains: in-flight requests get the shutdown grace period
// before the process exits. Without it ListenAndServe dies with the process
// on the first signal, cutting active SSE/Streamable HTTP streams
// mid-response. The listener is created up front so a bind failure surfaces
// before the signal handling starts, and tests can inject their own via
// serveListener.
func serveUntilSignal(ctx context.Context, addr string, handler http.Handler) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	return serveListener(ctx, ln, handler)
}

// serveListener is the testable core of serveUntilSignal: it serves on the
// given listener until ctx is canceled or a signal arrives, then shuts the
// server down with a grace period.
func serveListener(ctx context.Context, ln net.Listener, handler http.Handler) error {
	srv := newHTTPServer(ln.Addr().String(), handler)
	signalCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-signalCtx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	}
}

// requireAddrHost rejects an --addr with an empty host part (":8080"), which
// binds every interface. Operators must name the interface explicitly — the
// loopback guard already rejects such addrs in "none" auth mode, but bearer
// mode would otherwise accept and silently expose the port on all
// interfaces, and sseBaseURL would build the malformed "http://:8080".
func requireAddrHost(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("addr: %q is not a valid host:port; expected a value like \"host:port\"", addr)
	}
	if host == "" {
		return fmt.Errorf("addr: %q has no host; name an interface explicitly (e.g. \"localhost:8080\")", addr)
	}
	return nil
}

// newHTTPServer constructs the *http.Server used for the "sse" and "http"
// transports. WriteTimeout is deliberately left unset: it applies to the
// whole connection from the moment headers are read, so it would cut off
// the long-lived SSE and Streamable HTTP event streams these transports
// serve, which can legitimately stay open far longer than a single
// request-response round trip. The per-stream bound that these transports
// need instead lives in the httpauth middleware, which sets a per-request
// socket read/write deadline (via http.ResponseController, after headers
// are already parsed) computed from auth.max_stream_seconds and, in oauth
// mode, the earlier token expiry — enforceable at the socket level like
// WriteTimeout, but scoped to one request instead of severing every stream
// on the connection.
func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}
}

// serveFlags carries the resolved --addr, --endpoint-path, and --public-url
// flag values, plus the transport they apply to, into buildServeHandler.
type serveFlags struct {
	transport    string
	addr         string
	endpointPath string
	publicURL    string

	// mcpLogger, when non-nil, is handed to the streamable HTTP transport.
	// The session sweeper is otherwise silent and keeps its state in
	// unexported maps, so its log line is the only externally observable
	// proof that it runs; the sweeper test sets this.
	mcpLogger util.Logger
}

// sessionIdleTTLFor derives the streamable HTTP session sweeper's idle TTL
// from the configured stream bound.
//
// Without a positive TTL mcp-go never starts the sweeper (v0.47.0,
// streamable_http.go: the sweeper is gated on sessionIdleTTL > 0, and zero is
// the default), so a client that disconnects without sending DELETE leaves its
// per-session transport state registered for the process lifetime. Remote
// clients reached through a tunnel disconnect that way as a matter of course.
//
// The TTL must exceed the longest a live session can go untouched, or the
// sweeper would evict a connected client. A session is touched when a request
// arrives and when an event is written to a listen stream; cogvault emits no
// server-initiated notifications, so a listen stream is touched exactly once,
// at establishment, and then runs until the middleware's stream deadline. Twice
// that bound clears the longest such stream with margin.
//
// Eviction is not visible to a client that comes back: mcp-go validates a
// session ID by format rather than existence and creates an ephemeral session
// when it finds no registered one, and cogvault registers no session-scoped
// tools or resources for the sweep to discard.
func sessionIdleTTLFor(maxStreamSeconds int) time.Duration {
	return 2 * time.Duration(maxStreamSeconds) * time.Second
}

// buildServeHandler composes the full HTTP handler for the "sse" or "http"
// transport: the startup guards that make an unusable or dangerous
// configuration fail loudly instead of quietly serving, the httpauth.Config
// construction (including OAuth validator and JWKS cache wiring), the
// exact-path wrapper the http transport needs, and httpauth.Mount.
//
// httpauth.Mount panics on a config it cannot use by design (see
// httpauth.Middleware's doc comment): calling it here, once, before the
// caller ever starts listening, keeps that panic a startup-time backstop
// rather than a per-request crash on a public endpoint. The guards below
// exist so an operator sees an actionable error instead of that panic in
// the first place.
func buildServeHandler(cfg *config.Config, mcpSrv *server.MCPServer, f serveFlags) (http.Handler, error) {
	if err := requireAddrHost(f.addr); err != nil {
		return nil, err
	}
	loopback, err := isLoopbackAddr(f.addr)
	if err != nil {
		return nil, err
	}
	if cfg.Auth.Mode == "none" && !loopback {
		return nil, fmt.Errorf("addr: %q is not a loopback address; expected a loopback address (or set auth.mode to \"bearer\" or \"oauth\") when auth.mode is \"none\"", f.addr)
	}
	if cfg.Auth.Mode == "none" && f.publicURL != "" {
		return nil, fmt.Errorf("public-url: %q must not be set when auth.mode is \"none\"; expected no public URL (a public URL has no function in \"none\" mode and signals unauthenticated tunnel exposure; set auth.mode to \"bearer\" or \"oauth\" instead)", f.publicURL)
	}
	if cfg.Auth.Mode == "oauth" && f.transport == "sse" {
		return nil, fmt.Errorf("transport: %q is not supported when auth.mode is \"oauth\"; expected \"http\" (the sse transport serves fixed /sse and /message paths, so its protected resource metadata can never advertise the exact URL a conformant OAuth client requested, per RFC 9728 §3.3)", f.transport)
	}

	var publicURL string
	if f.publicURL != "" {
		u, err := validatePublicURL(f.publicURL)
		if err != nil {
			return nil, err
		}
		publicURL = u.String()
	}
	if cfg.Auth.Mode == "oauth" && publicURL == "" {
		return nil, fmt.Errorf("public-url: must be set when auth.mode is \"oauth\"; expected an absolute https:// URL")
	}

	var bearerToken string
	if cfg.Auth.Mode == "bearer" {
		bearerToken = os.Getenv(bearerTokenEnvVar)
		if len(bearerToken) < minBearerTokenBytes {
			return nil, fmt.Errorf("%s: must be set to at least %d bytes when auth.mode is \"bearer\"", bearerTokenEnvVar, minBearerTokenBytes)
		}
	}

	endpointPath, err := normalizeEndpointPath(f.endpointPath)
	if err != nil {
		return nil, err
	}

	authCfg := httpauth.Config{
		Mode:             cfg.Auth.Mode,
		BearerToken:      bearerToken,
		PublicURL:        publicURL,
		EndpointPath:     endpointPath,
		MaxBodyBytes:     int64(cfg.Auth.MaxBodyMB) * 1024 * 1024,
		MaxStreamSeconds: cfg.Auth.MaxStreamSeconds,
	}

	if cfg.Auth.Mode == "oauth" {
		resource := publicURL + endpointPath
		audience, err := resolveAudience(cfg.Auth.OAuth.Audience, resource)
		if err != nil {
			return nil, err
		}

		// NewJWKSCache detaches its fetch from the caller's context, so a
		// caller-supplied client with no timeout would let a hung fetch
		// block key rotation indefinitely. Passing nil gets the package's
		// own client, which carries a 10-second timeout.
		keys := httpauth.NewJWKSCache(cfg.Auth.OAuth.Issuer, time.Duration(cfg.Auth.OAuth.JWKSTTLSeconds)*time.Second, nil)

		authCfg.Issuer = cfg.Auth.OAuth.Issuer
		authCfg.RequiredScopes = cfg.Auth.OAuth.RequiredScopes
		authCfg.Validator = httpauth.NewOAuthValidator(cfg.Auth.OAuth.Issuer, audience, cfg.Auth.OAuth.RequiredScopes, keys)
	}

	var transportHandler http.Handler
	switch f.transport {
	case "http":
		// server.WithEndpointPath only takes effect through Start, which
		// this composition never calls (mcp-go v0.47.0,
		// streamable_http.go): used as an http.Handler,
		// StreamableHTTPServer.ServeHTTP dispatches on method only and
		// ignores the path entirely. exactPathHandler restores path
		// matching so the endpoint only answers at endpointPath — the
		// same string advertised as the PRM "resource" — and 404s
		// elsewhere instead of silently answering on every path.
		opts := []server.StreamableHTTPOption{
			server.WithSessionIdleTTL(sessionIdleTTLFor(cfg.Auth.MaxStreamSeconds)),
		}
		if f.mcpLogger != nil {
			opts = append(opts, server.WithLogger(f.mcpLogger))
		}
		transportHandler = exactPathHandler(endpointPath, server.NewStreamableHTTPServer(mcpSrv, opts...))
	case "sse":
		// SSEServer.ServeHTTP already matches its SSE and message paths
		// exactly, so it needs no wrapper. --endpoint-path is deliberately
		// not applied here; SSE keeps the library's default /sse and
		// /message paths.
		sseBaseURL := "http://" + f.addr
		if publicURL != "" {
			// A remote client reached through a public URL cannot use the
			// local bind address; without this, useFullURLForMessageEndpoint
			// (the mcp-go default) would hand it a message endpoint it
			// cannot reach, over plain HTTP.
			sseBaseURL = publicURL
		}
		transportHandler = server.NewSSEServer(mcpSrv, server.WithBaseURL(sseBaseURL))
	default:
		// runServe only calls this for "http" and "sse", but buildServeHandler
		// is also called directly by tests: fail closed here too, rather than
		// handing httpauth.Mount a nil handler.
		return nil, fmt.Errorf("--transport: %q not supported; use \"stdio\", \"sse\", or \"http\"", f.transport)
	}

	return httpauth.Mount(authCfg, transportHandler), nil
}

// exactPathHandler serves next only for requests whose path is exactly
// path, and 404s otherwise. path must already be normalized (leading slash,
// no trailing slash).
func exactPathHandler(path string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isLoopbackAddr reports whether addr's host part refers only to the local
// machine. An empty host (as in ":8080") binds every interface and is
// therefore not loopback, even though it names no remote host either.
// "localhost", "127.0.0.1", and "::1" are loopback; every other host,
// including "0.0.0.0", is not.
func isLoopbackAddr(addr string) (bool, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false, fmt.Errorf("addr: %q is not a valid host:port; expected a value like \"host:port\"", addr)
	}
	if host == "" {
		return false, nil
	}
	if host == "localhost" {
		return true, nil
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback(), nil
}

// validatePublicURL checks that raw is an absolute https:// URL with a
// non-empty host and no trailing slash, query, or fragment. A path
// component is allowed, for deployments mounted under a subpath, as long as
// it does not end in "/".
func validatePublicURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("public-url: %q is not a valid URL; expected an absolute https:// URL", raw)
	}
	if u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("public-url: %q is not an absolute https:// URL; expected a scheme-qualified https URL", raw)
	}
	if u.User != nil {
		return nil, fmt.Errorf("public-url: %q must not carry userinfo; expected no \"user:pass@\" component", raw)
	}
	if u.RawQuery != "" || u.ForceQuery {
		return nil, fmt.Errorf("public-url: %q must not carry a query; expected no \"?\" component", raw)
	}
	if u.Fragment != "" {
		return nil, fmt.Errorf("public-url: %q must not carry a fragment; expected no \"#\" component", raw)
	}
	if strings.HasSuffix(u.Path, "/") {
		return nil, fmt.Errorf("public-url: %q must not carry a trailing slash; expected no trailing \"/\"", raw)
	}
	return u, nil
}

// normalizeEndpointPath applies the same normalization
// server.WithEndpointPath documents (a leading slash, no trailing slash) so
// the value used to build the PRM "resource" and the value the exact-path
// wrapper matches on are always the same string. A value with nothing left
// after trimming slashes cannot be normalized into a meaningful path, so it
// is rejected rather than silently collapsed to "/".
func normalizeEndpointPath(raw string) (string, error) {
	trimmed := strings.Trim(raw, "/")
	if trimmed == "" {
		return "", fmt.Errorf("endpoint-path: %q must not be empty; expected a path like \"/mcp\"", raw)
	}
	return "/" + trimmed, nil
}

// resolveAudience returns the effective OAuth audience for "oauth" mode. An
// unset configured value defaults to resource (publicURL+endpointPath),
// since the PRM "resource" and a token's "aud" must be identical (RFC 8707):
// if they disagree, every token is rejected as wrong-audience. A configured
// value that disagrees with resource is a startup error rather than a
// silent runtime rejection, so a confused deputy is caught before the
// server ever accepts a connection.
func resolveAudience(configured, resource string) (string, error) {
	if configured == "" {
		return resource, nil
	}
	if configured != resource {
		return "", fmt.Errorf("auth.oauth.audience: %q disagrees with the advertised resource %q; expected them to match, or leave audience unset to default to it", configured, resource)
	}
	return configured, nil
}
