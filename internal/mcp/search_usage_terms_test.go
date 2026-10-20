package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/teslamint/cogvault/internal/adapter"
	"github.com/teslamint/cogvault/internal/adapter/markdown"
	"github.com/teslamint/cogvault/internal/config"
	"github.com/teslamint/cogvault/internal/index"
	"github.com/teslamint/cogvault/internal/storage"
)

func TestWikiSearchShowsSharingConditionsWithoutDetailLookup(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{WikiDir: root, DBPath: filepath.Join(t.TempDir(), "index.db"), Adapter: "markdown"}
	store := storage.NewFSStorage(root, cfg)
	content, err := os.ReadFile(filepath.Join("..", "..", "testdata", "terms-review-demo", "wiki", "search-ready.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Write("example.md", content); err != nil {
		t.Fatal(err)
	}
	idx, err := index.NewSQLiteIndex(root, cfg.DBPath, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	handler := handleWikiSearch(cfg, store, idx, markdown.New())
	result, err := handler(context.Background(), makeReq(map[string]any{"query": "search-terms-demo"}))
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Path       string `json:"path"`
		UsageTerms struct {
			Scope      string `json:"scope"`
			Where      string `json:"where"`
			Level      string `json:"level"`
			Conditions string `json:"conditions"`
			Status     string `json:"status"`
			Evidence   struct {
				Reference string `json:"reference"`
			} `json:"evidence"`
		} `json:"usage_terms"`
	}
	if err := json.Unmarshal(callToolJSON(t, result), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Path != "example.md" {
		t.Fatalf("search did not return the fictional source: %+v", rows)
	}
	got := rows[0].UsageTerms
	if got.Scope != "this fictional summary only; not the original" || got.Where != "external exercise reviewers" || got.Level != "synthetic summary only" || got.Conditions != "attribution required; redistribution of the fictional original prohibited" || got.Evidence.Reference != "fictional-policy.md#synthetic-summaries" || got.Status != "recorded" {
		t.Fatalf("MCP search omitted or changed declared sharing information: %+v", got)
	}
}

func TestWikiSearchDoesNotExposeMetadataFromFailedRead(t *testing.T) {
	idx := &mockIndex{
		checkConsistencyFn: func(storage.Storage, adapter.Adapter, bool) (int, int, int, error) { return 0, 0, 0, nil },
		searchFn:           func(string, int) ([]index.Result, error) { return []index.Result{{Path: "unreadable.md"}}, nil },
	}
	store := &mockStorage{readFn: func(string) ([]byte, error) {
		return []byte("---\nusage_terms:\n  conditions: private-metadata-sentinel\n---\n"), errors.New("read denied")
	}}
	result, err := handleWikiSearch(testCfg(), store, idx, &mockAdapter{})(context.Background(), makeReq(map[string]any{"query": "example"}))
	if err != nil {
		t.Fatal(err)
	}
	data := callToolJSON(t, result)
	var rows []struct {
		UsageTerms map[string]any `json:"usage_terms"`
	}
	if err := json.Unmarshal(data, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].UsageTerms["status"] != "unverified" || rows[0].UsageTerms["conditions"] != "unverified" || strings.Contains(string(data), "private-metadata-sentinel") {
		t.Fatalf("failed read did not produce a safe unknown summary: %s", data)
	}
}
