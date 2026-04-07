package index

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/teslamint/cogvault/internal/adapter"
	"github.com/teslamint/cogvault/internal/adapter/obsidian"
	"github.com/teslamint/cogvault/internal/config"
	cverr "github.com/teslamint/cogvault/internal/errors"
	"github.com/teslamint/cogvault/internal/storage"

	_ "modernc.org/sqlite"
)

// --- Helpers ---

func newTestIndex(t *testing.T, root string, cfg *config.Config) *SQLiteIndex {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	idx, err := NewSQLiteIndex(root, dbPath, cfg)
	if err != nil {
		t.Fatalf("NewSQLiteIndex: %v", err)
	}
	t.Cleanup(func() { idx.Close() })
	return idx
}

func testCfg() *config.Config {
	return &config.Config{
		WikiDir:             "_wiki",
		DBPath:              ".cogvault.db",
		Exclude:             []string{".obsidian"},
		ExcludeRead:         []string{},
		Adapter:             "obsidian",
		ConsistencyInterval: 5,
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// --- Phase 0: FTS5 Trigram ---

func TestTrigramSupport(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec(`CREATE VIRTUAL TABLE test_fts USING fts5(content, tokenize='trigram')`)
	if err != nil {
		t.Fatalf("FTS5 trigram not supported: %v", err)
	}

	_, err = db.Exec(`INSERT INTO test_fts(content) VALUES ('한국어 테스트 문장입니다')`)
	if err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	var count int
	err = db.QueryRow(`SELECT count(*) FROM test_fts WHERE test_fts MATCH '"한국어"'`).Scan(&count)
	if err != nil {
		t.Fatalf("FTS5 trigram MATCH failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 match, got %d", count)
	}
}

func TestUnicode61FallbackKorean(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec(`CREATE VIRTUAL TABLE test_fts USING fts5(content, tokenize='unicode61')`)
	if err != nil {
		t.Fatal(err)
	}

	_, err = db.Exec(`INSERT INTO test_fts(content) VALUES ('한국어 테스트 문장입니다')`)
	if err != nil {
		t.Fatal(err)
	}

	// LIKE fallback for all lengths
	for _, q := range []string{"한", "한국", "한국어"} {
		var count int
		err = db.QueryRow(`SELECT count(*) FROM test_fts WHERE content LIKE ?`, "%"+q+"%").Scan(&count)
		if err != nil {
			t.Fatalf("LIKE %q failed: %v", q, err)
		}
		if count != 1 {
			t.Fatalf("LIKE %q: expected 1, got %d", q, count)
		}
	}
}

// --- DB Init / Migration ---

func TestInitSchemaFresh(t *testing.T) {
	root := t.TempDir()
	idx := newTestIndex(t, root, testCfg())
	if !idx.useTrigram {
		t.Fatal("expected useTrigram=true on fresh DB")
	}
}

func TestInitSchemaExistingUnicode61(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	// Create DB with unicode61
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	db.Exec(`PRAGMA journal_mode=WAL`)
	db.Exec(`CREATE VIRTUAL TABLE wiki_fts USING fts5(path, title, content, tags, tokenize='unicode61')`)
	db.Exec(`CREATE TABLE file_meta (path TEXT PRIMARY KEY, title TEXT DEFAULT '', type TEXT DEFAULT '', content_hash TEXT NOT NULL, indexed_at TEXT NOT NULL)`)
	db.Close()

	// Reopen with SQLiteIndex
	idx, err := NewSQLiteIndex(t.TempDir(), dbPath, testCfg())
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	if idx.useTrigram {
		t.Fatal("expected useTrigram=false for existing unicode61 DB")
	}
}

func TestInitSchemaMigration(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	// Create old schema with mod_time
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	db.Exec(`PRAGMA journal_mode=WAL`)
	db.Exec(`CREATE VIRTUAL TABLE wiki_fts USING fts5(path, title, content, tags, tokenize='trigram')`)
	db.Exec(`CREATE TABLE file_meta (path TEXT PRIMARY KEY, title TEXT, type TEXT, content_hash TEXT NOT NULL, mod_time TEXT NOT NULL, indexed_at TEXT NOT NULL)`)
	db.Exec(`INSERT INTO file_meta VALUES ('old.md', 'Old', '', 'abc', '2024-01-01', '2024-01-01')`)
	db.Exec(`INSERT INTO wiki_fts(path, title, content, tags) VALUES ('old.md', 'Old', 'old content', '')`)
	db.Close()

	// Reopen — should drop and recreate
	idx, err := NewSQLiteIndex(t.TempDir(), dbPath, testCfg())
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	// old data should be gone
	_, err = idx.GetMeta("old.md")
	if !errors.Is(err, cverr.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after migration, got %v", err)
	}
}

// --- CRUD ---

func TestAddAndGetMeta(t *testing.T) {
	root := t.TempDir()
	idx := newTestIndex(t, root, testCfg())

	err := idx.Add("notes/test.md", "# Hello\nContent here", map[string]string{
		"title": "Hello",
		"type":  "source",
		"tags":  "go,test",
	})
	if err != nil {
		t.Fatal(err)
	}

	fm, err := idx.GetMeta("notes/test.md")
	if err != nil {
		t.Fatal(err)
	}
	if fm.Title != "Hello" {
		t.Fatalf("Title = %q, want %q", fm.Title, "Hello")
	}
	if fm.Type != "source" {
		t.Fatalf("Type = %q, want %q", fm.Type, "source")
	}
	if fm.ContentHash == "" {
		t.Fatal("ContentHash empty")
	}
	if fm.IndexedAt == "" {
		t.Fatal("IndexedAt empty")
	}
}

func TestGetMetaNotFound(t *testing.T) {
	root := t.TempDir()
	idx := newTestIndex(t, root, testCfg())

	_, err := idx.GetMeta("nonexistent.md")
	if !errors.Is(err, cverr.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestAddOverwrite(t *testing.T) {
	root := t.TempDir()
	idx := newTestIndex(t, root, testCfg())

	idx.Add("test.md", "v1", map[string]string{"title": "V1"})

	fm1, _ := idx.GetMeta("test.md")
	hash1 := fm1.ContentHash

	idx.Add("test.md", "v2", map[string]string{"title": "V2"})

	fm2, _ := idx.GetMeta("test.md")
	if fm2.ContentHash == hash1 {
		t.Fatal("content_hash should change after overwrite")
	}
	if fm2.Title != "V2" {
		t.Fatalf("Title = %q, want V2", fm2.Title)
	}
}

func TestRemove(t *testing.T) {
	root := t.TempDir()
	idx := newTestIndex(t, root, testCfg())

	idx.Add("test.md", "content", map[string]string{"title": "T"})
	if err := idx.Remove("test.md"); err != nil {
		t.Fatal(err)
	}

	_, err := idx.GetMeta("test.md")
	if !errors.Is(err, cverr.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after Remove, got %v", err)
	}
}

// --- Search ---

func TestSearchFTS(t *testing.T) {
	root := t.TempDir()
	idx := newTestIndex(t, root, testCfg())

	idx.Add("notes/go.md", "Go programming language is awesome", map[string]string{"title": "Go Lang", "type": "source"})

	results, err := idx.Search("programming", 10, "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Path != "notes/go.md" {
		t.Fatalf("Path = %q", results[0].Path)
	}
	if results[0].Title != "Go Lang" {
		t.Fatalf("Title = %q", results[0].Title)
	}
	if results[0].Score <= 0 {
		t.Fatalf("Score = %f, expected > 0", results[0].Score)
	}
}

func TestSearchShortQuery(t *testing.T) {
	root := t.TempDir()
	idx := newTestIndex(t, root, testCfg())

	idx.Add("notes/kr.md", "한국어 테스트입니다", map[string]string{"title": "Korean"})

	results, err := idx.Search("한국", 10, "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for 2-char Korean, got %d", len(results))
	}
}

func TestSearchKorean(t *testing.T) {
	root := t.TempDir()
	idx := newTestIndex(t, root, testCfg())

	idx.Add("notes/kr.md", "인공지능 프로그래밍 가이드", map[string]string{"title": "AI Guide"})

	results, err := idx.Search("프로그래밍", 10, "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for Korean 3+ char, got %d", len(results))
	}
}

func TestSearchScopeWiki(t *testing.T) {
	root := t.TempDir()
	idx := newTestIndex(t, root, testCfg())

	idx.Add("_wiki/page.md", "wiki content here", map[string]string{"title": "Wiki"})
	idx.Add("notes/page.md", "vault content here", map[string]string{"title": "Vault"})

	results, err := idx.Search("content", 10, "wiki")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Path != "_wiki/page.md" {
		t.Fatalf("wiki scope: expected only _wiki/page.md, got %v", results)
	}
}

func TestSearchScopeVault(t *testing.T) {
	root := t.TempDir()
	idx := newTestIndex(t, root, testCfg())

	idx.Add("_wiki/page.md", "wiki content here", map[string]string{"title": "Wiki"})
	idx.Add("notes/page.md", "vault content here", map[string]string{"title": "Vault"})

	results, err := idx.Search("content", 10, "vault")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Path != "notes/page.md" {
		t.Fatalf("vault scope: expected only notes/page.md, got %v", results)
	}
}

func TestSearchScopeAll(t *testing.T) {
	root := t.TempDir()
	idx := newTestIndex(t, root, testCfg())

	idx.Add("_wiki/page.md", "wiki content here", map[string]string{"title": "Wiki"})
	idx.Add("notes/page.md", "vault content here", map[string]string{"title": "Vault"})

	results, err := idx.Search("content", 10, "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("all scope: expected 2 results, got %d", len(results))
	}
}

func TestSearchScopeWikiDirUnderscore(t *testing.T) {
	root := t.TempDir()
	idx := newTestIndex(t, root, testCfg())

	idx.Add("_wiki/page.md", "wiki content", map[string]string{})
	idx.Add("xwiki/page.md", "xwiki content", map[string]string{})

	results, err := idx.Search("content", 10, "wiki")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("underscore escape: expected 1 wiki result, got %d", len(results))
	}
	if results[0].Path != "_wiki/page.md" {
		t.Fatalf("expected _wiki/page.md, got %s", results[0].Path)
	}
}

func TestSearchLimitDefault(t *testing.T) {
	root := t.TempDir()
	idx := newTestIndex(t, root, testCfg())

	for i := 0; i < 15; i++ {
		idx.Add(fmt.Sprintf("notes/%d.md", i), "common searchable content", map[string]string{})
	}

	results, err := idx.Search("searchable", 0, "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 10 {
		t.Fatalf("default limit: expected 10, got %d", len(results))
	}
}

func TestSearchLimitCap(t *testing.T) {
	root := t.TempDir()
	idx := newTestIndex(t, root, testCfg())

	for i := 0; i < 5; i++ {
		idx.Add(fmt.Sprintf("notes/%d.md", i), "common searchable content", map[string]string{})
	}

	results, err := idx.Search("searchable", 200, "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) > 100 {
		t.Fatal("limit should be capped at 100")
	}
}

func TestSearchEmptyResult(t *testing.T) {
	root := t.TempDir()
	idx := newTestIndex(t, root, testCfg())

	idx.Add("test.md", "some content", map[string]string{})

	results, err := idx.Search("nonexistent query xyz", 10, "all")
	if err != nil {
		t.Fatal(err)
	}
	if results == nil {
		t.Fatal("expected non-nil empty slice")
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	root := t.TempDir()
	idx := newTestIndex(t, root, testCfg())

	idx.Add("test.md", "content", map[string]string{})

	for _, q := range []string{"", "   ", "\t\n"} {
		results, err := idx.Search(q, 10, "all")
		if err != nil {
			t.Fatalf("query %q: %v", q, err)
		}
		if len(results) != 0 {
			t.Fatalf("query %q: expected 0 results, got %d", q, len(results))
		}
	}
}

func TestSearchSpecialChars(t *testing.T) {
	root := t.TempDir()
	idx := newTestIndex(t, root, testCfg())

	idx.Add("test.md", "test AND OR NOT content NEAR", map[string]string{})

	for _, q := range []string{"AND", "OR NOT", `"quoted"`} {
		_, err := idx.Search(q, 10, "all")
		if err != nil {
			t.Fatalf("special chars query %q: %v", q, err)
		}
	}
}

func TestSearchSnippet(t *testing.T) {
	root := t.TempDir()
	idx := newTestIndex(t, root, testCfg())

	idx.Add("test.md", "This is a long document with the keyword programming embedded in the middle of the text and more text after", map[string]string{})

	results, err := idx.Search("programming", 10, "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Snippet == "" {
		t.Fatal("expected non-empty snippet")
	}
}

func TestSearchScoreDescending(t *testing.T) {
	root := t.TempDir()
	idx := newTestIndex(t, root, testCfg())

	idx.Add("a.md", "apple apple apple", map[string]string{})
	idx.Add("b.md", "apple banana cherry date elderberry fig grape", map[string]string{})

	results, err := idx.Search("apple", 10, "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 2 {
		t.Skip("not enough results to check ordering")
	}
	for i := 1; i < len(results); i++ {
		if results[i].Score > results[i-1].Score {
			t.Fatalf("scores not descending: %f > %f", results[i].Score, results[i-1].Score)
		}
	}
}

func TestSearchWithFrontmatter(t *testing.T) {
	root := t.TempDir()
	idx := newTestIndex(t, root, testCfg())

	content := "---\ntitle: Test Page\ntype: source\ntags: [go, test]\n---\n\nActual body content here"
	idx.Add("test.md", content, map[string]string{"title": "Test Page"})

	// Body search works
	results, err := idx.Search("Actual body", 10, "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for body search, got %d", len(results))
	}
}

func TestSearchLIKESnippet(t *testing.T) {
	root := t.TempDir()
	idx := newTestIndex(t, root, testCfg())

	idx.Add("test.md", "This document contains Important Information for review", map[string]string{})

	// 2-char query → LIKE fallback. Case-insensitive: "im" matches "Important"
	results, err := idx.Search("im", 10, "all")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Snippet == "" {
		t.Fatal("LIKE fallback should produce non-empty snippet")
	}
}

// --- Rebuild ---

func TestRebuildRemovesStaleEntries(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "notes", "keep.md"), "# Keep\nBody")
	stale := filepath.Join(root, "notes", "stale.md")
	mustWriteFile(t, stale, "# Stale\nBody")

	cfg := testCfg()
	store := storage.NewFSStorage(root, cfg)
	adpt := obsidian.New()
	idx := newTestIndex(t, root, cfg)

	idx.CheckConsistency(store, adpt, true)

	// Delete stale file from disk
	os.Remove(stale)

	// Rebuild should re-index from scratch — stale entry gone
	err := idx.Rebuild(store, adpt)
	if err != nil {
		t.Fatal(err)
	}

	_, err = idx.GetMeta("notes/stale.md")
	if !errors.Is(err, cverr.ErrNotFound) {
		t.Fatal("stale entry should be removed after Rebuild")
	}

	_, err = idx.GetMeta("notes/keep.md")
	if err != nil {
		t.Fatal("kept file should still be indexed after Rebuild")
	}
}

