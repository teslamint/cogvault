package httpauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

// TestMetadataHandler exercises MetadataHandler directly: the PRM document
// shape, the omitempty behavior of scopes_supported, method handling, and
// Content-Type.
func TestMetadataHandler(t *testing.T) {
	t.Run("happy path document shape", func(t *testing.T) {
		cfg := Config{
			PublicURL:      "https://mcp.example.com",
			EndpointPath:   "/mcp",
			Issuer:         "https://issuer.example.com",
			RequiredScopes: []string{"wiki:read", "wiki:write"},
		}
		h := MetadataHandler(cfg)
		req := httptest.NewRequest(http.MethodGet, wellKnownPRMPath, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Fatalf("Content-Type = %q, want %q", ct, "application/json")
		}

		var doc struct {
			Resource               string   `json:"resource"`
			AuthorizationServers   []string `json:"authorization_servers"`
			ScopesSupported        []string `json:"scopes_supported"`
			BearerMethodsSupported []string `json:"bearer_methods_supported"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
			t.Fatalf("json.Unmarshal: %v", err)
		}
		if want := cfg.PublicURL + cfg.EndpointPath; doc.Resource != want {
			t.Errorf("resource = %q, want %q", doc.Resource, want)
		}
		if len(doc.AuthorizationServers) != 1 || doc.AuthorizationServers[0] != cfg.Issuer {
			t.Errorf("authorization_servers = %v, want [%q]", doc.AuthorizationServers, cfg.Issuer)
		}
		if len(doc.ScopesSupported) != 2 || doc.ScopesSupported[0] != "wiki:read" || doc.ScopesSupported[1] != "wiki:write" {
			t.Errorf("scopes_supported = %v, want %v", doc.ScopesSupported, cfg.RequiredScopes)
		}
		if len(doc.BearerMethodsSupported) != 1 || doc.BearerMethodsSupported[0] != "header" {
			t.Errorf("bearer_methods_supported = %v, want [header]", doc.BearerMethodsSupported)
		}
	})

	t.Run("empty RequiredScopes omits scopes_supported entirely", func(t *testing.T) {
		cfg := Config{PublicURL: "https://mcp.example.com", EndpointPath: "/mcp", Issuer: "https://issuer.example.com"}
		h := MetadataHandler(cfg)
		req := httptest.NewRequest(http.MethodGet, wellKnownPRMPath, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if strings.Contains(rec.Body.String(), "scopes_supported") {
			t.Fatalf("body contains scopes_supported when RequiredScopes is empty: %s", rec.Body.String())
		}
	})

	t.Run("non-default EndpointPath is reflected in resource", func(t *testing.T) {
		cfg := Config{PublicURL: "https://mcp.example.com", EndpointPath: "/wiki", Issuer: "https://issuer.example.com"}
		h := MetadataHandler(cfg)
		req := httptest.NewRequest(http.MethodGet, wellKnownPRMPath+"/wiki", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		var doc struct {
			Resource string `json:"resource"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
			t.Fatalf("json.Unmarshal: %v", err)
		}
		if want := "https://mcp.example.com/wiki"; doc.Resource != want {
			t.Errorf("resource = %q, want %q", doc.Resource, want)
		}
	})

	t.Run("HEAD returns 200 with an empty body", func(t *testing.T) {
		cfg := Config{PublicURL: "https://mcp.example.com", EndpointPath: "/mcp", Issuer: "https://issuer.example.com"}
		h := MetadataHandler(cfg)
		req := httptest.NewRequest(http.MethodHead, wellKnownPRMPath, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if rec.Body.Len() != 0 {
			t.Fatalf("HEAD response has a body: %q", rec.Body.String())
		}
	})

	t.Run("POST returns 405", func(t *testing.T) {
		cfg := Config{PublicURL: "https://mcp.example.com", EndpointPath: "/mcp", Issuer: "https://issuer.example.com"}
		h := MetadataHandler(cfg)
		req := httptest.NewRequest(http.MethodPost, wellKnownPRMPath, strings.NewReader("{}"))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
		}
	})
}

