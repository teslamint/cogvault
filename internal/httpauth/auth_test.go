package httpauth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"
)

// errValidatorRejected is a sentinel error a stubValidator returns to
// simulate an oauth validator rejecting a token.
var errValidatorRejected = errors.New("validator rejected token")

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
		wantHeader string
	}{
		{
			name:       "missing Authorization header",
			wantCode:   http.StatusUnauthorized,
			wantHeader: "Bearer",
		},
		{
			name:       "malformed non-Bearer Authorization header",
			authHeader: "Basic dXNlcjpwYXNz",
			wantCode:   http.StatusUnauthorized,
			wantHeader: "Bearer",
		},
		{
			name:       "wrong token",
			authHeader: "Bearer wrongtoken",
			wantCode:   http.StatusUnauthorized,
			wantHeader: `Bearer error="invalid_token"`,
		},
		{
			name:       "empty token after the Bearer prefix",
			authHeader: "Bearer ",
			wantCode:   http.StatusUnauthorized,
			wantHeader: `Bearer error="invalid_token"`,
		},
		{
			name:       "token is a prefix of the correct one",
			authHeader: "Bearer " + correctToken[:len(correctToken)-3],
			wantCode:   http.StatusUnauthorized,
			wantHeader: `Bearer error="invalid_token"`,
		},
		{
			name:       "token is an extension of the correct one",
			authHeader: "Bearer " + correctToken + "extra",
			wantCode:   http.StatusUnauthorized,
			wantHeader: `Bearer error="invalid_token"`,
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
				if got != tt.wantHeader {
					t.Fatalf("WWW-Authenticate = %q, want %q", got, tt.wantHeader)
				}
			}
		})
	}
}

func TestOAuthMode(t *testing.T) {
	const publicURL = "https://mcp.example.com"
	wantMissingChallenge := `Bearer resource_metadata="` + publicURL + `/.well-known/oauth-protected-resource"`
	wantInvalidChallenge := `Bearer error="invalid_token", resource_metadata="` + publicURL + `/.well-known/oauth-protected-resource"`

	tests := []struct {
		name       string
		authHeader string
		validator  stubValidator
		wantHeader string
	}{
		{
			name:       "missing Authorization header",
			wantHeader: wantMissingChallenge,
		},
		{
			name:       "malformed non-Bearer Authorization header",
			authHeader: "Basic dXNlcjpwYXNz",
			wantHeader: wantMissingChallenge,
		},
		{
			name:       "validator returns an error",
			authHeader: "Bearer whatever-the-stub-rejects",
			validator:  stubValidator{err: errValidatorRejected},
			wantHeader: wantInvalidChallenge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ran bool
			cfg := Config{
				Mode:             "oauth",
				PublicURL:        publicURL,
				MaxBodyBytes:     1024,
				MaxStreamSeconds: 30,
				Validator:        tt.validator,
			}
			h := Middleware(cfg)(sentinelHandler(&ran))

			req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{}"))
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if ran {
				t.Fatal("next handler ran for a rejected oauth request")
			}
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
			}
			got := rec.Header().Get("WWW-Authenticate")
			if got != tt.wantHeader {
				t.Fatalf("WWW-Authenticate = %q, want %q", got, tt.wantHeader)
			}
		})
	}
}