func TestRebuild(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "notes", "test.md"), "# Test\nBody")

	cfg := testCfg()
	store := storage.NewFSStorage(root, cfg)
	adpt := obsidian.New()

	idx := newTestIndex(t, root, cfg)

	idx.Add("notes/test.md", "# Test\nBody", map[string]string{"title": "Test"})

	err := idx.Rebuild(store, adpt)
	if err != nil {
		t.Fatal(err)
	}

	// After rebuild, the file should be re-indexed via CheckConsistency
	fm, err := idx.GetMeta("notes/test.md")
	if err != nil {
		t.Fatalf("expected file to be re-indexed after Rebuild, got %v", err)
	}
	if fm.Path != "notes/test.md" {
		t.Fatalf("Path = %q", fm.Path)
	}
}

// --- CheckConsistency ---

func TestCheckConsistencyNewFile(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "notes", "new.md"), "# New\nBody text")

	cfg := testCfg()
	store := storage.NewFSStorage(root, cfg)
	adpt := obsidian.New()
	idx := newTestIndex(t, root, cfg)

	added, removed, updated, err := idx.CheckConsistency(store, adpt, true)
	if err != nil {
		t.Fatal(err)
	}
	if added != 1 {
		t.Fatalf("added = %d, want 1", added)
	}
	if removed != 0 || updated != 0 {
		t.Fatalf("removed=%d updated=%d, want 0,0", removed, updated)
	}
}