// newMountedFixture builds a Mount-composed handler over a sentinel mcp
// handler, plus a pointer the test can inspect to confirm whether that
// sentinel ran.
func newMountedFixture(mode, endpointPath string) (http.Handler, *bool) {
	var ran bool
	cfg := Config{
		Mode:             mode,
		BearerToken:      "unused-in-these-tests",
		PublicURL:        "https://mcp.example.com",
		EndpointPath:     endpointPath,
		Issuer:           "https://issuer.example.com",
		MaxBodyBytes:     1024,
		MaxStreamSeconds: 30,
		Validator:        stubValidator{},
	}
	return Mount(cfg, sentinelHandler(&ran)), &ran
}

// TestWellKnownExactMatch is the security-critical test for this unit: it
// proves Mount routes the two well-known metadata paths by exact string
// equality, not by prefix. Every near-miss subtest here is written so that
// changing Mount's comparison from == to strings.HasPrefix would make it
// fail: a prefix match would serve the metadata document, unauthenticated,
// for a path that must instead reach the middleware and be rejected.
func TestWellKnownExactMatch(t *testing.T) {
	t.Run("oauth mode: bare well-known path serves metadata unauthenticated", func(t *testing.T) {
		h, ran := newMountedFixture("oauth", "/mcp")
		req := httptest.NewRequest(http.MethodGet, wellKnownPRMPath, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if *ran {
			t.Fatal("mcp handler ran for a metadata request")
		}
	})

	t.Run("oauth mode: path-suffixed well-known path serves metadata unauthenticated", func(t *testing.T) {
		h, ran := newMountedFixture("oauth", "/wiki")
		req := httptest.NewRequest(http.MethodGet, wellKnownPRMPath+"/wiki", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if *ran {
			t.Fatal("mcp handler ran for a metadata request")
		}
	})

	nearMissPaths := []string{
		wellKnownPRMPath + "-x",
		wellKnownPRMPath + "/../mcp",
	}

	for _, p := range nearMissPaths {
		t.Run("oauth mode: near-miss path "+p+" is not served as metadata", func(t *testing.T) {
			h, ran := newMountedFixture("oauth", "/mcp")
			req := httptest.NewRequest(http.MethodGet, p, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			// An unauthenticated request that reached the middleware
			// (rather than the metadata handler) must be rejected with
			// 401. A prefix-matching regression would instead return 200
			// with the metadata document here.
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d (near-miss path must fall through to the middleware, not the metadata handler)", rec.Code, http.StatusUnauthorized)
			}
			if *ran {
				t.Fatal("mcp handler ran for an unauthenticated near-miss request")
			}
		})
	}

	bearerModePaths := append([]string{wellKnownPRMPath, wellKnownPRMPath + "/mcp"}, nearMissPaths...)
	for _, p := range bearerModePaths {
		t.Run("bearer mode: well-known path "+p+" falls through to the middleware like any other path", func(t *testing.T) {
			h, ran := newMountedFixture("bearer", "/mcp")
			req := httptest.NewRequest(http.MethodGet, p, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
			}
			if *ran {
				t.Fatal("mcp handler ran for an unauthenticated request")
			}
		})
	}

	t.Run("none mode: well-known path falls through to the mcp handler like any other path", func(t *testing.T) {
		// Distinct observable from bearer mode: "none" mode has no
		// credential check at all, so fall-through here means the request
		// reaches mcp (ran == true, 200), not a 401 rejection.
		h, ran := newMountedFixture("none", "/mcp")
		req := httptest.NewRequest(http.MethodGet, wellKnownPRMPath, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if !*ran {
			t.Fatal("mcp handler did not run for a well-known path in none mode")
		}
	})

	t.Run("oauth mode: POST to a metadata path returns 405 without reaching the middleware", func(t *testing.T) {
		h, ran := newMountedFixture("oauth", "/mcp")
		req := httptest.NewRequest(http.MethodPost, wellKnownPRMPath, strings.NewReader("{}"))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
		}
		if *ran {
			t.Fatal("mcp handler ran for a POST to a metadata path")
		}
	})

	t.Run("Mount panics for the same unusable configs Middleware rejects", func(t *testing.T) {
		for _, tt := range unusableConfigCases() {
			t.Run(tt.name, func(t *testing.T) {
				defer func() {
					if r := recover(); r == nil {
						t.Error("Mount did not panic for an unusable config")
					}
				}()
				var ran bool
				Mount(tt.cfg, sentinelHandler(&ran))
			})
		}
	})
}

// TestOAuthRoundTrip (Covers S1, S2) walks the full oauth-mode flow against a
// single Mount-composed handler: an unauthenticated request is rejected with
// a pointer to the metadata document, that document is fetched from the same
// handler and names the issuer, and a token signed for that issuer and
// audience succeeds.
//
// This lives in metadata_test.go rather than oauth_test.go: the brief names
// it as belonging to oauth_test.go, but this unit's global constraints
// forbid modifying that file, and it needs Mount and MetadataHandler, which
// don't exist until this unit. It reuses oauth_test.go's fixtures
// (newTestOAuthFixture, baseClaims, mint, testAudience, testKid) since they
// live in the same package.
func TestOAuthRoundTrip(t *testing.T) {
	const publicURL = "https://mcp.example.com"
	const endpointPath = "/mcp" // publicURL + endpointPath == testAudience

	issuer, key, keys := newTestOAuthFixture(t)
	validator := NewOAuthValidator(issuer, testAudience, nil, keys)

	var ran bool
	cfg := Config{
		Mode:             "oauth",
		PublicURL:        publicURL,
		EndpointPath:     endpointPath,
		Issuer:           issuer,
		MaxBodyBytes:     1024,
		MaxStreamSeconds: 30,
		Validator:        validator,
	}
	h := Mount(cfg, sentinelHandler(&ran))

	// Step 1: unauthenticated request to the MCP endpoint is rejected with a
	// pointer to the metadata document.
	unauthReq := httptest.NewRequest(http.MethodPost, endpointPath, strings.NewReader("{}"))
	unauthRec := httptest.NewRecorder()
	h.ServeHTTP(unauthRec, unauthReq)

	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated request status = %d, want %d", unauthRec.Code, http.StatusUnauthorized)
	}
	if ran {
		t.Fatal("mcp handler ran for an unauthenticated request")
	}
	challenge := unauthRec.Header().Get("WWW-Authenticate")
	start, end := strings.Index(challenge, `"`), strings.LastIndex(challenge, `"`)
	if start == -1 || end == -1 || start == end {
		t.Fatalf("could not parse resource_metadata URL from WWW-Authenticate: %q", challenge)
	}
	metadataURL := challenge[start+1 : end]
	metadataPath := strings.TrimPrefix(metadataURL, publicURL)

	// Step 2: fetch the metadata document from the pointer, on the same
	// mounted handler, and confirm it names the issuer.
	metaReq := httptest.NewRequest(http.MethodGet, metadataPath, nil)
	metaRec := httptest.NewRecorder()
	h.ServeHTTP(metaRec, metaReq)

	if metaRec.Code != http.StatusOK {
		t.Fatalf("metadata fetch status = %d, want %d", metaRec.Code, http.StatusOK)
	}
	var doc struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
	}
	if err := json.Unmarshal(metaRec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if doc.Resource != testAudience {
		t.Errorf("resource = %q, want %q", doc.Resource, testAudience)
	}
	if len(doc.AuthorizationServers) != 1 || doc.AuthorizationServers[0] != issuer {
		t.Errorf("authorization_servers = %v, want [%q]", doc.AuthorizationServers, issuer)
	}

	// Step 3: a validly signed token succeeds against the mounted handler.
	token := mint(t, jwt.SigningMethodRS256, key, testKid, baseClaims(issuer, testAudience))
	okReq := httptest.NewRequest(http.MethodPost, endpointPath, strings.NewReader("{}"))
	okReq.Header.Set("Authorization", "Bearer "+token)
	okRec := httptest.NewRecorder()
	h.ServeHTTP(okRec, okReq)

	if okRec.Code != http.StatusOK {
		t.Fatalf("authenticated request status = %d, want %d", okRec.Code, http.StatusOK)
	}
	if !ran {
		t.Fatal("mcp handler did not run for a validly authenticated request")
	}
}
