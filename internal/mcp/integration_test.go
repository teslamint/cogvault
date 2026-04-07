package mcp_test

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/server"
	"github.com/teslamint/cogvault/internal/adapter/obsidian"
	"github.com/teslamint/cogvault/internal/config"
	"github.com/teslamint/cogvault/internal/index"
	"github.com/teslamint/cogvault/internal/mcp"
	"github.com/teslamint/cogvault/internal/storage"
)

// copyFixture copies a fixture directory to a temp dir and returns the root.
func copyFixture(t *testing.T, fixturePath string) string {
	t.Helper()
	root := t.TempDir()
	err := filepath.WalkDir(fixturePath, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, _ := filepath.Rel(fixturePath, path)
		dest := filepath.Join(root, rel)
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copyFixture: %v", err)
	}
	return root
}

// setupIntegrationWithConfig creates a vault with a custom config.
func setupIntegrationWithConfig(t *testing.T, cfg *config.Config) (string, *server.MCPServer) {
	t.Helper()
	root := t.TempDir()

	os.MkdirAll(filepath.Join(root, cfg.WikiDir), 0o755)

	store := storage.NewFSStorage(root, cfg)
	dbPath := filepath.Join(root, cfg.DBPath)
	idx, err := index.NewSQLiteIndex(root, dbPath, cfg)
	if err != nil {
		t.Fatalf("NewSQLiteIndex: %v", err)
	}
	t.Cleanup(func() { idx.Close() })

	adpt := obsidian.New()
	s := mcp.NewServer(root, cfg, store, idx, adpt)
	return root, s
}

// setupRealVault copies the real/ fixture, creates index, and runs Rebuild.
func setupRealVault(t *testing.T) (string, *server.MCPServer) {
	t.Helper()
	root := copyFixture(t, "../../testdata/fixtures/real")

	cfg := &config.Config{
		WikiDir:             "_wiki",
		DBPath:              ".cogvault.db",
		Exclude:             []string{".obsidian", ".trash"},
		ExcludeRead:         []string{},
		Adapter:             "obsidian",
		ConsistencyInterval: 0,
	}

	store := storage.NewFSStorage(root, cfg)
	dbPath := filepath.Join(root, cfg.DBPath)
	idx, err := index.NewSQLiteIndex(root, dbPath, cfg)
	if err != nil {
		t.Fatalf("NewSQLiteIndex: %v", err)
	}
	t.Cleanup(func() { idx.Close() })

	adpt := obsidian.New()
	if err := idx.Rebuild(store, adpt); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	s := mcp.NewServer(root, cfg, store, idx, adpt)
	return root, s
}

// --- 3a. Eventual Consistency ---

func TestIntegration_WriteNonMdNotIndexed(t *testing.T) {
	_, s := setupIntegration(t)

	// Write a .txt file
	result := callTool(t, s, "wiki_write", map[string]any{
		"path":    "_wiki/data.txt",
		"content": "some text content with unique keyword xyznonmd",
	})
	if result.IsError {
		t.Fatalf("wiki_write error: %s", extractText(t, result))
	}

	// Search should not find it (non-.md not indexed)
	searchResult := callTool(t, s, "wiki_search", map[string]any{
		"query": "xyznonmd",
	})
	searchText := extractText(t, searchResult)
	var results []map[string]any
	json.Unmarshal([]byte(searchText), &results)
	if len(results) != 0 {
		t.Errorf("expected no search results for non-md file, got %d", len(results))
	}
}

