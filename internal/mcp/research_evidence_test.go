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

func TestWikiWriteResearchEvidenceWarnings(t *testing.T) {
	const content = `---
research_evidence:
  version: 1
  extra: preserve-me
  claims:
    - id: claim-1
      text: A test claim.
      kind: empirical
      sources:
        - ref: sources/missing.md
      checks:
        - kind: bibliography
          extra_check: preserve-me-too
      gaps: []
---
Body.
`
	var saved []byte
	store := &mockStorage{
		writeFn: func(_ string, data []byte) error {
			saved = append([]byte(nil), data...)
			return nil
		},
		readFn:   func(string) ([]byte, error) { return saved, nil },
		existsFn: func(string) (bool, error) { return false, nil },
	}
	idx := &mockIndex{addFn: func(string, string, map[string]string) error { return nil }}
	adpt := &mockAdapter{parseFn: func(string, string, bool) (*adapter.Source, error) {
		return &adapter.Source{Frontmatter: map[string]any{}}, nil
	}}
	result, err := handleWikiWrite("/vault", testCfg(), store, idx, adpt)(context.Background(), makeReq(map[string]any{
		"path": "notes/evidence.md", "content": content,
	}))
	if err != nil || result.IsError {
		t.Fatalf("wiki_write should succeed: err=%v result=%+v", err, result)
	}
	var response struct {
		Status   string   `json:"status"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(callToolJSON(t, result), &response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "written" {
		t.Fatalf("status = %q, want written", response.Status)
	}
	if len(response.Warnings) < 3 || response.Warnings[0] != "missing frontmatter field: title" {
		t.Fatalf("pre-existing warnings should come first, got %v", response.Warnings)
	}
	if !hasWarningPrefix(response.Warnings, "[EVID005] claim-1:") || !hasWarningPrefix(response.Warnings, "[EVID004] claim-1:") {
		t.Fatalf("missing research_evidence warnings: %v", response.Warnings)
	}
	if string(saved) != content {
		t.Fatalf("saved page changed: got %q", saved)
	}
	readResult, err := handleWikiRead(store)(context.Background(), makeReq(map[string]any{"path": "notes/evidence.md"}))
	if err != nil || readResult.IsError || len(readResult.Content) == 0 {
		t.Fatalf("wiki_read failed: err=%v result=%+v", err, readResult)
	}
	readContent, err := json.Marshal(readResult.Content[0])
	if err != nil {
		t.Fatal(err)
	}
	var readText struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(readContent, &readText); err != nil {
		t.Fatal(err)
	}
	if readText.Text != content || !strings.Contains(readText.Text, "extra: preserve-me") || !strings.Contains(readText.Text, "extra_check: preserve-me-too") {
		t.Fatalf("wiki_read did not preserve original frontmatter: %s", readText.Text)
	}
}

func TestWikiWriteResearchEvidenceExistingRef(t *testing.T) {
	const content = `---
title: Evidence
research_evidence:
  version: 1
  claims:
    - id: claim-1
      text: A test claim.
      kind: empirical
      sources:
        - ref: sources/a.md
      checks: []
      gaps: []
---
Body.
`
	store := &mockStorage{
		writeFn:  func(string, []byte) error { return nil },
		existsFn: func(path string) (bool, error) { return path == "sources/a.md", nil },
	}
	idx := &mockIndex{addFn: func(string, string, map[string]string) error { return nil }}
	adpt := &mockAdapter{parseFn: func(string, string, bool) (*adapter.Source, error) {
		return &adapter.Source{Frontmatter: map[string]any{"title": "Evidence"}}, nil
	}}
	result, err := handleWikiWrite("/vault", testCfg(), store, idx, adpt)(context.Background(), makeReq(map[string]any{
		"path": "notes/evidence.md", "content": content,
	}))
	if err != nil || result.IsError {
		t.Fatalf("wiki_write should succeed: err=%v result=%+v", err, result)
	}
	var response struct {
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(callToolJSON(t, result), &response); err != nil {
		t.Fatal(err)
	}
	for _, warning := range response.Warnings {
		if strings.HasPrefix(warning, "[EVID") {
			t.Fatalf("existing valid ref produced evidence warning %q", warning)
		}
	}
}

func TestWikiWriteResearchEvidenceRequiresRegularFile(t *testing.T) {
	root := t.TempDir()
	wiki := filepath.Join(root, "wiki")
	if err := os.MkdirAll(filepath.Join(wiki, "sources", "paper.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wiki, "sources", "real.md"), []byte("A real source.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		WikiDir:     wiki,
		DBPath:      filepath.Join(root, "index.db"),
		Adapter:     "markdown",
		Exclude:     []string{},
		ExcludeRead: []string{},
	}
	store := storage.NewFSStorage(wiki, cfg)
	idx, err := index.NewSQLiteIndex(wiki, cfg.DBPath, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	handler := handleWikiWrite(wiki, cfg, store, idx, markdown.New())
	for _, tc := range []struct {
		name       string
		ref        string
		wantEVID04 bool
	}{
		{name: "directory", ref: "sources/paper.md", wantEVID04: true},
		{name: "regular file", ref: "sources/real.md"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := "---\ntitle: Evidence\nresearch_evidence:\n  version: 1\n  claims:\n    - id: claim-1\n      text: A test claim.\n      kind: empirical\n      sources:\n        - ref: " + tc.ref + "\n      checks: []\n      gaps: []\n---\nBody.\n"
			result, err := handler(context.Background(), makeReq(map[string]any{
				"path": "notes/" + tc.name + ".md", "content": content,
			}))
			if err != nil || result.IsError {
				t.Fatalf("wiki_write should succeed: err=%v result=%+v", err, result)
			}
			var response struct {
				Warnings []string `json:"warnings"`
			}
			if err := json.Unmarshal(callToolJSON(t, result), &response); err != nil {
				t.Fatal(err)
			}
			foundEVID04 := hasWarningPrefix(response.Warnings, "[EVID004] claim-1:")
			if foundEVID04 != tc.wantEVID04 {
				t.Fatalf("EVID004 warning presence = %t, want %t; warnings: %v", foundEVID04, tc.wantEVID04, response.Warnings)
			}
			for _, warning := range response.Warnings {
				if strings.HasPrefix(warning, "[EVID") && !strings.HasPrefix(warning, "[EVID004]") {
					t.Fatalf("unexpected evidence warning %q", warning)
				}
			}
		})
	}
}

func TestWikiSearchResearchEvidenceSummaries(t *testing.T) {
	pages := map[string][]byte{
		"recorded.md": []byte(`---
usage_terms:
  scope: test scope
research_evidence:
  version: 1
  claims:
    - id: claim-1
      text: A recorded claim.
      kind: empirical
      sources: []
      checks:
        - kind: bibliography
          result: Checked.
      gaps:
        - kind: verification_pending
          detail: More verification is needed.
          next_step: Verify the source.
---
Recorded.
`),
		"plain.md":   []byte("---\ntitle: Plain\n---\nPlain page.\n"),
		"version.md": []byte("---\nresearch_evidence:\n  version: 2\n  claims: []\n---\nVersion two.\n"),
		"invalid.md": []byte("---\nresearch_evidence:\n  version: 1\n  claims:\n    - text: Missing required id.\n      kind: empirical\n      sources: []\n---\nInvalid.\n"),
	}
	paths := []string{"recorded.md", "plain.md", "version.md", "invalid.md", "unreadable.md"}
	idx := &mockIndex{
		checkConsistencyFn: func(storage.Storage, adapter.Adapter, bool) (int, int, int, error) { return 0, 0, 0, nil },
		searchFn: func(string, int) ([]index.Result, error) {
			results := make([]index.Result, 0, len(paths))
			for _, path := range paths {
				results = append(results, index.Result{Path: path, Title: path, Score: 1})
			}
			return results, nil
		},
	}
	reads := make(map[string]int)
	store := &mockStorage{readFn: func(path string) ([]byte, error) {
		reads[path]++
		content, ok := pages[path]
		if !ok {
			return []byte("private content"), errors.New("read denied")
		}
		return content, nil
	}}
	result, err := handleWikiSearch(testCfg(), store, idx, &mockAdapter{})(context.Background(), makeReq(map[string]any{"query": "evidence"}))
	if err != nil || result.IsError {
		t.Fatalf("wiki_search failed: err=%v result=%+v", err, result)
	}
	type evidenceClaim struct {
		ID     string   `json:"id"`
		Kind   string   `json:"kind"`
		Checks []string `json:"checks"`
		Gaps   []string `json:"gaps"`
	}
	type evidenceSummary struct {
		Status     string          `json:"status"`
		ClaimCount int             `json:"claim_count"`
		Claims     []evidenceClaim `json:"claims"`
		Reason     string          `json:"reason"`
	}
	type searchRow struct {
		Path       string          `json:"path"`
		Title      string          `json:"title"`
		Score      float64         `json:"score"`
		UsageTerms map[string]any  `json:"usage_terms"`
		Evidence   evidenceSummary `json:"research_evidence"`
	}
	var rows []searchRow
	if err := json.Unmarshal(callToolJSON(t, result), &rows); err != nil {
		t.Fatal(err)
	}
	byPath := make(map[string]searchRow, len(rows))
	for _, row := range rows {
		byPath[row.Path] = row
	}
	if len(rows) != len(paths) {
		t.Fatalf("got %d search rows, want %d", len(rows), len(paths))
	}
	recorded := byPath["recorded.md"]
	if recorded.Evidence.Status != "recorded" || recorded.Evidence.ClaimCount != 1 || len(recorded.Evidence.Claims) != 1 || recorded.Evidence.Claims[0].ID != "claim-1" || recorded.Evidence.Claims[0].Kind != "empirical" || len(recorded.Evidence.Claims[0].Checks) != 1 || recorded.Evidence.Claims[0].Checks[0] != "bibliography" || len(recorded.Evidence.Claims[0].Gaps) != 1 || recorded.Evidence.Claims[0].Gaps[0] != "verification_pending" {
		t.Fatalf("recorded summary = %+v", recorded.Evidence)
	}
	if recorded.UsageTerms["scope"] != "test scope" || recorded.Title != "recorded.md" || recorded.Score != 1 {
		t.Fatalf("existing search metadata changed: %+v", recorded)
	}
	if plain := byPath["plain.md"]; plain.Evidence.Status != "absent" || plain.Evidence.Claims == nil {
		t.Fatalf("plain page summary = %+v, want absent with empty claims", plain.Evidence)
	}
	for _, path := range []string{"version.md", "invalid.md"} {
		if got := byPath[path].Evidence; got.Status != "unavailable" {
			t.Errorf("%s status = %q, want unavailable", path, got.Status)
		}
	}
	if got := byPath["unreadable.md"].Evidence; got.Status != "unavailable" || got.Reason != "page could not be read" {
		t.Errorf("unreadable summary = %+v", got)
	}
	for _, path := range paths {
		if reads[path] != 1 {
			t.Errorf("store.Read(%q) called %d times, want once", path, reads[path])
		}
	}
}

func hasWarningPrefix(warnings []string, prefix string) bool {
	for _, warning := range warnings {
		if strings.HasPrefix(warning, prefix) {
			return true
		}
	}
	return false
}
