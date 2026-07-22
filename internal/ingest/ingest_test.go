package ingest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/teslamint/cogvault/internal/config"
	"github.com/teslamint/cogvault/internal/index"
	"github.com/teslamint/cogvault/internal/llm"
	"github.com/teslamint/cogvault/internal/storage"
)

var errPermanent = errors.New("permanent digest failure")

type mockLLM struct {
	mu       sync.Mutex
	requests []llm.DigestRequest
	fn       func(req llm.DigestRequest) (*llm.DigestResult, error)
}

func (m *mockLLM) Digest(_ context.Context, req llm.DigestRequest) (*llm.DigestResult, error) {
	m.mu.Lock()
	m.requests = append(m.requests, req)
	m.mu.Unlock()
	return m.fn(req)
}

func (m *mockLLM) Name() string { return "mock" }

func validPage(title string) string {
	return "---\ntitle: " + title + "\ntype: source\ntags:\n  - alpha\n  - beta\n---\n\nbody content here\n"
}

func okLLM() *mockLLM {
	return &mockLLM{fn: func(req llm.DigestRequest) (*llm.DigestResult, error) {
		return &llm.DigestResult{PageContent: validPage(req.PageSlug)}, nil
	}}
}

type harness struct {
	runner  *Runner
	llm     *mockLLM
	srcDir  string
	wikiDir string
	dbPath  string
	store   storage.Storage
	idx     index.Index
}

func newHarness(t *testing.T, types []string, m *mockLLM) *harness {
	t.Helper()
	root := t.TempDir()
	wikiDir := filepath.Join(root, "wiki")
	srcDir := filepath.Join(root, "src")
	dbPath := filepath.Join(root, "cogvault.db")
	if err := os.MkdirAll(wikiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		WikiDir: wikiDir,
		DBPath:  dbPath,
		Sources: []config.SourceDir{{Path: srcDir, Types: types}},
		Adapter: "obsidian",
	}
	store := storage.NewFSStorage(wikiDir, cfg)
	idx, err := index.NewSQLiteIndex(wikiDir, dbPath, cfg)
	if err != nil {
		t.Fatalf("NewSQLiteIndex: %v", err)
	}
	t.Cleanup(func() { idx.Close() })

	runner, err := New(cfg, store, idx, m, dbPath)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { runner.Close() })
	runner.settleWindow = 0 // digest fresh temp files by default

	return &harness{runner: runner, llm: m, srcDir: srcDir, wikiDir: wikiDir, dbPath: dbPath, store: store, idx: idx}
}

