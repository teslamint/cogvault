package ingest

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/teslamint/cogvault/internal/adapter/markdown"
	"github.com/teslamint/cogvault/internal/config"
	"github.com/teslamint/cogvault/internal/index"
	"github.com/teslamint/cogvault/internal/llm"
	"github.com/teslamint/cogvault/internal/storage"
)

var errPermanent = errors.New("permanent digest failure")

type mockLLM struct {
	mu        sync.Mutex
	requests  []llm.DigestRequest
	fn        func(req llm.DigestRequest) (*llm.DigestResult, error)
	inputMode llm.InputMode
}

func (m *mockLLM) Digest(_ context.Context, req llm.DigestRequest) (*llm.DigestResult, error) {
	m.mu.Lock()
	m.requests = append(m.requests, req)
	m.mu.Unlock()
	return m.fn(req)
}

func (m *mockLLM) InputMode() llm.InputMode { return m.inputMode }

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
		WikiDir:       wikiDir,
		DBPath:        dbPath,
		Sources:       []config.SourceDir{{Path: srcDir, Types: types}},
		Adapter:       "obsidian",
		MaxFileSizeMB: 32,
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

func (h *harness) writeInDir(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func logFixture(t *testing.T, h *harness, extras ...string) {
	t.Helper()
	root := filepath.Dir(h.wikiDir)
	t.Logf("fixture_root=%s wiki_dir=%s src_dir=%s db_path=%s", root, h.wikiDir, h.srcDir, h.dbPath)
	for _, extra := range extras {
		t.Log(extra)
	}
}

func TestNewAttentionPermanentFailureReachesMaxAttempts(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	h.runner.cfg.LLM.Model = "current-model"
	report := &Report{}
	entry := scanEntry{absPath: filepath.Join(h.srcDir, "permanent.md"), sourceDir: h.srcDir}
	prev := &ledgerRow{attempts: maxAttempts - 1, llmModel: "current-model"}

	h.runner.recordFailure(entry, "hash", "scheduled", prev, report, "validate: invalid page", classPermanent)

	want := []FileResult{{Path: entry.absPath, Action: actionFailed, Error: "validate: invalid page"}}
	if !reflect.DeepEqual(report.NewAttention, want) {
		t.Fatalf("NewAttention = %#v, want %#v", report.NewAttention, want)
	}
}

func TestNewAttentionAlreadyExhaustedCurrentModelIsExcluded(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	h.runner.cfg.LLM.Model = "current-model"
	report := &Report{}
	entry := scanEntry{absPath: filepath.Join(h.srcDir, "exhausted.md"), sourceDir: h.srcDir}
	prev := &ledgerRow{attempts: maxAttempts, llmModel: "current-model"}

	h.runner.recordFailure(entry, "hash", "scheduled", prev, report, "validate: invalid page", classPermanent)

	if len(report.NewAttention) != 0 {
		t.Fatalf("NewAttention = %#v, want empty", report.NewAttention)
	}
}

func TestNewAttentionNewRefusal(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	h.runner.cfg.LLM.Model = "current-model"
	report := &Report{}
	entry := scanEntry{absPath: filepath.Join(h.srcDir, "refused.md"), sourceDir: h.srcDir}

	h.runner.recordFailure(entry, "hash", "scheduled", nil, report, "digest: policy refusal", classRefused)

	want := []FileResult{{Path: entry.absPath, Action: actionRefused, Error: "digest: policy refusal"}}
	if !reflect.DeepEqual(report.NewAttention, want) {
		t.Fatalf("NewAttention = %#v, want %#v", report.NewAttention, want)
	}
}

func TestNewAttentionAlreadyRefusedCurrentModelIsExcluded(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	h.runner.cfg.LLM.Model = "current-model"
	report := &Report{}
	entry := scanEntry{absPath: filepath.Join(h.srcDir, "refused.md"), sourceDir: h.srcDir}
	prev := &ledgerRow{status: "refused", llmModel: "current-model"}

	h.runner.recordFailure(entry, "hash", "scheduled", prev, report, "digest: policy refusal", classRefused)

	if len(report.NewAttention) != 0 {
		t.Fatalf("NewAttention = %#v, want empty", report.NewAttention)
	}
}

func TestNewAttentionModelChangeCarryover(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	h.runner.cfg.LLM.Model = "new-model"
	report := &Report{}
	entry := scanEntry{absPath: filepath.Join(h.srcDir, "carryover.md"), sourceDir: h.srcDir}
	prev := &ledgerRow{attempts: maxAttempts, llmModel: "old-model"}

	h.runner.recordFailure(entry, "hash", "scheduled", prev, report, "validate: invalid page", classPermanent)

	want := []FileResult{{Path: entry.absPath, Action: actionFailed, Error: "validate: invalid page"}}
	if !reflect.DeepEqual(report.NewAttention, want) {
		t.Fatalf("NewAttention = %#v, want %#v", report.NewAttention, want)
	}
}

func TestNewAttentionTransientAndInfraFailuresAreExcluded(t *testing.T) {
	for _, class := range []failureClass{classTransient, classInfra} {
		t.Run(failureClassName(class), func(t *testing.T) {
			h := newHarness(t, []string{"md"}, okLLM())
			h.runner.cfg.LLM.Model = "current-model"
			report := &Report{}
			entry := scanEntry{absPath: filepath.Join(h.srcDir, "retry.md"), sourceDir: h.srcDir}
			prev := &ledgerRow{attempts: maxAttempts, llmModel: "old-model"}

			h.runner.recordFailure(entry, "hash", "scheduled", prev, report, "digest: retry", class)

			if len(report.NewAttention) != 0 {
				t.Fatalf("NewAttention = %#v, want empty", report.NewAttention)
			}
		})
	}
}

func TestNewAttentionLedgerUpsertFailureStillProducesCandidate(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	h.runner.cfg.LLM.Model = "current-model"
	report := &Report{}
	entry := scanEntry{absPath: filepath.Join(h.srcDir, "closed-ledger.md"), sourceDir: h.srcDir}
	prev := &ledgerRow{attempts: maxAttempts - 1, llmModel: "current-model"}
	if err := h.runner.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	h.runner.recordFailure(entry, "hash", "scheduled", prev, report, "validate: invalid page", classPermanent)

	want := []FileResult{{Path: entry.absPath, Action: actionFailed, Error: "validate: invalid page"}}
	if !reflect.DeepEqual(report.NewAttention, want) {
		t.Fatalf("NewAttention = %#v, want %#v", report.NewAttention, want)
	}
}

func failureClassName(class failureClass) string {
	switch class {
	case classTransient:
		return "transient"
	case classInfra:
		return "infra"
	default:
		return "unknown"
	}
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
	if rep.SumMismatch != "" {
		t.Fatalf("sum mismatch: %s", rep.SumMismatch)
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
	if rep.SumMismatch != "" {
		t.Fatalf("sum mismatch: %s", rep.SumMismatch)
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
	if rep.SumMismatch != "" {
		t.Fatalf("sum mismatch: %s", rep.SumMismatch)
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
	if rep.SumMismatch != "" {
		t.Fatalf("sum mismatch: %s", rep.SumMismatch)
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

func TestRunSymlinkSilentlySkipped(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	h.write(t, "real.md", "content")
	target := filepath.Join(h.srcDir, "real.md")
	link := filepath.Join(h.srcDir, "link.md")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Scanned != 1 {
		t.Errorf("scanned = %d, want 1 (symlink silently skipped pre-scan)", rep.Scanned)
	}
	if rep.Digested != 1 {
		t.Errorf("digested = %d, want 1 (only real.md)", rep.Digested)
	}
}

func TestRunConfigMaxFileSize(t *testing.T) {
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
		WikiDir:       wikiDir,
		DBPath:        dbPath,
		Sources:       []config.SourceDir{{Path: srcDir, Types: []string{"md"}}},
		Adapter:       "obsidian",
		MaxFileSizeMB: 1,
	}
	store := storage.NewFSStorage(wikiDir, cfg)
	idx, err := index.NewSQLiteIndex(wikiDir, dbPath, cfg)
	if err != nil {
		t.Fatalf("NewSQLiteIndex: %v", err)
	}
	t.Cleanup(func() { idx.Close() })

	m := okLLM()
	runner, err := New(cfg, store, idx, m, dbPath)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { runner.Close() })
	runner.settleWindow = 0

	if runner.maxFileSize != int64(1)<<20 {
		t.Fatalf("maxFileSize = %d, want %d (1MB from config)", runner.maxFileSize, int64(1)<<20)
	}

	if err := os.WriteFile(filepath.Join(srcDir, "small.md"), []byte("fits"), 0o644); err != nil {
		t.Fatal(err)
	}
	big := make([]byte, (1<<20)+1)
	for i := range big {
		big[i] = 'x'
	}
	if err := os.WriteFile(filepath.Join(srcDir, "big.md"), big, 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := runner.Run(context.Background(), RunOptions{Origin: "interactive"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Digested != 1 {
		t.Fatalf("digested = %d, want 1", rep.Digested)
	}
	if rep.Skipped != 1 {
		t.Fatalf("skipped = %d, want 1", rep.Skipped)
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
	if rep.SumMismatch != "" {
		t.Fatalf("sum mismatch: %s", rep.SumMismatch)
	}
	if len(m.requests) != callsBefore {
		t.Fatal("exhausted file was re-digested")
	}
	if len(rep.PerFile) != 1 || rep.PerFile[0].Action != actionExhausted {
		t.Fatalf("perfile = %+v, want exhausted", rep.PerFile)
	}
}

func TestRunRefusedTerminalNoAttempt(t *testing.T) {
	m := &mockLLM{fn: func(req llm.DigestRequest) (*llm.DigestResult, error) {
		return nil, llm.ErrRefused
	}}
	h := newHarness(t, []string{"md"}, m)
	src := h.write(t, "a.md", "one")

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Refused != 1 || rep.Failed != 0 {
		t.Fatalf("counts = %+v, want refused=1 failed=0", rep)
	}
	if rep.SumMismatch != "" {
		t.Fatalf("sum mismatch: %s", rep.SumMismatch)
	}
	row, found, _ := h.runner.ledger.lookup(src, contentHash([]byte("one")))
	if !found || row.status != "refused" || row.attempts != 0 {
		t.Fatalf("row = %+v, want refused attempts=0", row)
	}
	if len(rep.PerFile) != 1 || rep.PerFile[0].Action != actionRefused {
		t.Fatalf("perfile = %+v, want refused action", rep.PerFile)
	}
}

func TestRunRefusedSkippedWhenModelMatches(t *testing.T) {
	m := &mockLLM{fn: func(req llm.DigestRequest) (*llm.DigestResult, error) {
		return nil, llm.ErrRefused
	}}
	h := newHarness(t, []string{"md"}, m)
	h.write(t, "a.md", "one")

	// First run with default model "" -> refused row llmModel="".
	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	callsBefore := len(m.requests)

	// Second run, same model "" -> skip, no new llm call.
	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if len(m.requests) != callsBefore {
		t.Fatal("refused file re-digested despite matching model")
	}
	if rep.Refused != 0 || rep.Skipped != 1 {
		t.Fatalf("counts = %+v, want refused=0 skipped=1", rep)
	}
	if rep.SumMismatch != "" {
		t.Fatalf("sum mismatch: %s", rep.SumMismatch)
	}
}

func TestRunRefusedReattemptedOnModelChange(t *testing.T) {
	m := &mockLLM{fn: func(req llm.DigestRequest) (*llm.DigestResult, error) {
		return nil, llm.ErrRefused
	}}
	h := newHarness(t, []string{"md"}, m)
	src := h.write(t, "a.md", "one")

	// First run with default model "" -> refused row llmModel="".
	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"}); err != nil {
		t.Fatalf("first run: %v", err)
	}

	// Switch to a passing digest on a new model.
	m.mu.Lock()
	m.fn = func(req llm.DigestRequest) (*llm.DigestResult, error) {
		return &llm.DigestResult{PageContent: validPage(req.PageSlug)}, nil
	}
	m.mu.Unlock()
	h.runner.cfg.LLM.Model = "opus"
	callsBefore := len(m.requests)

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if len(m.requests) == callsBefore {
		t.Fatal("refused file not re-attempted on model change")
	}
	if rep.Digested != 1 {
		t.Fatalf("digested = %d, want 1 (re-attempt succeeded)", rep.Digested)
	}
	row, _, _ := h.runner.ledger.lookup(src, contentHash([]byte("one")))
	if row.status != "success" || row.llmModel != "opus" {
		t.Fatalf("row = %+v, want success llmModel=opus", row)
	}
}

func TestRunRefusedRowRecordsConfiguredModel(t *testing.T) {
	m := &mockLLM{fn: func(req llm.DigestRequest) (*llm.DigestResult, error) {
		return nil, llm.ErrRefused
	}}
	h := newHarness(t, []string{"md"}, m)
	h.runner.cfg.LLM.Model = "opus"
	src := h.write(t, "a.md", "one")
	hash := contentHash([]byte("one"))

	// First run under "opus": the refusal upsert must thread the configured model
	// into the ledger row (attempts=0, terminal-under-same-model).
	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"})
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if rep.Refused != 1 || rep.Failed != 0 {
		t.Fatalf("counts = %+v, want refused=1 failed=0", rep)
	}
	row, found, _ := h.runner.ledger.lookup(src, hash)
	if !found || row.status != "refused" || row.attempts != 0 || row.llmModel != "opus" {
		t.Fatalf("row = %+v found=%v, want refused attempts=0 llmModel=opus", row, found)
	}

	// Second run, still under "opus": the stored model matches the configured
	// model, so the refused file is skipped before the LLM runs (no new calls).
	callsBefore := len(m.requests)
	rep2, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if len(m.requests) != callsBefore {
		t.Fatal("refused file re-digested despite matching model")
	}
	if rep2.Refused != 0 || rep2.Skipped != 1 {
		t.Fatalf("counts = %+v, want refused=0 skipped=1", rep2)
	}
}

func TestRunSuccessNotReattemptedOnModelChange(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	h.write(t, "a.md", "one")
	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	callsBefore := len(h.llm.requests)

	h.runner.cfg.LLM.Model = "opus"
	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if rep.Unchanged != 1 || rep.Digested != 0 {
		t.Fatalf("counts = %+v, want unchanged=1 digested=0", rep)
	}
	if len(h.llm.requests) != callsBefore {
		t.Fatal("success file re-digested on model change")
	}
}

func TestRunExhaustedReattemptedOnModelChange(t *testing.T) {
	m := &mockLLM{fn: func(req llm.DigestRequest) (*llm.DigestResult, error) {
		return nil, errPermanent
	}}
	h := newHarness(t, []string{"md"}, m)
	src := h.write(t, "a.md", "one")
	hash := contentHash([]byte("one"))

	for i := 0; i < 3; i++ {
		if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"}); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	row, _, _ := h.runner.ledger.lookup(src, hash)
	if row.attempts != 3 {
		t.Fatalf("attempts = %d, want 3", row.attempts)
	}

	// Same model -> exhausted skip, no new llm call.
	callsBefore := len(m.requests)
	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"})
	if err != nil {
		t.Fatalf("run 4: %v", err)
	}
	if len(m.requests) != callsBefore || rep.Skipped != 1 {
		t.Fatalf("run 4: calls changed or not skipped, rep=%+v", rep)
	}

	// Model change -> re-attempt.
	h.runner.cfg.LLM.Model = "opus"
	callsBefore = len(m.requests)
	rep2, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"})
	if err != nil {
		t.Fatalf("run 5: %v", err)
	}
	if len(m.requests) == callsBefore {
		t.Fatal("exhausted file not re-attempted on model change")
	}
	if rep2.Failed != 1 {
		t.Fatalf("run 5 failed = %d, want 1 (re-attempt failed permanent)", rep2.Failed)
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

func TestRunContextCanceledWithoutOrphanCandidates(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	logFixture(t, h, "boundary_sentinel="+h.srcDir)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rep, err := h.runner.Run(ctx, RunOptions{Origin: "scheduled"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context canceled", err)
	}
	if rep == nil {
		t.Fatal("report should be non-nil")
	}
}

func TestRunReturnsSweepLedgerQueryFailure(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	src := h.write(t, "known.md", "known content")
	h.write(t, "pending.md", "pending content")
	logFixture(t, h, "boundary_sentinel="+filepath.Join(h.wikiDir, "sources", "known.md"))

	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"}); err != nil {
		t.Fatal(err)
	}
	knownResults, err := h.idx.Search("known", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(knownResults) == 0 {
		t.Fatal("expected known page in index before failure")
	}
	callsBefore := len(h.llm.requests)
	beforeWiki := snapshotPaths(t, h.wikiDir)
	beforeRows := ledgerCount(t, h.runner.ledger)
	if err := h.runner.ledger.close(); err != nil {
		t.Fatal(err)
	}

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "ingest.ledger.successRows") {
		t.Fatalf("err = %v, want successRows cause", err)
	}
	if rep == nil {
		t.Fatal("report = nil, want partial report")
	}
	if len(rep.PerFile) != 0 {
		t.Fatalf("per-file = %+v, want empty", rep.PerFile)
	}
	if rep.Digested != 0 || rep.Failed != 0 || rep.Archived != 0 || rep.SourceErrors != 0 || rep.Unchanged != 0 || rep.Skipped != 0 || rep.Deferred != 0 || rep.Refused != 0 {
		t.Fatalf("report = %+v, want empty counts", rep)
	}
	if len(h.llm.requests) != callsBefore {
		t.Fatalf("llm calls = %d, want %d total (no extra calls after forced failure)", len(h.llm.requests), callsBefore)
	}
	afterWiki := snapshotPaths(t, h.wikiDir)
	if !reflect.DeepEqual(beforeWiki, afterWiki) {
		t.Fatalf("wiki changed across forced failure: before=%v after=%v", beforeWiki, afterWiki)
	}
	knownResultsAfter, err := h.idx.Search("known", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(knownResultsAfter) != len(knownResults) {
		t.Fatalf("index changed across forced failure: before=%d after=%d", len(knownResults), len(knownResultsAfter))
	}
	if count := ledgerCountClosedOK(t, h.dbPath); count != beforeRows {
		t.Fatalf("ledger rows changed across forced failure: before=%d after=%d", beforeRows, count)
	}
	row, found, err := lookupWithFreshLedger(h.dbPath, src, contentHash([]byte("known content")))
	if err != nil {
		t.Fatal(err)
	}
	if !found || row.status != "success" {
		t.Fatalf("row after forced failure: found=%v row=%+v, want preserved success", found, row)
	}
}

func TestRunRetriesAfterSweepLedgerQueryFailure(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	src := h.write(t, "retry.md", "retry content")
	logFixture(t, h, "boundary_sentinel="+filepath.Join(h.wikiDir, "sources", "retry.md"))

	if err := h.runner.ledger.close(); err != nil {
		t.Fatal(err)
	}
	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"})
	if err == nil {
		t.Fatal("expected first-run error")
	}
	if rep == nil || len(rep.PerFile) != 0 {
		t.Fatalf("rep = %+v, want empty partial report", rep)
	}

	reopened, err := openLedger(h.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	h.runner.ledger = reopened

	rep2, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"})
	if err != nil {
		t.Fatalf("recovery run: %v", err)
	}
	if rep2.Digested != 1 {
		t.Fatalf("digested = %d, want 1", rep2.Digested)
	}
	if len(h.llm.requests) != 1 {
		t.Fatalf("llm calls = %d, want 1 after recovery rerun", len(h.llm.requests))
	}
	if ok, _ := h.store.Exists("sources/retry.md"); !ok {
		t.Fatal("recovery run should reacquire lock and write the page")
	}
	row, found, err := h.runner.ledger.lookup(src, contentHash([]byte("retry content")))
	if err != nil {
		t.Fatal(err)
	}
	if !found || row.status != "success" {
		t.Fatalf("row after recovery: found=%v row=%+v, want success", found, row)
	}
}

func TestRunScheduledReturnsSweepLedgerQueryFailure(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	h.write(t, "known.md", "known content")
	logFixture(t, h, "boundary_sentinel="+filepath.Join(h.srcDir, "known.md"))

	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"}); err != nil {
		t.Fatal(err)
	}
	if err := h.runner.ledger.close(); err != nil {
		t.Fatal(err)
	}

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "ingest.ledger.successRows") {
		t.Fatalf("err = %v, want successRows cause", err)
	}
	if rep == nil || len(rep.PerFile) != 0 {
		t.Fatalf("rep = %+v, want empty partial report", rep)
	}
	if len(h.llm.requests) != 1 {
		t.Fatalf("llm calls = %d, want unchanged pre-failure count", len(h.llm.requests))
	}
}

func TestRunSourceDirReadError(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	h.write(t, "note.md", "content")

	// Prepend a nonexistent source dir; the valid one must still process and the
	// read failure must surface in the report while the run exits without error.
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	_, wantReadErr := os.ReadDir(missing)
	if wantReadErr == nil {
		t.Fatal("ReadDir missing source: got nil error")
	}
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
		if f.Action == actionSourceError && f.Path == missing {
			if f.Error != wantReadErr.Error() {
				t.Fatalf("source error = %q, want %q", f.Error, wantReadErr.Error())
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("no source-error entry for %s in %+v", missing, rep.PerFile)
	}
}

func TestRunSourcePermissionDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses mode bits")
	}

	const permissionDiagnostic = "permission denied: cannot read source"
	permissionError := permissionDiagnostic
	if runtime.GOOS == "darwin" {
		permissionError += `; macOS consent required, see README "Schedule zero-touch ingest"`
	}

	tests := []struct {
		name             string
		prepare          func(t *testing.T, h *harness) string
		wantAction       string
		wantSourceErrors int
		wantEntries      int
	}{
		{
			name: "dir",
			prepare: func(t *testing.T, h *harness) string {
				h.write(t, "note.md", "content")
				if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"}); err != nil {
					t.Fatalf("first Run: %v", err)
				}
				if err := os.Chmod(h.srcDir, 0); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(h.srcDir, 0o755) })
				return h.srcDir
			},
			wantAction:       actionSourceError,
			wantSourceErrors: 2,
			wantEntries:      2,
		},
		{
			name: "dir-unswept",
			prepare: func(t *testing.T, h *harness) string {
				if err := os.Chmod(h.srcDir, 0); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(h.srcDir, 0o755) })
				return h.srcDir
			},
			wantAction:       actionSourceError,
			wantSourceErrors: 1,
			wantEntries:      1,
		},
		{
			name: "file",
			prepare: func(t *testing.T, h *harness) string {
				path := h.write(t, "note.md", "content")
				if err := os.Chmod(path, 0); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
				return path
			},
			wantAction:       actionSkipped,
			wantSourceErrors: 0,
			wantEntries:      1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, []string{"md"}, okLLM())
			path := tt.prepare(t, h)

			rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if rep.SourceErrors != tt.wantSourceErrors {
				t.Fatalf("SourceErrors = %d, want %d", rep.SourceErrors, tt.wantSourceErrors)
			}

			var entries []FileResult
			for _, result := range rep.PerFile {
				if result.Path == path && result.Action == tt.wantAction {
					entries = append(entries, result)
				}
			}
			if len(entries) != tt.wantEntries {
				t.Fatalf("entries = %+v, want %d %s entries for %s", entries, tt.wantEntries, tt.wantAction, path)
			}
			wantError := permissionError
			if tt.wantAction == actionSkipped {
				wantError = "read: " + wantError
			}
			for _, entry := range entries {
				if entry.Error != wantError {
					t.Fatalf("error = %q, want %q", entry.Error, wantError)
				}
			}
		})
	}
}

func TestSourceErrorTextPermissionDenied(t *testing.T) {
	want := "permission denied: cannot read source"
	if runtime.GOOS == "darwin" {
		want += `; macOS consent required, see README "Schedule zero-touch ingest"`
	}
	if got := sourceErrorText(fs.ErrPermission); got != want {
		t.Fatalf("sourceErrorText(fs.ErrPermission) = %q, want %q", got, want)
	}
}

func TestSweepOrphansSourceDeleted(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	src := h.write(t, "victim.md", "content")
	h.write(t, "survivor.md", "still here")
	logFixture(t, h, "boundary_sentinel="+filepath.Join(h.srcDir, "survivor.md"))

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"})
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if rep.Digested != 2 {
		t.Fatalf("digested = %d, want 2", rep.Digested)
	}

	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}

	rep2, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if rep2.Archived != 1 {
		t.Fatalf("archived = %d, want 1", rep2.Archived)
	}

	if ok, _ := h.store.Exists("sources/victim.md"); ok {
		t.Fatal("original page should be gone")
	}

	hash := contentHash([]byte("content"))
	archivePath := "sources/_archived/victim-" + hash[:8] + ".md"
	if ok, err := h.store.Exists(archivePath); err != nil || !ok {
		t.Fatalf("archive exists=%v err=%v, want true nil", ok, err)
	}
	archivedBody, err := h.store.Read(archivePath)
	if err != nil {
		t.Fatalf("Read(%q): %v", archivePath, err)
	}
	if !strings.Contains(string(archivedBody), "title: victim") {
		t.Fatalf("archive body missing victim title: %q", string(archivedBody))
	}
	row, found, _ := h.runner.ledger.lookup(src, hash)
	if !found || row.status != "superseded" {
		t.Fatalf("ledger: found=%v status=%v, want superseded", found, row.status)
	}
}

func TestSweepOrphansAllPresent(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	h.write(t, "keep.md", "content")

	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"}); err != nil {
		t.Fatal(err)
	}

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Archived != 0 {
		t.Fatalf("archived = %d, want 0", rep.Archived)
	}
}

