package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/teslamint/cogvault/internal/config"
	"github.com/teslamint/cogvault/internal/index"
	"github.com/teslamint/cogvault/internal/llm"
	"github.com/teslamint/cogvault/internal/storage"
)

func TestFindTitle(t *testing.T) {
	tests := []struct {
		content string
		want    string
	}{
		{"---\ntitle: My Title\ntype: source\n---\n\nbody", "My Title"},
		{"# Heading Title\nbody", "Heading Title"},
		{"no title here", ""},
		{"", ""},
		{"title:", ""}, // too short to be "title: " + value
	}
	for _, tt := range tests {
		if got := findTitle(tt.content); got != tt.want {
			t.Errorf("findTitle(%q) = %q, want %q", tt.content, got, tt.want)
		}
	}
}

func TestBuildEmbedTextCapsAndTitles(t *testing.T) {
	root := t.TempDir()
	store := storage.NewFSStorage(root, &config.Config{})

	long := strings.Repeat("한", 3000)
	if err := store.Write("p.md", []byte("---\ntitle: T\n---\n\n"+long)); err != nil {
		t.Fatal(err)
	}
	text, err := buildEmbedText(store, "p.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(text, "T\n\n") {
		t.Fatalf("text must start with title, got %q", text)
	}
	runes := []rune(text)
	if len(runes) != len("T\n\n")+embedTextRuneCap {
		t.Fatalf("text length = %d runes, want title + cap", len(runes))
	}
}

func TestBatchEmbedHappySkippedAndFailed(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{}
	store := storage.NewFSStorage(root, cfg)

	if err := store.Write("a.md", []byte("# A\ncontent a")); err != nil {
		t.Fatal(err)
	}
	if err := store.Write("b.md", []byte("# B\ncontent b")); err != nil {
		t.Fatal(err)
	}
	// c.md is listed stale but missing on disk -> skipped.

	// Embedding backend: deterministic unit vectors keyed by first letter.
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

	embedder := llm.NewOllamaEmbedder(srv.URL, "m")

	idx := newIndexForTest(t, root, cfg)
	defer idx.Close()
	if err := idx.InitEmbeddingsTable(); err != nil {
		t.Fatal(err)
	}

	stale := []index.StaleEntry{
		{Path: "a.md", ContentHash: "ha"},
		{Path: "b.md", ContentHash: "hb"},
		{Path: "c.md", ContentHash: "hc"},
	}

	res := batchEmbed(context.Background(), idx, store, embedder, "m", stale, 2)
	if res.embedded != 2 || res.skipped != 1 || res.failed != 0 {
		t.Fatalf("res = %+v, want embedded=2 skipped=1 failed=0", res)
	}
	if _, err := idx.GetEmbedding("a.md", "m"); err != nil {
		t.Fatalf("a.md embedding missing: %v", err)
	}
}

func TestBatchEmbedCountsFailures(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{}
	store := storage.NewFSStorage(root, cfg)
	if err := store.Write("a.md", []byte("# A\ncontent")); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model missing", http.StatusNotFound)
	}))
	defer srv.Close()

	embedder := llm.NewOllamaEmbedder(srv.URL, "missing")

	idx := newIndexForTest(t, root, cfg)
	defer idx.Close()
	if err := idx.InitEmbeddingsTable(); err != nil {
		t.Fatal(err)
	}

	stale := []index.StaleEntry{{Path: "a.md", ContentHash: "ha"}}
	res := batchEmbed(context.Background(), idx, store, embedder, "m", stale, 32)
	if res.failed != 1 || res.embedded != 0 {
		t.Fatalf("res = %+v, want failed=1", res)
	}
}

func TestSimilarFallsBackToFTS(t *testing.T) {
	configPath, wikiDir, _ := testVault(t)
	// searchSimilarFTS ranks by the target's title words. Give the two
	// related pages a shared title token; the unrelated page must not rank.
	// No embedding model is configured, so the FTS path runs.
	writeWikiPage(t, wikiDir, "sources/base.md", "Quantum Computing", "# Quantum Computing\nan introduction")
	writeWikiPage(t, wikiDir, "sources/other.md", "Quantum Computing Advanced", "# Quantum Computing Advanced\nmore depth")
	writeWikiPage(t, wikiDir, "sources/unrelated.md", "Gardening", "# Gardening\ntomato care")

	if _, _, err := executeCommand("init", "--config", configPath); err != nil {
		t.Fatalf("init: %v", err)
	}

	stdout, _, err := executeCommand("similar", "--config", configPath, "sources/base.md")
	if err != nil {
		t.Fatalf("similar: %v", err)
	}
	if !strings.Contains(stdout, "sources/other.md") {
		t.Fatalf("stdout = %q, want other.md as similar page", stdout)
	}
	if strings.Contains(stdout, "Unrelated") {
		t.Fatalf("stdout = %q, unrelated page must not rank", stdout)
	}
}