func TestOAuthInsufficientScope(t *testing.T) {
	const publicURL = "https://mcp.example.com"
	wantChallenge := `Bearer error="insufficient_scope", resource_metadata="` + publicURL + `/.well-known/oauth-protected-resource"`
	wantUnauthorizedChallenge := `Bearer error="invalid_token", resource_metadata="` + publicURL + `/.well-known/oauth-protected-resource"`

	tests := []struct {
		name       string
		validator  stubValidator
		wantCode   int
		wantHeader string
	}{
		{
			name:       "insufficient scope returns 403 with error=insufficient_scope",
			validator:  stubValidator{err: ErrInsufficientScope},
			wantCode:   http.StatusForbidden,
			wantHeader: wantChallenge,
		},
		{
			name:       "wrapped ErrInsufficientScope is still detected via errors.Is",
			validator:  stubValidator{err: fmt.Errorf("checkScopes: %w", ErrInsufficientScope)},
			wantCode:   http.StatusForbidden,
			wantHeader: wantChallenge,
		},
		{
			name:       "other validator errors still return 401 with error=invalid_token",
			validator:  stubValidator{err: errValidatorRejected},
			wantCode:   http.StatusUnauthorized,
			wantHeader: wantUnauthorizedChallenge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ran bool
			cfg := Config{
				Mode:             "oauth",
				PublicURL:        publicURL,
				MaxBodyBytes:     1024,
				MaxStreamSeconds: 30,
				Validator:        tt.validator,
			}
			h := Middleware(cfg)(sentinelHandler(&ran))

			req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{}"))
			req.Header.Set("Authorization", "Bearer whatever-the-stub-rejects")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if ran {
				t.Fatal("next handler ran for a rejected oauth request")
			}
			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantCode)
			}
			got := rec.Header().Get("WWW-Authenticate")
			if got != tt.wantHeader {
				t.Fatalf("WWW-Authenticate = %q, want %q", got, tt.wantHeader)
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

// TestStreamDeadlineRealTransport (Covers T1) proves the stream deadline
// bounds a real long-lived transport, not only the synthetic
// <-r.Context().Done() handler TestStreamDeadline uses: it mounts a real
// server.NewSSEServer through Mount with a short MaxStreamSeconds, opens the
// SSE stream, and asserts the connection closes near that bound rather than
// staying open indefinitely. A pre-merge review probe observed closure at
// 1.002s for a 1-second bound; this promotes that probe to a permanent
// regression test. The bound is kept small so the test itself stays fast.
func TestStreamDeadlineRealTransport(t *testing.T) {
	mcpSrv := server.NewMCPServer("test", "0.0.0")
	sseSrv := server.NewSSEServer(mcpSrv)

	cfg := Config{Mode: "none", MaxBodyBytes: 1024, MaxStreamSeconds: 1}
	handler := Mount(cfg, sseSrv)

	ts := httptest.NewServer(handler)
	defer ts.Close()

	start := time.Now()
	resp, err := http.Get(ts.URL + "/sse")
	if err != nil {
		t.Fatalf("GET /sse: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	// No events are ever sent on this stream; a working deadline is the
	// only thing that ends it. io.Copy blocks until the server closes the
	// connection (io.EOF) or the test's own timeout below fires.
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, resp.Body)
		done <- err
	}()

	select {
	case err := <-done:
		elapsed := time.Since(start)
		// The connection is cut mid-response (the deadline firing while a
		// chunked body is still open), not closed cleanly, so the client
		// sees io.ErrUnexpectedEOF rather than a plain io.EOF; either is
		// exactly what proves the deadline closed the connection.
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("reading stream: %v (elapsed %v)", err, elapsed)
		}
		if elapsed < 900*time.Millisecond {
			t.Fatalf("connection closed too early: elapsed = %v, want ~1s", elapsed)
		}
		if elapsed > 5*time.Second {
			t.Fatalf("connection closed too late: elapsed = %v, want ~1s", elapsed)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("stream deadline did not close a real SSE connection within 10s")
	}
}

// TestPastExpiryRejectedAsInvalidCredential (Covers C3/F5) proves a
// validator-reported expiry that is already in the past — as happens when a
// token is accepted only because it falls within the JWT parser's 60s
// clock-skew leeway (NewOAuthValidator) — is rejected as an invalid
// credential (401) rather than handed to the wrapped handler with an
// already-cancelled context. Before this fix, the handler ran dead and the
// client saw an opaque stream failure instead of a 401 telling it to
// refresh. Uses stubValidator, not a minted JWT, to isolate the deadline
// check in Middleware from OAuthValidator's own leeway handling (covered
// separately by TestOAuthValidatorRejections).
func TestPastExpiryRejectedAsInvalidCredential(t *testing.T) {
	const publicURL = "https://mcp.example.com"
	cfg := Config{
		Mode:             "oauth",
		PublicURL:        publicURL,
		MaxBodyBytes:     1024,
		MaxStreamSeconds: 30,
		Validator:        stubValidator{expiresAt: time.Now().Add(-time.Second)},
	}
	var ran bool
	h := Middleware(cfg)(sentinelHandler(&ran))

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer whatever-the-stub-accepts")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if ran {
		t.Fatal("next handler ran with an already-elapsed deadline")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	wantChallenge := `Bearer error="invalid_token", resource_metadata="` + publicURL + `/.well-known/oauth-protected-resource"`
	if got := rec.Header().Get("WWW-Authenticate"); got != wantChallenge {
		t.Fatalf("WWW-Authenticate = %q, want %q", got, wantChallenge)
	}
}

// unusableConfigCase names one Config that is invalid enough that both
// Middleware and Mount must panic on it rather than silently misconfigure.
type unusableConfigCase struct {
	name string
	cfg  Config
}

// unusableConfigCases is shared by TestMiddlewarePanicsOnUnusableConfig here
// and by the equivalent Mount coverage in metadata_test.go, since both
// exercise the same set of unusable Configs against their respective
// constructors.
func unusableConfigCases() []unusableConfigCase {
	baseCfg := Config{
		PublicURL:        "https://mcp.example.com",
		MaxBodyBytes:     1024,
		MaxStreamSeconds: 30,
	}
	return []unusableConfigCase{
		{
			name: "empty Mode",
			cfg:  baseCfg,
		},
		{
			name: "unknown Mode value",
			cfg:  func() Config { c := baseCfg; c.Mode = "Bearer"; return c }(),
		},
		{
			name: "bearer mode with empty BearerToken",
			cfg:  func() Config { c := baseCfg; c.Mode = "bearer"; return c }(),
		},
		{
			name: "oauth mode with nil Validator",
			cfg:  func() Config { c := baseCfg; c.Mode = "oauth"; return c }(),
		},
	}
}

func TestMiddlewarePanicsOnUnusableConfig(t *testing.T) {
	for _, tt := range unusableConfigCases() {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Error("Middleware did not panic for an unusable config")
				}
			}()
			Middleware(tt.cfg)
		})
	}
}