func TestSweepOrphansDryRun(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	src := h.write(t, "dry.md", "content")
	h.write(t, "survivor.md", "still here")

	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"}); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}

	rep, err := h.runner.Run(context.Background(), RunOptions{DryRun: true, Origin: "interactive"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Archived != 1 {
		t.Fatalf("archived = %d, want 1", rep.Archived)
	}

	found := false
	for _, f := range rep.PerFile {
		if f.Action == actionWouldArchive {
			found = true
		}
	}
	if !found {
		t.Fatal("expected would-archive action in report")
	}

	if ok, _ := h.store.Exists("sources/dry.md"); !ok {
		t.Fatal("dry-run should not move the page")
	}
	if ok, _ := h.store.Exists("sources/_archived/dry-" + contentHash([]byte("content"))[:8] + ".md"); ok {
		t.Fatal("dry-run should not create an archive destination")
	}
	row, found, err := h.runner.ledger.lookup(src, contentHash([]byte("content")))
	if err != nil {
		t.Fatal(err)
	}
	if !found || row.status != "success" {
		t.Fatalf("ledger: found=%v status=%v, want success", found, row.status)
	}
}

func TestSweepOrphansSourceDirMissing(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	src := h.write(t, "orphan.md", "content")

	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"}); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(h.srcDir); err != nil {
		t.Fatal(err)
	}

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Archived != 0 {
		t.Fatalf("archived = %d, want 0 (dir missing = skip)", rep.Archived)
	}
}

func TestSweepOrphansWikiPageAlreadyGone(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	src := h.write(t, "gone.md", "content")
	h.write(t, "survivor.md", "still here")
	logFixture(t, h, "boundary_sentinel="+filepath.Join(h.wikiDir, "sources", "_archived", "gone-"+contentHash([]byte("content"))[:8]+".md"))

	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"}); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	livePath := filepath.Join(h.wikiDir, "sources", "gone.md")
	archivePath := filepath.Join(h.wikiDir, "sources", "_archived", "gone-"+contentHash([]byte("content"))[:8]+".md")
	archivedBody, err := os.ReadFile(livePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(archivePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archivePath, archivedBody, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(livePath); err != nil {
		t.Fatal(err)
	}

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Archived != 1 {
		t.Fatalf("archived = %d, want 1 (ledger still updated)", rep.Archived)
	}

	hash := contentHash([]byte("content"))
	row, found, _ := h.runner.ledger.lookup(src, hash)
	if !found || row.status != "superseded" {
		t.Fatalf("ledger: found=%v status=%v, want superseded", found, row.status)
	}
	archivedBodyAfter, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(archivedBodyAfter) != string(archivedBody) {
		t.Fatalf("archive bytes changed across rerun")
	}
}

func TestSweepOrphansSearchExclusion(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	h.runner.cfg.Exclude = []string{"sources/_archived"}
	src := h.write(t, "searchme.md", "unique-search-term-xyz")
	h.write(t, "survivor.md", "still here")

	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"}); err != nil {
		t.Fatal(err)
	}

	results, err := h.idx.Search("searchme", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected search hit before archive")
	}

	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}

	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"}); err != nil {
		t.Fatal(err)
	}

	adpt := &markdown.MarkdownAdapter{}
	sqlIdx := h.idx.(*index.SQLiteIndex)
	if _, _, _, err := sqlIdx.CheckConsistency(h.store, adpt, true); err != nil {
		t.Fatalf("CheckConsistency: %v", err)
	}

	results, err = h.idx.Search("searchme", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 search results after archive, got %d", len(results))
	}
}