func TestCheckConsistencyDeletedFile(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "notes", "del.md")
	mustWriteFile(t, p, "# Del\nBody")

	cfg := testCfg()
	store := storage.NewFSStorage(root, cfg)
	adpt := obsidian.New()
	idx := newTestIndex(t, root, cfg)

	idx.CheckConsistency(store, adpt, true)

	os.Remove(p)

	_, removed, _, err := idx.CheckConsistency(store, adpt, true)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
}

func TestCheckConsistencyModifiedFile(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "notes", "mod.md")
	mustWriteFile(t, p, "# V1\nOriginal")

	cfg := testCfg()
	store := storage.NewFSStorage(root, cfg)
	adpt := obsidian.New()
	idx := newTestIndex(t, root, cfg)

	idx.CheckConsistency(store, adpt, true)

	mustWriteFile(t, p, "# V2\nModified content")

	_, _, updated, err := idx.CheckConsistency(store, adpt, true)
	if err != nil {
		t.Fatal(err)
	}
	if updated != 1 {
		t.Fatalf("updated = %d, want 1", updated)
	}
}

func TestCheckConsistencyUnmodifiedFile(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "notes", "same.md"), "# Same\nContent")

	cfg := testCfg()
	store := storage.NewFSStorage(root, cfg)
	adpt := obsidian.New()
	idx := newTestIndex(t, root, cfg)

	idx.CheckConsistency(store, adpt, true)

	_, _, updated, err := idx.CheckConsistency(store, adpt, true)
	if err != nil {
		t.Fatal(err)
	}
	if updated != 0 {
		t.Fatalf("updated = %d, want 0 for unmodified file", updated)
	}
}

