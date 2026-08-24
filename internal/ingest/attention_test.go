package ingest

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAttentionRowsExported(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "ledger.db")
	l, err := openLedger(dbPath)
	if err != nil {
		t.Fatalf("openLedger: %v", err)
	}
	wantTimestamp := "2026-08-25T01:02:03.123456789Z"
	if err := l.upsert(ledgerRow{
		sourcePath: "/src/exhausted.md", contentHash: "hash",
		digestedAt: wantTimestamp, status: "failed", attempts: 3,
		lastError: "validate: missing title", llmModel: "current-model",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := l.close(); err != nil {
		t.Fatalf("close ledger: %v", err)
	}

	got, err := AttentionRows(dbPath, "current-model")
	if err != nil {
		t.Fatalf("AttentionRows: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(rows) = %d, want 1: %+v", len(got), got)
	}
	want := AttentionRow{
		Path: "/src/exhausted.md", Status: "exhausted",
		Error: "validate: missing title", LastAttempt: wantTimestamp,
		Model: "current-model", Attempts: 3,
	}
	if got[0] != want {
		t.Fatalf("row = %+v, want %+v", got[0], want)
	}

	encoded, err := json.Marshal(got[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	wantJSON := `{"path":"/src/exhausted.md","status":"exhausted","error":"validate: missing title","last_attempt":"2026-08-25T01:02:03.123456789Z","llm_model":"current-model","attempts":3}`
	if string(encoded) != wantJSON {
		t.Fatalf("JSON = %s, want %s", encoded, wantJSON)
	}
}

func TestAttentionRowsMissingDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "missing.db")
	got, err := AttentionRows(dbPath, "current-model")
	if err != nil {
		t.Fatalf("AttentionRows: %v", err)
	}
	if got != nil {
		t.Fatalf("rows = %+v, want nil", got)
	}
	if _, err := os.Stat(dbPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("database stat error = %v, want not exist", err)
	}
}