// TestReadDeadlineUnblocksTricklingBody proves the socket read deadline set by
// Middleware unblocks a handler stuck in r.Body.Read on a trickling chunked
// POST at roughly MaxStreamSeconds. It is the discriminating test for that
// deadline: with the http.NewResponseController block deleted, the handler
// stays blocked and this test fails, because a context deadline alone cannot
// interrupt a blocked Read.
func TestReadDeadlineUnblocksTricklingBody(t *testing.T) {
	unblocked := make(chan time.Duration, 1)
	cfg := Config{Mode: "none", MaxBodyBytes: 1 << 20, MaxStreamSeconds: 1}
	h := Middleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		_, _ = io.Copy(io.Discard, r.Body)
		unblocked <- time.Since(start)
	}))
	ts := httptest.NewServer(h)
	defer ts.Close()

	pr, pw := io.Pipe()
	req, err := http.NewRequest(http.MethodPost, ts.URL, pr)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	go func() {
		_, _ = pw.Write([]byte("x"))
		time.Sleep(8 * time.Second)
		pw.Close()
	}()
	go func() {
		if resp, err := ts.Client().Do(req); err == nil {
			resp.Body.Close()
		}
	}()

	select {
	case d := <-unblocked:
		if d < 500*time.Millisecond || d > 3*time.Second {
			t.Fatalf("handler unblocked after %v, want ~1s", d)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("handler still blocked in r.Body.Read past the 1s deadline")
	}
}