func TestSweepOrphansSkipsWhenNoSurvivorRemains(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	first := h.write(t, "first.md", "one")
	second := h.write(t, "second.md", "two")

	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(first); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(second); err != nil {
		t.Fatal(err)
	}

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Archived != 0 {
		t.Fatalf("archived = %d, want 0", rep.Archived)
	}
	for _, page := range []string{"sources/first.md", "sources/second.md"} {
		if ok, _ := h.store.Exists(page); !ok {
			t.Fatalf("%s missing; ambiguous no-survivor state must keep pages live", page)
		}
	}
}

func TestSweepOrphansSkipsWhenSeveralRowsAreMissing(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	survivor := h.write(t, "survivor.md", "keep")
	missingOne := h.write(t, "missing-one.md", "one")
	missingTwo := h.write(t, "missing-two.md", "two")

	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{missingOne, missingTwo} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Archived != 0 {
		t.Fatalf("archived = %d, want 0", rep.Archived)
	}
	if _, err := os.Stat(survivor); err != nil {
		t.Fatalf("survivor stat: %v", err)
	}
	for _, page := range []string{"sources/missing-one.md", "sources/missing-two.md"} {
		if ok, _ := h.store.Exists(page); !ok {
			t.Fatalf("%s missing; multi-missing state must keep pages live", page)
		}
	}
}

