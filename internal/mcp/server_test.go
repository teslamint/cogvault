package mcp_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	gomcp "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/teslamint/cogvault/internal/adapter/obsidian"
	"github.com/teslamint/cogvault/internal/config"
	"github.com/teslamint/cogvault/internal/index"
	"github.com/teslamint/cogvault/internal/mcp"
	"github.com/teslamint/cogvault/internal/storage"
)

func setupIntegration(t *testing.T) (string, *server.MCPServer) {
	t.Helper()
	root := t.TempDir()

	cfg := &config.Config{
		WikiDir:             "_wiki",
		DBPath:              ".cogvault.db",
		Exclude:             []string{".obsidian", ".trash"},
		ExcludeRead:         []string{},
		Adapter:             "obsidian",
		ConsistencyInterval: 0,
	}

	os.MkdirAll(filepath.Join(root, "_wiki"), 0755)

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

func callTool(t *testing.T, s *server.MCPServer, toolName string, args map[string]any) *gomcp.CallToolResult {
	t.Helper()
	reqMsg := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      toolName,
			"arguments": args,
		},
	}
	raw, _ := json.Marshal(reqMsg)

	initMsg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      0,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":   map[string]any{},
			"clientInfo": map[string]any{
				"name":    "test",
				"version": "0.0.1",
			},
		},
	})

	ctx := context.Background()
	s.HandleMessage(ctx, initMsg)

	resp := s.HandleMessage(ctx, raw)

	respJSON, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	var rpcResp struct {
		Result *gomcp.CallToolResult `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respJSON, &rpcResp); err != nil {
		t.Fatalf("unmarshal rpc response: %v\nraw: %s", err, respJSON)
	}
	if rpcResp.Error != nil {
		t.Fatalf("RPC error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	if rpcResp.Result == nil {
		t.Fatalf("nil result in response: %s", respJSON)
	}
	return rpcResp.Result
}

func extractText(t *testing.T, result *gomcp.CallToolResult) string {
	t.Helper()
	if len(result.Content) == 0 {
		t.Fatal("empty content")
	}
	b, _ := json.Marshal(result.Content[0])
	var tc struct{ Text string }
	json.Unmarshal(b, &tc)
	return tc.Text
}

func TestRoundTrip(t *testing.T) {
	_, s := setupIntegration(t)

	content := `---
title: "Test Entity"
type: entity
tags: [go, mcp]
---
# Test Entity

This is a [[related-note]] about MCP servers.
Links to ![[diagram.png]].
`

	// 1. wiki_write
	writeResult := callTool(t, s, "wiki_write", map[string]any{
		"path":    "_wiki/entities/test-entity.md",
		"content": content,
	})
	if writeResult.IsError {
		t.Fatalf("wiki_write error: %s", extractText(t, writeResult))
	}
	text := extractText(t, writeResult)
	var writeResp map[string]any
	json.Unmarshal([]byte(text), &writeResp)
	if writeResp["status"] != "written" {
		t.Errorf("write status = %v", writeResp["status"])
	}

	// 2. wiki_read
	readResult := callTool(t, s, "wiki_read", map[string]any{
		"path": "_wiki/entities/test-entity.md",
	})
	if readResult.IsError {
		t.Fatalf("wiki_read error: %s", extractText(t, readResult))
	}
	readContent := extractText(t, readResult)
	if readContent != content {
		t.Errorf("read content mismatch:\ngot:  %q\nwant: %q", readContent, content)
	}

	// 3. wiki_search
	searchResult := callTool(t, s, "wiki_search", map[string]any{
		"query": "MCP servers",
		"scope": "wiki",
	})
	if searchResult.IsError {
		t.Fatalf("wiki_search error: %s", extractText(t, searchResult))
	}
	searchText := extractText(t, searchResult)
	var searchResults []map[string]any
	json.Unmarshal([]byte(searchText), &searchResults)
	if len(searchResults) == 0 {
		t.Fatal("wiki_search returned no results")
	}
	if searchResults[0]["path"] != "_wiki/entities/test-entity.md" {
		t.Errorf("search path = %v", searchResults[0]["path"])
	}

	// 4. wiki_list
	listResult := callTool(t, s, "wiki_list", map[string]any{
		"prefix": "_wiki/entities",
	})
	if listResult.IsError {
		t.Fatalf("wiki_list error: %s", extractText(t, listResult))
	}
	listText := extractText(t, listResult)
	var listEntries []map[string]any
	json.Unmarshal([]byte(listText), &listEntries)
	if len(listEntries) == 0 {
		t.Fatal("wiki_list returned no entries")
	}
	found := false
	for _, e := range listEntries {
		if e["name"] == "test-entity.md" {
			found = true
			if e["title"] != "Test Entity" {
				t.Errorf("list title = %v", e["title"])
			}
			if e["type"] != "entity" {
				t.Errorf("list type = %v", e["type"])
			}
		}
	}
	if !found {
		t.Error("test-entity.md not found in list")
	}

	// 5. wiki_scan
	scanResult := callTool(t, s, "wiki_scan", map[string]any{
		"dir": "_wiki",
	})
	if scanResult.IsError {
		t.Fatalf("wiki_scan error: %s", extractText(t, scanResult))
	}
	scanText := extractText(t, scanResult)
	var scanPaths []string
	json.Unmarshal([]byte(scanText), &scanPaths)
	scanFound := false
	for _, p := range scanPaths {
		if p == "_wiki/entities/test-entity.md" {
			scanFound = true
		}
	}
	if !scanFound {
		t.Errorf("wiki_scan did not find test-entity.md: %v", scanPaths)
	}

	// 6. wiki_parse
	parseResult := callTool(t, s, "wiki_parse", map[string]any{
		"path":            "_wiki/entities/test-entity.md",
		"include_content": true,
	})
	if parseResult.IsError {
		t.Fatalf("wiki_parse error: %s", extractText(t, parseResult))
	}
	parseText := extractText(t, parseResult)
	var parsed map[string]any
	json.Unmarshal([]byte(parseText), &parsed)
	if parsed["title"] != "Test Entity" {
		t.Errorf("parse title = %v", parsed["title"])
	}
	if parsed["source_type"] != "obsidian" {
		t.Errorf("parse source_type = %v", parsed["source_type"])
	}
	if links, ok := parsed["links"].([]any); ok {
		if len(links) == 0 || links[0] != "related-note" {
			t.Errorf("parse links = %v", links)
		}
	} else {
		t.Error("parse links missing")
	}
	if attachments, ok := parsed["attachments"].([]any); ok {
		if len(attachments) == 0 || attachments[0] != "diagram.png" {
			t.Errorf("parse attachments = %v", attachments)
		}
	} else {
		t.Error("parse attachments missing")
	}
	if parsed["content"] == nil || parsed["content"] == "" {
		t.Error("parse content should be present with include_content=true")
	}

	// 7. wiki_parse without content - verify field omission
	parseNoContentResult := callTool(t, s, "wiki_parse", map[string]any{
		"path":            "_wiki/entities/test-entity.md",
		"include_content": false,
	})
	if parseNoContentResult.IsError {
		t.Fatalf("wiki_parse error: %s", extractText(t, parseNoContentResult))
	}
	parseNoContentText := extractText(t, parseNoContentResult)
	var parsedNoContent map[string]any
	json.Unmarshal([]byte(parseNoContentText), &parsedNoContent)
	if _, hasContent := parsedNoContent["content"]; hasContent {
		t.Error("content field should be omitted when include_content=false")
	}
}

func TestRoundTrip_ErrorCases(t *testing.T) {
	_, s := setupIntegration(t)

	t.Run("read nonexistent", func(t *testing.T) {
		result := callTool(t, s, "wiki_read", map[string]any{"path": "nonexistent.md"})
		if !result.IsError {
			t.Fatal("expected error")
		}
		text := extractText(t, result)
		if text != "not found: nonexistent.md" {
			t.Errorf("error message = %q", text)
		}
	})

	t.Run("parse non-markdown", func(t *testing.T) {
		result := callTool(t, s, "wiki_parse", map[string]any{"path": "file.txt"})
		if !result.IsError {
			t.Fatal("expected error")
		}
	})

	t.Run("scan traversal", func(t *testing.T) {
		result := callTool(t, s, "wiki_scan", map[string]any{"dir": "../outside"})
		if !result.IsError {
			t.Fatal("expected error for traversal")
		}
	})
}