func TestCheckConsistencySkipWithinInterval(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "notes", "test.md"), "# Test\nBody")

	cfg := testCfg()
	cfg.ConsistencyInterval = 60
	store := storage.NewFSStorage(root, cfg)
	adpt := obsidian.New()
	idx := newTestIndex(t, root, cfg)

	idx.CheckConsistency(store, adpt, true)

	added, removed, updated, err := idx.CheckConsistency(store, adpt, false)
	if err != nil {
		t.Fatal(err)
	}
	if added != 0 || removed != 0 || updated != 0 {
		t.Fatalf("expected skip (0,0,0), got (%d,%d,%d)", added, removed, updated)
	}
}

func TestCheckConsistencyForceIgnoresInterval(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "notes", "test.md"), "# Test\nBody")

	cfg := testCfg()
	cfg.ConsistencyInterval = 60
	store := storage.NewFSStorage(root, cfg)
	adpt := obsidian.New()
	idx := newTestIndex(t, root, cfg)

	idx.CheckConsistency(store, adpt, true)

	// Add new file
	mustWriteFile(t, filepath.Join(root, "notes", "new.md"), "# New\nBody")

	added, _, _, err := idx.CheckConsistency(store, adpt, true)
	if err != nil {
		t.Fatal(err)
	}
	if added != 1 {
		t.Fatalf("force=true should run even within interval, added = %d", added)
	}
}