func TestSweepOrphansRestoredBeforeMoveCancelsArchive(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	src := h.write(t, "victim.md", "content")
	h.write(t, "survivor.md", "keep")

	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}

	defaultReadDir := os.ReadDir
	var calls int
	h.runner.readDir = func(path string) ([]os.DirEntry, error) {
		calls++
		entries, err := defaultReadDir(path)
		if err != nil {
			return nil, err
		}
		if calls == 1 {
			return entries, nil
		}
		if err := os.WriteFile(src, []byte("content"), 0o644); err != nil {
			t.Fatalf("restore source: %v", err)
		}
		return defaultReadDir(path)
	}

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Archived != 0 {
		t.Fatalf("archived = %d, want 0", rep.Archived)
	}
	if ok, _ := h.store.Exists("sources/victim.md"); !ok {
		t.Fatal("restored source must cancel archive move")
	}
	hash := contentHash([]byte("content"))
	row, found, _ := h.runner.ledger.lookup(src, hash)
	if !found || row.status != "success" {
		t.Fatalf("ledger: found=%v status=%v, want success", found, row.status)
	}
}

func TestSweepOrphansReadFailure(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	src := h.write(t, "victim.md", "content")
	h.write(t, "survivor.md", "keep")

	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}

	wantErr := errors.New("snapshot read failed")
	h.runner.readDir = func(string) ([]os.DirEntry, error) {
		return nil, wantErr
	}

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if rep == nil {
		t.Fatal("report = nil, want partial report")
	}
	if rep.Archived != 0 {
		t.Fatalf("archived = %d, want 0", rep.Archived)
	}
	foundReport := false
	for _, file := range rep.PerFile {
		if file.Action == actionSourceError && file.Path == h.srcDir && strings.Contains(file.Error, wantErr.Error()) {
			foundReport = true
			break
		}
	}
	if !foundReport {
		t.Fatalf("missing source-error report entry for snapshot failure: %+v", rep.PerFile)
	}
	if ok, _ := h.store.Exists("sources/victim.md"); !ok {
		t.Fatal("read failure must preserve live page")
	}
	hash := contentHash([]byte("content"))
	row, found, _ := h.runner.ledger.lookup(src, hash)
	if !found || row.status != "success" {
		t.Fatalf("ledger: found=%v status=%v, want success", found, row.status)
	}
}

