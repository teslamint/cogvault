package httpauth

import (
	"context"
	"crypto/rsa"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	testAudience = "https://mcp.example.com/mcp"
	testKid      = "test-key-1"
)

// newTestOAuthFixture spins up a stub JWKS server backed by a fresh RSA key
// and returns the issuer URL, the RSA key to sign tokens with, and a
// JWKSCache pointed at the stub so KeyFor resolves testKid to the matching
// public key. Reuses newStubServer/rsaJWK from jwks_test.go so the JWKS wire
// shape is produced the same way a real IdP would, not the way the cache
// happens to decode it.
func newTestOAuthFixture(t *testing.T) (issuer string, key *rsa.PrivateKey, keys *JWKSCache) {
	t.Helper()
	rsaKey := genRSAKey(t)
	stub := newStubServer(t, []testJWK{rsaJWK(testKid, &rsaKey.PublicKey)})
	cache := NewJWKSCache(stub.srv.URL, time.Hour, stub.srv.Client())
	return stub.srv.URL, rsaKey, cache
}

// baseClaims builds a minimal valid claim set. aud accepts either a string
// or a []string so callers can exercise both wire shapes of the aud claim.
func baseClaims(iss string, aud any) jwt.MapClaims {
	return jwt.MapClaims{
		"iss": iss,
		"aud": aud,
		"exp": time.Now().Add(time.Hour).Unix(),
	}
}

// mint signs claims with method and key, setting kid in the header when
// non-empty.
func mint(t *testing.T, method jwt.SigningMethod, key any, kid string, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(method, claims)
	if kid != "" {
		tok.Header["kid"] = kid
	}
	signed, err := tok.SignedString(key)
	if err != nil {
		t.Fatalf("SignedString: %v", err)
	}
	return signed
}

func TestOAuthValidatorHappyPath(t *testing.T) {
	issuer, key, keys := newTestOAuthFixture(t)
	v := NewOAuthValidator(issuer, testAudience, nil, keys)

	wantExp := time.Now().Add(45 * time.Minute).Truncate(time.Second)
	claims := baseClaims(issuer, testAudience)
	claims["exp"] = wantExp.Unix()
	token := mint(t, jwt.SigningMethodRS256, key, testKid, claims)

	gotExp, err := v.Validate(context.Background(), token)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !gotExp.Equal(wantExp) {
		t.Errorf("exp = %v, want %v", gotExp, wantExp)
	}
}

func TestOAuthValidatorAudienceShapes(t *testing.T) {
	issuer, key, keys := newTestOAuthFixture(t)
	v := NewOAuthValidator(issuer, testAudience, nil, keys)

	t.Run("aud array containing configured audience validates", func(t *testing.T) {
		claims := baseClaims(issuer, []string{"https://unrelated.example.com", testAudience})
		token := mint(t, jwt.SigningMethodRS256, key, testKid, claims)
		if _, err := v.Validate(context.Background(), token); err != nil {
			t.Fatalf("Validate: %v", err)
		}
	})

	t.Run("aud bare string containing it validates", func(t *testing.T) {
		claims := baseClaims(issuer, testAudience)
		token := mint(t, jwt.SigningMethodRS256, key, testKid, claims)
		if _, err := v.Validate(context.Background(), token); err != nil {
			t.Fatalf("Validate: %v", err)
		}
	})
}