func TestIntegration_ExternalChangeDetected(t *testing.T) {
	root, s := setupIntegration(t)

	// Write file directly to filesystem (bypass MCP)
	dir := filepath.Join(root, "_wiki", "external")
	os.MkdirAll(dir, 0o755)
	content := "---\ntitle: External File\n---\n# External File\n\nContent with externaldetect keyword."
	os.WriteFile(filepath.Join(root, "_wiki", "external", "ext.md"), []byte(content), 0o644)

	// wiki_list triggers CheckConsistency, which should detect the new file
	listResult := callTool(t, s, "wiki_list", map[string]any{
		"prefix": "_wiki/external",
	})
	if listResult.IsError {
		t.Fatalf("wiki_list error: %s", extractText(t, listResult))
	}
	listText := extractText(t, listResult)
	var entries []map[string]any
	json.Unmarshal([]byte(listText), &entries)

	found := false
	for _, e := range entries {
		if e["name"] == "ext.md" {
			found = true
			if e["title"] != "External File" {
				t.Errorf("title = %v, want External File", e["title"])
			}
		}
	}
	if !found {
		t.Error("external file not detected by CheckConsistency via wiki_list")
	}
}

func TestIntegration_DeletedFileRemovedAfterConsistency(t *testing.T) {
	root, s := setupIntegration(t)

	// Write a file via MCP
	content := "---\ntitle: Deletable\n---\n# Deletable\n\nContent with deletableunique keyword."
	writeResult := callTool(t, s, "wiki_write", map[string]any{
		"path":    "_wiki/deletable.md",
		"content": content,
	})
	if writeResult.IsError {
		t.Fatalf("wiki_write error: %s", extractText(t, writeResult))
	}

	// Verify it's searchable
	searchResult := callTool(t, s, "wiki_search", map[string]any{
		"query": "deletableunique",
	})
	searchText := extractText(t, searchResult)
	var results []map[string]any
	json.Unmarshal([]byte(searchText), &results)
	if len(results) == 0 {
		t.Fatal("expected search results before deletion")
	}

	// Delete file directly from filesystem
	os.Remove(filepath.Join(root, "_wiki", "deletable.md"))

	// Search again — CheckConsistency should detect deletion
	searchResult2 := callTool(t, s, "wiki_search", map[string]any{
		"query": "deletableunique",
	})
	searchText2 := extractText(t, searchResult2)
	var results2 []map[string]any
	json.Unmarshal([]byte(searchText2), &results2)
	if len(results2) != 0 {
		t.Errorf("expected no results after deletion, got %d", len(results2))
	}
}

// --- 3b. Security at MCP Boundary ---

func TestIntegration_Security_WriteSchema(t *testing.T) {
	_, s := setupIntegration(t)

	result := callTool(t, s, "wiki_write", map[string]any{
		"path":    "_wiki/_schema.md",
		"content": "modified schema",
	})
	if !result.IsError {
		t.Fatal("expected error for schema write")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "access denied") {
		t.Errorf("error = %q, want 'access denied'", text)
	}
}

func TestIntegration_Security_WriteOutsideWikiDir(t *testing.T) {
	_, s := setupIntegration(t)

	result := callTool(t, s, "wiki_write", map[string]any{
		"path":    "notes/test.md",
		"content": "outside wiki dir",
	})
	if !result.IsError {
		t.Fatal("expected error for write outside wiki_dir")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "access denied") {
		t.Errorf("error = %q, want 'access denied'", text)
	}
}

func TestIntegration_Security_ReadTraversal(t *testing.T) {
	_, s := setupIntegration(t)

	result := callTool(t, s, "wiki_read", map[string]any{
		"path": "../etc/passwd",
	})
	if !result.IsError {
		t.Fatal("expected error for traversal read")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "invalid path") {
		t.Errorf("error = %q, want 'invalid path'", text)
	}
}

