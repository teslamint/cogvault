package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/teslamint/cogvault/internal/schema"
)

func executeCommand(args ...string) (stdout, stderr string, err error) {
	root := newRootCmd()
	outBuf := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)
	root.SetOut(outBuf)
	root.SetErr(errBuf)
	root.SetArgs(args)
	err = root.Execute()
	return outBuf.String(), errBuf.String(), err
}

func TestInitCreatesFiles(t *testing.T) {
	dir := t.TempDir()
	stdout, _, err := executeCommand("init", "--vault", dir)
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	if stdout == "" {
		t.Error("expected output from init")
	}

	for _, rel := range []string{
		".cogvault.yaml",
		"_wiki",
		filepath.Join("_wiki", "_schema.md"),
		".cogvault.db",
	} {
		p := filepath.Join(dir, rel)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s to exist: %v", rel, err)
		}
	}
}

func TestInitSchemaMatchesEmbed(t *testing.T) {
	dir := t.TempDir()
	_, _, err := executeCommand("init", "--vault", dir)
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	got := readFile(t, filepath.Join(dir, "_wiki", "_schema.md"))
	if got != schema.DefaultContent {
		t.Errorf("schema file content does not match embedded asset:\ngot length:  %d\nwant length: %d", len(got), len(schema.DefaultContent))
	}
}

func TestInitIdempotent(t *testing.T) {
	dir := t.TempDir()

	// First init
	_, _, err := executeCommand("init", "--vault", dir)
	if err != nil {
		t.Fatalf("first init failed: %v", err)
	}

	// Record content for config and schema (not mtime — low-resolution FS may false-pass)
	configContent := readFile(t, filepath.Join(dir, ".cogvault.yaml"))
	schemaContent := readFile(t, filepath.Join(dir, "_wiki", "_schema.md"))

	// Second init
	_, _, err = executeCommand("init", "--vault", dir)
	if err != nil {
		t.Fatalf("second init failed: %v", err)
	}

	if got := readFile(t, filepath.Join(dir, ".cogvault.yaml")); got != configContent {
		t.Error("config file content changed on second init")
	}
	if got := readFile(t, filepath.Join(dir, "_wiki", "_schema.md")); got != schemaContent {
		t.Error("schema file content changed on second init")
	}
}

func TestInitIndexesExistingFiles(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "hello.md"), []byte("# Hello World\n\nSome content about testing."), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := executeCommand("init", "--vault", dir)
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	stdout, _, err := executeCommand("search", "--vault", dir, "Hello")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if !strings.Contains(stdout, "hello.md") {
		t.Errorf("expected search results to contain hello.md, got: %q", stdout)
	}
}

func TestInitReindexesOnSecondRun(t *testing.T) {
	dir := t.TempDir()

	_, _, err := executeCommand("init", "--vault", dir)
	if err != nil {
		t.Fatalf("first init failed: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "newfile.md"), []byte("# New File\n\nUnique content about cogvault."), 0o644); err != nil {
		t.Fatal(err)
	}

	// Second init: CheckConsistency(force=true) picks up the new file
	_, _, err = executeCommand("init", "--vault", dir)
	if err != nil {
		t.Fatalf("second init failed: %v", err)
	}

	stdout, _, err := executeCommand("search", "--vault", dir, "cogvault")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if !strings.Contains(stdout, "newfile.md") {
		t.Errorf("expected newfile.md in search results, got: %q", stdout)
	}
}

func TestSearchNoResults(t *testing.T) {
	dir := t.TempDir()

	_, _, err := executeCommand("init", "--vault", dir)
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	stdout, _, err := executeCommand("search", "--vault", dir, "nonexistentxyz")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if !strings.Contains(stdout, "No results") {
		t.Errorf("expected 'No results' message, got: %q", stdout)
	}
}

func TestSearchScopeFiltering(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "vaultnote.md"), []byte("# Vault Only\n\nSpecial vault content scopetest."), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := executeCommand("init", "--vault", dir)
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	wikiDir := filepath.Join(dir, "_wiki")
	if err := os.WriteFile(filepath.Join(wikiDir, "wikinote.md"), []byte("---\ntype: source\n---\n# Wiki Note\n\nWiki content scopetest."), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err = executeCommand("init", "--vault", dir)
	if err != nil {
		t.Fatalf("re-init failed: %v", err)
	}

	// wiki scope
	stdout, _, err := executeCommand("search", "--vault", dir, "--scope", "wiki", "scopetest")
	if err != nil {
		t.Fatalf("wiki search failed: %v", err)
	}
	if !strings.Contains(stdout, "wikinote.md") {
		t.Errorf("expected wikinote.md in wiki scope, got: %q", stdout)
	}
	if strings.Contains(stdout, "vaultnote.md") {
		t.Errorf("vaultnote.md should not appear in wiki scope, got: %q", stdout)
	}

	// vault scope
	stdout, _, err = executeCommand("search", "--vault", dir, "--scope", "vault", "scopetest")
	if err != nil {
		t.Fatalf("vault search failed: %v", err)
	}
	if !strings.Contains(stdout, "vaultnote.md") {
		t.Errorf("expected vaultnote.md in vault scope, got: %q", stdout)
	}
	if strings.Contains(stdout, "wikinote.md") {
		t.Errorf("wikinote.md should not appear in vault scope, got: %q", stdout)
	}
}

func TestSearchLimitClamping(t *testing.T) {
	dir := t.TempDir()

	_, _, err := executeCommand("init", "--vault", dir)
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	// These should not error — invalid limits silently reset to 10
	for _, limit := range []string{"0", "-1", "200"} {
		_, _, err := executeCommand("search", "--vault", dir, "--limit", limit, "test")
		if err != nil {
			t.Errorf("search with --limit %s failed: %v", limit, err)
		}
	}
}

func TestConfigMissingError(t *testing.T) {
	dir := t.TempDir()

	_, _, err := executeCommand("search", "--vault", dir, "test")
	if err == nil {
		t.Error("expected error when config is missing for search")
	}

	_, _, err = executeCommand("serve", "--vault", dir)
	if err == nil {
		t.Error("expected error when config is missing for serve")
	}
}

func TestVaultNotSpecifiedUsesCwd(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetArgs([]string{})
	cmd.PersistentFlags().Set("vault", "")

	root, err := resolveVaultRoot(cmd)
	if err != nil {
		t.Fatalf("resolveVaultRoot failed: %v", err)
	}

	cwd, _ := os.Getwd()
	if root != cwd {
		t.Errorf("expected %q, got %q", cwd, root)
	}
}

func TestPathRebase(t *testing.T) {
	dir := t.TempDir()

	// Run init from a different cwd (t.Chdir is Go 1.24+ safe)
	t.Chdir(t.TempDir())

	_, _, err := executeCommand("init", "--vault", dir)
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	for _, rel := range []string{
		".cogvault.yaml",
		"_wiki",
		filepath.Join("_wiki", "_schema.md"),
		".cogvault.db",
	} {
		p := filepath.Join(dir, rel)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s under vault root: %v", rel, err)
		}
	}
}

