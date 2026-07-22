package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// copyFixtureInto copies the fixture tree into dest.
func copyFixtureInto(t *testing.T, src, dest string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copyFixtureInto: %v", err)
	}
}

func TestCLIIntegration_InitWithRealFixture(t *testing.T) {
	base := t.TempDir()
	wikiDir := filepath.Join(base, "wiki")
	dbPath := filepath.Join(base, "index.db")
	configPath := filepath.Join(base, "config.yaml")

	// The wiki root holds the fixture content that the single-root index scans.
	copyFixtureInto(t, "../../testdata/fixtures/real", wikiDir)
	writeConfigFile(t, configPath, wikiDir, dbPath, "")

	stdout, stderr, err := executeCommand("init", "--config", configPath)
	if err != nil {
		t.Fatalf("init failed: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stdout, "Initialized") {
		t.Errorf("expected 'Initialized' in output, got: %q", stdout)
	}

	// Korean content
	stdout, _, err = executeCommand("search", "--config", configPath, "프로젝트")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if !strings.Contains(stdout, "프로젝트") {
		t.Errorf("expected Korean search results, got: %q", stdout)
	}

	// English content
	stdout, _, err = executeCommand("search", "--config", configPath, "LLM")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if !strings.Contains(stdout, "llm") {
		t.Errorf("expected LLM in search results, got: %q", stdout)
	}
}