func TestIntegration_Security_ReadExcludeRead(t *testing.T) {
	cfg := &config.Config{
		WikiDir:             "_wiki",
		DBPath:              ".cogvault.db",
		Exclude:             []string{".obsidian", ".trash"},
		ExcludeRead:         []string{"private"},
		Adapter:             "obsidian",
		ConsistencyInterval: 0,
	}
	root, s := setupIntegrationWithConfig(t, cfg)

	// Create a file in the excluded directory
	privDir := filepath.Join(root, "private")
	os.MkdirAll(privDir, 0o755)
	os.WriteFile(filepath.Join(privDir, "secret.md"), []byte("# Secret\n\nConfidential."), 0o644)

	result := callTool(t, s, "wiki_read", map[string]any{
		"path": "private/secret.md",
	})
	if !result.IsError {
		t.Fatal("expected error for exclude_read path")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "access denied") {
		t.Errorf("error = %q, want 'access denied'", text)
	}
}

func TestIntegration_Security_Symlink(t *testing.T) {
	root, s := setupIntegration(t)

	// Create a target file outside wiki
	target := filepath.Join(root, "outside.md")
	os.WriteFile(target, []byte("# Outside\n\nShould not be accessible."), 0o644)

	// Create symlink inside wiki dir
	link := filepath.Join(root, "_wiki", "symlink.md")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlink creation not supported:", err)
	}

	result := callTool(t, s, "wiki_read", map[string]any{
		"path": "_wiki/symlink.md",
	})
	if !result.IsError {
		t.Fatal("expected error for symlink read")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "invalid path") {
		t.Errorf("error = %q, want 'invalid path'", text)
	}
}

func TestIntegration_Security_ExcludeNotInSearch(t *testing.T) {
	cfg := &config.Config{
		WikiDir:             "_wiki",
		DBPath:              ".cogvault.db",
		Exclude:             []string{".obsidian", ".trash", "private"},
		ExcludeRead:         []string{},
		Adapter:             "obsidian",
		ConsistencyInterval: 0,
	}
	root, s := setupIntegrationWithConfig(t, cfg)

	// Create file in excluded path
	privDir := filepath.Join(root, "private")
	os.MkdirAll(privDir, 0o755)
	os.WriteFile(filepath.Join(privDir, "hidden.md"), []byte("---\ntitle: Hidden\n---\n# Hidden\n\nexcludesearchunique content."), 0o644)

	// Also create a findable file in wiki
	os.WriteFile(filepath.Join(root, "_wiki", "findable.md"), []byte("---\ntitle: Findable\n---\n# Findable\n\nexcludesearchunique content."), 0o644)

	// Search — excluded file should not appear, wiki file should
	searchResult := callTool(t, s, "wiki_search", map[string]any{
		"query": "excludesearchunique",
	})
	searchText := extractText(t, searchResult)
	var results []map[string]any
	json.Unmarshal([]byte(searchText), &results)

	for _, r := range results {
		path, _ := r["path"].(string)
		if strings.HasPrefix(path, "private/") {
			t.Errorf("excluded file appeared in search results: %s", path)
		}
	}
	foundFindable := false
	for _, r := range results {
		if r["path"] == "_wiki/findable.md" {
			foundFindable = true
		}
	}
	if !foundFindable {
		t.Error("expected _wiki/findable.md in search results")
	}
}

// --- 3c. Ingest Workflow ---