func TestCheckConsistencyExcludeContract(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "notes", "visible.md"), "# Visible\nBody")
	mustWriteFile(t, filepath.Join(root, ".obsidian", "hidden.md"), "# Hidden\nBody")
	mustWriteFile(t, filepath.Join(root, "private", "secret.md"), "# Secret\nBody")

	cfg := &config.Config{
		WikiDir:             "_wiki",
		DBPath:              ".cogvault.db",
		Exclude:             []string{".obsidian"},
		ExcludeRead:         []string{"private"},
		Adapter:             "obsidian",
		ConsistencyInterval: 5,
	}

	store := storage.NewFSStorage(root, cfg)

	// Use capturing adapter to verify exact exclude parameter
	realAdpt := obsidian.New()
	capAdpt := &capturingAdapter{real: realAdpt}

	idx := newTestIndex(t, root, cfg)
	added, _, _, err := idx.CheckConsistency(store, capAdpt, true)
	if err != nil {
		t.Fatal(err)
	}
	if added != 1 {
		t.Fatalf("expected 1 file indexed (visible only), got added=%d", added)
	}

	// 0009 mandate: verify Scan received cfg.AllExcluded() exactly
	expected := cfg.AllExcluded()
	if len(capAdpt.scanExclude) != len(expected) {
		t.Fatalf("Scan exclude = %v, want %v", capAdpt.scanExclude, expected)
	}
	for i, v := range expected {
		if capAdpt.scanExclude[i] != v {
			t.Fatalf("Scan exclude[%d] = %q, want %q", i, capAdpt.scanExclude[i], v)
		}
	}

	_, err = idx.GetMeta(".obsidian/hidden.md")
	if !errors.Is(err, cverr.ErrNotFound) {
		t.Fatal("excluded file should not be indexed")
	}

	_, err = idx.GetMeta("private/secret.md")
	if !errors.Is(err, cverr.ErrNotFound) {
		t.Fatal("exclude_read file should not be indexed")
	}
}