func TestSweepOrphansMoveFailure(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	src := h.write(t, "victim.md", "content")
	h.write(t, "survivor.md", "keep")
	logFixture(t, h, "boundary_sentinel="+filepath.Join(h.wikiDir, "sources", "victim.md"))

	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}

	h.runner.cfg.ExcludeRead = []string{"sources/_archived"}

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Archived != 0 {
		t.Fatalf("archived = %d, want 0", rep.Archived)
	}
	if ok, _ := h.store.Exists("sources/victim.md"); !ok {
		t.Fatal("move failure must preserve live page")
	}
	hash := contentHash([]byte("content"))
	row, found, _ := h.runner.ledger.lookup(src, hash)
	if !found || row.status != "success" {
		t.Fatalf("ledger: found=%v status=%v, want success", found, row.status)
	}
}

func TestSweepOrphansLedgerSupersedeFailure(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	src := h.write(t, "victim.md", "content")
	h.write(t, "survivor.md", "keep")
	logFixture(t, h, "boundary_sentinel="+filepath.Join(h.wikiDir, "sources", "victim.md"))

	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	if err := installSupersedeAbortTrigger(h.runner.ledger); err != nil {
		t.Fatal(err)
	}

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Archived != 0 {
		t.Fatalf("archived = %d, want 0", rep.Archived)
	}
	archivePath := "sources/_archived/victim-" + contentHash([]byte("content"))[:8] + ".md"
	if ok, _ := h.store.Exists(archivePath); !ok {
		t.Fatalf("expected archived page at %s", archivePath)
	}
	row, found, err := h.runner.ledger.lookup(src, contentHash([]byte("content")))
	if err != nil {
		t.Fatal(err)
	}
	if !found || row.status != "success" {
		t.Fatalf("ledger: found=%v status=%v, want success", found, row.status)
	}
}

func TestSweepOrphansLedgerFailureMissingSourceRerun(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	src := h.write(t, "victim.md", "content")
	h.write(t, "survivor.md", "keep")
	logFixture(t, h, "boundary_sentinel="+filepath.Join(h.wikiDir, "sources", "_archived", "victim-"+contentHash([]byte("content"))[:8]+".md"))

	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	if err := installSupersedeAbortTrigger(h.runner.ledger); err != nil {
		t.Fatal(err)
	}
	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"}); err != nil {
		t.Fatal(err)
	}
	if err := dropSupersedeAbortTrigger(h.runner.ledger); err != nil {
		t.Fatal(err)
	}

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Archived != 1 {
		t.Fatalf("archived = %d, want 1", rep.Archived)
	}
	row, found, err := h.runner.ledger.lookup(src, contentHash([]byte("content")))
	if err != nil {
		t.Fatal(err)
	}
	if !found || row.status != "superseded" {
		t.Fatalf("ledger: found=%v status=%v, want superseded", found, row.status)
	}
}

func TestSweepOrphansLedgerFailureRestoredSource(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	src := h.write(t, "victim.md", "content")
	h.write(t, "survivor.md", "keep")
	logFixture(t, h, "boundary_sentinel="+src)

	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	if err := installSupersedeAbortTrigger(h.runner.ledger); err != nil {
		t.Fatal(err)
	}
	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"}); err != nil {
		t.Fatal(err)
	}
	if err := dropSupersedeAbortTrigger(h.runner.ledger); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Digested != 1 {
		t.Fatalf("digested = %d, want 1", rep.Digested)
	}
	if ok, _ := h.store.Exists("sources/victim.md"); !ok {
		t.Fatal("restored source should rebuild live page")
	}
	after, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("source mtime changed during recovery: before=%v after=%v", before.ModTime(), after.ModTime())
	}
	row, found, err := h.runner.ledger.lookup(src, contentHash([]byte("content")))
	if err != nil {
		t.Fatal(err)
	}
	if !found || row.status != "success" || row.wikiPage != "sources/victim.md" {
		t.Fatalf("ledger: found=%v row=%+v, want success bound to sources/victim.md", found, row)
	}
}

func TestSweepOrphansLedgerFailureScheduledRecovery(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	src := h.write(t, "victim.md", "content")
	h.write(t, "survivor.md", "keep")
	logFixture(t, h, "boundary_sentinel="+src)

	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	if err := installSupersedeAbortTrigger(h.runner.ledger); err != nil {
		t.Fatal(err)
	}
	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"}); err != nil {
		t.Fatal(err)
	}
	if err := dropSupersedeAbortTrigger(h.runner.ledger); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Digested != 1 {
		t.Fatalf("digested = %d, want 1", rep.Digested)
	}
	row, found, err := h.runner.ledger.lookup(src, contentHash([]byte("content")))
	if err != nil {
		t.Fatal(err)
	}
	if !found || row.status != "success" || row.runOrigin != "scheduled" {
		t.Fatalf("ledger: found=%v row=%+v, want scheduled success", found, row)
	}
}

