package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/teslamint/cogvault/internal/config"
	"github.com/teslamint/cogvault/internal/schema"
	"golang.org/x/sys/unix"
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

// testVault creates a base dir with a valid config file pointing wiki_dir and
// db_path at absolute, non-overlapping paths. Returns configPath, wikiDir, dbPath.
func testVault(t *testing.T) (configPath, wikiDir, dbPath string) {
	t.Helper()
	base := t.TempDir()
	wikiDir = filepath.Join(base, "wiki")
	dbPath = filepath.Join(base, "index.db")
	configPath = filepath.Join(base, "config.yaml")
	if err := os.MkdirAll(wikiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfigFile(t, configPath, wikiDir, dbPath, "")
	return configPath, wikiDir, dbPath
}

// writeConfigFile writes a valid config YAML. If srcDir is non-empty, a single
// source directory (types: [md]) is included.
func writeConfigFile(t *testing.T, configPath, wikiDir, dbPath, srcDir string) {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "wiki_dir: %s\n", wikiDir)
	fmt.Fprintf(&b, "db_path: %s\n", dbPath)
	b.WriteString("adapter: obsidian\n")
	if srcDir != "" {
		fmt.Fprintf(&b, "sources:\n  - path: %s\n    types: [md]\n", srcDir)
	}
	if err := os.WriteFile(configPath, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInitCreatesFiles(t *testing.T) {
	configPath, wikiDir, dbPath := testVault(t)

	stdout, _, err := executeCommand("init", "--config", configPath)
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	if stdout == "" {
		t.Error("expected output from init")
	}

	for _, p := range []string{
		configPath,
		wikiDir,
		filepath.Join(wikiDir, "_schema.md"),
		dbPath,
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s to exist: %v", p, err)
		}
	}
}

func TestInitFirstRunScaffoldsConfig(t *testing.T) {
	base := t.TempDir()
	configPath := filepath.Join(base, "sub", "config.yaml")

	// No config exists yet: init should scaffold it, print guidance, and exit 0
	// without creating a wiki/db (the default config has empty wiki_dir/db_path).
	stdout, _, err := executeCommand("init", "--config", configPath)
	if err != nil {
		t.Fatalf("first-run init should succeed with guidance: %v", err)
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Errorf("expected config file to be created: %v", err)
	}
	if !strings.Contains(stdout, configPath) {
		t.Errorf("expected guidance naming the config path, got: %q", stdout)
	}
	if !strings.Contains(stdout, "re-run") {
		t.Errorf("expected guidance to instruct re-running, got: %q", stdout)
	}
}

func TestInitSchemaMatchesEmbed(t *testing.T) {
	configPath, wikiDir, _ := testVault(t)
	if _, _, err := executeCommand("init", "--config", configPath); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	got := readFile(t, filepath.Join(wikiDir, "_schema.md"))
	if got != schema.DefaultContent {
		t.Errorf("schema file content does not match embedded asset:\ngot length:  %d\nwant length: %d", len(got), len(schema.DefaultContent))
	}
}

func TestInitIdempotent(t *testing.T) {
	configPath, wikiDir, _ := testVault(t)

	if _, _, err := executeCommand("init", "--config", configPath); err != nil {
		t.Fatalf("first init failed: %v", err)
	}

	configContent := readFile(t, configPath)
	schemaContent := readFile(t, filepath.Join(wikiDir, "_schema.md"))

	if _, _, err := executeCommand("init", "--config", configPath); err != nil {
		t.Fatalf("second init failed: %v", err)
	}

	if got := readFile(t, configPath); got != configContent {
		t.Error("config file content changed on second init")
	}
	if got := readFile(t, filepath.Join(wikiDir, "_schema.md")); got != schemaContent {
		t.Error("schema file content changed on second init")
	}
}

func TestInitIndexesExistingFiles(t *testing.T) {
	configPath, wikiDir, _ := testVault(t)

	if err := os.WriteFile(filepath.Join(wikiDir, "hello.md"), []byte("# Hello World\n\nSome content about testing."), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := executeCommand("init", "--config", configPath); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	stdout, _, err := executeCommand("search", "--config", configPath, "Hello")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if !strings.Contains(stdout, "hello.md") {
		t.Errorf("expected search results to contain hello.md, got: %q", stdout)
	}
}

func TestInitReindexesOnSecondRun(t *testing.T) {
	configPath, wikiDir, _ := testVault(t)

	if _, _, err := executeCommand("init", "--config", configPath); err != nil {
		t.Fatalf("first init failed: %v", err)
	}

	if err := os.WriteFile(filepath.Join(wikiDir, "newfile.md"), []byte("# New File\n\nUnique content about cogvault."), 0o644); err != nil {
		t.Fatal(err)
	}

	// Second init: CheckConsistency(force=true) picks up the new file.
	if _, _, err := executeCommand("init", "--config", configPath); err != nil {
		t.Fatalf("second init failed: %v", err)
	}

	stdout, _, err := executeCommand("search", "--config", configPath, "cogvault")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if !strings.Contains(stdout, "newfile.md") {
		t.Errorf("expected newfile.md in search results, got: %q", stdout)
	}
}

func TestSearchNoResults(t *testing.T) {
	configPath, _, _ := testVault(t)

	if _, _, err := executeCommand("init", "--config", configPath); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	stdout, _, err := executeCommand("search", "--config", configPath, "nonexistentxyz")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if !strings.Contains(stdout, "No results") {
		t.Errorf("expected 'No results' message, got: %q", stdout)
	}
}

func TestSearchLimitClamping(t *testing.T) {
	configPath, _, _ := testVault(t)

	if _, _, err := executeCommand("init", "--config", configPath); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	// Invalid limits silently reset to 10 — should never error.
	for _, limit := range []string{"0", "-1", "200"} {
		if _, _, err := executeCommand("search", "--config", configPath, "--limit", limit, "test"); err != nil {
			t.Errorf("search with --limit %s failed: %v", limit, err)
		}
	}
}

func TestConfigMissingError(t *testing.T) {
	base := t.TempDir()
	missing := filepath.Join(base, "does-not-exist.yaml")

	_, _, err := executeCommand("search", "--config", missing, "test")
	if err == nil {
		t.Error("expected error when config is missing for search")
	} else if !strings.Contains(err.Error(), missing) {
		t.Errorf("expected error to name the config path %q, got: %v", missing, err)
	}

	_, _, err = executeCommand("serve", "--config", missing)
	if err == nil {
		t.Error("expected error when config is missing for serve")
	}
}

func TestResolveConfigPathDefaults(t *testing.T) {
	cmd := newRootCmd()
	got, err := resolveConfigPath(cmd)
	if err != nil {
		t.Fatalf("resolveConfigPath failed: %v", err)
	}
	want, err := config.DefaultConfigPath()
	if err != nil {
		t.Fatalf("DefaultConfigPath failed: %v", err)
	}
	if got != want {
		t.Errorf("expected default %q, got %q", want, got)
	}
}

func TestInitUsesAbsoluteConfigPathsRegardlessOfCwd(t *testing.T) {
	configPath, wikiDir, dbPath := testVault(t)

	// Run init from an unrelated cwd; config paths are absolute so output lands
	// at the configured locations.
	t.Chdir(t.TempDir())

	if _, _, err := executeCommand("init", "--config", configPath); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	for _, p := range []string{
		wikiDir,
		filepath.Join(wikiDir, "_schema.md"),
		dbPath,
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s at configured absolute location: %v", p, err)
		}
	}
}

func TestServeInitFailure(t *testing.T) {
	base := t.TempDir()
	missing := filepath.Join(base, "config.yaml")

	if _, _, err := executeCommand("serve", "--config", missing); err == nil {
		t.Error("expected error when serving without config")
	}
}

func TestInitPerFileErrorContinues(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("test requires non-root to enforce file permissions")
	}

	configPath, wikiDir, _ := testVault(t)

	if err := os.WriteFile(filepath.Join(wikiDir, "good.md"), []byte("# Good\n\nSearchable content."), 0o644); err != nil {
		t.Fatal(err)
	}
	badPath := filepath.Join(wikiDir, "unreadable.md")
	if err := os.WriteFile(badPath, []byte("# Bad\n\nContent."), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := executeCommand("init", "--config", configPath); err != nil {
		t.Fatalf("first init failed: %v", err)
	}

	// Change size (so the stat-gate does not skip it), then drop read permission
	// so the forced re-read fails as a per-file error.
	if err := os.WriteFile(badPath, []byte("# Bad\n\nContent that is now a different length."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(badPath, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(badPath, 0o644) })

	_, stderr, err := executeCommand("init", "--config", configPath)
	if err != nil {
		t.Fatalf("init should succeed despite per-file errors: %v", err)
	}
	if !strings.Contains(stderr, "warning") {
		t.Errorf("expected per-file warning on stderr, got: %q", stderr)
	}

	stdout, _, err := executeCommand("search", "--config", configPath, "Searchable")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if !strings.Contains(stdout, "good.md") {
		t.Errorf("expected good.md in results, got: %q", stdout)
	}
}

func TestInitSystemicErrorFails(t *testing.T) {
	configPath, _, dbPath := testVault(t)

	if _, _, err := executeCommand("init", "--config", configPath); err != nil {
		t.Fatalf("first init failed: %v", err)
	}

	if err := os.WriteFile(dbPath, []byte("corrupted data"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := executeCommand("init", "--config", configPath); err == nil {
		t.Error("expected error when DB is corrupted")
	}
}

func TestWriteSchemaRejectsDirectory(t *testing.T) {
	configPath, wikiDir, _ := testVault(t)

	// Create wiki/_schema.md as a directory (the conflict).
	schemaDir := filepath.Join(wikiDir, "_schema.md")
	if err := os.MkdirAll(schemaDir, 0o755); err != nil {
		t.Fatal(err)
	}

	_, _, err := executeCommand("init", "--config", configPath)
	if err == nil {
		t.Error("expected error when schema path is a directory")
	}
	if err != nil && !strings.Contains(err.Error(), "directory") {
		t.Errorf("expected directory-related error, got: %v", err)
	}
}

// --- ingest command ---

// fakeClaudeOnPath prepends the fake `claude` binary dir (from internal/llm
// testdata) to PATH so exec.LookPath("claude") succeeds without a real CLI.
func fakeClaudeOnPath(t *testing.T) {
	t.Helper()
	binDir, err := filepath.Abs("../../internal/llm/testdata/bin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(binDir, "claude")); err != nil {
		t.Fatalf("fake claude binary missing: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func newIngestVault(t *testing.T) (configPath, srcDir string) {
	t.Helper()
	base := t.TempDir()
	wikiDir := filepath.Join(base, "wiki")
	dbPath := filepath.Join(base, "index.db")
	srcDir = filepath.Join(base, "src")
	configPath = filepath.Join(base, "config.yaml")
	for _, d := range []string{wikiDir, srcDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeConfigFile(t, configPath, wikiDir, dbPath, srcDir)
	return configPath, srcDir
}

func TestIngestDryRunListsPending(t *testing.T) {
	fakeClaudeOnPath(t)
	configPath, srcDir := newIngestVault(t)

	// A source file older than the settle window (2m) so it is not deferred.
	srcFile := filepath.Join(srcDir, "note.md")
	if err := os.WriteFile(srcFile, []byte("# Note\n\nSome ingestible content."), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-5 * time.Minute)
	if err := os.Chtimes(srcFile, old, old); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := executeCommand("ingest", "--config", configPath, "--dry-run")
	if err != nil {
		t.Fatalf("ingest --dry-run failed: %v", err)
	}
	if !strings.Contains(stdout, "would-digest") {
		t.Errorf("expected 'would-digest' in dry-run output, got: %q", stdout)
	}
	if !strings.Contains(stdout, "note.md") {
		t.Errorf("expected pending file in dry-run output, got: %q", stdout)
	}
}

func TestIngestLockHeldFails(t *testing.T) {
	configPath, _ := newIngestVault(t)

	// Hold the flock the runner will try to acquire.
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	lockPath := filepath.Join(filepath.Dir(cfg.DBPath), "ingest.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatalf("acquire test lock: %v", err)
	}
	defer unix.Flock(int(f.Fd()), unix.LOCK_UN)

	fakeClaudeOnPath(t)
	_, _, err = executeCommand("ingest", "--config", configPath)
	if err == nil {
		t.Fatal("expected error when lock is held")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("expected already-running error, got: %v", err)
	}
}

func TestIngestMissingClaudeBinary(t *testing.T) {
	configPath, _ := newIngestVault(t)

	// Scrub PATH so exec.LookPath("claude") fails.
	t.Setenv("PATH", "")

	_, _, err := executeCommand("ingest", "--config", configPath)
	if err == nil {
		t.Fatal("expected error when claude binary is absent")
	}
	if !strings.Contains(err.Error(), "claude CLI not found") {
		t.Errorf("expected claude-not-found error, got: %v", err)
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
