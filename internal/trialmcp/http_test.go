package trialmcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/teslamint/cogvault/internal/httpauth"
)

func TestHTTPRefusesUnauthenticatedConfiguration(t *testing.T) {
	for _, mode := range []string{"", "none", "invalid", "bearer", "oauth"} {
		_, err := NewHTTPHandler(server.NewMCPServer("test", "1"), httpauth.Config{Mode: mode, PublicURL: "https://trial.example", EndpointPath: "/trial/mcp"})
		if err == nil {
			t.Fatalf("accepted auth mode %q without credentials", mode)
		}
	}
}

func TestHTTPBoundary(t *testing.T) {
	cfg := httpauth.Config{Mode: "bearer", BearerToken: strings.Repeat("t", 32), PublicURL: "https://trial.example", EndpointPath: "/trial/mcp"}
	h, err := NewHTTPHandler(server.NewMCPServer("test", "1"), cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, path, token, origin, body string
		code                            int
	}{
		{name: "missing", path: "/trial/mcp", code: 401},
		{name: "wrong", path: "/trial/mcp", token: "wrong", code: 401},
		{name: "origin", path: "/trial/mcp", token: cfg.BearerToken, origin: "https://foreign.example", code: 403},
		{name: "suffix", path: "/trial/mcp/", token: cfg.BearerToken, code: 404},
		{name: "production", path: "/mcp", token: cfg.BearerToken, code: 404},
		{name: "encoded", path: "/trial/%6dcp", token: cfg.BearerToken, code: 404},
		{name: "query", path: "/trial/mcp?x=1", token: cfg.BearerToken, code: 404},
		{name: "large", path: "/trial/mcp", token: cfg.BearerToken, body: strings.Repeat("x", MaxRequestBytes+1), code: 413},
		{name: "valid", path: "/trial/mcp", token: cfg.BearerToken, origin: "https://trial.example", body: `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`, code: 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Accept", "application/json, text/event-stream")
			if tc.token != "" {
				r.Header.Set("Authorization", "Bearer "+tc.token)
			}
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.code {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestHTTPInvalidRoutesAndPublicURLs(t *testing.T) {
	base := httpauth.Config{Mode: "bearer", BearerToken: strings.Repeat("x", 32), PublicURL: "https://trial.example", EndpointPath: "/trial/mcp"}
	for _, p := range []string{"", "/", "/mcp/", "/mcp/../other", "/mcp//x", "/%6dcp", "/mcp?x", "/.well-known/test", "/mcp\\other", `/mcp"x`, "/mcp;x"} {
		c := base
		c.EndpointPath = p
		if _, err := NewHTTPHandler(server.NewMCPServer("test", "1"), c); err == nil {
			t.Errorf("accepted path %q", p)
		}
	}
	for _, u := range []string{"", "http://localhost", "https://trial.example/", "https://trial.example/path", "https://user:password@trial.example", "https://trial.example?x=1", "https://trial.example#fragment"} {
		c := base
		c.PublicURL = u
		if _, err := NewHTTPHandler(server.NewMCPServer("test", "1"), c); err == nil {
			t.Errorf("accepted URL %q", u)
		}
	}
}

type deniedToken struct{}

func (deniedToken) Validate(context.Context, string) (time.Time, error) {
	return time.Time{}, errors.New("secret infrastructure error")
}

func TestOAuthDiscoveryIsTrialSpecific(t *testing.T) {
	cfg := httpauth.Config{Mode: "oauth", PublicURL: "https://trial.example", EndpointPath: "/trial/mcp", Issuer: "https://issuer.example/", RequiredScopes: []string{"trial:submit", "trial:read"}, Validator: deniedToken{}}
	h, err := NewHTTPHandler(server.NewMCPServer("test", "1"), cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"", "bad"} {
		r := httptest.NewRequest(http.MethodPost, "/trial/mcp", nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := `resource_metadata="https://trial.example/.well-known/oauth-protected-resource/trial/mcp"`
		if w.Code != 401 || !strings.Contains(w.Header().Get("WWW-Authenticate"), want) {
			t.Fatalf("challenge = %d %s", w.Code, w.Header().Get("WWW-Authenticate"))
		}
		if strings.Contains(w.Body.String(), "secret") {
			t.Fatal("validator error leaked")
		}
	}
	for _, tc := range []struct {
		path string
		code int
	}{{"/.well-known/oauth-protected-resource", 404}, {"/.well-known/oauth-protected-resource/trial/mcp", 200}, {"/.well-known/oauth-protected-resource/trial/mcp/extra", 404}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if w.Code != tc.code {
			t.Errorf("%s = %d", tc.path, w.Code)
		}
		if w.Code == 200 && !strings.Contains(w.Body.String(), `"resource":"https://trial.example/trial/mcp"`) {
			t.Errorf("wrong resource metadata: %s", w.Body.String())
		}
	}
}

func TestHTTPUnknownLengthBodyIsBounded(t *testing.T) {
	cfg := httpauth.Config{Mode: "bearer", BearerToken: strings.Repeat("x", 32), PublicURL: "https://trial.example", EndpointPath: "/mcp"}
	h, err := NewHTTPHandler(server.NewMCPServer("test", "1"), cfg)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(strings.Repeat("x", MaxRequestBytes+1)))
	r.ContentLength = -1
	r.Header.Set("Authorization", "Bearer "+cfg.BearerToken)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("accepted oversize chunked body: %d", w.Code)
	}
}

func TestAuthenticatedHTTPSubmitStatusRoundTrip(t *testing.T) {
	s := fixture(t)
	cfg := httpauth.Config{Mode: "bearer", BearerToken: strings.Repeat("x", 32), PublicURL: "https://trial.example", EndpointPath: "/trial/mcp"}
	h, err := NewHTTPHandler(NewServer(s), cfg)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	defer ts.Close()
	c, err := client.NewStreamableHttpClient(ts.URL+cfg.EndpointPath, transport.WithHTTPHeaders(map[string]string{"Authorization": "Bearer " + cfg.BearerToken}))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION, ClientInfo: mcp.Implementation{Name: "test", Version: "1"}}}); err != nil {
		t.Fatal(err)
	}
	list, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil || len(list.Tools) != 4 {
		t.Fatalf("tools: %+v %v", list, err)
	}
	callTool := func(name string, args map[string]any) string {
		t.Helper()
		request := mcp.CallToolRequest{}
		request.Params.Name = name
		request.Params.Arguments = args
		result, err := c.CallTool(ctx, request)
		if err != nil || result.IsError {
			t.Fatalf("%s: %+v %v", name, result, err)
		}
		if len(result.Content) != 1 {
			t.Fatalf("content: %+v", result)
		}
		text, ok := result.Content[0].(mcp.TextContent)
		if !ok {
			t.Fatal("not text content")
		}
		return text.Text
	}
	submitted := callTool("trial_submit", map[string]any{"url": "https://www.rfc-editor.org/rfc/rfc9110.pdf"})
	var result struct {
		Job struct {
			ID string `json:"job_id"`
		} `json:"job"`
		Existing bool `json:"existing"`
	}
	if err := json.Unmarshal([]byte(submitted), &result); err != nil || !jobIDRE.MatchString(result.Job.ID) || result.Existing {
		t.Fatalf("submit: %s %v", submitted, err)
	}
	status := callTool("trial_status", map[string]any{"job_id": result.Job.ID})
	if !strings.Contains(status, `"state":"queued"`) || !strings.Contains(status, result.Job.ID) {
		t.Fatalf("status: %s", status)
	}
	duplicate := callTool("trial_submit", map[string]any{"url": "https://www.rfc-editor.org/rfc/rfc9110.pdf"})
	if !strings.Contains(duplicate, `"existing":true`) {
		t.Fatalf("dedupe: %s", duplicate)
	}
	budget := callTool("trial_status", map[string]any{})
	if !strings.Contains(budget, `"requests_last_day":0`) || !strings.Contains(budget, `"submissions_last_day":1`) {
		t.Fatalf("budget: %s", budget)
	}
}