func TestSweepOrphansScheduled(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	src := h.write(t, "victim.md", "content")
	h.write(t, "survivor.md", "keep")
	logFixture(t, h, "boundary_sentinel="+filepath.Join(h.srcDir, "survivor.md"))

	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Archived != 1 {
		t.Fatalf("archived = %d, want 1", rep.Archived)
	}
}

func TestSweepOrphansCancellationBetweenCandidates(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	srcDir2 := filepath.Join(filepath.Dir(h.srcDir), "src2")
	if err := os.MkdirAll(srcDir2, 0o755); err != nil {
		t.Fatal(err)
	}
	h.runner.cfg.Sources = []config.SourceDir{
		{Path: h.srcDir, Types: []string{"md"}},
		{Path: srcDir2, Types: []string{"md"}},
	}
	logFixture(t, h, "boundary_sentinel="+filepath.Join(h.wikiDir, "sources", "second-missing.md"))

	firstMissing := h.write(t, "first-missing.md", "one")
	h.write(t, "first-survivor.md", "keep one")
	secondMissing := h.writeInDir(t, srcDir2, "second-missing.md", "two")
	h.writeInDir(t, srcDir2, "second-survivor.md", "keep two")

	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{firstMissing, secondMissing} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defaultReadDir := os.ReadDir
	var calls atomic.Int32
	h.runner.readDir = func(path string) ([]os.DirEntry, error) {
		n := calls.Add(1)
		entries, err := defaultReadDir(path)
		if err != nil {
			return nil, err
		}
		if n == 3 {
			cancel()
		}
		return entries, nil
	}

	rep, err := h.runner.Run(ctx, RunOptions{Origin: "interactive"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context canceled", err)
	}
	if rep == nil {
		t.Fatal("report = nil, want partial report")
	}
	firstHash := contentHash([]byte("one"))
	firstRow, found, _ := h.runner.ledger.lookup(firstMissing, firstHash)
	if !found || firstRow.status != "superseded" {
		t.Fatalf("first row: found=%v status=%v, want superseded", found, firstRow.status)
	}
	secondHash := contentHash([]byte("two"))
	secondRow, found, _ := h.runner.ledger.lookup(secondMissing, secondHash)
	if !found || secondRow.status != "success" {
		t.Fatalf("second row: found=%v status=%v, want success", found, secondRow.status)
	}
	if ok, _ := h.store.Exists("sources/second-missing.md"); !ok {
		t.Fatal("second candidate page must remain live after cancellation")
	}
}

func TestSweepOrphansCanceledAfterFinalRecheck(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	src := h.write(t, "victim.md", "content")
	h.write(t, "survivor.md", "keep")

	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defaultReadDir := os.ReadDir
	var calls int
	h.runner.readDir = func(path string) ([]os.DirEntry, error) {
		calls++
		entries, err := defaultReadDir(path)
		if err != nil {
			return nil, err
		}
		if calls == 2 {
			cancel()
		}
		return entries, nil
	}

	rep, err := h.runner.Run(ctx, RunOptions{Origin: "interactive"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context canceled", err)
	}
	if rep == nil {
		t.Fatal("report = nil, want partial report")
	}
	if rep.Archived != 0 {
		t.Fatalf("archived = %d, want 0", rep.Archived)
	}
	if ok, _ := h.store.Exists("sources/victim.md"); !ok {
		t.Fatal("cancellation after final recheck must preserve live page")
	}
	row, found, err := h.runner.ledger.lookup(src, contentHash([]byte("content")))
	if err != nil {
		t.Fatal(err)
	}
	if !found || row.status != "success" {
		t.Fatalf("ledger: found=%v row=%+v, want preserved success row", found, row)
	}
}

func TestSweepOrphansArchivesTwoSameBasenameVersionsSeparately(t *testing.T) {
	m := &mockLLM{fn: func(req llm.DigestRequest) (*llm.DigestResult, error) {
		body, err := os.ReadFile(req.SourcePath)
		if err != nil {
			return nil, err
		}
		label := strings.TrimSpace(string(body))
		return &llm.DigestResult{PageContent: "---\ntitle: " + label + "\ntype: source\n---\n\nbody " + label + "\n"}, nil
	}}
	h := newHarness(t, []string{"md"}, m)
	src := h.write(t, "victim.md", "v1")
	h.write(t, "survivor.md", "keep")

	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	if err := installSupersedeAbortTrigger(h.runner.ledger); err != nil {
		t.Fatal(err)
	}
	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"}); err != nil {
		t.Fatal(err)
	}
	if err := dropSupersedeAbortTrigger(h.runner.ledger); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Archived != 1 {
		t.Fatalf("archived = %d, want 1", rep.Archived)
	}
	hash1 := contentHash([]byte("v1"))
	hash2 := contentHash([]byte("v2"))
	archive1 := "sources/_archived/victim-" + hash1[:8] + ".md"
	archive2 := "sources/_archived/victim-" + hash2[:8] + ".md"
	if archive1 == archive2 {
		t.Fatal("archive paths should differ by content hash")
	}
	body1, err := h.store.Read(archive1)
	if err != nil {
		t.Fatalf("Read(%q): %v", archive1, err)
	}
	body2, err := h.store.Read(archive2)
	if err != nil {
		t.Fatalf("Read(%q): %v", archive2, err)
	}
	if string(body1) == string(body2) {
		t.Fatalf("archived bodies should differ between versions")
	}
	if !strings.Contains(string(body1), "title: v1") || !strings.Contains(string(body2), "title: v2") {
		t.Fatalf("unexpected archived bodies: body1=%q body2=%q", string(body1), string(body2))
	}
	row1, found, err := h.runner.ledger.lookup(src, hash1)
	if err != nil {
		t.Fatal(err)
	}
	if !found || row1.status != "superseded" {
		t.Fatalf("row1: found=%v row=%+v, want superseded", found, row1)
	}
	row2, found, err := h.runner.ledger.lookup(src, hash2)
	if err != nil {
		t.Fatal(err)
	}
	if !found || row2.status != "superseded" {
		t.Fatalf("row2: found=%v row=%+v, want superseded", found, row2)
	}
}

func TestSweepOrphansExactSnapshotUsesTrackedPaths(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	src := h.write(t, "victim.md", "content")
	h.write(t, "survivor.md", "keep")

	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.srcDir, "victim-copy.md"), []byte("replacement"), 0o644); err != nil {
		t.Fatal(err)
	}

	defaultReadDir := os.ReadDir
	var snapshots [][]string
	h.runner.readDir = func(path string) ([]os.DirEntry, error) {
		entries, err := defaultReadDir(path)
		if err != nil {
			return nil, err
		}
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		snapshots = append(snapshots, names)
		return entries, nil
	}

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Archived != 1 {
		t.Fatalf("archived = %d, want 1", rep.Archived)
	}
	if len(snapshots) < 2 || !reflect.DeepEqual(snapshots[0], snapshots[1]) {
		t.Fatalf("snapshots = %#v, want identical pre-move recheck", snapshots)
	}
}

func TestRunRebuildsMissingSuccessPage(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	src := h.write(t, "missing-page.md", "content")
	logFixture(t, h, "boundary_sentinel="+src)

	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(h.wikiDir, "sources", "missing-page.md")); err != nil {
		t.Fatal(err)
	}

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Digested != 1 || rep.Unchanged != 0 {
		t.Fatalf("counts = %+v, want digested=1 unchanged=0", rep)
	}
	if ok, _ := h.store.Exists("sources/missing-page.md"); !ok {
		t.Fatal("missing success page should be rebuilt")
	}
	if meta, err := h.idx.GetMeta("sources/missing-page.md"); err != nil || meta == nil {
		t.Fatalf("index meta: %v", err)
	}
	row, found, err := h.runner.ledger.lookup(src, contentHash([]byte("content")))
	if err != nil {
		t.Fatal(err)
	}
	if !found || row.status != "success" || row.wikiPage != "sources/missing-page.md" {
		t.Fatalf("ledger: found=%v row=%+v, want rebuilt success row", found, row)
	}
}

