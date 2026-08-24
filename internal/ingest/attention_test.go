package ingest

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
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
	refusedTimestamp := "2026-08-25T01:02:04Z"
	if err := l.upsert(ledgerRow{
		sourcePath: "/src/refused.md", contentHash: "refused-hash",
		digestedAt: refusedTimestamp, status: "refused", attempts: 1,
		lastError: "digest: policy refusal", llmModel: "current-model",
	}); err != nil {
		t.Fatalf("upsert refused: %v", err)
	}
	if err := l.close(); err != nil {
		t.Fatalf("close ledger: %v", err)
	}

	got, err := AttentionRows(dbPath, "current-model")
	if err != nil {
		t.Fatalf("AttentionRows: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(rows) = %d, want 2: %+v", len(got), got)
	}
	sort.Slice(got, func(i, j int) bool { return got[i].Path < got[j].Path })
	want := []AttentionRow{
		{
			Path: "/src/exhausted.md", Status: "exhausted",
			Error: "validate: missing title", LastAttempt: wantTimestamp,
			Model: "current-model", Attempts: 3,
		},
		{
			Path: "/src/refused.md", Status: "refused",
			Error: "digest: policy refusal", LastAttempt: refusedTimestamp,
			Model: "current-model", Attempts: 1,
		},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rows[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}

	wantJSON := []string{
		`{"path":"/src/exhausted.md","status":"exhausted","error":"validate: missing title","last_attempt":"2026-08-25T01:02:03.123456789Z","llm_model":"current-model","attempts":3}`,
		`{"path":"/src/refused.md","status":"refused","error":"digest: policy refusal","last_attempt":"2026-08-25T01:02:04Z","llm_model":"current-model","attempts":1}`,
	}
	for i := range wantJSON {
		encoded, err := json.Marshal(got[i])
		if err != nil {
			t.Fatalf("marshal rows[%d]: %v", i, err)
		}
		if string(encoded) != wantJSON[i] {
			t.Fatalf("JSON rows[%d] = %s, want %s", i, encoded, wantJSON[i])
		}
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
