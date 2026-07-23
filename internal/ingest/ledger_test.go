package ingest

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func newTestLedger(t *testing.T) *ledger {
	t.Helper()
	l, err := openLedger(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatalf("openLedger: %v", err)
	}
	t.Cleanup(func() { l.close() })
	return l
}

func TestOpenLedgerCreatesTableAndWAL(t *testing.T) {
	l := newTestLedger(t)

	var name string
	err := l.db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='ingest_ledger'`,
	).Scan(&name)
	if err != nil {
		t.Fatalf("table lookup: %v", err)
	}
	if name != "ingest_ledger" {
		t.Fatalf("table = %q, want ingest_ledger", name)
	}

	var mode string
	if err := l.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
}

func TestLedgerUpsertLookupRoundTrip(t *testing.T) {
	l := newTestLedger(t)

	want := ledgerRow{
		sourcePath: "/src/a.md", contentHash: "h1", sourceDir: "/src",
		digestedAt: "2026-07-22T00:00:00Z", wikiPage: "sources/a.md",
		status: "success", attempts: 0, lastError: "", runOrigin: "scheduled",
	}
	if err := l.upsert(want); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, found, err := l.lookup("/src/a.md", "h1")
	if err != nil || !found {
		t.Fatalf("lookup: found=%v err=%v", found, err)
	}
	if got.status != "success" || got.wikiPage != "sources/a.md" || got.runOrigin != "scheduled" {
		t.Fatalf("row mismatch: %+v", got)
	}
}

func TestLedgerModelRoundTrip(t *testing.T) {
	l := newTestLedger(t)
	want := ledgerRow{
		sourcePath: "/src/m.md", contentHash: "h1", status: "success",
		wikiPage: "sources/m.md", runOrigin: "scheduled", llmModel: "opus",
	}
	if err := l.upsert(want); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, found, err := l.lookup("/src/m.md", "h1")
	if err != nil || !found {
		t.Fatalf("lookup: found=%v err=%v", found, err)
	}
	if got.llmModel != "opus" {
		t.Fatalf("llmModel = %q, want opus", got.llmModel)
	}
}

func TestOpenLedgerMigratesLLMModelColumn(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "old.db")

	// Pre-create with the OLD 9-column schema (no llm_model) + a row.
	raw, err := sql.Open("sqlite", dsnWithPragmas(dbPath))
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	if _, err := raw.Exec(`CREATE TABLE ingest_ledger (
		source_path TEXT, content_hash TEXT, source_dir TEXT, digested_at TEXT,
		wiki_page TEXT, status TEXT, attempts INTEGER, last_error TEXT, run_origin TEXT,
		PRIMARY KEY (source_path, content_hash))`); err != nil {
		t.Fatalf("create old table: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO ingest_ledger
		(source_path, content_hash, source_dir, digested_at, wiki_page, status, attempts, last_error, run_origin)
		VALUES ('/src/old.md','h1','/src','2026-07-22T00:00:00Z','sources/old.md','success',0,'','scheduled')`); err != nil {
		t.Fatalf("insert old row: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	// openLedger must add the column and preserve the row.
	l, err := openLedger(dbPath)
	if err != nil {
		t.Fatalf("openLedger: %v", err)
	}
	t.Cleanup(func() { l.close() })

	got, found, err := l.lookup("/src/old.md", "h1")
	if err != nil || !found {
		t.Fatalf("lookup: found=%v err=%v", found, err)
	}
	if got.status != "success" || got.llmModel != "" {
		t.Fatalf("row = %+v, want success llmModel=\"\"", got)
	}
}

func TestLedgerLookupMiss(t *testing.T) {
	l := newTestLedger(t)
	_, found, err := l.lookup("/nope", "x")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if found {
		t.Fatal("found = true, want false")
	}
}

func TestLedgerReplaceSamePK(t *testing.T) {
	l := newTestLedger(t)
	base := ledgerRow{sourcePath: "/src/a.md", contentHash: "h1", status: "failed", attempts: 1, lastError: "boom"}
	if err := l.upsert(base); err != nil {
		t.Fatalf("upsert failed: %v", err)
	}
	base.status = "success"
	base.attempts = 1
	base.lastError = ""
	base.wikiPage = "sources/a.md"
	if err := l.upsert(base); err != nil {
		t.Fatalf("upsert success: %v", err)
	}

	var count int
	if err := l.db.QueryRow(`SELECT COUNT(1) FROM ingest_ledger WHERE source_path='/src/a.md'`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1 (replace, not duplicate)", count)
	}
	got, _, _ := l.lookup("/src/a.md", "h1")
	if got.status != "success" {
		t.Fatalf("status = %q, want success", got.status)
	}
}

func TestLedgerSupersedePrevSuccess(t *testing.T) {
	l := newTestLedger(t)
	// A prior success (old hash) and an unrelated failed row.
	_ = l.upsert(ledgerRow{sourcePath: "/src/a.md", contentHash: "old", status: "success", wikiPage: "sources/a.md"})
	_ = l.upsert(ledgerRow{sourcePath: "/src/b.md", contentHash: "bh", status: "failed", attempts: 1})

	if err := l.supersedePrevSuccess("/src/a.md"); err != nil {
		t.Fatalf("supersede: %v", err)
	}

	got, _, _ := l.lookup("/src/a.md", "old")
	if got.status != "superseded" {
		t.Fatalf("a status = %q, want superseded", got.status)
	}
	other, _, _ := l.lookup("/src/b.md", "bh")
	if other.status != "failed" {
		t.Fatalf("b status = %q, want failed (untouched)", other.status)
	}
}

func TestLedgerWikiPageTakenByOther(t *testing.T) {
	l := newTestLedger(t)
	_ = l.upsert(ledgerRow{sourcePath: "/src/a.md", contentHash: "h1", status: "success", wikiPage: "sources/note.md"})

	taken, err := l.wikiPageTakenByOther("sources/note.md", "/src/b.md")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !taken {
		t.Fatal("taken = false, want true (different source)")
	}

	self, _ := l.wikiPageTakenByOther("sources/note.md", "/src/a.md")
	if self {
		t.Fatal("self reported as taken by other")
	}

	free, _ := l.wikiPageTakenByOther("sources/free.md", "/src/b.md")
	if free {
		t.Fatal("unused page reported taken")
	}
}