func (h *harness) write(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(h.srcDir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunHappyPathTwoFiles(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	a := h.write(t, "note-one.md", "content one")
	b := h.write(t, "note-two.md", "content two")

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Digested != 2 || rep.Failed != 0 || rep.Skipped != 0 {
		t.Fatalf("counts = %+v", rep)
	}

	for _, p := range []string{"sources/note-one.md", "sources/note-two.md"} {
		if ok, _ := h.store.Exists(p); !ok {
			t.Fatalf("page %s missing", p)
		}
		if meta, err := h.idx.GetMeta(p); err != nil || meta == nil {
			t.Fatalf("index meta %s: %v", p, err)
		}
	}

	for _, src := range []string{a, b} {
		row, found, _ := h.runner.ledger.lookup(src, contentHash(mustRead(t, src)))
		if !found || row.status != "success" || row.runOrigin != "scheduled" {
			t.Fatalf("ledger row for %s: found=%v row=%+v", src, found, row)
		}
	}
}

func TestRunIdempotentSecondRun(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	h.write(t, "a.md", "content one")
	h.write(t, "b.md", "content two")

	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if rep.Digested != 0 || rep.Unchanged != 2 {
		t.Fatalf("second run counts = %+v, want digested=0 unchanged=2", rep)
	}
	if len(h.llm.requests) != 2 {
		t.Fatalf("llm called %d times, want 2 (no re-digest)", len(h.llm.requests))
	}
}

func TestRunDeferredWithinSettleWindow(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	h.runner.settleWindow = settleWindow // restore default 2m
	h.write(t, "fresh.md", "content")

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Deferred != 1 || rep.Digested != 0 {
		t.Fatalf("counts = %+v, want deferred=1 digested=0", rep)
	}
	if len(h.llm.requests) != 0 {
		t.Fatal("llm called for deferred file")
	}
}

func TestRunSupersedeOnContentChange(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	src := h.write(t, "note.md", "original content")
	oldHash := contentHash([]byte("original content"))

	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"}); err != nil {
		t.Fatalf("first run: %v", err)
	}

	// change content -> new hash -> re-digest -> old row superseded
	h.write(t, "note.md", "changed content")
	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if rep.Digested != 1 {
		t.Fatalf("second run digested = %d, want 1", rep.Digested)
	}

	oldRow, found, _ := h.runner.ledger.lookup(src, oldHash)
	if !found || oldRow.status != "superseded" {
		t.Fatalf("old row status: found=%v row=%+v, want superseded", found, oldRow)
	}
	newRow, found, _ := h.runner.ledger.lookup(src, contentHash([]byte("changed content")))
	if !found || newRow.status != "success" {
		t.Fatalf("new row: found=%v row=%+v, want success", found, newRow)
	}
	// same page path overwritten
	if ok, _ := h.store.Exists("sources/note.md"); !ok {
		t.Fatal("sources/note.md missing after overwrite")
	}
}

func TestRunSlugCollisionDifferentSource(t *testing.T) {
	m := okLLM()
	h := newHarness(t, []string{"md"}, m)
	// second source dir with a same-basename file
	srcDir2 := filepath.Join(filepath.Dir(h.srcDir), "src2")
	if err := os.MkdirAll(srcDir2, 0o755); err != nil {
		t.Fatal(err)
	}
	h.runner.cfg.Sources = append(h.runner.cfg.Sources, config.SourceDir{Path: srcDir2, Types: []string{"md"}})
	h.write(t, "note.md", "first")
	other := filepath.Join(srcDir2, "note.md")
	if err := os.WriteFile(other, []byte("second"), 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Digested != 2 {
		t.Fatalf("digested = %d, want 2", rep.Digested)
	}
	if ok, _ := h.store.Exists("sources/note.md"); !ok {
		t.Fatal("base page missing")
	}
	suffixed := "sources/note-" + hash8([]byte(other)) + ".md"
	if ok, _ := h.store.Exists(suffixed); !ok {
		t.Fatalf("collision page %s missing", suffixed)
	}
}

func TestRunOversizedAndTypeExcluded(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	h.runner.maxFileSize = 8
	h.write(t, "image.png", "not markdown type")
	h.write(t, "big.md", "this content exceeds eight bytes")

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Skipped != 2 || rep.Digested != 0 {
		t.Fatalf("counts = %+v, want skipped=2 digested=0", rep)
	}
	// neither persisted to ledger
	var count int
	if err := h.runner.ledger.db.QueryRow(`SELECT COUNT(1) FROM ingest_ledger`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("ledger rows = %d, want 0", count)
	}
}

func TestRunLimitOne(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	h.write(t, "a.md", "one")
	h.write(t, "b.md", "two")

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled", Limit: 1})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Digested != 1 {
		t.Fatalf("digested = %d, want 1", rep.Digested)
	}
	if len(h.llm.requests) != 1 {
		t.Fatalf("llm called %d times, want 1", len(h.llm.requests))
	}
}

func TestRunDryRunWritesNothing(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	h.write(t, "a.md", "one")
	h.write(t, "b.md", "two")

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled", DryRun: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Digested != 2 {
		t.Fatalf("digested = %d, want 2 (would-digest)", rep.Digested)
	}
	if len(h.llm.requests) != 0 {
		t.Fatal("llm invoked during dry run")
	}
	if ok, _ := h.store.Exists("sources/a.md"); ok {
		t.Fatal("dry run wrote a page")
	}
	var count int
	h.runner.ledger.db.QueryRow(`SELECT COUNT(1) FROM ingest_ledger`).Scan(&count)
	if count != 0 {
		t.Fatalf("dry run wrote %d ledger rows", count)
	}
}

func TestRunTransientErrorNoAttemptIncrement(t *testing.T) {
	m := &mockLLM{fn: func(req llm.DigestRequest) (*llm.DigestResult, error) {
		return nil, llm.ErrTransient
	}}
	h := newHarness(t, []string{"md"}, m)
	src := h.write(t, "a.md", "one")

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Failed != 1 {
		t.Fatalf("failed = %d, want 1", rep.Failed)
	}
	row, found, _ := h.runner.ledger.lookup(src, contentHash([]byte("one")))
	if !found || row.status != "failed" || row.attempts != 0 {
		t.Fatalf("row = %+v, want failed attempts=0", row)
	}
}