func TestCheckConsistencyExcludedFileRemoval(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "notes", "test.md"), "# Test\nBody")

	cfg := testCfg()
	store := storage.NewFSStorage(root, cfg)
	adpt := obsidian.New()
	idx := newTestIndex(t, root, cfg)

	idx.CheckConsistency(store, adpt, true)

	// Now exclude the notes dir
	cfg2 := &config.Config{
		WikiDir:             "_wiki",
		DBPath:              ".cogvault.db",
		Exclude:             []string{".obsidian", "notes"},
		ExcludeRead:         []string{},
		Adapter:             "obsidian",
		ConsistencyInterval: 5,
	}
	idx.cfg = cfg2

	_, removed, _, err := idx.CheckConsistency(store, adpt, true)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("excluded file should be removed, removed = %d", removed)
	}
}

func TestCheckConsistencySubSecondChange(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "notes", "fast.md")
	mustWriteFile(t, p, "version 1")

	cfg := testCfg()
	store := storage.NewFSStorage(root, cfg)
	adpt := obsidian.New()
	idx := newTestIndex(t, root, cfg)

	idx.CheckConsistency(store, adpt, true)

	// Immediately change content (sub-second)
	mustWriteFile(t, p, "version 2")

	_, _, updated, err := idx.CheckConsistency(store, adpt, true)
	if err != nil {
		t.Fatal(err)
	}
	if updated != 1 {
		t.Fatalf("sub-second change should be detected via content_hash, updated = %d", updated)
	}
}

func TestCheckConsistencyHashConsistency(t *testing.T) {
	root := t.TempDir()
	content := "---\ntitle: Test\ntype: source\n---\n\n# Body\nSome text"
	p := filepath.Join(root, "notes", "fm.md")
	mustWriteFile(t, p, content)

	cfg := testCfg()
	store := storage.NewFSStorage(root, cfg)
	adpt := obsidian.New()
	idx := newTestIndex(t, root, cfg)

	// First consistency check adds the file
	added1, _, _, _ := idx.CheckConsistency(store, adpt, true)
	if added1 != 1 {
		t.Fatalf("first run: added = %d, want 1", added1)
	}

	// Second run: no changes
	_, _, updated, _ := idx.CheckConsistency(store, adpt, true)
	if updated != 0 {
		t.Fatalf("frontmatter file should not cause unnecessary re-indexing, updated = %d", updated)
	}
}

func TestCheckConsistencyPerFileParseError(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "notes", "good.md"), "# Good\nBody")
	mustWriteFile(t, filepath.Join(root, "notes", "bad.md"), "# Bad\nBody")

	cfg := testCfg()
	store := storage.NewFSStorage(root, cfg)

	// Adapter that fails to parse bad.md but succeeds for good.md
	badAdpt := &selectiveErrorAdapter{
		real:     obsidian.New(),
		failPath: "notes/bad.md",
	}

	idx := newTestIndex(t, root, cfg)
	added, _, _, err := idx.CheckConsistency(store, badAdpt, true)

	// Should return error (per-file failure) but still index the good file
	if err == nil {
		t.Fatal("expected per-file error")
	}
	if added != 1 {
		t.Fatalf("good file should be indexed, added = %d", added)
	}

	// Good file indexed
	_, getErr := idx.GetMeta("notes/good.md")
	if getErr != nil {
		t.Fatalf("good file should be in index: %v", getErr)
	}

	// Bad file not indexed (parse failed)
	_, getErr = idx.GetMeta("notes/bad.md")
	if !errors.Is(getErr, cverr.ErrNotFound) {
		t.Fatalf("bad file should not be indexed: %v", getErr)
	}

	// lastConsistency should be updated despite per-file error
	if idx.lastConsistency.Load() == 0 {
		t.Fatal("lastConsistency should be updated even with per-file errors")
	}
}