func TestOAuthDiscoveryMethods(t *testing.T) {
	cfg := httpauth.Config{Mode: "oauth", PublicURL: "https://trial.example", EndpointPath: "/trial/mcp", Issuer: "https://issuer.example/", RequiredScopes: []string{"trial:read"}, Validator: deniedToken{}}
	h, err := NewHTTPHandler(NewServer(fixture(t)), cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method string
		code   int
		allow  string
	}{{http.MethodHead, 200, ""}, {http.MethodPost, 405, "GET, HEAD"}, {http.MethodDelete, 405, "GET, HEAD"}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(tc.method, "/.well-known/oauth-protected-resource/trial/mcp", nil))
		if w.Code != tc.code || w.Body.Len() != 0 || w.Header().Get("Allow") != tc.allow {
			t.Errorf("%s: code=%d body=%q allow=%q", tc.method, w.Code, w.Body.String(), w.Header().Get("Allow"))
		}
	}
}

type insufficientScopeToken struct{}

func (insufficientScopeToken) Validate(context.Context, string) (time.Time, error) {
	return time.Time{}, httpauth.ErrInsufficientScope
}

func TestOAuthInsufficientScopeKeepsTrialChallenge(t *testing.T) {
	cfg := httpauth.Config{Mode: "oauth", PublicURL: "https://trial.example", EndpointPath: "/trial/mcp", Issuer: "https://issuer.example/", RequiredScopes: []string{"trial:read", "trial:submit"}, Validator: insufficientScopeToken{}}
	h, err := NewHTTPHandler(NewServer(fixture(t)), cfg)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/trial/mcp", nil)
	r.Header.Set("Authorization", "Bearer wrong-scope")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	challenge := w.Header().Get("WWW-Authenticate")
	if w.Code != 403 || !strings.Contains(challenge, `error="insufficient_scope"`) || !strings.Contains(challenge, `resource_metadata="https://trial.example/.well-known/oauth-protected-resource/trial/mcp"`) {
		t.Fatalf("code=%d challenge=%s", w.Code, challenge)
	}
}

func TestUnauthorizedHTTPSubmitCannotQueue(t *testing.T) {
	s := fixture(t)
	cfg := httpauth.Config{Mode: "bearer", BearerToken: strings.Repeat("x", 32), PublicURL: "https://trial.example", EndpointPath: "/trial/mcp"}
	h, err := NewHTTPHandler(NewServer(s), cfg)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"trial_submit","arguments":{"url":"https://www.rfc-editor.org/rfc/rfc9110.pdf"}}}`
	for _, token := range []string{"", "wrong"} {
		r := httptest.NewRequest(http.MethodPost, cfg.EndpointPath, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatalf("unauthorized submit: %d", w.Code)
		}
	}
	activity, err := s.A.JobActivity(context.Background(), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if activity.Created != 0 || activity.Active != 0 || activity.Fetches != 0 {
		t.Fatalf("unauthorized request changed registry: %+v", activity)
	}
}