func TestIntegration_IngestWorkflow(t *testing.T) {
	root, s := setupIntegration(t)

	// Pre-populate a notes file on disk
	notesDir := filepath.Join(root, "notes")
	os.MkdirAll(notesDir, 0o755)
	noteContent := "---\ntitle: Source Note\ntags: [research]\n---\n# Source Note\n\nImportant information about ingestworkflow topic.\n\nSee also [[other-note]].\n"
	os.WriteFile(filepath.Join(notesDir, "source.md"), []byte(noteContent), 0o644)

	// Step 1: wiki_scan("notes/")
	scanResult := callTool(t, s, "wiki_scan", map[string]any{
		"dir": "notes",
	})
	scanText := extractText(t, scanResult)
	var scanPaths []string
	json.Unmarshal([]byte(scanText), &scanPaths)
	if len(scanPaths) == 0 {
		t.Fatal("wiki_scan returned no paths")
	}
	foundSource := false
	for _, p := range scanPaths {
		if p == "notes/source.md" {
			foundSource = true
		}
	}
	if !foundSource {
		t.Errorf("wiki_scan did not find notes/source.md: %v", scanPaths)
	}

	// Step 2: wiki_parse(source.md, include_content=true)
	parseResult := callTool(t, s, "wiki_parse", map[string]any{
		"path":            "notes/source.md",
		"include_content": true,
	})
	parseText := extractText(t, parseResult)
	var parsed map[string]any
	json.Unmarshal([]byte(parseText), &parsed)
	if parsed["title"] != "Source Note" {
		t.Errorf("parse title = %v", parsed["title"])
	}
	if links, ok := parsed["links"].([]any); !ok || len(links) == 0 {
		t.Error("expected links in parsed result")
	}
	if parsed["content"] == nil || parsed["content"] == "" {
		t.Error("expected content with include_content=true")
	}

	// Step 3: wiki_search(keyword, "wiki") — empty (no wiki pages yet)
	searchResult := callTool(t, s, "wiki_search", map[string]any{
		"query": "ingestworkflow",
		"scope": "wiki",
	})
	searchText := extractText(t, searchResult)
	var searchResults []map[string]any
	json.Unmarshal([]byte(searchText), &searchResults)
	if len(searchResults) != 0 {
		t.Errorf("expected empty wiki search before write, got %d results", len(searchResults))
	}

	// Step 4: wiki_write source page
	sourcePageContent := "---\ntitle: Source Note (Source)\ntype: source\nsource_path: notes/source.md\ningested_at: \"2024-01-15\"\n---\n# Source Note (Source)\n\n## 요약\n\nImportant information about ingestworkflow topic.\n\n## 핵심 포인트\n\n- Key finding about the topic\n\n## 관련 페이지\n\n- (none yet)\n"
	writeResult := callTool(t, s, "wiki_write", map[string]any{
		"path":    "_wiki/sources/source-note.md",
		"content": sourcePageContent,
	})
	if writeResult.IsError {
		t.Fatalf("wiki_write error: %s", extractText(t, writeResult))
	}

	// Step 5: wiki_read the source page back
	readResult := callTool(t, s, "wiki_read", map[string]any{
		"path": "_wiki/sources/source-note.md",
	})
	if readResult.IsError {
		t.Fatalf("wiki_read error: %s", extractText(t, readResult))
	}
	readContent := extractText(t, readResult)
	if readContent != sourcePageContent {
		t.Error("wiki_read content does not match written content")
	}

	// Step 6: wiki_write updated content
	updatedContent := strings.Replace(sourcePageContent, "- (none yet)", "- [[related-entity]]", 1)
	updateResult := callTool(t, s, "wiki_write", map[string]any{
		"path":    "_wiki/sources/source-note.md",
		"content": updatedContent,
	})
	if updateResult.IsError {
		t.Fatalf("wiki_write update error: %s", extractText(t, updateResult))
	}

	// Verify updated content is searchable
	searchResult2 := callTool(t, s, "wiki_search", map[string]any{
		"query": "ingestworkflow",
		"scope": "wiki",
	})
	searchText2 := extractText(t, searchResult2)
	var searchResults2 []map[string]any
	json.Unmarshal([]byte(searchText2), &searchResults2)
	if len(searchResults2) == 0 {
		t.Error("expected search results after write")
	}
}

// --- 3d. Real Vault Fixture ---