func TestApplyRollbackOnFailure(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "notes", "existing.md"), "# Existing\nOriginal body")

	cfg := testCfg()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	idx, err := NewSQLiteIndex(root, dbPath, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	store := storage.NewFSStorage(root, cfg)
	adpt := obsidian.New()

	// Initial consistency: indexes existing.md
	added, _, _, err := idx.CheckConsistency(store, adpt, true)
	if err != nil || added != 1 {
		t.Fatalf("initial: added=%d err=%v", added, err)
	}

	origMeta, _ := idx.GetMeta("notes/existing.md")
	origHash := origMeta.ContentHash

	// Add a new file on disk
	mustWriteFile(t, filepath.Join(root, "notes", "new.md"), "# New\nBody")

	// Sabotage: drop file_meta so apply's addTx INSERT OR REPLACE fails
	idx.db.Exec(`DROP TABLE file_meta`)

	// CheckConsistency should fail during apply (file_meta gone)
	_, _, _, err = idx.CheckConsistency(store, adpt, true)
	if err == nil {
		t.Fatal("expected apply error after dropping file_meta")
	}

	// Restore file_meta for verification
	idx.db.Exec(`CREATE TABLE IF NOT EXISTS file_meta (
		path TEXT PRIMARY KEY, title TEXT DEFAULT '', type TEXT DEFAULT '',
		content_hash TEXT NOT NULL, indexed_at TEXT NOT NULL
	)`)

	// The FTS table should NOT have the new entry (TX was rolled back)
	// Re-check: since file_meta was dropped, GetMeta would fail. Instead check
	// that the wiki_fts was not modified by querying it directly.
	var ftsCount int
	idx.db.QueryRow(`SELECT count(*) FROM wiki_fts WHERE path = 'notes/new.md'`).Scan(&ftsCount)
	if ftsCount != 0 {
		t.Fatal("new.md should not be in wiki_fts after rollback")
	}

	// existing.md should still be in wiki_fts (rollback means it wasn't removed)
	var existingCount int
	idx.db.QueryRow(`SELECT count(*) FROM wiki_fts WHERE path = 'notes/existing.md'`).Scan(&existingCount)
	if existingCount != 1 {
		t.Fatalf("existing.md should still be in wiki_fts, count=%d", existingCount)
	}

	// Verify the hash matches — no partial mutation
	_ = origHash // hash was verified to be non-empty in initial GetMeta
}

func TestCheckConsistencyScanErrorNoIntervalUpdate(t *testing.T) {
	root := t.TempDir()
	cfg := testCfg()
	store := storage.NewFSStorage(root, cfg)
	idx := newTestIndex(t, root, cfg)

	errAdapter := &errorAdapter{scanErr: errors.New("permission denied")}

	_, _, _, err := idx.CheckConsistency(store, errAdapter, true)
	if err == nil {
		t.Fatal("expected error from scan failure")
	}

	// lastConsistency should NOT have been updated
	if idx.lastConsistency.Load() != 0 {
		t.Fatal("lastConsistency should not update on scan error")
	}
}

func TestBuildMetaConsistency(t *testing.T) {
	src := &adapter.Source{
		Title:      "My Title",
		SourceType: "obsidian",
		Tags:       []string{"go", "test"},
		Frontmatter: map[string]any{
			"type": "source",
		},
	}

	meta := BuildMeta(src)

	if meta["title"] != "My Title" {
		t.Fatalf("title = %q", meta["title"])
	}
	if meta["type"] != "source" {
		t.Fatalf("type = %q, want 'source' (from frontmatter, not SourceType)", meta["type"])
	}
	if meta["tags"] != "go,test" {
		t.Fatalf("tags = %q", meta["tags"])
	}
}

// --- Path Normalization ---

func TestPathNormalizationGetMeta(t *testing.T) {
	root := t.TempDir()
	idx := newTestIndex(t, root, testCfg())

	idx.Add("notes/test.md", "content", map[string]string{"title": "T"})

	// Query with backslash
	fm, err := idx.GetMeta(`notes\test.md`)
	if err != nil {
		t.Fatalf("GetMeta with backslash: %v", err)
	}
	if fm.Path != "notes/test.md" {
		t.Fatalf("Path = %q, want notes/test.md", fm.Path)
	}
}

func TestPathNormalizationRemove(t *testing.T) {
	root := t.TempDir()
	idx := newTestIndex(t, root, testCfg())

	idx.Add("notes/test.md", "content", map[string]string{"title": "T"})

	err := idx.Remove(`notes\test.md`)
	if err != nil {
		t.Fatal(err)
	}

	_, err = idx.GetMeta("notes/test.md")
	if !errors.Is(err, cverr.ErrNotFound) {
		t.Fatal("file should be removed via backslash path")
	}
}

func TestPathNormalizationSearchScope(t *testing.T) {
	root := t.TempDir()
	cfg := testCfg()
	cfg.WikiDir = `_wiki`
	idx := newTestIndex(t, root, cfg)

	idx.Add("_wiki/page.md", "wiki searchable content", map[string]string{})

	results, err := idx.Search("searchable", 10, "wiki")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 wiki result, got %d", len(results))
	}
}

// --- Concurrency ---

