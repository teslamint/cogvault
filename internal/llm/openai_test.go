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

func TestInputModesAndSourceText(t *testing.T) {
	if NewClaudeCode("claude", "").InputMode() != PathInput {
		t.Fatal("ClaudeCode must use path input")
	}
	if NewOllama("http://localhost:11434", "m").InputMode() != PathInput {
		t.Fatal("Ollama must use path input")
	}
	if NewOpenAI("http://127.0.0.1:1/v1", "m").InputMode() != TextInput {
		t.Fatal("OpenAI must use text input")
	}
}

type countingRoundTripper struct {
	calls int
}

func (rt *countingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	rt.calls++
	return nil, errors.New("unexpected outbound request")
}

func TestOpenAIDigestRejectsRemoteEndpointBeforeRequest(t *testing.T) {
	rt := &countingRoundTripper{}
	a := NewOpenAI("http://203.0.113.10/v1", "m")
	a.client.Transport = rt

	_, err := a.Digest(context.Background(), DigestRequest{SourceText: "source"})
	if err == nil {
		t.Fatal("expected remote endpoint rejection")
	}
	if rt.calls != 0 {
		t.Fatalf("outbound requests=%d, want 0", rt.calls)
	}
}

func TestOpenAIDigestAcceptsLoopbackEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"# Page"}}]}`))
	}))
	defer srv.Close()

	res, err := NewOpenAI(srv.URL, "m").Digest(context.Background(), DigestRequest{SourceText: "source"})
	if err != nil {
		t.Fatal(err)
	}
	if res.PageContent != "# Page" {
		t.Fatalf("content=%q", res.PageContent)
	}
}

func TestOpenAIDigestPostsCompleteTextAndMetadata(t *testing.T) {
	var gotPath string
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"choices\":[{\"message\":{\"role\":\"assistant\",\"content\":\"```markdown\\n# Page\\n```\"}}]}"))
	}))
	defer srv.Close()
	a := NewOpenAI(srv.URL+"/v1/", "model")
	res, err := a.Digest(context.Background(), DigestRequest{SourcePath: "/private/a.pdf", SourceExt: ".pdf", SchemaText: "SCHEMA", PageSlug: "slug", SourceText: "complete source text"})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("path=%q", gotPath)
	}
	if res.PageContent != "# Page" {
		t.Fatalf("content=%q", res.PageContent)
	}
	b, _ := json.Marshal(got)
	s := string(b)
	if !strings.Contains(s, "complete source text") || !strings.Contains(s, "/private/a.pdf") || !strings.Contains(s, "SCHEMA") || !strings.Contains(s, "slug") {
		t.Fatalf("request missing fields: %s", s)
	}
	if strings.Contains(s, "Read the PDF file at path") {
		t.Fatalf("request contains path-read instruction: %s", s)
	}
}

func TestOpenAIHTTPClassification(t *testing.T) {
	for _, tc := range []struct {
		code      int
		transient bool
	}{{408, true}, {429, true}, {500, true}, {503, true}, {400, false}, {404, false}} {
		t.Run(http.StatusText(tc.code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(`{"error":{"message":"bad"}}`))
			}))
			defer srv.Close()
			_, err := NewOpenAI(srv.URL, "m").Digest(context.Background(), DigestRequest{SourceText: "x"})
			if err == nil || errors.Is(err, ErrTransient) != tc.transient {
				t.Fatalf("err=%v transient=%v", err, tc.transient)
			}
		})
	}
}

func TestOpenAIUnloadedModelMessageTransient(t *testing.T) {
	for _, msg := range []string{"No model loaded. Call POST /inference/load first.", "model is not loaded"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"` + msg + `"}}`))
		}))
		_, err := NewOpenAI(srv.URL, "m").Digest(context.Background(), DigestRequest{SourceText: "x"})
		srv.Close()
		if err == nil || !errors.Is(err, ErrTransient) {
			t.Fatalf("message %q err=%v", msg, err)
		}
	}
}

func TestOpenAIRejectsMissingSourceText(t *testing.T) {
	a := NewOpenAI("http://127.0.0.1:1/v1", "m")
	_, err := a.Digest(context.Background(), DigestRequest{SourcePath: "/x.pdf"})
	if err == nil {
		t.Fatal("expected missing source text error")
	}
}

func TestOpenAIReady(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"m","loaded":true}]}`))
	}))
	defer srv.Close()
	if err := CheckOpenAIReady(context.Background(), srv.URL+"/v1/", "m"); err != nil {
		t.Fatal(err)
	}
}

func TestOpenAIReadyMissingOrUnloaded(t *testing.T) {
	for _, body := range []string{`{"data":[{"id":"other"}]}`, `{"data":[{"id":"m","loaded":false}]}`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
		err := CheckOpenAIReady(context.Background(), srv.URL+"/v1", "m")
		srv.Close()
		if err == nil {
			t.Fatal("expected readiness error")
		}
	}
}

func TestOpenAICanonicalURLAndRedirectDisabled(t *testing.T) {
	redirected := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected = true
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"bad"}}]}`))
	}))
	defer target.Close()
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redir.Close()
	_, err := NewOpenAI(redir.URL+"/v1/", "m").Digest(context.Background(), DigestRequest{SourceText: "x"})
	if err == nil || redirected {
		t.Fatalf("redirect should fail without follow-on request: err=%v redirected=%v", err, redirected)
	}
}

func TestOpenAIReadyDeadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := CheckOpenAIReady(ctx, srv.URL, "m"); err == nil || !errors.Is(err, ErrTransient) {
		t.Fatalf("err=%v", err)
	}
}
