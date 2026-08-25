package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

// appendConfig appends extra YAML (e.g. an llm: block) to the config file.
func appendConfig(t *testing.T, configPath, extra string) {
	t.Helper()
	f, err := os.OpenFile(configPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(extra); err != nil {
		t.Fatal(err)
	}
}

// runEmbed happy path through the CLI: config with embedding_model, a fake
// embedding backend, and one stale page.
func TestRunEmbedHappyPath(t *testing.T) {
	configPath, wikiDir, _ := testVault(t)
	writeWikiPage(t, wikiDir, "sources/a.md", "A", "# A\ncontent a")

	// Embedding backend serving deterministic unit vectors.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		embs := make([][]float32, len(req.Input))
		for i := range req.Input {
			embs[i] = []float32{1, 0}
		}
		json.NewEncoder(w).Encode(map[string]any{"embeddings": embs})
	}))
	defer srv.Close()

	appendConfig(t, configPath, "llm:\n  embedding_model: test-emb\n  embedding_base_url: "+srv.URL+"\n")

	stdout, _, err := executeCommand("embed", "--config", configPath)
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if !strings.Contains(stdout, "embed: 1 embedded, 0 skipped, 0 failed, 1 total") {
		t.Fatalf("stdout = %q", stdout)
	}

	// Second run: nothing stale.
	stdout2, _, err := executeCommand("embed", "--config", configPath)
	if err != nil {
		t.Fatalf("embed 2: %v", err)
	}
	if !strings.Contains(stdout2, "all embeddings up to date") {
		t.Fatalf("stdout2 = %q", stdout2)
	}
}

func TestRunEmbedBatchSizeClamp(t *testing.T) {
	configPath, wikiDir, _ := testVault(t)
	writeWikiPage(t, wikiDir, "sources/a.md", "A", "# A\ncontent a")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		embs := make([][]float32, len(req.Input))
		for i := range req.Input {
			embs[i] = []float32{0, 1}
		}
		json.NewEncoder(w).Encode(map[string]any{"embeddings": embs})
	}))
	defer srv.Close()

	appendConfig(t, configPath, "llm:\n  embedding_model: test-emb\n  embedding_base_url: "+srv.URL+"\n")

	if _, _, err := executeCommand("embed", "--config", configPath, "--batch-size", "0"); err != nil {
		t.Fatalf("embed batch-size 0: %v", err)
	}
	if _, _, err := executeCommand("embed", "--config", configPath, "--batch-size", "-3"); err != nil {
		t.Fatalf("embed batch-size -3: %v", err)
	}
}

// Ingest with Digested>0 triggers postIngestEmbed when embedding_model is
// configured; the fake embedding backend must receive the digested page.
func TestIngestTriggersPostIngestEmbed(t *testing.T) {
	fakeClaudeOnPath(t)
	t.Setenv("CLAUDE_FAKE_MODE", "ok")
	configPath, srcDir, _, _ := setupIngestVault(t)

	var mu sync.Mutex
	embedRequests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		embedRequests += len(req.Input)
		mu.Unlock()
		embs := make([][]float32, len(req.Input))
		for i := range req.Input {
			embs[i] = []float32{1, 0}
		}
		json.NewEncoder(w).Encode(map[string]any{"embeddings": embs})
	}))
	defer srv.Close()

	// Rewrite config with the embedding backend added.
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	amended := string(data) + "llm:\n  embedding_model: test-emb\n  embedding_base_url: " + srv.URL + "\n"
	if err := os.WriteFile(configPath, []byte(amended), 0o644); err != nil {
		t.Fatal(err)
	}

	writeAgedSource(t, srcDir, "one.pdf", "post ingest embed fixture")

	stdout, _, err := executeCommand("ingest", "--config", configPath)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if !strings.Contains(stdout, "post-ingest embed: 1 embedded") {
		t.Fatalf("stdout = %q, want post-ingest embed line", stdout)
	}
	if embedRequests != 1 {
		t.Fatalf("embedRequests = %d, want 1", embedRequests)
	}

	// Second ingest digests nothing; postIngestEmbed must not run (no
	// stale embeddings) — requests stays 1.
	if _, _, err := executeCommand("ingest", "--config", configPath); err != nil {
		t.Fatalf("ingest 2: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if embedRequests != 1 {
		t.Fatalf("embedRequests after second run = %d, want still 1", embedRequests)
	}
}
