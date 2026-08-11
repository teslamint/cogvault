// Package httpauth provides the HTTP authorization middleware for cogvault's
// remote MCP server. It is the entire access boundary for a server that
// exposes wiki_write and wiki_delete to the public internet: every rejection
// path here is load-bearing, and no rejection path may log a credential.
package httpauth

import (
	"context"
	"crypto/subtle"
	"errors"
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
// mode. Issuer and RequiredScopes are consumed only by Mount's protected
// resource metadata document in "oauth" mode; they are ignored in "none" and
// "bearer" mode, where there is no authorization server to advertise.
type Config struct {
	Mode             string
	BearerToken      string
	PublicURL        string
	EndpointPath     string
	MaxBodyBytes     int64
	MaxStreamSeconds int
	Validator        TokenValidator
	Issuer           string
	RequiredScopes   []string
}

const bearerPrefix = "Bearer "

// validateConfig checks that cfg is usable and panics with a clear message
// if it is not. Mode must be one of "none", "bearer", or "oauth" — the zero
// value "" and any typo (e.g. "Bearer") are rejected rather than silently
// falling open. "bearer" mode requires a non-empty BearerToken, since an
// empty configured token would authenticate an empty submitted token.
// "oauth" mode requires a non-nil Validator, since Middleware dereferences
// it on every request in that mode.
func validateConfig(cfg Config) {
	switch cfg.Mode {
	case "none":
	case "bearer":
		if cfg.BearerToken == "" {
			panic(`httpauth: Mode "bearer" requires a non-empty BearerToken`)
		}
	case "oauth":
		if cfg.Validator == nil {
			panic(`httpauth: Mode "oauth" requires a non-nil Validator`)
		}
	default:
		panic(fmt.Sprintf("httpauth: Mode: unknown value %q; expected one of \"none\", \"bearer\", \"oauth\"", cfg.Mode))
	}
}

// Middleware returns an http.Handler wrapper that enforces the resource
// bounds (body size, request Origin, stream deadline) in every mode, and the
// credential check for "bearer" and "oauth" modes. "none" mode skips only
// the credential check.
//
// Middleware panics if cfg is unusable: Mode must be one of "none", "bearer",
// or "oauth"; "bearer" mode requires a non-empty BearerToken; "oauth" mode
// requires a non-nil Validator. This package has no error return to report a
// misconfiguration through, and turning it into a startup-time panic the
// operator sees once is strictly safer than a per-request authorization
// failure on a public endpoint.
func Middleware(cfg Config) func(http.Handler) http.Handler {
	validateConfig(cfg)
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
				if !ok {
					logRejection("missing_credential", r)
					writeUnauthorized(w, cfg, false)
					return
				}
				if !bearerTokenEqual(cfg.BearerToken, token) {
					logRejection("invalid_credential", r)
					writeUnauthorized(w, cfg, true)
					return
				}
			case "oauth":
				token, ok := bearerToken(r)
				if !ok {
					logRejection("missing_credential", r)
					writeUnauthorized(w, cfg, false)
					return
				}
				exp, err := cfg.Validator.Validate(r.Context(), token)
				if err != nil {
					if errors.Is(err, ErrInsufficientScope) {
						logRejection("insufficient_scope", r)
						writeInsufficientScope(w, cfg)
						return
					}
					logRejection("invalid_credential", r)
					writeUnauthorized(w, cfg, true)
					return
				}
				expiresAt = exp
				hasExpiry = true
			default:
				// Defense in depth: validateConfig already rejects any mode
				// outside {none, bearer, oauth} at construction, so this
				// should be unreachable. Fail closed rather than fall
				// through to the wrapped handler if it is ever hit.
				logRejection("unknown_mode", r)
				writeUnauthorized(w, cfg, false)
				return
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

// Mount composes the "oauth" mode Protected Resource Metadata routes with
// Middleware and returns a single handler for the whole authorization
// boundary: mcp handles everything else.
//
// In "oauth" mode, Mount serves the metadata document, unauthenticated by
// design (RFC 9728 — it carries only public discovery data), at exactly two
// paths: the bare well-known path and that path suffixed with
// cfg.EndpointPath. Both are matched with plain string equality against
// r.URL.Path, never a prefix match: a prefix match would serve the metadata
// document, unauthenticated, to every path merely beginning with the
// well-known prefix — and would silently widen to cover any future handler
// mounted under a path sharing that prefix. Every other path, including any
// path that only starts with the well-known prefix, is routed through
// Middleware to mcp. In "none" and "bearer" mode there is no authorization
// server to advertise, so the well-known paths are not special-cased at all
// and fall through to Middleware like any other path.
//
// Mount calls Middleware(cfg) unconditionally, so it panics on the same
// unusable configs Middleware does — mounting a misconfigured server fails
// at construction, not at request time.
func Mount(cfg Config, mcp http.Handler) http.Handler {
	wrapped := Middleware(cfg)(mcp)
	if cfg.Mode != "oauth" {
		return wrapped
	}

	metadata := MetadataHandler(cfg)
	suffixedPRMPath := wellKnownPRMPath + cfg.EndpointPath

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == wellKnownPRMPath || r.URL.Path == suffixedPRMPath {
			metadata.ServeHTTP(w, r)
			return
		}
		wrapped.ServeHTTP(w, r)
	})
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
//
// invalidCredential distinguishes the two RFC 6750 §3.1 cases: a missing
// credential (no Authorization header, or one that is not a Bearer
// challenge) SHOULD NOT carry an error code, while a credential that was
// present and rejected — a wrong bearer token, or a token the validator
// refused — carries error="invalid_token" alongside any resource_metadata
// parameter, in the same "error=..., resource_metadata=..." ordering
// writeInsufficientScope uses.
func writeUnauthorized(w http.ResponseWriter, cfg Config, invalidCredential bool) {
	switch {
	case cfg.Mode == "oauth" && invalidCredential:
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer error="invalid_token", resource_metadata="%s%s"`, cfg.PublicURL, wellKnownPRMPath))
	case cfg.Mode == "oauth":
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s%s"`, cfg.PublicURL, wellKnownPRMPath))
	case invalidCredential:
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
	default:
		w.Header().Set("WWW-Authenticate", "Bearer")
	}
	w.WriteHeader(http.StatusUnauthorized)
}

// writeInsufficientScope writes the 403 challenge for a valid token whose
// scopes miss cfg.RequiredScopes. It is only reachable in "oauth" mode, since
// that is the only mode with a Validator that can return
// ErrInsufficientScope, so it always advertises resource_metadata alongside
// error="insufficient_scope" — RFC 6750's form for distinguishing a scope
// failure from an authentication failure.
func writeInsufficientScope(w http.ResponseWriter, cfg Config) {
	w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer error="insufficient_scope", resource_metadata="%s%s"`, cfg.PublicURL, wellKnownPRMPath))
	w.WriteHeader(http.StatusForbidden)
}

// logRejection logs the reason class and remote address only. Credentials
// must never reach the logs: no Authorization header value, bearer token, or
// raw JWT may appear in a log line.
func logRejection(reason string, r *http.Request) {
	slog.Warn("httpauth: request rejected", "reason", reason, "remote_addr", r.RemoteAddr)
}
