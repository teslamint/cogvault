package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/teslamint/cogvault/internal/adapter"
	"github.com/teslamint/cogvault/internal/config"
	cverr "github.com/teslamint/cogvault/internal/errors"
	"github.com/teslamint/cogvault/internal/index"
	"github.com/teslamint/cogvault/internal/storage"
)

type mockStorage struct {
	readFn   func(path string) ([]byte, error)
	writeFn  func(path string, data []byte) error
	listFn   func(prefix string) ([]storage.ListEntry, error)
	existsFn func(path string) (bool, error)
}

func (m *mockStorage) Read(path string) ([]byte, error)          { return m.readFn(path) }
func (m *mockStorage) Write(path string, data []byte) error      { return m.writeFn(path, data) }
func (m *mockStorage) List(prefix string) ([]storage.ListEntry, error) { return m.listFn(prefix) }
func (m *mockStorage) Exists(path string) (bool, error)          { return m.existsFn(path) }

type mockIndex struct {
	addFn              func(path, content string, meta map[string]string) error
	searchFn           func(query string, limit int, scope string) ([]index.Result, error)
	removeFn           func(path string) error
	rebuildFn          func(store storage.Storage, adpt adapter.Adapter) error
	checkConsistencyFn func(store storage.Storage, adpt adapter.Adapter, force bool) (int, int, int, error)
	getMetaFn          func(path string) (*index.FileMeta, error)
	closeFn            func() error
}

func (m *mockIndex) Add(path, content string, meta map[string]string) error { return m.addFn(path, content, meta) }
func (m *mockIndex) Search(query string, limit int, scope string) ([]index.Result, error) { return m.searchFn(query, limit, scope) }
func (m *mockIndex) Remove(path string) error                               { return m.removeFn(path) }
func (m *mockIndex) Rebuild(store storage.Storage, adpt adapter.Adapter) error { return m.rebuildFn(store, adpt) }
func (m *mockIndex) CheckConsistency(store storage.Storage, adpt adapter.Adapter, force bool) (int, int, int, error) { return m.checkConsistencyFn(store, adpt, force) }
func (m *mockIndex) GetMeta(path string) (*index.FileMeta, error) { return m.getMetaFn(path) }
func (m *mockIndex) Close() error                                { return m.closeFn() }

type mockAdapter struct {
	nameFn  func() string
	scanFn  func(root string, exclude []string, fn func(path string) error) error
	parseFn func(root, relPath string, includeContent bool) (*adapter.Source, error)
}

func (m *mockAdapter) Name() string { return m.nameFn() }
func (m *mockAdapter) Scan(root string, exclude []string, fn func(path string) error) error { return m.scanFn(root, exclude, fn) }
func (m *mockAdapter) Parse(root, relPath string, includeContent bool) (*adapter.Source, error) { return m.parseFn(root, relPath, includeContent) }

func testCfg() *config.Config {
	return &config.Config{
		WikiDir:             "_wiki",
		DBPath:              ".cogvault.db",
		Exclude:             []string{".obsidian", ".trash"},
		ExcludeRead:         []string{},
		Adapter:             "obsidian",
		ConsistencyInterval: 5,
	}
}

func callToolJSON(t *testing.T, result *mcp.CallToolResult) json.RawMessage {
	t.Helper()
	if result.IsError {
		t.Fatalf("unexpected tool error: %v", result.Content)
	}
	if len(result.Content) == 0 {
		t.Fatal("empty result content")
	}
	b, err := json.Marshal(result.Content[0])
	if err != nil {
		t.Fatalf("marshal content: %v", err)
	}
	var tc struct{ Text string }
	json.Unmarshal(b, &tc)
	return json.RawMessage(tc.Text)
}

func makeReq(args map[string]any) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: args,
		},
	}
}

func TestMapError(t *testing.T) {
	tests := []struct {
		err  error
		path string
		want string
	}{
		{fmt.Errorf("wrap: %w", cverr.ErrNotFound), "test.md", "not found: test.md"},
		{fmt.Errorf("wrap: %w", cverr.ErrPermission), "secret.md", "access denied: secret.md"},
		{fmt.Errorf("wrap: %w", cverr.ErrTraversal), "../bad", "invalid path: ../bad"},
		{fmt.Errorf("wrap: %w", cverr.ErrSymlink), "link.md", "invalid path: link.md"},
		{fmt.Errorf("wrap: %w", cverr.ErrNotMarkdown), "file.txt", "not a markdown file: file.txt"},
		{fmt.Errorf("unknown error"), "x", "internal error: unknown error"},
	}
	for _, tt := range tests {
		result := mapError(tt.err, tt.path)
		if !result.IsError {
			t.Errorf("expected IsError=true for %v", tt.err)
		}
		b, _ := json.Marshal(result.Content[0])
		var tc struct{ Text string }
		json.Unmarshal(b, &tc)
		if tc.Text != tt.want {
			t.Errorf("mapError(%v, %q) = %q, want %q", tt.err, tt.path, tc.Text, tt.want)
		}
	}
}

