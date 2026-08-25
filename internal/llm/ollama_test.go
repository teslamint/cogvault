package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func ollamaTestServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func TestOllamaDigestSuccess(t *testing.T) {
	srv := ollamaTestServer(t, http.StatusOK, `{"response":"# Page\n\nbody"}`)
	defer srv.Close()

	o := NewOllama(srv.URL, "test-model")
	res, err := o.Digest(context.Background(), DigestRequest{SourcePath: "/tmp/a.pdf"})
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	if res.PageContent != "# Page\n\nbody" {
		t.Fatalf("PageContent = %q", res.PageContent)
	}
	if o.Name() != "ollama" {
		t.Fatalf("Name = %q, want ollama", o.Name())
	}
}

func TestOllamaDigestStripsFence(t *testing.T) {
	resp := map[string]string{"response": "```markdown\n# Page\n\nbody\n```"}
	b, _ := json.Marshal(resp)
	srv := ollamaTestServer(t, http.StatusOK, string(b))
	defer srv.Close()

	o := NewOllama(srv.URL, "test-model")
	res, err := o.Digest(context.Background(), DigestRequest{SourcePath: "/tmp/a.md"})
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	if res.PageContent != "# Page\n\nbody" {
		t.Fatalf("PageContent = %q, want fenced output stripped", res.PageContent)
	}
}

func TestOllamaDigestTransientStatuses(t *testing.T) {
	for _, status := range []int{408, 429, 500, 502, 503, 504} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := ollamaTestServer(t, status, "overloaded")
			defer srv.Close()

			o := NewOllama(srv.URL, "test-model")
			_, err := o.Digest(context.Background(), DigestRequest{SourcePath: "/tmp/a.pdf"})
			if !errors.Is(err, ErrTransient) {
				t.Fatalf("HTTP %d: err = %v, want ErrTransient", status, err)
			}
		})
	}
}

func TestOllamaDigestPermanentStatuses(t *testing.T) {
	for _, status := range []int{400, 404} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := ollamaTestServer(t, status, "bad request")
			defer srv.Close()

			o := NewOllama(srv.URL, "test-model")
			_, err := o.Digest(context.Background(), DigestRequest{SourcePath: "/tmp/a.pdf"})
			if err == nil || errors.Is(err, ErrTransient) {
				t.Fatalf("HTTP %d: err = %v, want permanent error", status, err)
			}
		})
	}
}

func TestOllamaDigestEmptyResponse(t *testing.T) {
	srv := ollamaTestServer(t, http.StatusOK, `{"response":"   "}`)
	defer srv.Close()

	o := NewOllama(srv.URL, "test-model")
	_, err := o.Digest(context.Background(), DigestRequest{SourcePath: "/tmp/a.pdf"})
	if err == nil || !strings.Contains(err.Error(), "empty response") {
		t.Fatalf("err = %v, want empty response error", err)
	}
}

func TestOllamaDefaultBaseURL(t *testing.T) {
	o := NewOllama("", "m")
	if o.baseURL != "http://localhost:11434" {
		t.Fatalf("baseURL = %q", o.baseURL)
	}
}

func TestWithTimeoutPropagates(t *testing.T) {
	o := NewOllama("http://unused", "m", WithTimeout(1500*time.Millisecond))
	if o.client.Timeout != 1500*time.Millisecond {
		t.Fatalf("client.Timeout = %v, want 1.5s", o.client.Timeout)
	}
	// Zero/negative keeps the default.
	o2 := NewOllama("http://unused", "m", WithTimeout(0), WithTimeout(-time.Second))
	if o2.client.Timeout != 5*time.Minute {
		t.Fatalf("client.Timeout = %v, want default 5m", o2.client.Timeout)
	}
}

func TestClaudeCodeWithTimeoutPropagates(t *testing.T) {
	c := NewClaudeCode("claude", "m", WithTimeout(2*time.Second))
	if c.timeout != 2*time.Second {
		t.Fatalf("timeout = %v, want 2s", c.timeout)
	}
	// Zero keeps the digest-time default (field stays zero; digest() falls
	// back to defaultTimeout).
	c0 := NewClaudeCode("claude", "m")
	if c0.timeout != 0 {
		t.Fatalf("timeout = %v, want 0 (default applied at digest time)", c0.timeout)
	}
}