func TestOAuthValidatorRejections(t *testing.T) {
	issuer, key, keys := newTestOAuthFixture(t)
	v := NewOAuthValidator(issuer, testAudience, nil, keys)

	otherKey := genRSAKey(t)

	tests := []struct {
		name      string
		token     func() string
		wantErrIs error
	}{
		{
			name: "expired",
			token: func() string {
				claims := baseClaims(issuer, testAudience)
				claims["exp"] = time.Now().Add(-time.Hour).Unix()
				return mint(t, jwt.SigningMethodRS256, key, testKid, claims)
			},
			wantErrIs: jwt.ErrTokenExpired,
		},
		{
			name: "not yet valid",
			token: func() string {
				claims := baseClaims(issuer, testAudience)
				claims["nbf"] = time.Now().Add(10 * time.Minute).Unix()
				return mint(t, jwt.SigningMethodRS256, key, testKid, claims)
			},
			wantErrIs: jwt.ErrTokenNotValidYet,
		},
		{
			name: "wrong aud",
			token: func() string {
				claims := baseClaims(issuer, "https://wrong.example.com")
				return mint(t, jwt.SigningMethodRS256, key, testKid, claims)
			},
			wantErrIs: jwt.ErrTokenInvalidAudience,
		},
		{
			name: "aud array omitting configured audience",
			token: func() string {
				claims := baseClaims(issuer, []string{"https://other1.example.com", "https://other2.example.com"})
				return mint(t, jwt.SigningMethodRS256, key, testKid, claims)
			},
			wantErrIs: jwt.ErrTokenInvalidAudience,
		},
		{
			name: "wrong iss",
			token: func() string {
				claims := baseClaims("https://wrong-issuer.example.com", testAudience)
				return mint(t, jwt.SigningMethodRS256, key, testKid, claims)
			},
			wantErrIs: jwt.ErrTokenInvalidIssuer,
		},
		{
			name: "bad signature",
			token: func() string {
				claims := baseClaims(issuer, testAudience)
				// Signed with a key the JWKS cache never advertises under
				// testKid: KeyFor resolves testKid to the real public key,
				// but that key does not match this signature.
				return mint(t, jwt.SigningMethodRS256, otherKey, testKid, claims)
			},
			wantErrIs: jwt.ErrTokenSignatureInvalid,
		},
		{
			name: "unknown kid",
			token: func() string {
				claims := baseClaims(issuer, testAudience)
				return mint(t, jwt.SigningMethodRS256, key, "does-not-exist", claims)
			},
			wantErrIs: jwt.ErrTokenUnverifiable,
		},
		{
			name: "malformed JWT",
			token: func() string {
				return "not-a-valid-jwt"
			},
			wantErrIs: jwt.ErrTokenMalformed,
		},
		{
			name: "alg none",
			token: func() string {
				claims := baseClaims(issuer, testAudience)
				return mint(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, testKid, claims)
			},
			wantErrIs: jwt.ErrTokenSignatureInvalid,
		},
		{
			name: "no exp claim",
			token: func() string {
				claims := baseClaims(issuer, testAudience)
				delete(claims, "exp")
				return mint(t, jwt.SigningMethodRS256, key, testKid, claims)
			},
			wantErrIs: jwt.ErrTokenRequiredClaimMissing,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotExp, err := v.Validate(context.Background(), tt.token())
			if err == nil {
				t.Fatal("Validate: want error, got nil")
			}
			if !errors.Is(err, tt.wantErrIs) {
				t.Fatalf("Validate error = %v, want errors.Is(..., %v)", err, tt.wantErrIs)
			}
			if !gotExp.IsZero() {
				t.Errorf("Validate returned non-zero expiry %v on an error path", gotExp)
			}
		})
	}
}

