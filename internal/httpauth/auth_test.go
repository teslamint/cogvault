package httpauth

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// stubValidator is a test double for TokenValidator. The real implementation
// arrives in a later unit; this package only depends on the interface.
type stubValidator struct {
	expiresAt time.Time
	err       error
}

func (s stubValidator) Validate(_ context.Context, _ string) (time.Time, error) {
	return s.expiresAt, s.err
}

// sentinelHandler records whether it ran and echoes 200 OK.
func sentinelHandler(ran *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*ran = true
		w.WriteHeader(http.StatusOK)
	})
}

func TestNoneMode(t *testing.T) {
	var ran bool
	cfg := Config{Mode: "none", MaxBodyBytes: 1024, MaxStreamSeconds: 30}
	h := Middleware(cfg)(sentinelHandler(&ran))

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("hello"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !ran {
		t.Fatal("next handler did not run")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestBodyCap(t *testing.T) {
	t.Run("oversized body rejected without buffering", func(t *testing.T) {
		var ran bool
		cfg := Config{Mode: "none", MaxBodyBytes: 4, MaxStreamSeconds: 30}
		h := Middleware(cfg)(sentinelHandler(&ran))

		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("this body is far larger than four bytes"))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if ran {
			t.Fatal("next handler ran for an oversized body")
		}
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
		}
	})

	t.Run("absent Origin header passes", func(t *testing.T) {
		var ran bool
		cfg := Config{Mode: "none", MaxBodyBytes: 1024, MaxStreamSeconds: 30, PublicURL: "https://mcp.example.com"}
		h := Middleware(cfg)(sentinelHandler(&ran))

		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("hi"))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if !ran {
			t.Fatal("next handler did not run for a request with no Origin header")
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
	})
}

func TestBearerMode(t *testing.T) {
	const correctToken = "supersecrettoken123"
	baseCfg := Config{
		Mode:             "bearer",
		BearerToken:      correctToken,
		PublicURL:        "https://mcp.example.com",
		MaxBodyBytes:     1024,
		MaxStreamSeconds: 30,
	}

	tests := []struct {
		name       string
		authHeader string
		origin     string
		wantCode   int
		wantRan    bool
	}{
		{
			name:     "missing Authorization header",
			wantCode: http.StatusUnauthorized,
		},
		{
			name:       "wrong token",
			authHeader: "Bearer wrongtoken",
			wantCode:   http.StatusUnauthorized,
		},
		{
			name:       "token is a prefix of the correct one",
			authHeader: "Bearer " + correctToken[:len(correctToken)-3],
			wantCode:   http.StatusUnauthorized,
		},
		{
			name:       "token is an extension of the correct one",
			authHeader: "Bearer " + correctToken + "extra",
			wantCode:   http.StatusUnauthorized,
		},
		{
			name:       "foreign Origin rejected even with a valid token",
			authHeader: "Bearer " + correctToken,
			origin:     "https://evil.example.com",
			wantCode:   http.StatusForbidden,
		},
		{
			name:       "correct token passes",
			authHeader: "Bearer " + correctToken,
			wantCode:   http.StatusOK,
			wantRan:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ran bool
			h := Middleware(baseCfg)(sentinelHandler(&ran))

			req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{}"))
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantCode)
			}
			if ran != tt.wantRan {
				t.Fatalf("next handler ran = %v, want %v", ran, tt.wantRan)
			}
			if tt.wantCode == http.StatusUnauthorized {
				got := rec.Header().Get("WWW-Authenticate")
				if got != "Bearer" {
					t.Fatalf("WWW-Authenticate = %q, want %q (bearer mode must not advertise resource_metadata)", got, "Bearer")
				}
			}
		})
	}
}

