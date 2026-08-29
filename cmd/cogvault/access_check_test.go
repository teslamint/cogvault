package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func writeAccessCheckConfig(t *testing.T, path, wikiDir, dbPath string, sources ...string) {
	t.Helper()
	body := "wiki_dir: " + wikiDir + "\n" + "db_path: " + dbPath + "\n"
	if len(sources) > 0 {
		body += "sources:\n"
		for _, source := range sources {
			body += "  - path: " + source + "\n    types: [md]\n"
		}
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAccessCheckConfiguredSurfaces(t *testing.T) {
	base := t.TempDir()
	wikiDir := filepath.Join(base, "wiki")
	dbDir := filepath.Join(base, "state")
	sourceA := filepath.Join(base, "source-a")
	sourceB := filepath.Join(base, "source-b")
	for _, dir := range []string{wikiDir, dbDir, sourceA, sourceB} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for path, body := range map[string]string{
		filepath.Join(sourceA, "accepted.md"):  "alpha",
		filepath.Join(sourceA, "rejected.txt"): "ignored",
		filepath.Join(sourceB, "empty.pdf"):    "",
	} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(base, "config.yaml")
	configBody := "wiki_dir: " + wikiDir + "\n" +
		"db_path: " + filepath.Join(dbDir, "cogvault.db") + "\n" +
		"sources:\n" +
		"  - path: " + sourceA + "\n    types: [md]\n" +
		"  - path: " + sourceB + "\n    types: [pdf]\n"
	if err := os.WriteFile(configPath, []byte(configBody), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := executeCommand("access-check", "--config", configPath)
	if err != nil {
		t.Fatalf("access-check failed: %v", err)
	}
	for _, want := range []string{
		"passed: wiki_dir: " + wikiDir,
		"passed: db_parent: " + dbDir,
		"passed: source: " + sourceA,
		"passed: source: " + sourceB,
		"configured ingest access check passed",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

func TestAccessCheckDoesNotBootstrap(t *testing.T) {
	base := t.TempDir()
	wikiDir := filepath.Join(base, "wiki")
	dbDir := filepath.Join(base, "state")
	if err := os.Mkdir(wikiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dbDir, "cogvault.db")
	configPath := filepath.Join(base, "config.yaml")
	writeAccessCheckConfig(t, configPath, wikiDir, dbPath)

	before, err := os.ReadDir(base)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCommand("access-check", "--config", configPath); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadDir(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatalf("base entries changed: before=%v after=%v", before, after)
	}
	for _, forbidden := range []string{dbPath, dbPath + "-wal", dbPath + "-shm", filepath.Join(wikiDir, "_schema.md")} {
		if _, err := os.Lstat(forbidden); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("forbidden runtime artifact exists: %s", forbidden)
		}
	}
}

func TestAccessCheckReadsAcceptedButNotRejectedFile(t *testing.T) {
	base := t.TempDir()
	wikiDir := filepath.Join(base, "wiki")
	dbDir := filepath.Join(base, "state")
	sourceDir := filepath.Join(base, "source")
	for _, dir := range []string{wikiDir, dbDir, sourceDir} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	accepted := filepath.Join(sourceDir, "accepted.md")
	rejected := filepath.Join(sourceDir, "rejected.txt")
	for _, path := range []string{accepted, rejected} {
		if err := os.WriteFile(path, []byte("body"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(base, "config.yaml")
	writeAccessCheckConfig(t, configPath, wikiDir, filepath.Join(dbDir, "cogvault.db"), sourceDir)

	original := defaultAccessCheckOps
	t.Cleanup(func() { defaultAccessCheckOps = original })
	defaultAccessCheckOps.read = func(file *os.File, b []byte) (int, error) {
		if file.Name() == rejected {
			return 0, errors.New("sentinel rejected read")
		}
		return file.Read(b)
	}
	if _, _, err := executeCommand("access-check", "--config", configPath); err != nil {
		t.Fatalf("rejected-extension read hook affected result: %v", err)
	}

	defaultAccessCheckOps.read = func(file *os.File, _ []byte) (int, error) {
		if file.Name() == accepted {
			return 0, errors.New("sentinel accepted read")
		}
		return 0, io.EOF
	}
	_, _, err := executeCommand("access-check", "--config", configPath)
	if err == nil || !strings.Contains(err.Error(), accepted) || !strings.Contains(err.Error(), "read") || !strings.Contains(err.Error(), "sentinel accepted read") {
		t.Fatalf("accepted read error = %v", err)
	}
}

func TestAccessCheckRejectsPositionalArguments(t *testing.T) {
	_, _, err := executeCommand("access-check", "unexpected")
	if err == nil || !strings.Contains(err.Error(), "unknown command") && !strings.Contains(err.Error(), "accepts 0 arg") {
		t.Fatalf("positional argument error = %v", err)
	}
}

func TestAccessCheckPreservesSentinelReadAndCleanupErrors(t *testing.T) {
	base := t.TempDir()
	wikiDir := filepath.Join(base, "wiki")
	dbDir := filepath.Join(base, "state")
	for _, dir := range []string{wikiDir, dbDir} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(base, "config.yaml")
	writeAccessCheckConfig(t, configPath, wikiDir, filepath.Join(dbDir, "cogvault.db"))

	original := defaultAccessCheckOps
	t.Cleanup(func() { defaultAccessCheckOps = original })
	defaultAccessCheckOps.readFile = func(path string) ([]byte, error) {
		if filepath.Dir(path) == wikiDir {
			return nil, errors.New("sentinel read denied")
		}
		return os.ReadFile(path)
	}
	defaultAccessCheckOps.remove = func(path string) error {
		if filepath.Dir(path) == wikiDir {
			return errors.New("sentinel remove denied")
		}
		return os.Remove(path)
	}

	_, _, err := executeCommand("access-check", "--config", configPath)
	if err == nil {
		t.Fatal("access-check unexpectedly succeeded")
	}
	for _, want := range []string{"wiki_dir", wikiDir, "read sentinel", "sentinel read denied", "remove sentinel", "sentinel remove denied"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

func TestAccessCheckRejectsOpenedFileIdentityReplacement(t *testing.T) {
	base := t.TempDir()
	wikiDir := filepath.Join(base, "wiki")
	dbDir := filepath.Join(base, "state")
	sourceDir := filepath.Join(base, "source")
	for _, dir := range []string{wikiDir, dbDir, sourceDir} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	originalPath := filepath.Join(sourceDir, "original.md")
	replacementPath := filepath.Join(sourceDir, "replacement.md")
	for path, body := range map[string]string{originalPath: "original", replacementPath: "replacement"} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(base, "config.yaml")
	writeAccessCheckConfig(t, configPath, wikiDir, filepath.Join(dbDir, "cogvault.db"), sourceDir)

	originalOps := defaultAccessCheckOps
	t.Cleanup(func() { defaultAccessCheckOps = originalOps })
	defaultAccessCheckOps.open = func(path string, flags int, mode uint32) (int, error) {
		if path == originalPath {
			return unix.Open(replacementPath, flags, mode)
		}
		return unix.Open(path, flags, mode)
	}
	_, _, err := executeCommand("access-check", "--config", configPath)
	if err == nil || !strings.Contains(err.Error(), originalPath) || !strings.Contains(err.Error(), "file identity changed") {
		t.Fatalf("identity replacement error = %v", err)
	}
}

func TestAccessCheckSkipsNonRegularAndOversizedEntries(t *testing.T) {
	base := t.TempDir()
	wikiDir := filepath.Join(base, "wiki")
	dbDir := filepath.Join(base, "state")
	sourceDir := filepath.Join(base, "source")
	for _, dir := range []string{wikiDir, dbDir, sourceDir, filepath.Join(sourceDir, "directory.md")} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	accepted := filepath.Join(sourceDir, "accepted.md")
	oversized := filepath.Join(sourceDir, "oversized.md")
	if err := os.WriteFile(accepted, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oversized, make([]byte, 2<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(accepted, filepath.Join(sourceDir, "link.md")); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(base, "config.yaml")
	writeAccessCheckConfig(t, configPath, wikiDir, filepath.Join(dbDir, "cogvault.db"), sourceDir)
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte("max_file_size_mb: 1\n")...)
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	originalOps := defaultAccessCheckOps
	t.Cleanup(func() { defaultAccessCheckOps = originalOps })
	var reads []string
	defaultAccessCheckOps.read = func(file *os.File, b []byte) (int, error) {
		reads = append(reads, file.Name())
		return file.Read(b)
	}
	if _, _, err := executeCommand("access-check", "--config", configPath); err != nil {
		t.Fatal(err)
	}
	if len(reads) != 1 || reads[0] != accepted {
		t.Fatalf("content reads = %v, want only %s", reads, accepted)
	}
}