func TestServeInitFailure(t *testing.T) {
	dir := t.TempDir()

	_, _, err := executeCommand("serve", "--vault", dir)
	if err == nil {
		t.Error("expected error when serving without config")
	}
}

func TestInitPerFileErrorContinues(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("test requires non-root to enforce file permissions")
	}

	dir := t.TempDir()

	// Create two files: one readable, one that will become unreadable
	if err := os.WriteFile(filepath.Join(dir, "good.md"), []byte("# Good\n\nSearchable content."), 0o644); err != nil {
		t.Fatal(err)
	}
	badPath := filepath.Join(dir, "unreadable.md")
	if err := os.WriteFile(badPath, []byte("# Bad\n\nContent."), 0o644); err != nil {
		t.Fatal(err)
	}

	// First init indexes both files
	_, _, err := executeCommand("init", "--vault", dir)
	if err != nil {
		t.Fatalf("first init failed: %v", err)
	}

	// Remove read permission — adapter scan finds it, storage.Read fails → per-file error
	if err := os.Chmod(badPath, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(badPath, 0o644) })

	// Second init: per-file error for unreadable.md, but init should succeed
	_, stderr, err := executeCommand("init", "--vault", dir)
	if err != nil {
		t.Fatalf("init should succeed despite per-file errors: %v", err)
	}
	if !strings.Contains(stderr, "warning") {
		t.Errorf("expected per-file warning on stderr, got: %q", stderr)
	}

	// Good file should still be searchable
	stdout, _, err := executeCommand("search", "--vault", dir, "Searchable")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if !strings.Contains(stdout, "good.md") {
		t.Errorf("expected good.md in results, got: %q", stdout)
	}
}

func TestInitSystemicErrorFails(t *testing.T) {
	dir := t.TempDir()

	// First init to create DB
	_, _, err := executeCommand("init", "--vault", dir)
	if err != nil {
		t.Fatalf("first init failed: %v", err)
	}

	// Corrupt the DB to trigger systemic error on CheckConsistency
	dbPath := filepath.Join(dir, ".cogvault.db")
	if err := os.WriteFile(dbPath, []byte("corrupted data"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Second init should fail with systemic error (can't open corrupted DB)
	_, _, err = executeCommand("init", "--vault", dir)
	if err == nil {
		t.Error("expected error when DB is corrupted")
	}
}

func TestWriteSchemaRejectsDirectory(t *testing.T) {
	dir := t.TempDir()

	// Create config first
	if err := os.WriteFile(filepath.Join(dir, ".cogvault.yaml"), []byte("wiki_dir: _wiki\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create _wiki/_schema.md as a directory (the conflict)
	schemaDir := filepath.Join(dir, "_wiki", "_schema.md")
	if err := os.MkdirAll(schemaDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Init should fail because _schema.md path is a directory
	_, _, err := executeCommand("init", "--vault", dir)
	if err == nil {
		t.Error("expected error when schema path is a directory")
	}
	if err != nil && !strings.Contains(err.Error(), "directory") {
		t.Errorf("expected directory-related error, got: %v", err)
	}
}

// helpers

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