func TestRunMissingSuccessPageDigestFailure(t *testing.T) {
	m := okLLM()
	h := newHarness(t, []string{"md"}, m)
	src := h.write(t, "missing-page.md", "content")
	logFixture(t, h, "boundary_sentinel="+src)

	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(h.wikiDir, "sources", "missing-page.md")); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.fn = func(req llm.DigestRequest) (*llm.DigestResult, error) {
		return nil, llm.ErrTransient
	}
	m.mu.Unlock()

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Unchanged != 0 || rep.Failed != 1 {
		t.Fatalf("counts = %+v, want unchanged=0 failed=1", rep)
	}
	row, found, err := h.runner.ledger.lookup(src, contentHash([]byte("content")))
	if err != nil {
		t.Fatal(err)
	}
	if !found || row.status != "failed" || row.attempts != 0 {
		t.Fatalf("ledger: found=%v row=%+v, want retryable failed row", found, row)
	}
}

func TestRunMissingSuccessPageRetry(t *testing.T) {
	m := okLLM()
	h := newHarness(t, []string{"md"}, m)
	src := h.write(t, "missing-page.md", "content")
	logFixture(t, h, "boundary_sentinel="+src)

	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(h.wikiDir, "sources", "missing-page.md")); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.fn = func(req llm.DigestRequest) (*llm.DigestResult, error) {
		return nil, llm.ErrTransient
	}
	m.mu.Unlock()
	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"}); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.fn = func(req llm.DigestRequest) (*llm.DigestResult, error) {
		return &llm.DigestResult{PageContent: validPage(req.PageSlug)}, nil
	}
	m.mu.Unlock()

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Digested != 1 {
		t.Fatalf("digested = %d, want 1", rep.Digested)
	}
	if ok, _ := h.store.Exists("sources/missing-page.md"); !ok {
		t.Fatal("retry should rebuild live page")
	}
	row, found, err := h.runner.ledger.lookup(src, contentHash([]byte("content")))
	if err != nil {
		t.Fatal(err)
	}
	if !found || row.status != "success" {
		t.Fatalf("ledger: found=%v row=%+v, want success", found, row)
	}
}

func TestRunMissingSuccessPageWriteFailure(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	src := h.write(t, "missing-page.md", "content")
	logFixture(t, h, "boundary_sentinel="+src)

	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(h.wikiDir, "sources", "missing-page.md")); err != nil {
		t.Fatal(err)
	}
	writeErr := errors.New("disk full")
	h.runner.store = failWriteStorage{Storage: h.store, err: writeErr}

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Failed != 1 || rep.Unchanged != 0 {
		t.Fatalf("counts = %+v, want failed=1 unchanged=0", rep)
	}
	if ok, _ := h.store.Exists("sources/missing-page.md"); ok {
		t.Fatal("write failure should not leave a partial page")
	}
	row, found, err := h.runner.ledger.lookup(src, contentHash([]byte("content")))
	if err != nil {
		t.Fatal(err)
	}
	if !found || row.status != "failed" {
		t.Fatalf("ledger: found=%v row=%+v, want failed", found, row)
	}

	h.runner.store = h.store
	rep2, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"})
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Digested != 1 {
		t.Fatalf("retry digested = %d, want 1", rep2.Digested)
	}
}

func TestRunMissingSuccessPageScheduled(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	src := h.write(t, "missing-page.md", "content")
	logFixture(t, h, "boundary_sentinel="+src)

	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(h.wikiDir, "sources", "missing-page.md")); err != nil {
		t.Fatal(err)
	}

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "scheduled"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Digested != 1 {
		t.Fatalf("digested = %d, want 1", rep.Digested)
	}
	row, found, err := h.runner.ledger.lookup(src, contentHash([]byte("content")))
	if err != nil {
		t.Fatal(err)
	}
	if !found || row.status != "success" || row.runOrigin != "scheduled" {
		t.Fatalf("ledger: found=%v row=%+v, want scheduled success", found, row)
	}
}

func TestRunMissingSuccessPageCanceled(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	src := h.write(t, "missing-page.md", "content")
	logFixture(t, h, "boundary_sentinel="+src)

	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(h.wikiDir, "sources", "missing-page.md")); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rep, err := h.runner.Run(ctx, RunOptions{Origin: "interactive"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context canceled", err)
	}
	if rep == nil {
		t.Fatal("report = nil, want partial report")
	}
	if ok, _ := h.store.Exists("sources/missing-page.md"); ok {
		t.Fatal("cancellation should prevent a rebuilt page")
	}
	row, found, err := h.runner.ledger.lookup(src, contentHash([]byte("content")))
	if err != nil {
		t.Fatal(err)
	}
	if !found || row.status != "success" {
		t.Fatalf("ledger: found=%v row=%+v, want original success row", found, row)
	}
}

func TestRunMissingSuccessPageStatPermissionFailure(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	src := h.write(t, "missing-page.md", "content")
	logFixture(t, h, "boundary_sentinel="+src)

	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"}); err != nil {
		t.Fatal(err)
	}
	h.runner.cfg.ExcludeRead = []string{"sources"}

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Failed != 1 || rep.Unchanged != 0 {
		t.Fatalf("counts = %+v, want failed=1 unchanged=0", rep)
	}
	foundFailed := false
	for _, file := range rep.PerFile {
		if file.Action == actionFailed && file.Path == src && strings.Contains(file.Error, "stat wiki page:") {
			foundFailed = true
			break
		}
	}
	if !foundFailed {
		t.Fatalf("missing stat failure entry: %+v", rep.PerFile)
	}
	row, found, err := h.runner.ledger.lookup(src, contentHash([]byte("content")))
	if err != nil {
		t.Fatal(err)
	}
	if !found || row.status != "success" {
		t.Fatalf("ledger: found=%v row=%+v, want preserved success row", found, row)
	}
}