func TestRunPermanentErrorExhausts(t *testing.T) {
	m := &mockLLM{fn: func(req llm.DigestRequest) (*llm.DigestResult, error) {
		return nil, errPermanent
	}}
	h := newHarness(t, []string{"md"}, m)
	src := h.write(t, "a.md", "one")
	hash := contentHash([]byte("one"))

	for i := 1; i <= 3; i++ {
		rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"})
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if rep.Failed != 1 {
			t.Fatalf("run %d failed = %d, want 1", i, rep.Failed)
		}
		row, _, _ := h.runner.ledger.lookup(src, hash)
		if row.attempts != i {
			t.Fatalf("run %d attempts = %d, want %d", i, row.attempts, i)
		}
	}

	// 4th run: attempts >= maxAttempts -> exhausted skip, no new llm call
	callsBefore := len(m.requests)
	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"})
	if err != nil {
		t.Fatalf("run 4: %v", err)
	}
	if rep.Skipped != 1 || rep.Failed != 0 {
		t.Fatalf("run 4 counts = %+v, want skipped=1 failed=0", rep)
	}
	if len(m.requests) != callsBefore {
		t.Fatal("exhausted file was re-digested")
	}
	if len(rep.PerFile) != 1 || rep.PerFile[0].Action != actionExhausted {
		t.Fatalf("perfile = %+v, want exhausted", rep.PerFile)
	}
}

func TestRunUnparsableFrontmatterPermanentNoWrite(t *testing.T) {
	m := &mockLLM{fn: func(req llm.DigestRequest) (*llm.DigestResult, error) {
		return &llm.DigestResult{PageContent: "plain text, no frontmatter, no title"}, nil
	}}
	h := newHarness(t, []string{"md"}, m)
	src := h.write(t, "a.md", "one")

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Failed != 1 || rep.Digested != 0 {
		t.Fatalf("counts = %+v, want failed=1 digested=0", rep)
	}
	row, _, _ := h.runner.ledger.lookup(src, contentHash([]byte("one")))
	if row.status != "failed" || row.attempts != 1 {
		t.Fatalf("row = %+v, want failed attempts=1 (permanent)", row)
	}
	// nothing written under the wiki root sources dir
	if entries, err := os.ReadDir(filepath.Join(h.wikiDir, "sources")); err == nil && len(entries) > 0 {
		t.Fatalf("wiki sources not empty: %v", entries)
	}
}

func TestRunMissingFrontmatterTitlePermanent(t *testing.T) {
	m := &mockLLM{fn: func(req llm.DigestRequest) (*llm.DigestResult, error) {
		return &llm.DigestResult{PageContent: "---\ntype: source\n---\n\nbody\n"}, nil
	}}
	h := newHarness(t, []string{"md"}, m)
	h.write(t, "a.md", "one")

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Failed != 1 {
		t.Fatalf("failed = %d, want 1 (missing title)", rep.Failed)
	}
}

func TestRunAlreadyRunningLock(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	h.write(t, "a.md", "one")

	lockPath := filepath.Join(filepath.Dir(h.dbPath), "ingest.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatalf("test-side flock: %v", err)
	}
	defer unix.Flock(int(f.Fd()), unix.LOCK_UN)

	_, err = h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"})
	if err != ErrAlreadyRunning {
		t.Fatalf("err = %v, want ErrAlreadyRunning", err)
	}
}

// failWriteStorage wraps a Storage and forces Write to fail, exercising the
// infrastructure-failure class (attempts must not be consumed).
type failWriteStorage struct {
	storage.Storage
	err error
}

func (f failWriteStorage) Write(string, []byte) error { return f.err }