func TestOAuthValidatorScopes(t *testing.T) {
	issuer, key, keys := newTestOAuthFixture(t)

	t.Run("token carries every required scope", func(t *testing.T) {
		v := NewOAuthValidator(issuer, testAudience, []string{"wiki:read", "wiki:write"}, keys)
		claims := baseClaims(issuer, testAudience)
		claims["scope"] = "wiki:read wiki:write extra:scope"
		token := mint(t, jwt.SigningMethodRS256, key, testKid, claims)
		if _, err := v.Validate(context.Background(), token); err != nil {
			t.Fatalf("Validate: %v", err)
		}
	})

	t.Run("token missing a required scope fails with ErrInsufficientScope", func(t *testing.T) {
		v := NewOAuthValidator(issuer, testAudience, []string{"wiki:read", "wiki:write"}, keys)
		claims := baseClaims(issuer, testAudience)
		claims["scope"] = "wiki:read"
		token := mint(t, jwt.SigningMethodRS256, key, testKid, claims)
		_, err := v.Validate(context.Background(), token)
		if !errors.Is(err, ErrInsufficientScope) {
			t.Fatalf("Validate error = %v, want errors.Is(..., ErrInsufficientScope)", err)
		}
	})

	t.Run("no scope claim at all fails with ErrInsufficientScope", func(t *testing.T) {
		v := NewOAuthValidator(issuer, testAudience, []string{"wiki:read"}, keys)
		claims := baseClaims(issuer, testAudience)
		token := mint(t, jwt.SigningMethodRS256, key, testKid, claims)
		_, err := v.Validate(context.Background(), token)
		if !errors.Is(err, ErrInsufficientScope) {
			t.Fatalf("Validate error = %v, want errors.Is(..., ErrInsufficientScope)", err)
		}
	})

	t.Run("RequiredScopes empty never inspects the scope claim", func(t *testing.T) {
		v := NewOAuthValidator(issuer, testAudience, nil, keys)
		claims := baseClaims(issuer, testAudience)
		// A scope value that isn't even a string or array: if RequiredScopes
		// is empty this must never be parsed, so it must not cause a
		// failure either.
		claims["scope"] = 12345
		token := mint(t, jwt.SigningMethodRS256, key, testKid, claims)
		if _, err := v.Validate(context.Background(), token); err != nil {
			t.Fatalf("Validate: %v", err)
		}
	})

	// RFC 6749 defines scope as a space-delimited string, but some identity
	// providers emit a JSON array instead (ambiguity resolved in the brief:
	// handle both shapes rather than failing).
	t.Run("scope claim as a JSON array is accepted", func(t *testing.T) {
		v := NewOAuthValidator(issuer, testAudience, []string{"wiki:read"}, keys)
		claims := baseClaims(issuer, testAudience)
		claims["scope"] = []string{"wiki:read", "wiki:write"}
		token := mint(t, jwt.SigningMethodRS256, key, testKid, claims)
		if _, err := v.Validate(context.Background(), token); err != nil {
			t.Fatalf("Validate: %v", err)
		}
	})
}

func TestOAuthValidatorConstructionGuards(t *testing.T) {
	_, _, keys := newTestOAuthFixture(t)

	tests := []struct {
		name     string
		issuer   string
		audience string
	}{
		{name: "empty issuer", issuer: "", audience: testAudience},
		{name: "empty audience", issuer: "https://issuer.example.com", audience: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Error("NewOAuthValidator did not panic for an unusable config")
				}
			}()
			NewOAuthValidator(tt.issuer, tt.audience, nil, keys)
		})
	}
}

// TestExpiredTokenChallenge (Covers S6) drives Middleware end-to-end in
// oauth mode with an expired token, proving OAuthValidator satisfies
// TokenValidator well enough for the earlier unit's middleware to consume:
// a rejected oauth token must produce the same 401 + resource_metadata
// challenge the middleware's own tests assert with a stub validator.
func TestExpiredTokenChallenge(t *testing.T) {
	const publicURL = "https://mcp.example.com"
	issuer, key, keys := newTestOAuthFixture(t)
	v := NewOAuthValidator(issuer, testAudience, nil, keys)

	cfg := Config{
		Mode:             "oauth",
		PublicURL:        publicURL,
		MaxBodyBytes:     1024,
		MaxStreamSeconds: 30,
		Validator:        v,
	}
	var ran bool
	h := Middleware(cfg)(sentinelHandler(&ran))

	claims := baseClaims(issuer, testAudience)
	claims["exp"] = time.Now().Add(-time.Hour).Unix()
	token := mint(t, jwt.SigningMethodRS256, key, testKid, claims)

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if ran {
		t.Fatal("next handler ran for an expired token")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	wantChallenge := `Bearer resource_metadata="` + publicURL + `/.well-known/oauth-protected-resource"`
	if got := rec.Header().Get("WWW-Authenticate"); got != wantChallenge {
		t.Fatalf("WWW-Authenticate = %q, want %q", got, wantChallenge)
	}
}