func TestOrigin(t *testing.T) {
	baseCfg := Config{
		Mode:             "none",
		PublicURL:        "https://mcp.example.com",
		MaxBodyBytes:     1024,
		MaxStreamSeconds: 30,
	}

	tests := []struct {
		name     string
		origin   string
		wantCode int
	}{
		{name: "no Origin header passes", origin: "", wantCode: http.StatusOK},
		{name: "matching PublicURL passes", origin: "https://mcp.example.com", wantCode: http.StatusOK},
		{name: "loopback http localhost passes", origin: "http://localhost:5173", wantCode: http.StatusOK},
		{name: "loopback https 127.0.0.1 passes", origin: "https://127.0.0.1:9000", wantCode: http.StatusOK},
		{name: "loopback IPv6 passes", origin: "http://[::1]:3000", wantCode: http.StatusOK},
		{name: "foreign origin rejected", origin: "https://evil.example.com", wantCode: http.StatusForbidden},
		{name: "matching host wrong port rejected", origin: "https://mcp.example.com:8443", wantCode: http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ran bool
			h := Middleware(baseCfg)(sentinelHandler(&ran))

			req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("hi"))
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantCode)
			}
		})
	}
}

func TestNoCredentialLogging(t *testing.T) {
	const correctToken = "supersecrettoken123"
	var buf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	cfg := Config{
		Mode:             "bearer",
		BearerToken:      correctToken,
		PublicURL:        "https://mcp.example.com",
		MaxBodyBytes:     1024,
		MaxStreamSeconds: 30,
	}

	rejectionRequests := []func() *http.Request{
		func() *http.Request {
			r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{}"))
			r.Header.Set("Authorization", "Bearer wrongtoken")
			return r
		},
		func() *http.Request {
			return httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{}"))
		},
		func() *http.Request {
			r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{}"))
			r.Header.Set("Authorization", "Bearer "+correctToken)
			r.Header.Set("Origin", "https://evil.example.com")
			return r
		},
	}

	var ran bool
	h := Middleware(cfg)(sentinelHandler(&ran))
	for _, mk := range rejectionRequests {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, mk())
	}

	logged := buf.String()
	if logged == "" {
		t.Fatal("expected rejection logging, got nothing")
	}
	if strings.Contains(logged, correctToken) {
		t.Fatalf("log output contains the bearer token: %q", logged)
	}
	if strings.Contains(logged, "wrongtoken") {
		t.Fatalf("log output contains a submitted token value: %q", logged)
	}
	if strings.Contains(logged, "Bearer "+correctToken) || strings.Contains(logged, "Bearer wrongtoken") {
		t.Fatalf("log output contains a raw Authorization header value: %q", logged)
	}
}

func TestStreamDeadline(t *testing.T) {
	t.Run("no validator: deadline comes from MaxStreamSeconds", func(t *testing.T) {
		cfg := Config{Mode: "none", MaxBodyBytes: 1024, MaxStreamSeconds: 1}

		released := make(chan struct{})
		blocking := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
			close(released)
		})
		h := Middleware(cfg)(blocking)

		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("hi"))
		rec := httptest.NewRecorder()

		start := time.Now()
		h.ServeHTTP(rec, req)
		elapsed := time.Since(start)

		select {
		case <-released:
		default:
			t.Fatal("next handler was not released by the middleware's deadline")
		}
		if elapsed < time.Second {
			t.Fatalf("handler released too early: elapsed = %v, want >= 1s", elapsed)
		}
		if elapsed > 3*time.Second {
			t.Fatalf("handler released too late: elapsed = %v, want ~1s", elapsed)
		}
	})

	t.Run("with validator: deadline is the earlier of stream deadline and token expiry", func(t *testing.T) {
		cfg := Config{
			Mode:             "oauth",
			PublicURL:        "https://mcp.example.com",
			MaxBodyBytes:     1024,
			MaxStreamSeconds: 30,
			Validator:        stubValidator{expiresAt: time.Now().Add(1 * time.Second)},
		}

		released := make(chan struct{})
		blocking := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
			close(released)
		})
		h := Middleware(cfg)(blocking)

		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("hi"))
		req.Header.Set("Authorization", "Bearer whatever-the-stub-accepts")
		rec := httptest.NewRecorder()

		start := time.Now()
		h.ServeHTTP(rec, req)
		elapsed := time.Since(start)

		select {
		case <-released:
		default:
			t.Fatal("next handler was not released by the validator's expiry")
		}
		if elapsed >= 30*time.Second {
			t.Fatalf("handler used the MaxStreamSeconds deadline instead of the earlier token expiry: elapsed = %v", elapsed)
		}
		if elapsed > 3*time.Second {
			t.Fatalf("handler released too late: elapsed = %v, want ~1s", elapsed)
		}
	})
}
