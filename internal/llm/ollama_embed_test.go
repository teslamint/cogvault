package llm

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOllamaEmbedderEmbed(t *testing.T) {
	dim := 4
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.Error(w, "not found", 404)
			return
		}
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if req.Model != "test-model" {
			t.Errorf("model = %q, want test-model", req.Model)
		}

		embeddings := make([][]float32, len(req.Input))
		for i := range req.Input {
			v := make([]float32, dim)
			for j := range v {
				v[j] = float32(i+1) * float32(j+1)
			}
			embeddings[i] = v
		}
		json.NewEncoder(w).Encode(map[string]any{"embeddings": embeddings})
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	e := NewOllamaEmbedder(srv.URL, "test-model")
	vecs, err := e.Embed(context.Background(), []string{"hello", "world"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 2 {
		t.Fatalf("got %d vectors, want 2", len(vecs))
	}
	if len(vecs[0]) != dim {
		t.Fatalf("dims = %d, want %d", len(vecs[0]), dim)
	}
	if e.Dims() != dim {
		t.Fatalf("Dims() = %d, want %d", e.Dims(), dim)
	}

	for _, v := range vecs {
		var norm float64
		for _, x := range v {
			norm += float64(x) * float64(x)
		}
		norm = math.Sqrt(norm)
		if math.Abs(norm-1.0) > 1e-5 {
			t.Errorf("vector not normalized: norm = %f", norm)
		}
	}
}

func TestOllamaEmbedderHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model not found", 404)
	}))
	defer srv.Close()

	e := NewOllamaEmbedder(srv.URL, "missing-model")
	_, err := e.Embed(context.Background(), []string{"test"})
	if err == nil {
		t.Fatal("expected error for HTTP 404")
	}
}

func TestOllamaEmbedderEmpty(t *testing.T) {
	e := NewOllamaEmbedder("http://unused", "model")
	vecs, err := e.Embed(context.Background(), nil)
	if err != nil {
		t.Fatalf("Embed(nil): %v", err)
	}
	if vecs != nil {
		t.Fatalf("expected nil, got %v", vecs)
	}
}

func TestNormalizeVec(t *testing.T) {
	v := []float32{3, 4}
	normalizeVec(v)
	expected := float32(3.0 / 5.0)
	if math.Abs(float64(v[0]-expected)) > 1e-6 {
		t.Errorf("v[0] = %f, want %f", v[0], expected)
	}

	zero := []float32{0, 0, 0}
	normalizeVec(zero)
	for i, x := range zero {
		if x != 0 {
			t.Errorf("zero[%d] = %f, want 0", i, x)
		}
	}
}