func TestHandleWikiRead(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		store := &mockStorage{readFn: func(path string) ([]byte, error) {
			return []byte("hello world"), nil
		}}
		handler := handleWikiRead(store)
		result, err := handler(context.Background(), makeReq(map[string]any{"path": "test.md"}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected tool error")
		}
	})

	t.Run("not found", func(t *testing.T) {
		store := &mockStorage{readFn: func(path string) ([]byte, error) {
			return nil, fmt.Errorf("storage.Read: %w", cverr.ErrNotFound)
		}}
		handler := handleWikiRead(store)
		result, err := handler(context.Background(), makeReq(map[string]any{"path": "missing.md"}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected tool error")
		}
	})

	t.Run("missing path param", func(t *testing.T) {
		store := &mockStorage{}
		handler := handleWikiRead(store)
		result, err := handler(context.Background(), makeReq(map[string]any{}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected tool error for missing path")
		}
	})
}

func TestHandleWikiWrite(t *testing.T) {
	t.Run("success with indexing", func(t *testing.T) {
		var writtenPath string
		var writtenData []byte
		var indexed bool

		store := &mockStorage{
			writeFn: func(path string, data []byte) error {
				writtenPath = path
				writtenData = data
				return nil
			},
		}
		idx := &mockIndex{
			addFn: func(path, content string, meta map[string]string) error {
				indexed = true
				return nil
			},
		}
		adpt := &mockAdapter{
			parseFn: func(root, relPath string, includeContent bool) (*adapter.Source, error) {
				return &adapter.Source{Title: "Test", Frontmatter: map[string]any{"type": "entity"}}, nil
			},
		}

		handler := handleWikiWrite("/vault", testCfg(), store, idx, adpt)
		result, err := handler(context.Background(), makeReq(map[string]any{
			"path":    "_wiki/test.md",
			"content": "# Test",
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatal("unexpected tool error")
		}
		if writtenPath != "_wiki/test.md" {
			t.Errorf("written path = %q", writtenPath)
		}
		if string(writtenData) != "# Test" {
			t.Errorf("written data = %q", writtenData)
		}
		if !indexed {
			t.Error("expected indexing to occur for .md file")
		}
	})

	t.Run("non-md file skips indexing", func(t *testing.T) {
		store := &mockStorage{writeFn: func(string, []byte) error { return nil }}
		idx := &mockIndex{addFn: func(string, string, map[string]string) error {
			t.Fatal("should not index non-md")
			return nil
		}}
		adpt := &mockAdapter{}

		handler := handleWikiWrite("/vault", testCfg(), store, idx, adpt)
		result, err := handler(context.Background(), makeReq(map[string]any{
			"path":    "file.txt",
			"content": "data",
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatal("unexpected tool error")
		}
	})

	t.Run("write response format", func(t *testing.T) {
		store := &mockStorage{writeFn: func(string, []byte) error { return nil }}
		idx := &mockIndex{addFn: func(string, string, map[string]string) error { return nil }}
		adpt := &mockAdapter{parseFn: func(string, string, bool) (*adapter.Source, error) {
			return &adapter.Source{Frontmatter: map[string]any{}}, nil
		}}

		handler := handleWikiWrite("/vault", testCfg(), store, idx, adpt)
		result, err := handler(context.Background(), makeReq(map[string]any{
			"path":    "_wiki/p.md",
			"content": "abc",
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		raw := callToolJSON(t, result)
		var resp struct {
			Status   string   `json:"status"`
			Path     string   `json:"path"`
			Bytes    int      `json:"bytes"`
			Warnings []string `json:"warnings"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if resp.Status != "written" {
			t.Errorf("status = %q, want written", resp.Status)
		}
		if resp.Path != "_wiki/p.md" {
			t.Errorf("path = %q", resp.Path)
		}
		if resp.Bytes != 3 {
			t.Errorf("bytes = %d, want 3", resp.Bytes)
		}
		if resp.Warnings == nil {
			t.Error("warnings should be non-nil empty array")
		}
	})
}

func TestHandleWikiList(t *testing.T) {
	t.Run("with metadata enrichment", func(t *testing.T) {
		store := &mockStorage{
			listFn: func(prefix string) ([]storage.ListEntry, error) {
				return []storage.ListEntry{
					{Path: "_wiki/test.md", Name: "test.md", IsDir: false},
					{Path: "_wiki/sub/", Name: "sub", IsDir: true},
				}, nil
			},
		}
		idx := &mockIndex{
			checkConsistencyFn: func(storage.Storage, adapter.Adapter, bool) (int, int, int, error) {
				return 0, 0, 0, nil
			},
			getMetaFn: func(path string) (*index.FileMeta, error) {
				return &index.FileMeta{Title: "Test Page", Type: "entity"}, nil
			},
		}
		adpt := &mockAdapter{}

		handler := handleWikiList(testCfg(), store, idx, adpt)
		result, err := handler(context.Background(), makeReq(map[string]any{"prefix": "_wiki/"}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		raw := callToolJSON(t, result)
		var entries []map[string]any
		if err := json.Unmarshal(raw, &entries); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(entries) != 2 {
			t.Fatalf("expected 2 entries, got %d", len(entries))
		}
		if entries[0]["title"] != "Test Page" {
			t.Errorf("title = %v", entries[0]["title"])
		}
		if entries[1]["title"] != "" {
			t.Errorf("dir should have empty title, got %v", entries[1]["title"])
		}
	})

	t.Run("empty prefix defaults to dot", func(t *testing.T) {
		var gotPrefix string
		store := &mockStorage{
			listFn: func(prefix string) ([]storage.ListEntry, error) {
				gotPrefix = prefix
				return nil, nil
			},
		}
		idx := &mockIndex{
			checkConsistencyFn: func(storage.Storage, adapter.Adapter, bool) (int, int, int, error) {
				return 0, 0, 0, nil
			},
		}
		handler := handleWikiList(testCfg(), store, idx, &mockAdapter{})
		handler(context.Background(), makeReq(map[string]any{}))
		if gotPrefix != "." {
			t.Errorf("expected prefix '.', got %q", gotPrefix)
		}
	})

	t.Run("systemic consistency error propagates", func(t *testing.T) {
		idx := &mockIndex{
			checkConsistencyFn: func(storage.Storage, adapter.Adapter, bool) (int, int, int, error) {
				return 0, 0, 0, fmt.Errorf("index.CheckConsistency: scan: %w: %w", index.ErrConsistencySystemic, errors.New("disk error"))
			},
		}
		handler := handleWikiList(testCfg(), &mockStorage{}, idx, &mockAdapter{})
		result, err := handler(context.Background(), makeReq(map[string]any{"prefix": "_wiki/"}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected tool error for systemic consistency failure")
		}
	})

	t.Run("per-file consistency error continues", func(t *testing.T) {
		store := &mockStorage{
			listFn: func(string) ([]storage.ListEntry, error) { return nil, nil },
		}
		idx := &mockIndex{
			checkConsistencyFn: func(storage.Storage, adapter.Adapter, bool) (int, int, int, error) {
				return 1, 0, 0, fmt.Errorf("parse file.md: broken frontmatter")
			},
		}
		handler := handleWikiList(testCfg(), store, idx, &mockAdapter{})
		result, err := handler(context.Background(), makeReq(map[string]any{"prefix": "_wiki/"}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatal("per-file errors should not cause tool error")
		}
	})
}

func TestHandleWikiSearch(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		idx := &mockIndex{
			checkConsistencyFn: func(storage.Storage, adapter.Adapter, bool) (int, int, int, error) {
				return 0, 0, 0, nil
			},
			searchFn: func(query string, limit int, scope string) ([]index.Result, error) {
				return []index.Result{{Path: "test.md", Title: "Test", Score: 1.5}}, nil
			},
		}
		handler := handleWikiSearch(testCfg(), &mockStorage{}, idx, &mockAdapter{})
		result, err := handler(context.Background(), makeReq(map[string]any{"query": "test"}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatal("unexpected tool error")
		}
	})

	t.Run("systemic consistency error propagates", func(t *testing.T) {
		idx := &mockIndex{
			checkConsistencyFn: func(storage.Storage, adapter.Adapter, bool) (int, int, int, error) {
				return 0, 0, 0, fmt.Errorf("index.CheckConsistency: scan: %w: %w", index.ErrConsistencySystemic, errors.New("disk error"))
			},
		}
		handler := handleWikiSearch(testCfg(), &mockStorage{}, idx, &mockAdapter{})
		result, err := handler(context.Background(), makeReq(map[string]any{"query": "test"}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected tool error for systemic consistency failure")
		}
	})

	t.Run("per-file consistency error continues", func(t *testing.T) {
		idx := &mockIndex{
			checkConsistencyFn: func(storage.Storage, adapter.Adapter, bool) (int, int, int, error) {
				return 1, 0, 0, fmt.Errorf("parse broken.md: bad frontmatter")
			},
			searchFn: func(query string, limit int, scope string) ([]index.Result, error) {
				return []index.Result{{Path: "test.md"}}, nil
			},
		}
		handler := handleWikiSearch(testCfg(), &mockStorage{}, idx, &mockAdapter{})
		result, err := handler(context.Background(), makeReq(map[string]any{"query": "test"}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatal("per-file errors should not cause tool error")
		}
	})

	t.Run("limit clamping", func(t *testing.T) {
		var gotLimit int
		idx := &mockIndex{
			checkConsistencyFn: func(storage.Storage, adapter.Adapter, bool) (int, int, int, error) { return 0, 0, 0, nil },
			searchFn: func(query string, limit int, scope string) ([]index.Result, error) {
				gotLimit = limit
				return nil, nil
			},
		}
		handler := handleWikiSearch(testCfg(), &mockStorage{}, idx, &mockAdapter{})
		handler(context.Background(), makeReq(map[string]any{"query": "x", "limit": 999}))
		if gotLimit != 100 {
			t.Errorf("limit = %d, want 100", gotLimit)
		}
	})
}

func TestHandleWikiScan(t *testing.T) {
	t.Run("full scan", func(t *testing.T) {
		adpt := &mockAdapter{
			scanFn: func(root string, exclude []string, fn func(string) error) error {
				fn("notes/a.md")
				fn("_wiki/b.md")
				return nil
			},
		}
		handler := handleWikiScan("/vault", testCfg(), adpt)
		result, err := handler(context.Background(), makeReq(map[string]any{}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		raw := callToolJSON(t, result)
		var paths []string
		json.Unmarshal(raw, &paths)
		if len(paths) != 2 {
			t.Errorf("expected 2 paths, got %d", len(paths))
		}
	})

	t.Run("dir filter", func(t *testing.T) {
		root := t.TempDir()
		os.MkdirAll(filepath.Join(root, "notes", "sub"), 0755)

		adpt := &mockAdapter{
			scanFn: func(r string, exclude []string, fn func(string) error) error {
				fn("notes/a.md")
				fn("notes/sub/b.md")
				fn("_wiki/c.md")
				return nil
			},
		}
		handler := handleWikiScan(root, testCfg(), adpt)
		result, err := handler(context.Background(), makeReq(map[string]any{"dir": "notes"}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		raw := callToolJSON(t, result)
		var paths []string
		json.Unmarshal(raw, &paths)
		if len(paths) != 2 {
			t.Errorf("expected 2 paths under notes/, got %d: %v", len(paths), paths)
		}
	})

	t.Run("nonexistent dir returns ErrNotFound", func(t *testing.T) {
		root := t.TempDir()
		handler := handleWikiScan(root, testCfg(), &mockAdapter{})
		result, _ := handler(context.Background(), makeReq(map[string]any{"dir": "nonexistent"}))
		if !result.IsError {
			t.Fatal("expected error for nonexistent dir")
		}
		b, _ := json.Marshal(result.Content[0])
		var tc struct{ Text string }
		json.Unmarshal(b, &tc)
		if tc.Text != "not found: nonexistent" {
			t.Errorf("error = %q, want 'not found: nonexistent'", tc.Text)
		}
	})

	t.Run("file path returns ErrNotFound", func(t *testing.T) {
		root := t.TempDir()
		os.WriteFile(filepath.Join(root, "file.md"), []byte("x"), 0644)
		handler := handleWikiScan(root, testCfg(), &mockAdapter{})
		result, _ := handler(context.Background(), makeReq(map[string]any{"dir": "file.md"}))
		if !result.IsError {
			t.Fatal("expected error for file path")
		}
	})

	t.Run("traversal rejected", func(t *testing.T) {
		handler := handleWikiScan("/tmp", testCfg(), &mockAdapter{})
		result, _ := handler(context.Background(), makeReq(map[string]any{"dir": "../etc"}))
		if !result.IsError {
			t.Fatal("expected error for path traversal")
		}
	})

	t.Run("absolute path rejected", func(t *testing.T) {
		handler := handleWikiScan("/tmp", testCfg(), &mockAdapter{})
		result, _ := handler(context.Background(), makeReq(map[string]any{"dir": "/etc"}))
		if !result.IsError {
			t.Fatal("expected error for absolute path")
		}
	})
}

func TestHandleWikiParse(t *testing.T) {
	t.Run("without content", func(t *testing.T) {
		adpt := &mockAdapter{
			parseFn: func(root, relPath string, includeContent bool) (*adapter.Source, error) {
				return &adapter.Source{
					Path:        relPath,
					Title:       "My Note",
					Content:     "full body text",
					Frontmatter: map[string]any{"type": "entity"},
					Links:       []string{"other"},
					Tags:        []string{"tag1"},
					SourceType:  "obsidian",
				}, nil
			},
		}
		handler := handleWikiParse("/vault", adpt)
		result, err := handler(context.Background(), makeReq(map[string]any{
			"path":            "notes/test.md",
			"include_content": false,
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		raw := callToolJSON(t, result)
		var src map[string]any
		json.Unmarshal(raw, &src)
		if src["title"] != "My Note" {
			t.Errorf("title = %v", src["title"])
		}
		if _, ok := src["content"]; ok {
			t.Error("content field should be omitted when include_content=false")
		}
	})

	t.Run("with content", func(t *testing.T) {
		adpt := &mockAdapter{
			parseFn: func(root, relPath string, includeContent bool) (*adapter.Source, error) {
				s := &adapter.Source{Path: relPath, Title: "Note"}
				if includeContent {
					s.Content = "body"
				}
				return s, nil
			},
		}
		handler := handleWikiParse("/vault", adpt)
		result, _ := handler(context.Background(), makeReq(map[string]any{
			"path":            "n.md",
			"include_content": true,
		}))
		raw := callToolJSON(t, result)
		var src map[string]any
		json.Unmarshal(raw, &src)
		if src["content"] != "body" {
			t.Errorf("content = %v, want 'body'", src["content"])
		}
	})

	t.Run("non-markdown rejected", func(t *testing.T) {
		handler := handleWikiParse("/vault", &mockAdapter{})
		result, _ := handler(context.Background(), makeReq(map[string]any{"path": "file.txt"}))
		if !result.IsError {
			t.Fatal("expected error for non-markdown file")
		}
	})
}

func TestSchemaInstructions(t *testing.T) {
	t.Run("short schema", func(t *testing.T) {
		store := &mockStorage{
			readFn: func(path string) ([]byte, error) {
				return []byte("# Schema\nRules here."), nil
			},
		}
		result := schemaInstructions(testCfg(), store)
		if result != "# Schema\nRules here." {
			t.Errorf("unexpected: %q", result)
		}
	})

	t.Run("truncation", func(t *testing.T) {
		long := make([]rune, 2500)
		for i := range long {
			long[i] = 'A'
		}
		store := &mockStorage{
			readFn: func(path string) ([]byte, error) {
				return []byte(string(long)), nil
			},
		}
		result := schemaInstructions(testCfg(), store)
		runes := []rune(result)
		if len(runes) <= maxSchemaLen {
			t.Error("expected truncation message appended")
		}
		if !containsSubstring(result, "wiki_read") {
			t.Error("truncated result should reference wiki_read")
		}
	})

	t.Run("read failure fallback", func(t *testing.T) {
		store := &mockStorage{
			readFn: func(path string) ([]byte, error) {
				return nil, fmt.Errorf("read: %w", cverr.ErrNotFound)
			},
		}
		result := schemaInstructions(testCfg(), store)
		if result == "" {
			t.Error("fallback should not be empty")
		}
		if !containsSubstring(result, "_wiki") {
			t.Error("fallback should reference wiki_dir")
		}
	})

	t.Run("uses cfg.SchemaPath dynamically", func(t *testing.T) {
		cfg := &config.Config{
			WikiDir:             "my_wiki",
			DBPath:              ".cogvault.db",
			Adapter:             "obsidian",
			ConsistencyInterval: 5,
		}
		store := &mockStorage{
			readFn: func(path string) ([]byte, error) {
				return nil, cverr.ErrNotFound
			},
		}
		result := schemaInstructions(cfg, store)
		if !containsSubstring(result, "my_wiki") {
			t.Error("fallback should use configured wiki_dir")
		}
	})
}

func containsSubstring(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsCheck(s, sub))
}

func containsCheck(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
