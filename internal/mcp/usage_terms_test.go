package mcp

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/teslamint/cogvault/internal/adapter/markdown"
	"github.com/teslamint/cogvault/internal/config"
	"github.com/teslamint/cogvault/internal/index"
	"github.com/teslamint/cogvault/internal/storage"
)

func TestWikiWriteCannotDiscardUsageEvidence(t *testing.T) {
	root := t.TempDir()
	wiki := filepath.Join(root, "wiki")
	if err := os.MkdirAll(wiki, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{WikiDir: wiki, DBPath: filepath.Join(root, "test.db"), Adapter: "markdown"}
	store := storage.NewFSStorage(wiki, cfg)
	idx, err := index.NewSQLiteIndex(wiki, cfg.DBPath, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	handler := handleWikiWrite(wiki, cfg, store, idx, markdown.New())
	// Fictional validation terms only; not evidence for rights to real material.
	original := "---\ntitle: Fictional permission\ntype: source\nusage_terms:\n  status: unverified\n  conditions: Preserve the fictional acknowledgement.\n  evidence:\n    reference: fictional-policy\n    quote: Synthetic permission requires acknowledgement.\n---\nOriginal summary.\n"
	created, err := handler(context.Background(), makeReq(map[string]any{"path": "example.md", "content": original}))
	if err != nil || created.IsError {
		t.Fatalf("initial wiki_write: result=%+v error=%v", created, err)
	}
	replacement := "---\ntitle: Fictional permission\ntype: source\n---\nRewritten summary without its permission evidence.\n"
	result, err := handler(context.Background(), makeReq(map[string]any{"path": "example.md", "content": replacement}))
	if err != nil || !result.IsError {
		t.Fatalf("wiki_write discarded explicit terms: result=%+v error=%v", result, err)
	}
	stored, err := store.Read("example.md")
	if err != nil || string(stored) != original {
		t.Fatalf("rejected write changed old page: %q, %v", stored, err)
	}
	results, err := idx.Search("acknowledgement", 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range results {
		if r.Path == "example.md" {
			found = true
		}
	}
	if !found {
		t.Fatalf("rejected write lost indexed evidence: %+v", results)
	}
	meta, err := idx.GetMeta("example.md")
	if err != nil || meta.ContentHash != fmt.Sprintf("%x", sha256.Sum256([]byte(original))) {
		t.Fatalf("rejected write changed indexed content: meta=%+v error=%v", meta, err)
	}
}