func TestRunInfraWriteFailureSparesAttempts(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	writeErr := errors.New("disk full")
	h.runner.store = failWriteStorage{Storage: h.store, err: writeErr}
	src := h.write(t, "a.md", "one")
	hash := contentHash([]byte("one"))

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Failed != 1 || rep.Digested != 0 {
		t.Fatalf("counts = %+v, want failed=1 digested=0", rep)
	}
	row, found, _ := h.runner.ledger.lookup(src, hash)
	if !found || row.status != "failed" || row.attempts != 0 {
		t.Fatalf("row = %+v found=%v, want failed attempts=0 (infra failure spares attempts)", row, found)
	}
	if !strings.Contains(row.lastError, "write:") {
		t.Fatalf("lastError = %q, want write: prefix", row.lastError)
	}

	// Repair the store: the file must retry (attempts were not consumed) and succeed.
	h.runner.store = h.store
	rep2, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if rep2.Digested != 1 {
		t.Fatalf("second run digested = %d, want 1 (file retried after infra failure)", rep2.Digested)
	}
	row2, _, _ := h.runner.ledger.lookup(src, hash)
	if row2.status != "success" {
		t.Fatalf("row after repair = %+v, want success", row2)
	}
}

func TestRunCancelAfterFirstFile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls int
	m := &mockLLM{fn: func(req llm.DigestRequest) (*llm.DigestResult, error) {
		calls++
		res := &llm.DigestResult{PageContent: validPage(req.PageSlug)}
		if calls == 1 {
			cancel() // cancel the shared ctx after completing the first file's digest
		}
		return res, nil
	}}
	h := newHarness(t, []string{"md"}, m)
	// sorted by absPath: a-first before b-second
	a := h.write(t, "a-first.md", "content one")
	b := h.write(t, "b-second.md", "content two")

	rep, err := h.runner.Run(ctx, RunOptions{Origin: "scheduled"})
	if err == nil {
		t.Fatal("expected wrapped context error after mid-run cancel")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want wrapped context.Canceled", err)
	}
	if rep.Digested != 1 {
		t.Fatalf("Digested = %d, want 1 (only first file digested)", rep.Digested)
	}
	if calls != 1 {
		t.Fatalf("llm calls = %d, want 1 (second file never digested)", calls)
	}

	// First file: success ledger row.
	rowA, foundA, _ := h.runner.ledger.lookup(a, contentHash([]byte("content one")))
	if !foundA || rowA.status != "success" {
		t.Fatalf("first file row = %+v found=%v, want success", rowA, foundA)
	}
	// Second file: no ledger row at all.
	if _, foundB, _ := h.runner.ledger.lookup(b, contentHash([]byte("content two"))); foundB {
		t.Fatal("second file must have no ledger row")
	}

	// Lock released on abort: a subsequent Run acquires cleanly and finishes the backlog.
	m.mu.Lock()
	m.fn = func(req llm.DigestRequest) (*llm.DigestResult, error) {
		return &llm.DigestResult{PageContent: validPage(req.PageSlug)}, nil
	}
	m.mu.Unlock()
	rep2, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"})
	if err != nil {
		t.Fatalf("subsequent Run: %v (lock not released on abort?)", err)
	}
	if rep2.Digested != 1 || rep2.Unchanged != 1 {
		t.Fatalf("second run counts = %+v, want digested=1 unchanged=1", rep2)
	}
}

func TestRunContextCanceled(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	h.write(t, "a.md", "one")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rep, err := h.runner.Run(ctx, RunOptions{Origin: "scheduled"})
	if err == nil {
		t.Fatal("expected error on canceled context")
	}
	if !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("err = %v, want context canceled", err)
	}
	if rep == nil {
		t.Fatal("partial report should be non-nil")
	}
	if len(h.llm.requests) != 0 {
		t.Fatal("digest attempted after cancel")
	}
}

func TestRunSourceDirReadError(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	h.write(t, "note.md", "content")

	// Prepend a nonexistent source dir; the valid one must still process and the
	// read failure must surface in the report while the run exits without error.
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	h.runner.cfg.Sources = append([]config.SourceDir{{Path: missing, Types: []string{"md"}}}, h.runner.cfg.Sources...)

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.SourceErrors != 1 {
		t.Fatalf("SourceErrors = %d, want 1", rep.SourceErrors)
	}
	if rep.Digested != 1 {
		t.Fatalf("Digested = %d, want 1 (other sources must still process)", rep.Digested)
	}
	var found bool
	for _, f := range rep.PerFile {
		if f.Action == actionSourceError && f.Path == missing && f.Error != "" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no source-error entry for %s in %+v", missing, rep.PerFile)
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
