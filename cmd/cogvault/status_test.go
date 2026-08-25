package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const statusTestModel = "status-test-model"

func setupStatusVault(t *testing.T) (configPath, dbPath string) {
	t.Helper()
	configPath, srcDir, wikiDir, dbPath := setupIngestVault(t)
	writeIngestConfigWithModel(t, configPath, wikiDir, dbPath, srcDir, statusTestModel)
	if _, _, err := executeCommand("ingest", "--config", configPath, "--dry-run"); err != nil {
		t.Fatalf("create ingest ledger: %v", err)
	}
	return configPath, dbPath
}

func seedStatusRow(t *testing.T, dbPath, path, hash, timestamp, status string, attempts int, lastError string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer db.Close()
	_, err = db.Exec(
		`INSERT INTO ingest_ledger
		 (source_path, content_hash, source_dir, digested_at, wiki_page, status, attempts, last_error, run_origin, llm_model)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		path, hash, filepath.Dir(path), timestamp, "", status, attempts, lastError, "interactive", statusTestModel,
	)
	if err != nil {
		t.Fatalf("seed ledger row: %v", err)
	}
}

func TestStatusHumanOutput(t *testing.T) {
	configPath, dbPath := setupStatusVault(t)
	exhaustedTime := "2026-08-25T01:02:03.123456789Z"
	refusedTime := "2026-08-25T02:03:04Z"
	seedStatusRow(t, dbPath, "/source/broken.pdf", "broken", exhaustedTime, "failed", 3, "validate: missing title")
	seedStatusRow(t, dbPath, "/source/refused.pdf", "refused", refusedTime, "refused", 1, "digest: policy refusal")

	stdout, _, err := executeCommand("status", "--config", configPath)
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	want := "주의 필요: 2건\n" +
		"  exhausted  broken.pdf  validate: missing title  (" + statusLocalMinute(t, exhaustedTime) + ")\n" +
		"  refused  refused.pdf  digest: policy refusal  (" + statusLocalMinute(t, refusedTime) + ")\n"
	if stdout != want {
		t.Fatalf("status output = %q, want %q", stdout, want)
	}
}

func statusLocalMinute(t *testing.T, timestamp string) string {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		t.Fatalf("parse test timestamp: %v", err)
	}
	return parsed.In(time.Local).Format("2006-01-02 15:04")
}

func TestStatusJSONOutput(t *testing.T) {
	configPath, dbPath := setupStatusVault(t)
	wantTimestamp := "2026-08-25T01:02:03.123456789Z"
	seedStatusRow(t, dbPath, "/source/broken.pdf", "broken", wantTimestamp, "failed", 3, "validate: missing title")

	stdout, _, err := executeCommand("status", "--config", configPath, "--json")
	if err != nil {
		t.Fatalf("status --json failed: %v", err)
	}
	var got struct {
		Attention []map[string]any `json:"attention"`
		Model     string           `json:"model"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("decode status JSON %q: %v", stdout, err)
	}
	if got.Model != statusTestModel {
		t.Fatalf("model = %q, want %q", got.Model, statusTestModel)
	}
	if len(got.Attention) != 1 {
		t.Fatalf("attention count = %d, want 1: %s", len(got.Attention), stdout)
	}
	want := map[string]any{
		"path":         "/source/broken.pdf",
		"status":       "exhausted",
		"error":        "validate: missing title",
		"last_attempt": wantTimestamp,
		"llm_model":    statusTestModel,
		"attempts":     float64(3),
	}
	if len(got.Attention[0]) != len(want) {
		t.Fatalf("attention fields = %#v, want exactly %#v", got.Attention[0], want)
	}
	for key, wantValue := range want {
		if got.Attention[0][key] != wantValue {
			t.Errorf("attention[%q] = %#v, want %#v", key, got.Attention[0][key], wantValue)
		}
	}
}

func TestStatusMissingDatabaseIsClean(t *testing.T) {
	configPath, _, _, dbPath := setupIngestVault(t)
	if _, err := os.Stat(dbPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("database stat before status = %v, want not exist", err)
	}

	stdout, _, err := executeCommand("status", "--config", configPath)
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if stdout != "주의 필요 항목 없음.\n" {
		t.Fatalf("status output = %q, want clean output", stdout)
	}
	if _, err := os.Stat(dbPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("database stat after status = %v, want not exist", err)
	}
}

func TestStatusEmptyDatabaseIsClean(t *testing.T) {
	configPath, _ := setupStatusVault(t)
	stdout, _, err := executeCommand("status", "--config", configPath)
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if stdout != "주의 필요 항목 없음.\n" {
		t.Fatalf("status output = %q, want clean output", stdout)
	}
}

func TestStatusInvalidConfigPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	_, _, err := executeCommand("status", "--config", missing)
	if err == nil {
		t.Fatal("status succeeded with missing config, want error")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Fatalf("error = %q, want missing config path", err)
	}
}

func TestStatusRejectsInvalidStoredTimestamp(t *testing.T) {
	configPath, dbPath := setupStatusVault(t)
	seedStatusRow(t, dbPath, "/source/broken.pdf", "broken", "not-a-timestamp", "failed", 3, "validate: missing title")

	_, _, err := executeCommand("status", "--config", configPath)
	if err == nil {
		t.Fatal("status succeeded with invalid stored timestamp, want error")
	}
	if !strings.Contains(err.Error(), "invalid last_attempt") || !strings.Contains(err.Error(), "not-a-timestamp") {
		t.Fatalf("error = %q, want clear invalid last_attempt error", err)
	}
}

func TestStatusRegisteredWithFlags(t *testing.T) {
	stdout, _, err := executeCommand("status", "--help")
	if err != nil {
		t.Fatalf("status --help failed: %v", err)
	}
	for _, want := range []string{"cogvault status", "--config", "--json"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("status help missing %q: %s", want, stdout)
		}
	}
}
