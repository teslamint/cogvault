package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/teslamint/cogvault/internal/config"
	"github.com/teslamint/cogvault/internal/index"

	_ "modernc.org/sqlite"
)

func setupIngestVault(t *testing.T) (configPath, srcDir, wikiDir, dbPath string) {
	t.Helper()
	base := t.TempDir()
	wikiDir = filepath.Join(base, "wiki")
	dbPath = filepath.Join(base, "index.db")
	srcDir = filepath.Join(base, "src")
	configPath = filepath.Join(base, "config.yaml")
	for _, d := range []string{wikiDir, srcDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "wiki_dir: %s\n", wikiDir)
	fmt.Fprintf(&b, "db_path: %s\n", dbPath)
	b.WriteString("adapter: obsidian\n")
	fmt.Fprintf(&b, "sources:\n  - path: %s\n    types: [pdf]\n", srcDir)
	if err := os.WriteFile(configPath, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return configPath, srcDir, wikiDir, dbPath
}

func writeIngestConfigWithModel(t *testing.T, configPath, wikiDir, dbPath, srcDir, model string) {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "wiki_dir: %s\n", wikiDir)
	fmt.Fprintf(&b, "db_path: %s\n", dbPath)
	b.WriteString("adapter: obsidian\n")
	fmt.Fprintf(&b, "sources:\n  - path: %s\n    types: [pdf]\n", srcDir)
	if model != "" {
		fmt.Fprintf(&b, "llm:\n  model: %s\n", model)
	}
	if err := os.WriteFile(configPath, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeAgedSource(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-5 * time.Minute)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	return p
}

type ledgerSnapshot struct {
	sourcePath  string
	contentHash string
	status      string
	attempts    int
	runOrigin   string
	digestedAt  string
}

func readLedger(t *testing.T, dbPath string) []ledgerSnapshot {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT source_path, content_hash, status, attempts, run_origin, digested_at FROM ingest_ledger ORDER BY source_path, digested_at`)
	if err != nil {
		t.Fatalf("query ledger: %v", err)
	}
	defer rows.Close()
	var out []ledgerSnapshot
	for rows.Next() {
		var r ledgerSnapshot
		if err := rows.Scan(&r.sourcePath, &r.contentHash, &r.status, &r.attempts, &r.runOrigin, &r.digestedAt); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func TestE2EBacklogRun(t *testing.T) {
	// Covers S1.
	fakeClaudeOnPath(t)
	t.Setenv("CLAUDE_FAKE_MODE", "ok")
	configPath, srcDir, wikiDir, dbPath := setupIngestVault(t)

	names := []string{"alpha.pdf", "bravo.pdf", "charlie.pdf"}
	for _, n := range names {
		writeAgedSource(t, srcDir, n, "content of "+n)
	}

	stdout, _, err := executeCommand("ingest", "--config", configPath)
	if err != nil {
		t.Fatalf("ingest failed: %v", err)
	}
	if !strings.Contains(stdout, "digested=3") {
		t.Errorf("expected digested=3 in report, got: %q", stdout)
	}

	for _, base := range []string{"alpha", "bravo", "charlie"} {
		page := filepath.Join(wikiDir, "sources", base+".md")
		if _, err := os.Stat(page); err != nil {
			t.Errorf("expected page %s: %v", page, err)
		}
	}

	rows := readLedger(t, dbPath)
	if len(rows) != 3 {
		t.Fatalf("expected 3 ledger rows, got %d", len(rows))
	}
	for _, r := range rows {
		if r.status != "success" {
			t.Errorf("expected success for %s, got %s", r.sourcePath, r.status)
		}
	}

	searchOut, _, err := executeCommand("search", "--config", configPath, "Body")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	for _, base := range []string{"alpha", "bravo", "charlie"} {
		if !strings.Contains(searchOut, "sources/"+base+".md") {
			t.Errorf("expected sources/%s.md in search results, got: %q", base, searchOut)
		}
	}
}

func TestE2ESearchFindsDigested(t *testing.T) {
	// Covers S3.
	fakeClaudeOnPath(t)
	t.Setenv("CLAUDE_FAKE_MODE", "ok")
	configPath, srcDir, _, _ := setupIngestVault(t)

	writeAgedSource(t, srcDir, "report.pdf", "quarterly numbers")

	if _, _, err := executeCommand("ingest", "--config", configPath); err != nil {
		t.Fatalf("ingest failed: %v", err)
	}

	stdout, _, err := executeCommand("search", "--config", configPath, "Body")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if !strings.Contains(stdout, "sources/report.md") {
		t.Errorf("expected digested page in search results, got: %q", stdout)
	}
}

func TestE2EIncrementalRun(t *testing.T) {
	// Covers S2.
	fakeClaudeOnPath(t)
	t.Setenv("CLAUDE_FAKE_MODE", "ok")
	argvFile := filepath.Join(t.TempDir(), "claude-argv")
	t.Setenv("CLAUDE_FAKE_ARGV_FILE", argvFile)
	configPath, srcDir, _, dbPath := setupIngestVault(t)

	for _, n := range []string{"alpha.pdf", "bravo.pdf", "charlie.pdf"} {
		writeAgedSource(t, srcDir, n, "content of "+n)
	}
	if _, _, err := executeCommand("ingest", "--config", configPath); err != nil {
		t.Fatalf("first ingest failed: %v", err)
	}

	before := make(map[string]string)
	for _, r := range readLedger(t, dbPath) {
		before[r.sourcePath] = r.digestedAt
	}

	writeAgedSource(t, srcDir, "delta.pdf", "content of delta")

	stdout, _, err := executeCommand("ingest", "--config", configPath, "--scheduled")
	if err != nil {
		t.Fatalf("second ingest failed: %v", err)
	}
	if !strings.Contains(stdout, "digested=1") {
		t.Errorf("expected digested=1 on incremental run, got: %q", stdout)
	}
	if !strings.Contains(stdout, "unchanged=3") {
		t.Errorf("expected unchanged=3 on incremental run, got: %q", stdout)
	}

	deltaPath := filepath.Join(srcDir, "delta.pdf")
	for _, r := range readLedger(t, dbPath) {
		if r.sourcePath == deltaPath {
			if r.runOrigin != "scheduled" {
				t.Errorf("expected run_origin=scheduled for delta, got %q", r.runOrigin)
			}
			continue
		}
		if prior, ok := before[r.sourcePath]; ok && r.digestedAt != prior {
			t.Errorf("prior row %s digested_at changed: %q -> %q", r.sourcePath, prior, r.digestedAt)
		}
	}

	// No-change rerun: zero work, no LLM invocation.
	if err := os.Remove(argvFile); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	stdout, _, err = executeCommand("ingest", "--config", configPath, "--scheduled")
	if err != nil {
		t.Fatalf("third ingest failed: %v", err)
	}
	if !strings.Contains(stdout, "digested=0") {
		t.Errorf("expected digested=0 on no-change rerun, got: %q", stdout)
	}
	if !strings.Contains(stdout, "unchanged=4") {
		t.Errorf("expected unchanged=4 on no-change rerun, got: %q", stdout)
	}
	if _, err := os.Stat(argvFile); !os.IsNotExist(err) {
		t.Errorf("expected no LLM invocation on no-change rerun (argv file should be absent), stat err: %v", err)
	}
}

func TestE2EMixedFailureIsolation(t *testing.T) {
	// Covers S2 (per-file failure isolation): in ONE run, a single file fails
	// (rate limit → transient, attempts 0) while the others succeed and the run
	// still exits 0. A follow-up run retries the failed file successfully.
	//
	// CLAUDE_FAKE_MODE is run-global, so the fake CLI's per-file override
	// (CLAUDE_FAKE_MODE_MATCH="<substring>=<mode>") selects ratelimit for the one
	// file whose prompt carries the matching source path.
	fakeClaudeOnPath(t)
	t.Setenv("CLAUDE_FAKE_MODE", "ok")
	t.Setenv("CLAUDE_FAKE_MODE_MATCH", "bravo.pdf=ratelimit")
	configPath, srcDir, wikiDir, dbPath := setupIngestVault(t)

	for _, n := range []string{"alpha.pdf", "bravo.pdf", "charlie.pdf"} {
		writeAgedSource(t, srcDir, n, "content of "+n)
	}

	stdout, _, err := executeCommand("ingest", "--config", configPath)
	if err != nil {
		t.Fatalf("ingest should exit 0 despite per-file failure: %v", err)
	}
	if !strings.Contains(stdout, "digested=2") {
		t.Errorf("expected digested=2 in report, got: %q", stdout)
	}
	if !strings.Contains(stdout, "failed=1") {
		t.Errorf("expected failed=1 in report, got: %q", stdout)
	}

	// Succeeding files produced pages; the failed file did not.
	for _, base := range []string{"alpha", "charlie"} {
		if _, err := os.Stat(filepath.Join(wikiDir, "sources", base+".md")); err != nil {
			t.Errorf("expected page for %s: %v", base, err)
		}
	}
	if _, err := os.Stat(filepath.Join(wikiDir, "sources", "bravo.md")); !os.IsNotExist(err) {
		t.Errorf("expected no page for failed bravo, stat err: %v", err)
	}

	bravoPath := filepath.Join(srcDir, "bravo.pdf")
	rows := readLedger(t, dbPath)
	if len(rows) != 3 {
		t.Fatalf("expected 3 ledger rows, got %d", len(rows))
	}
	var success int
	for _, r := range rows {
		if r.sourcePath == bravoPath {
			if r.status != "failed" {
				t.Errorf("expected bravo status failed, got %s", r.status)
			}
			if r.attempts != 0 {
				t.Errorf("expected attempts=0 for transient failure, got %d", r.attempts)
			}
			continue
		}
		if r.status == "success" {
			success++
		} else {
			t.Errorf("expected success for %s, got %s", r.sourcePath, r.status)
		}
	}
	if success != 2 {
		t.Errorf("expected 2 success rows, got %d", success)
	}

	// Follow-up run with the override removed: the failed file retries and
	// succeeds; the already-succeeded files are unchanged.
	t.Setenv("CLAUDE_FAKE_MODE_MATCH", "")
	stdout, _, err = executeCommand("ingest", "--config", configPath)
	if err != nil {
		t.Fatalf("retry ingest failed: %v", err)
	}
	if !strings.Contains(stdout, "digested=1") {
		t.Errorf("expected digested=1 on retry, got: %q", stdout)
	}
	if !strings.Contains(stdout, "unchanged=2") {
		t.Errorf("expected unchanged=2 on retry, got: %q", stdout)
	}
	if _, err := os.Stat(filepath.Join(wikiDir, "sources", "bravo.md")); err != nil {
		t.Errorf("expected bravo page after retry: %v", err)
	}

	rows = readLedger(t, dbPath)
	if len(rows) != 3 {
		t.Fatalf("expected 3 ledger rows after retry, got %d", len(rows))
	}
	for _, r := range rows {
		if r.status != "success" {
			t.Errorf("expected success for %s after retry, got %s", r.sourcePath, r.status)
		}
	}
}

func TestE2ESupersedeOnContentChange(t *testing.T) {
	// Covers S2 (re-digest supersedes the prior success row).
	fakeClaudeOnPath(t)
	t.Setenv("CLAUDE_FAKE_MODE", "ok")
	configPath, srcDir, _, dbPath := setupIngestVault(t)

	src := writeAgedSource(t, srcDir, "memo.pdf", "original content")
	if _, _, err := executeCommand("ingest", "--config", configPath); err != nil {
		t.Fatalf("first ingest failed: %v", err)
	}

	if err := os.WriteFile(src, []byte("revised content, different hash"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-5 * time.Minute)
	if err := os.Chtimes(src, old, old); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := executeCommand("ingest", "--config", configPath)
	if err != nil {
		t.Fatalf("second ingest failed: %v", err)
	}
	if !strings.Contains(stdout, "digested=1") {
		t.Errorf("expected digested=1 after content change, got: %q", stdout)
	}

	rows := readLedger(t, dbPath)
	if len(rows) != 2 {
		t.Fatalf("expected 2 ledger rows (superseded + success), got %d", len(rows))
	}
	var superseded, success int
	for _, r := range rows {
		switch r.status {
		case "superseded":
			superseded++
		case "success":
			success++
		}
	}
	if superseded != 1 || success != 1 {
		t.Errorf("expected 1 superseded + 1 success, got superseded=%d success=%d", superseded, success)
	}
}

func TestE2ERefusalTerminal(t *testing.T) {
	// Covers S1, S4.
	fakeClaudeOnPath(t)
	t.Setenv("CLAUDE_FAKE_MODE", "ok")
	t.Setenv("CLAUDE_FAKE_MODE_MATCH", "bravo.pdf=refusal_exit0")
	configPath, srcDir, wikiDir, dbPath := setupIngestVault(t)

	for _, n := range []string{"alpha.pdf", "bravo.pdf", "charlie.pdf"} {
		writeAgedSource(t, srcDir, n, "content of "+n)
	}

	stdout, _, err := executeCommand("ingest", "--config", configPath)
	if err != nil {
		t.Fatalf("ingest should exit 0 despite refusal: %v", err)
	}
	if !strings.Contains(stdout, "refused=1") {
		t.Errorf("expected refused=1 in report, got: %q", stdout)
	}
	if !strings.Contains(stdout, "failed=0") {
		t.Errorf("expected failed=0 in report, got: %q", stdout)
	}

	for _, base := range []string{"alpha", "charlie"} {
		if _, err := os.Stat(filepath.Join(wikiDir, "sources", base+".md")); err != nil {
			t.Errorf("expected page for %s: %v", base, err)
		}
	}
	if _, err := os.Stat(filepath.Join(wikiDir, "sources", "bravo.md")); !os.IsNotExist(err) {
		t.Errorf("expected no page for refused bravo, stat err: %v", err)
	}

	bravoPath := filepath.Join(srcDir, "bravo.pdf")
	rows := readLedger(t, dbPath)
	if len(rows) != 3 {
		t.Fatalf("expected 3 ledger rows, got %d", len(rows))
	}
	var success int
	for _, r := range rows {
		if r.sourcePath == bravoPath {
			if r.status != "refused" {
				t.Errorf("expected bravo status refused, got %s", r.status)
			}
			if r.attempts != 0 {
				t.Errorf("expected attempts=0 for refusal, got %d", r.attempts)
			}
			continue
		}
		if r.status == "success" {
			success++
		} else {
			t.Errorf("expected success for %s, got %s", r.sourcePath, r.status)
		}
	}
	if success != 2 {
		t.Errorf("expected 2 success rows, got %d", success)
	}
}

func TestE2ERefusalNotRetried(t *testing.T) {
	// Covers S1.
	fakeClaudeOnPath(t)
	t.Setenv("CLAUDE_FAKE_MODE", "ok")
	t.Setenv("CLAUDE_FAKE_MODE_MATCH", "bravo.pdf=refusal_exit0")
	argvFile := filepath.Join(t.TempDir(), "claude-argv")
	t.Setenv("CLAUDE_FAKE_ARGV_FILE", argvFile)
	configPath, srcDir, _, dbPath := setupIngestVault(t)

	for _, n := range []string{"alpha.pdf", "bravo.pdf", "charlie.pdf"} {
		writeAgedSource(t, srcDir, n, "content of "+n)
	}
	if _, _, err := executeCommand("ingest", "--config", configPath); err != nil {
		t.Fatalf("first ingest failed: %v", err)
	}

	bravoPath := filepath.Join(srcDir, "bravo.pdf")
	var bravoBefore string
	for _, r := range readLedger(t, dbPath) {
		if r.sourcePath == bravoPath {
			bravoBefore = r.digestedAt
		}
	}

	// Second run, same model: the refused file is skipped before the LLM runs and
	// the two prior successes are unchanged, so nothing invokes the fake claude —
	// its argv record is never written.
	if err := os.Remove(argvFile); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	stdout, _, err := executeCommand("ingest", "--config", configPath)
	if err != nil {
		t.Fatalf("second ingest failed: %v", err)
	}
	if !strings.Contains(stdout, "digested=0") {
		t.Errorf("expected digested=0 on rerun, got: %q", stdout)
	}
	if _, err := os.Stat(argvFile); !os.IsNotExist(err) {
		t.Errorf("expected zero LLM calls on rerun (argv file absent), stat err: %v", err)
	}

	for _, r := range readLedger(t, dbPath) {
		if r.sourcePath == bravoPath && r.digestedAt != bravoBefore {
			t.Errorf("refused bravo re-digested: %q -> %q", bravoBefore, r.digestedAt)
		}
	}
}

func TestE2EModelChangeRecoversRefused(t *testing.T) {
	// Covers S2, S3.
	fakeClaudeOnPath(t)
	t.Setenv("CLAUDE_FAKE_MODE", "ok")
	t.Setenv("CLAUDE_FAKE_MODE_MATCH", "bravo.pdf=refusal_exit0")
	argvFile := filepath.Join(t.TempDir(), "claude-argv")
	t.Setenv("CLAUDE_FAKE_ARGV_FILE", argvFile)
	configPath, srcDir, wikiDir, dbPath := setupIngestVault(t)

	for _, n := range []string{"alpha.pdf", "bravo.pdf", "charlie.pdf"} {
		writeAgedSource(t, srcDir, n, "content of "+n)
	}
	if _, _, err := executeCommand("ingest", "--config", configPath); err != nil {
		t.Fatalf("first ingest failed: %v", err)
	}

	bravoPath := filepath.Join(srcDir, "bravo.pdf")
	for _, r := range readLedger(t, dbPath) {
		if r.sourcePath == bravoPath && r.status != "refused" {
			t.Fatalf("expected bravo refused after first run, got %s", r.status)
		}
	}

	// Change the configured model and let the fake succeed for bravo: the
	// previously-refused file is re-attempted only because the model changed.
	writeIngestConfigWithModel(t, configPath, wikiDir, dbPath, srcDir, "opus")
	t.Setenv("CLAUDE_FAKE_MODE_MATCH", "")
	if err := os.Remove(argvFile); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}

	stdout, _, err := executeCommand("ingest", "--config", configPath)
	if err != nil {
		t.Fatalf("second ingest failed: %v", err)
	}
	if !strings.Contains(stdout, "digested=1") {
		t.Errorf("expected digested=1 after model change, got: %q", stdout)
	}

	if _, err := os.Stat(filepath.Join(wikiDir, "sources", "bravo.md")); err != nil {
		t.Errorf("expected bravo page after model-change recovery: %v", err)
	}
	for _, r := range readLedger(t, dbPath) {
		if r.sourcePath == bravoPath && r.status != "success" {
			t.Errorf("expected bravo success after model change, got %s", r.status)
		}
	}

	argv, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("expected the re-attempt to invoke the fake claude: %v", err)
	}
	if !strings.Contains(string(argv), "--model opus") {
		t.Errorf("expected --model opus in re-attempt argv, got: %q", string(argv))
	}
}

func TestE2EModelUnchangedKeepsRefused(t *testing.T) {
	// Covers S1.
	fakeClaudeOnPath(t)
	t.Setenv("CLAUDE_FAKE_MODE", "ok")
	t.Setenv("CLAUDE_FAKE_MODE_MATCH", "bravo.pdf=refusal_exit0")
	argvFile := filepath.Join(t.TempDir(), "claude-argv")
	t.Setenv("CLAUDE_FAKE_ARGV_FILE", argvFile)
	configPath, srcDir, _, dbPath := setupIngestVault(t)

	for _, n := range []string{"alpha.pdf", "bravo.pdf", "charlie.pdf"} {
		writeAgedSource(t, srcDir, n, "content of "+n)
	}
	if _, _, err := executeCommand("ingest", "--config", configPath); err != nil {
		t.Fatalf("first ingest failed: %v", err)
	}

	bravoPath := filepath.Join(srcDir, "bravo.pdf")
	var bravoBefore string
	for _, r := range readLedger(t, dbPath) {
		if r.sourcePath == bravoPath {
			bravoBefore = r.digestedAt
		}
	}

	// Second run, same configured model: the refused row is not re-attempted, so
	// nothing invokes the fake claude and bravo's digested_at is unchanged.
	if err := os.Remove(argvFile); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	stdout, _, err := executeCommand("ingest", "--config", configPath)
	if err != nil {
		t.Fatalf("second ingest failed: %v", err)
	}
	if !strings.Contains(stdout, "digested=0") {
		t.Errorf("expected digested=0 on unchanged-model rerun, got: %q", stdout)
	}
	if _, err := os.Stat(argvFile); !os.IsNotExist(err) {
		t.Errorf("expected zero LLM calls on unchanged-model rerun (argv file absent), stat err: %v", err)
	}

	for _, r := range readLedger(t, dbPath) {
		if r.sourcePath == bravoPath && r.digestedAt != bravoBefore {
			t.Errorf("refused bravo re-digested on unchanged model: %q -> %q", bravoBefore, r.digestedAt)
		}
	}
}

func TestE2EConcurrentIndexWriteDuringRead(t *testing.T) {
	// Covers the contention smoke: an index write path (ingest) coexists with a
	// concurrent reader (serve/search) on the same DB file without "database is
	// locked" — WAL read-during-write plus busy_timeout.
	base := t.TempDir()
	wikiDir := filepath.Join(base, "wiki")
	dbPath := filepath.Join(base, "index.db")
	if err := os.MkdirAll(wikiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{WikiDir: wikiDir, DBPath: dbPath}

	writer, err := index.NewSQLiteIndex(wikiDir, dbPath, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	reader, err := index.NewSQLiteIndex(wikiDir, dbPath, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	const n = 60
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			if err := writer.Add(fmt.Sprintf("sources/p-%d.md", i), "concurrent body text", map[string]string{"title": "t"}); err != nil {
				errs <- err
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			if _, err := reader.Search("body", 5); err != nil {
				errs <- err
				return
			}
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent index write/read failed: %v", err)
		}
	}
}

func TestE2EBusyTimeoutSerializesWriters(t *testing.T) {
	// Covers the busy_timeout guarantee for the ledger's cross-process write
	// path: two independent connection pools with the production DSN, running
	// write-first transactions (the ledger's INSERT OR REPLACE pattern),
	// serialize under busy_timeout instead of erroring "database is locked".
	dbPath := filepath.Join(t.TempDir(), "ledger.db")
	dsn := dbPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	open := func() *sql.DB {
		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			t.Fatal(err)
		}
		return db
	}
	a := open()
	defer a.Close()
	b := open()
	defer b.Close()
	if _, err := a.Exec(`CREATE TABLE IF NOT EXISTS ledger(id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatal(err)
	}

	const n = 50
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	write := func(db *sql.DB, off int) {
		defer wg.Done()
		for i := 0; i < n; i++ {
			tx, err := db.Begin()
			if err != nil {
				errs <- err
				return
			}
			if _, err := tx.Exec(`INSERT OR REPLACE INTO ledger(id, v) VALUES(?, ?)`, off*1000+i, "x"); err != nil {
				tx.Rollback()
				errs <- err
				return
			}
			if err := tx.Commit(); err != nil {
				errs <- err
				return
			}
		}
	}
	wg.Add(2)
	go write(a, 1)
	go write(b, 2)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent writers failed (busy_timeout ineffective?): %v", err)
		}
	}
}