func TestRunMissingSuccessPageStatErrorPreservesSuccessRow(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	src := h.write(t, "missing-page.md", "content")
	logFixture(t, h, "boundary_sentinel="+src)

	if _, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"}); err != nil {
		t.Fatal(err)
	}
	h.runner.store = failStatStorage{Storage: h.store, target: "sources/missing-page.md", err: errors.New("stat exploded")}

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Failed != 1 || rep.Unchanged != 0 {
		t.Fatalf("counts = %+v, want failed=1 unchanged=0", rep)
	}
	foundFailed := false
	for _, file := range rep.PerFile {
		if file.Action == actionFailed && file.Path == src && strings.Contains(file.Error, "stat wiki page: stat exploded") {
			foundFailed = true
			break
		}
	}
	if !foundFailed {
		t.Fatalf("missing explicit stat failure entry: %+v", rep.PerFile)
	}
	row, found, err := h.runner.ledger.lookup(src, contentHash([]byte("content")))
	if err != nil {
		t.Fatal(err)
	}
	if !found || row.status != "success" {
		t.Fatalf("ledger: found=%v row=%+v, want preserved success row", found, row)
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

func snapshotPaths(t *testing.T, root string) []string {
	t.Helper()
	var paths []string
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		paths = append(paths, rel)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return paths
}

func ledgerCount(t *testing.T, l *ledger) int {
	t.Helper()
	var count int
	if err := l.db.QueryRow(`SELECT COUNT(1) FROM ingest_ledger`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func ledgerCountClosedOK(t *testing.T, dbPath string) int {
	t.Helper()
	l, err := openLedger(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer l.close()
	return ledgerCount(t, l)
}

func lookupWithFreshLedger(dbPath, sourcePath, hash string) (*ledgerRow, bool, error) {
	l, err := openLedger(dbPath)
	if err != nil {
		return nil, false, err
	}
	defer l.close()
	return l.lookup(sourcePath, hash)
}

func installSupersedeAbortTrigger(l *ledger) error {
	_, err := l.db.Exec(`CREATE TRIGGER reject_superseded BEFORE INSERT ON ingest_ledger
FOR EACH ROW WHEN NEW.status = 'superseded'
BEGIN
	SELECT RAISE(ABORT, 'reject superseded');
END;`)
	return err
}

func dropSupersedeAbortTrigger(l *ledger) error {
	_, err := l.db.Exec(`DROP TRIGGER IF EXISTS reject_superseded`)
	return err
}

type failStatStorage struct {
	storage.Storage
	target string
	err    error
}

func (f failStatStorage) Stat(path string) (int64, time.Time, error) {
	if path == f.target {
		return 0, time.Time{}, f.err
	}
	return f.Storage.Stat(path)
}

func TestSumCheckUnit(t *testing.T) {
	t.Run("balanced", func(t *testing.T) {
		r := &Report{Scanned: 5, Digested: 2, Failed: 1, Skipped: 1, Unchanged: 1}
		if err := r.SumCheck(); err != nil {
			t.Fatalf("SumCheck: %v", err)
		}
	})
	t.Run("with not-examined", func(t *testing.T) {
		r := &Report{Scanned: 5, Digested: 1, NotExamined: 4}
		if err := r.SumCheck(); err != nil {
			t.Fatalf("SumCheck: %v", err)
		}
	})
	t.Run("mismatch", func(t *testing.T) {
		r := &Report{Scanned: 5, Digested: 2}
		if err := r.SumCheck(); err == nil {
			t.Fatal("SumCheck should fail on mismatch")
		}
	})
	t.Run("archived excluded", func(t *testing.T) {
		r := &Report{Scanned: 2, Digested: 2, Archived: 3}
		if err := r.SumCheck(); err != nil {
			t.Fatalf("SumCheck should ignore Archived: %v", err)
		}
	})
	t.Run("source-errors excluded", func(t *testing.T) {
		r := &Report{Scanned: 2, Digested: 2, SourceErrors: 1}
		if err := r.SumCheck(); err != nil {
			t.Fatalf("SumCheck should ignore SourceErrors: %v", err)
		}
	})
	t.Run("zero scanned zero sum", func(t *testing.T) {
		r := &Report{}
		if err := r.SumCheck(); err != nil {
			t.Fatalf("SumCheck: %v", err)
		}
	})
	t.Run("all refused", func(t *testing.T) {
		r := &Report{Scanned: 3, Refused: 3}
		if err := r.SumCheck(); err != nil {
			t.Fatalf("SumCheck: %v", err)
		}
	})
}

func TestSumCheckIntegrationHappyPath(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	h.write(t, "a.md", "one")
	h.write(t, "b.md", "two")

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "test"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Scanned != 2 {
		t.Fatalf("scanned = %d, want 2", rep.Scanned)
	}
	if rep.SumMismatch != "" {
		t.Fatalf("unexpected sum mismatch: %s", rep.SumMismatch)
	}
}

func TestSumCheckWithLimit(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	h.write(t, "a.md", "one")
	h.write(t, "b.md", "two")
	h.write(t, "c.md", "three")

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "test", Limit: 1})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Scanned != 3 {
		t.Fatalf("scanned = %d, want 3", rep.Scanned)
	}
	if rep.Digested != 1 {
		t.Fatalf("digested = %d, want 1", rep.Digested)
	}
	if rep.NotExamined != 2 {
		t.Fatalf("not-examined = %d, want 2", rep.NotExamined)
	}
	if rep.SumMismatch != "" {
		t.Fatalf("unexpected sum mismatch: %s", rep.SumMismatch)
	}
}

func TestSumCheckSkippedType(t *testing.T) {
	h := newHarness(t, []string{"pdf"}, okLLM())
	h.write(t, "a.txt", "wrong type")
	h.write(t, "b.pdf", "right type")

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "test"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Scanned != 2 {
		t.Fatalf("scanned = %d, want 2", rep.Scanned)
	}
	if rep.Skipped != 1 {
		t.Fatalf("skipped = %d, want 1", rep.Skipped)
	}
	if rep.SumMismatch != "" {
		t.Fatalf("unexpected sum mismatch: %s", rep.SumMismatch)
	}
}

func TestSumCheckStringOutput(t *testing.T) {
	r := &Report{Scanned: 3, Digested: 2, Skipped: 1}
	s := r.String()
	if !strings.Contains(s, "scanned=3") {
		t.Fatalf("String() missing scanned=3: %s", s)
	}
	if strings.Contains(s, "not-examined=") {
		t.Fatalf("String() should omit not-examined when zero: %s", s)
	}

	r2 := &Report{Scanned: 5, Digested: 1, NotExamined: 4}
	s2 := r2.String()
	if !strings.Contains(s2, "not-examined=4") {
		t.Fatalf("String() missing not-examined=4: %s", s2)
	}
}

func TestSumCheckMismatchInString(t *testing.T) {
	r := &Report{Scanned: 5, Digested: 2, SumMismatch: "report sum mismatch: scanned=5 sum=2"}
	s := r.String()
	if !strings.Contains(s, "!!") {
		t.Fatalf("String() should show !! for mismatch: %s", s)
	}
	if !strings.Contains(s, "report sum mismatch") {
		t.Fatalf("String() should include mismatch message: %s", s)
	}
}

func TestSumCheckReadPermissionError(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	h.write(t, "good.md", "content")
	bad := filepath.Join(h.srcDir, "bad.md")
	os.WriteFile(bad, []byte("x"), 0o644)
	os.Chmod(bad, 0o000)
	t.Cleanup(func() { os.Chmod(bad, 0o644) })

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "test"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Scanned != 2 {
		t.Fatalf("scanned = %d, want 2 (good.md + bad.md)", rep.Scanned)
	}
	if rep.Skipped != 1 {
		t.Fatalf("skipped = %d, want 1 (bad.md read error)", rep.Skipped)
	}
	if rep.Digested != 1 {
		t.Fatalf("digested = %d, want 1 (good.md)", rep.Digested)
	}
	if rep.SumMismatch != "" {
		t.Fatalf("sum mismatch: %s", rep.SumMismatch)
	}
	found := false
	for _, f := range rep.PerFile {
		if f.Path == bad && f.Action == actionSkipped && strings.Contains(f.Error, "read:") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected skipped entry for bad.md with read error, got: %+v", rep.PerFile)
	}
}

func TestDigestSourceExtPassedToLLM(t *testing.T) {
	m := okLLM()
	h := newHarness(t, []string{"md"}, m)
	h.write(t, "notes.md", "some markdown content")

	rep, err := h.runner.Run(context.Background(), RunOptions{Origin: "test"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Digested != 1 {
		t.Fatalf("digested = %d, want 1", rep.Digested)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.requests) != 1 {
		t.Fatalf("llm requests = %d, want 1", len(m.requests))
	}
	if got := m.requests[0].SourceExt; got != ".md" {
		t.Errorf("SourceExt = %q, want %q", got, ".md")
	}
}