func TestIntegration_RealVault_InitAndSearch(t *testing.T) {
	_, s := setupRealVault(t)

	// Search Korean content (4+ chars)
	searchResult := callTool(t, s, "wiki_search", map[string]any{
		"query": "프로젝트",
	})
	searchText := extractText(t, searchResult)
	var results []map[string]any
	json.Unmarshal([]byte(searchText), &results)
	if len(results) == 0 {
		t.Fatal("expected search results for Korean query '프로젝트'")
	}

	// Verify Korean file is in results
	foundKorean := false
	for _, r := range results {
		path, _ := r["path"].(string)
		if strings.Contains(path, "프로젝트") {
			foundKorean = true
		}
	}
	if !foundKorean {
		t.Error("expected Korean file in search results")
	}

	// 2-char Korean search (LIKE fallback)
	searchResult2 := callTool(t, s, "wiki_search", map[string]any{
		"query": "프로",
	})
	searchText2 := extractText(t, searchResult2)
	var results2 []map[string]any
	json.Unmarshal([]byte(searchText2), &results2)
	if len(results2) == 0 {
		t.Error("expected results for 2-char Korean query '프로' (LIKE fallback)")
	}
}

func TestIntegration_RealVault_ScopeFiltering(t *testing.T) {
	_, s := setupRealVault(t)

	// scope=wiki should only return _wiki/ files
	wikiResult := callTool(t, s, "wiki_search", map[string]any{
		"query": "LLM",
		"scope": "wiki",
	})
	wikiText := extractText(t, wikiResult)
	var wikiResults []map[string]any
	json.Unmarshal([]byte(wikiText), &wikiResults)
	for _, r := range wikiResults {
		path, _ := r["path"].(string)
		if !strings.HasPrefix(path, "_wiki/") {
			t.Errorf("wiki scope returned non-wiki path: %s", path)
		}
	}

	// scope=vault should only return non-wiki files
	vaultResult := callTool(t, s, "wiki_search", map[string]any{
		"query": "LLM",
		"scope": "vault",
	})
	vaultText := extractText(t, vaultResult)
	var vaultResults []map[string]any
	json.Unmarshal([]byte(vaultText), &vaultResults)
	for _, r := range vaultResults {
		path, _ := r["path"].(string)
		if strings.HasPrefix(path, "_wiki/") {
			t.Errorf("vault scope returned wiki path: %s", path)
		}
	}

	// Both should have results (LLM appears in both wiki and vault)
	if len(wikiResults) == 0 {
		t.Error("expected wiki results for 'LLM'")
	}
	if len(vaultResults) == 0 {
		t.Error("expected vault results for 'LLM'")
	}
}

func TestIntegration_RealVault_ParseMetadata(t *testing.T) {
	root, s := setupRealVault(t)

	tests := []struct {
		path       string
		title      string
		sourceType string
		wantLinks  []string
		wantTags   []string
	}{
		{
			path:       "notes/프로젝트-개요.md",
			title:      "프로젝트 개요",
			sourceType: "obsidian",
			wantLinks:  []string{"meeting-2024-01", "research/llm-patterns"},
			wantTags:   []string{"project", "planning"},
		},
		{
			path:       "notes/meeting-2024-01.md",
			title:      "Meeting 2024-01",
			sourceType: "obsidian",
			wantLinks:  []string{"프로젝트-개요"},
			wantTags:   []string{"meeting"},
		},
		{
			path:       "_wiki/entities/llm.md",
			title:      "LLM",
			sourceType: "obsidian",
			wantLinks:  []string{"knowledge-graph"},
			wantTags:   []string{"entity", "llm"},
		},
	}

	_ = root // root used by setupRealVault
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := callTool(t, s, "wiki_parse", map[string]any{
				"path":            tt.path,
				"include_content": false,
			})
			if result.IsError {
				t.Fatalf("wiki_parse error: %s", extractText(t, result))
			}
			text := extractText(t, result)
			var parsed map[string]any
			json.Unmarshal([]byte(text), &parsed)

			if parsed["title"] != tt.title {
				t.Errorf("title = %v, want %v", parsed["title"], tt.title)
			}
			if parsed["source_type"] != tt.sourceType {
				t.Errorf("source_type = %v, want %v", parsed["source_type"], tt.sourceType)
			}

			// Check links
			links, _ := parsed["links"].([]any)
			for _, want := range tt.wantLinks {
				found := false
				for _, l := range links {
					if l == want {
						found = true
					}
				}
				if !found {
					t.Errorf("missing link %q in %v", want, links)
				}
			}

			// Check tags
			tags, _ := parsed["tags"].([]any)
			for _, want := range tt.wantTags {
				found := false
				for _, tag := range tags {
					if tag == want {
						found = true
					}
				}
				if !found {
					t.Errorf("missing tag %q in %v", want, tags)
				}
			}
		})
	}
}

