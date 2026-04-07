package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func copyFixtureDir(t *testing.T, src string) string {
	t.Helper()
	root := t.TempDir()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, _ := filepath.Rel(src, path)
		dest := filepath.Join(root, rel)
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copyFixtureDir: %v", err)
	}
	return root
}

func TestCLIIntegration_InitWithRealFixture(t *testing.T) {
	dir := copyFixtureDir(t, "../../testdata/fixtures/real")

	// Init the vault
	stdout, stderr, err := executeCommand("init", "--vault", dir)
	if err != nil {
		t.Fatalf("init failed: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stdout, "Initialized") {
		t.Errorf("expected 'Initialized' in output, got: %q", stdout)
	}

	// Search for Korean content
	stdout, _, err = executeCommand("search", "--vault", dir, "프로젝트")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if !strings.Contains(stdout, "프로젝트") {
		t.Errorf("expected Korean search results, got: %q", stdout)
	}

	// Search for English content
	stdout, _, err = executeCommand("search", "--vault", dir, "LLM")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if !strings.Contains(stdout, "llm") {
		t.Errorf("expected LLM in search results, got: %q", stdout)
	}

	// Scope filtering: wiki only
	stdout, _, err = executeCommand("search", "--vault", dir, "--scope", "wiki", "LLM")
	if err != nil {
		t.Fatalf("wiki search failed: %v", err)
	}
	if strings.Contains(stdout, "notes/") {
		t.Errorf("wiki scope should not return notes/ paths, got: %q", stdout)
	}
}
