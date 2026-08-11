// Package httpauth provides the HTTP authorization middleware for cogvault's
// remote MCP server. It is the entire access boundary for a server that
// exposes wiki_write and wiki_delete to the public internet: every rejection
// path here is load-bearing, and no rejection path may log a credential.
package httpauth

import (
	"context"
	"crypto/subtle"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// TokenValidator validates a bearer token in oauth mode and reports when it
// expires. The real JWT-backed implementation arrives in a later unit; this
// package depends only on the interface.
type TokenValidator interface {
	Validate(ctx context.Context, token string) (expiresAt time.Time, err error)
}

// Config configures Middleware. MaxBodyBytes is already in bytes; a later
// unit converts the config file's auth.max_body_mb into it. Validator is nil
// in "none" and "bearer" modes and must not be dereferenced outside "oauth"
// mode.
type Config struct {
	Mode             string
	BearerToken      string
	PublicURL        string
	EndpointPath     string
	MaxBodyBytes     int64
	MaxStreamSeconds int
	Validator        TokenValidator
}

const bearerPrefix = "Bearer "

// Middleware returns an http.Handler wrapper that enforces the resource
// bounds (body size, request Origin, stream deadline) in every mode, and the
// credential check for "bearer" and "oauth" modes. "none" mode skips only
// the credential check.
func Middleware(cfg Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > 0 && r.ContentLength > cfg.MaxBodyBytes {
				logRejection("body_too_large", r)
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, cfg.MaxBodyBytes)

			if !originAllowed(r.Header.Get("Origin"), cfg.PublicURL) {
				logRejection("foreign_origin", r)
				w.WriteHeader(http.StatusForbidden)
				return
			}

			expiresAt := time.Time{}
			hasExpiry := false

			switch cfg.Mode {
			case "none":
				// Credential check skipped; resource bounds above still apply.
			case "bearer":
				token, ok := bearerToken(r)
				if !ok || !bearerTokenEqual(cfg.BearerToken, token) {
					logRejection("invalid_credential", r)
					writeUnauthorized(w, cfg)
					return
				}
			case "oauth":
				token, ok := bearerToken(r)
				if !ok {
					logRejection("invalid_credential", r)
					writeUnauthorized(w, cfg)
					return
				}
				exp, err := cfg.Validator.Validate(r.Context(), token)
				if err != nil {
					logRejection("invalid_credential", r)
					writeUnauthorized(w, cfg)
					return
				}
				expiresAt = exp
				hasExpiry = true
			}

			deadline := time.Now().Add(time.Duration(cfg.MaxStreamSeconds) * time.Second)
			if hasExpiry && expiresAt.Before(deadline) {
				deadline = expiresAt
			}
			ctx, cancel := context.WithDeadline(r.Context(), deadline)
			defer cancel()

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// bearerToken extracts the token from an "Authorization: Bearer <token>"
// header. ok is false when the header is absent or malformed.
func bearerToken(r *http.Request) (token string, ok bool) {
	authz := r.Header.Get("Authorization")
	if !strings.HasPrefix(authz, bearerPrefix) {
		return "", false
	}
	return strings.TrimPrefix(authz, bearerPrefix), true
}

// bearerTokenEqual compares two bearer tokens without leaking their content
// through timing. subtle.ConstantTimeCompare returns 0 immediately for
// slices of different length, which would otherwise leak the token's length
// through timing if called directly on raw, unequal-length slices. Guarding
// the length separately (a single, non-secret-dependent comparison) and then
// running ConstantTimeCompare only over equal-length slices keeps the
// content comparison itself length-independent and constant-time.
func bearerTokenEqual(want, got string) bool {
	wantBytes := []byte(want)
	gotBytes := []byte(got)
	if len(wantBytes) != len(gotBytes) {
		return false
	}
	return subtle.ConstantTimeCompare(wantBytes, gotBytes) == 1
}

// originAllowed reports whether origin may reach the server. An absent
// Origin header is acceptable (not every MCP client is a browser). A
// loopback origin (localhost, 127.0.0.1, ::1) is accepted on any port and
// either scheme. Otherwise the origin's scheme+host+port must match
// publicURL's exactly; header values are never string-compared raw.
func originAllowed(origin, publicURL string) bool {
	if origin == "" {
		return true
	}
	o, err := url.Parse(origin)
	if err != nil || o.Scheme == "" || o.Host == "" {
		return false
	}
	switch o.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	p, err := url.Parse(publicURL)
	if err != nil {
		return false
	}
	return o.Scheme == p.Scheme && o.Host == p.Host
}

// writeUnauthorized writes the 401 challenge. In "oauth" mode it points at
// the protected-resource metadata document; "bearer" mode has no
// authorization server, so it omits resource_metadata rather than pointing
// clients at a document that names no issuer.
func writeUnauthorized(w http.ResponseWriter, cfg Config) {
	if cfg.Mode == "oauth" {
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource"`, cfg.PublicURL))
	} else {
		w.Header().Set("WWW-Authenticate", "Bearer")
	}
	w.WriteHeader(http.StatusUnauthorized)
}

// logRejection logs the reason class and remote address only. Credentials
// must never reach the logs: no Authorization header value, bearer token, or
// raw JWT may appear in a log line.
func logRejection(reason string, r *http.Request) {
	slog.Warn("httpauth: request rejected", "reason", reason, "remote_addr", r.RemoteAddr)
}