func TestIntegration_RealVault_ListWithMeta(t *testing.T) {
	_, s := setupRealVault(t)

	listResult := callTool(t, s, "wiki_list", map[string]any{
		"prefix": "_wiki/sources",
	})
	if listResult.IsError {
		t.Fatalf("wiki_list error: %s", extractText(t, listResult))
	}
	listText := extractText(t, listResult)
	var entries []map[string]any
	json.Unmarshal([]byte(listText), &entries)

	found := false
	for _, e := range entries {
		name, _ := e["name"].(string)
		if strings.Contains(name, "프로젝트") {
			found = true
			if e["title"] != "프로젝트 개요 (Source)" {
				t.Errorf("title = %v", e["title"])
			}
			if e["type"] != "source" {
				t.Errorf("type = %v", e["type"])
			}
		}
	}
	if !found {
		t.Errorf("source file not found in list: %v", entries)
	}
}

func TestIntegration_RealVault_CrossLanguageLinks(t *testing.T) {
	_, s := setupRealVault(t)

	// Korean note links to English
	korResult := callTool(t, s, "wiki_parse", map[string]any{
		"path": "notes/프로젝트-개요.md",
	})
	korText := extractText(t, korResult)
	var korParsed map[string]any
	json.Unmarshal([]byte(korText), &korParsed)
	korLinks, _ := korParsed["links"].([]any)
	foundEn := false
	for _, l := range korLinks {
		if l == "meeting-2024-01" {
			foundEn = true
		}
	}
	if !foundEn {
		t.Error("Korean note should link to English 'meeting-2024-01'")
	}

	// English note links to Korean
	enResult := callTool(t, s, "wiki_parse", map[string]any{
		"path": "notes/meeting-2024-01.md",
	})
	enText := extractText(t, enResult)
	var enParsed map[string]any
	json.Unmarshal([]byte(enText), &enParsed)
	enLinks, _ := enParsed["links"].([]any)
	foundKo := false
	for _, l := range enLinks {
		if l == "프로젝트-개요" {
			foundKo = true
		}
	}
	if !foundKo {
		t.Error("English note should link to Korean '프로젝트-개요'")
	}
}

// --- 3e. Race Detection ---

func TestIntegration_Race_ConcurrentWriteAndSearch(t *testing.T) {
	_, s := setupIntegration(t)

	var wg sync.WaitGroup
	const n = 10

	// Concurrent writes
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			callTool(t, s, "wiki_write", map[string]any{
				"path":    fmt.Sprintf("_wiki/race-ws-%d.md", i),
				"content": fmt.Sprintf("---\ntitle: Race %d\n---\n# Race %d\n\nRace content.", i, i),
			})
		}(i)
	}

	// Concurrent searches
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			callTool(t, s, "wiki_search", map[string]any{
				"query": "Race content",
			})
		}()
	}

	wg.Wait()
}

func TestIntegration_Race_ConcurrentListAndWrite(t *testing.T) {
	_, s := setupIntegration(t)

	var wg sync.WaitGroup
	const n = 10

	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			callTool(t, s, "wiki_write", map[string]any{
				"path":    fmt.Sprintf("_wiki/race-lw-%d.md", i),
				"content": fmt.Sprintf("---\ntitle: Race LW %d\n---\n# Content", i),
			})
		}(i)
	}

	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			callTool(t, s, "wiki_list", map[string]any{
				"prefix": "_wiki",
			})
		}()
	}

	wg.Wait()
}