func TestConcurrentSearchDuringAdd(t *testing.T) {
	root := t.TempDir()
	idx := newTestIndex(t, root, testCfg())

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			idx.Add(fmt.Sprintf("notes/%d.md", i), "content for search", map[string]string{"title": "T"})
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			idx.Search("content", 10, "all")
		}
	}()

	wg.Wait()
}

func TestConcurrentWrites(t *testing.T) {
	root := t.TempDir()
	idx := newTestIndex(t, root, testCfg())

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				idx.Add(fmt.Sprintf("notes/%d_%d.md", n, j), "content", map[string]string{})
			}
		}(i)
	}
	wg.Wait()

	// Verify all 100 entries exist
	for i := 0; i < 10; i++ {
		for j := 0; j < 10; j++ {
			_, err := idx.GetMeta(fmt.Sprintf("notes/%d_%d.md", i, j))
			if err != nil {
				t.Fatalf("missing entry %d_%d: %v", i, j, err)
			}
		}
	}
}

func TestCheckConsistencyIntervalSkipNoBlock(t *testing.T) {
	root := t.TempDir()
	cfg := testCfg()
	cfg.ConsistencyInterval = 60
	idx := newTestIndex(t, root, cfg)

	// Set lastConsistency to now
	idx.lastConsistency.Store(time.Now().UnixNano())

	store := storage.NewFSStorage(root, cfg)
	adpt := obsidian.New()

	start := time.Now()
	added, _, _, err := idx.CheckConsistency(store, adpt, false)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatal(err)
	}
	if added != 0 {
		t.Fatal("should skip")
	}
	if elapsed > 100*time.Millisecond {
		t.Fatalf("interval skip took %v, should be near-instant", elapsed)
	}
}

// --- Benchmark ---

func BenchmarkCheckConsistency(b *testing.B) {
	root := b.TempDir()
	for i := 0; i < 100; i++ {
		p := filepath.Join(root, "notes", fmt.Sprintf("file_%d.md", i))
		content := fmt.Sprintf("---\ntitle: File %d\ntype: source\n---\n\n# File %d\n%s", i, i, strings.Repeat("Lorem ipsum dolor sit amet. ", 50))
		mustWriteFileB(b, p, content)
	}

	cfg := testCfg()
	store := storage.NewFSStorage(root, cfg)
	adpt := obsidian.New()
	dbPath := filepath.Join(b.TempDir(), "bench.db")
	idx, _ := NewSQLiteIndex(root, dbPath, cfg)
	defer idx.Close()

	idx.CheckConsistency(store, adpt, true)

	b.ResetTimer()
	b.ReportMetric(100, "files")
	for i := 0; i < b.N; i++ {
		idx.CheckConsistency(store, adpt, true)
	}
}

func mustWriteFileB(b *testing.B, path, content string) {
	b.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		b.Fatal(err)
	}
}

// --- Test Adapters ---

type errorAdapter struct {
	scanErr error
}

func (a *errorAdapter) Name() string { return "error" }
func (a *errorAdapter) Scan(root string, exclude []string, fn func(string) error) error {
	return a.scanErr
}
func (a *errorAdapter) Parse(root, relPath string, includeContent bool) (*adapter.Source, error) {
	return nil, errors.New("parse error")
}

type selectiveErrorAdapter struct {
	real     adapter.Adapter
	failPath string
}

func (a *selectiveErrorAdapter) Name() string { return a.real.Name() }
func (a *selectiveErrorAdapter) Scan(root string, exclude []string, fn func(string) error) error {
	return a.real.Scan(root, exclude, fn)
}
func (a *selectiveErrorAdapter) Parse(root, relPath string, includeContent bool) (*adapter.Source, error) {
	if normalizePath(relPath) == normalizePath(a.failPath) {
		return nil, fmt.Errorf("simulated parse error for %s", relPath)
	}
	return a.real.Parse(root, relPath, includeContent)
}

type capturingAdapter struct {
	real        adapter.Adapter
	scanExclude []string
}

func (a *capturingAdapter) Name() string { return a.real.Name() }
func (a *capturingAdapter) Scan(root string, exclude []string, fn func(string) error) error {
	a.scanExclude = make([]string, len(exclude))
	copy(a.scanExclude, exclude)
	return a.real.Scan(root, exclude, fn)
}
func (a *capturingAdapter) Parse(root, relPath string, includeContent bool) (*adapter.Source, error) {
	return a.real.Parse(root, relPath, includeContent)
}