func TestSimilarNoResults(t *testing.T) {
	configPath, wikiDir, _ := testVault(t)
	writeWikiPage(t, wikiDir, "sources/only.md", "Only", "# Only\nunique words here")

	if _, _, err := executeCommand("init", "--config", configPath); err != nil {
		t.Fatalf("init: %v", err)
	}
	stdout, _, err := executeCommand("similar", "--config", configPath, "sources/only.md")
	if err != nil {
		t.Fatalf("similar: %v", err)
	}
	if !strings.Contains(stdout, "No similar pages found.") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestEmbedRequiresEmbeddingModel(t *testing.T) {
	configPath, _, _ := testVault(t)
	if _, _, err := executeCommand("init", "--config", configPath); err != nil {
		t.Fatalf("init: %v", err)
	}
	_, stderr, err := executeCommand("embed", "--config", configPath)
	if err == nil || !strings.Contains(err.Error()+stderr, "embedding_model not configured") {
		t.Fatalf("err = %v stderr = %q, want embedding_model error", err, stderr)
	}
}

func newIndexForTest(t *testing.T, root string, cfg *config.Config) *index.SQLiteIndex {
	t.Helper()
	idx, err := index.NewSQLiteIndex(root, filepath.Join(t.TempDir(), "idx.db"), cfg)
	if err != nil {
		t.Fatal(err)
	}
	return idx
}

func writeWikiPage(t *testing.T, wikiDir, rel, title, body string) {
	t.Helper()
	abs := filepath.Join(wikiDir, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	page := "---\ntitle: " + title + "\ntype: source\nsource_path: x\ningested_at: 2026-01-01T00:00:00Z\n---\n\n" + body + "\n"
	if err := os.WriteFile(abs, []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBatchEmbedBatchingSplitsRequests(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{}
	store := storage.NewFSStorage(root, cfg)
	for _, n := range []string{"a", "b", "c"} {
		if err := store.Write(n+".md", []byte("# "+strings.ToUpper(n)+"\nbody")); err != nil {
			t.Fatal(err)
		}
	}

	var mu sync.Mutex
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		requests++
		mu.Unlock()
		embs := make([][]float32, len(req.Input))
		for i := range req.Input {
			embs[i] = []float32{1, 0}
		}
		json.NewEncoder(w).Encode(map[string]any{"embeddings": embs})
	}))
	defer srv.Close()

	embedder := llm.NewOllamaEmbedder(srv.URL, "m")

	idx := newIndexForTest(t, root, cfg)
	defer idx.Close()
	if err := idx.InitEmbeddingsTable(); err != nil {
		t.Fatal(err)
	}

	stale := []index.StaleEntry{
		{Path: "a.md", ContentHash: "ha"},
		{Path: "b.md", ContentHash: "hb"},
		{Path: "c.md", ContentHash: "hc"},
	}
	res := batchEmbed(context.Background(), idx, store, embedder, "m", stale, 2)
	if res.embedded != 3 {
		t.Fatalf("embedded = %d, want 3", res.embedded)
	}
	if requests != 2 { // ceil(3/2)
		t.Fatalf("requests = %d, want 2 batches", requests)
	}
}

func TestBatchEmbedAllSkippedSendsNoRequest(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{}
	store := storage.NewFSStorage(root, cfg)

	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float32{}})
	}))
	defer srv.Close()

	embedder := llm.NewOllamaEmbedder(srv.URL, "m")
	idx := newIndexForTest(t, root, cfg)
	defer idx.Close()
	if err := idx.InitEmbeddingsTable(); err != nil {
		t.Fatal(err)
	}

	stale := []index.StaleEntry{{Path: "missing.md", ContentHash: "h"}}
	res := batchEmbed(context.Background(), idx, store, embedder, "m", stale, 32)
	if res.skipped != 1 || requests != 0 {
		t.Fatalf("res=%+v requests=%d, want skipped=1 requests=0", res, requests)
	}
}
