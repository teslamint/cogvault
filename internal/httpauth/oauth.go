package httpauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ErrInsufficientScope is returned by (*OAuthValidator).Validate when a
// token is otherwise valid but does not carry every scope in
// NewOAuthValidator's requiredScopes.
var ErrInsufficientScope = errors.New("insufficient scope")

// oauthClaims is the claim shape OAuthValidator parses. It embeds
// jwt.RegisteredClaims for iss/aud/exp/nbf and adds the OAuth2 scope claim.
// RFC 6749 defines scope as a space-delimited string, but some identity
// providers instead emit a JSON array; Scope is decoded as json.RawMessage
// so scopesFromClaim can handle both shapes.
type oauthClaims struct {
	jwt.RegisteredClaims
	Scope json.RawMessage `json:"scope,omitempty"`
}

// OAuthValidator implements httpauth.TokenValidator for OAuth2/OIDC bearer
// tokens. It verifies signature, issuer, audience, and expiry using
// github.com/golang-jwt/jwt/v5, resolving signing keys from a JWKSCache, and
// additionally enforces that the token's scope claim carries every scope in
// requiredScopes.
type OAuthValidator struct {
	requiredScopes []string
	keys           *JWKSCache
	parser         *jwt.Parser
}

// NewOAuthValidator builds an OAuthValidator that accepts only tokens issued
// by issuer for audience, carrying every scope in requiredScopes, signed by
// a key keys resolves by kid.
//
// NewOAuthValidator panics if issuer or audience is empty. An empty
// configured audience does not silently disable the audience check —
// jwt.WithAudience("") still requires the token's aud to contain the
// literal empty string, which no real token carries, so an empty audience
// would deny every token rather than act as a bypass. Requiring both
// non-empty here removes any doubt and fails loudly at construction time
// instead of relying on that behavior, mirroring the construction-time
// guard pattern in validateConfig (auth.go): a misconfiguration the
// operator can act on beats a silent per-request failure mode.
func NewOAuthValidator(issuer, audience string, requiredScopes []string, keys *JWKSCache) *OAuthValidator {
	if issuer == "" {
		panic("httpauth: NewOAuthValidator requires a non-empty issuer")
	}
	if audience == "" {
		panic("httpauth: NewOAuthValidator requires a non-empty audience")
	}

	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{"RS256", "ES256"}),
		jwt.WithExpirationRequired(),
		jwt.WithIssuer(issuer),
		jwt.WithAudience(audience),
		jwt.WithLeeway(60*time.Second),
	)

	return &OAuthValidator{
		requiredScopes: requiredScopes,
		keys:           keys,
		parser:         parser,
	}
}

// Validate parses and validates token, returning its expiry on success.
// Validate returns the zero time.Time on every error path: the middleware
// uses the returned time as a stream deadline, and a nonzero value on a
// rejected token could extend a stream past a credential that was never
// actually authorized.
func (v *OAuthValidator) Validate(ctx context.Context, token string) (time.Time, error) {
	var claims oauthClaims
	_, err := v.parser.ParseWithClaims(token, &claims, func(t *jwt.Token) (any, error) {
		kid, ok := t.Header["kid"].(string)
		if !ok || kid == "" {
			return nil, fmt.Errorf("httpauth: token header has no kid")
		}
		return v.keys.KeyFor(ctx, kid)
	})
	if err != nil {
		return time.Time{}, err
	}

	if err := v.checkScopes(claims); err != nil {
		return time.Time{}, err
	}

	return claims.ExpiresAt.Time, nil
}

// checkScopes verifies claims carries every scope in v.requiredScopes. When
// v.requiredScopes is empty, the scope claim is never inspected. Otherwise a
// missing scope claim, or one missing any required scope, fails with
// ErrInsufficientScope.
func (v *OAuthValidator) checkScopes(claims oauthClaims) error {
	if len(v.requiredScopes) == 0 {
		return nil
	}
	granted := scopesFromClaim(claims.Scope)
	grantedSet := make(map[string]struct{}, len(granted))
	for _, s := range granted {
		grantedSet[s] = struct{}{}
	}
	for _, want := range v.requiredScopes {
		if _, ok := grantedSet[want]; !ok {
			return ErrInsufficientScope
		}
	}
	return nil
}

// scopesFromClaim decodes a scope claim that may be absent, a
// space-delimited string per RFC 6749, or (observed from some identity
// providers) a JSON array of strings. Any other shape yields no scopes,
// which checkScopes turns into ErrInsufficientScope rather than a distinct
// error — a malformed scope claim must fail closed the same as a missing
// one.
func scopesFromClaim(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.Fields(s)
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr
	}
	return nil
}
