package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	wikiBefore := directoryNames(t, wikiDir)
	dbBefore := directoryNames(t, dbDir)

	for run := 1; run <= 2; run++ {
		stdout, _, err := executeCommand("access-check", "--config", configPath)
		if err != nil {
			t.Fatalf("access-check run %d failed: %v", run, err)
		}
		for _, want := range []string{
			"passed: wiki_dir: " + wikiDir,
			"passed: db_parent: " + dbDir,
			"passed: source: " + sourceA,
			"passed: source: " + sourceB,
			"configured ingest access check passed",
		} {
			if !strings.Contains(stdout, want) {
				t.Errorf("run %d stdout missing %q:\n%s", run, want, stdout)
			}
		}
		if got := directoryNames(t, wikiDir); strings.Join(got, "\x00") != strings.Join(wikiBefore, "\x00") {
			t.Fatalf("run %d wiki directory changed: before=%v after=%v", run, wikiBefore, got)
		}
		if got := directoryNames(t, dbDir); strings.Join(got, "\x00") != strings.Join(dbBefore, "\x00") {
			t.Fatalf("run %d database parent changed: before=%v after=%v", run, dbBefore, got)
		}
	}
}

func directoryNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name()
	}
	return names
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

func TestAccessCheckReportsCleanupOnlyFailure(t *testing.T) {
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
	defaultAccessCheckOps.remove = func(path string) error {
		if filepath.Dir(path) == wikiDir {
			return errors.New("cleanup denied")
		}
		return os.Remove(path)
	}
	_, _, err := executeCommand("access-check", "--config", configPath)
	if err == nil || !strings.Contains(err.Error(), "remove sentinel") || !strings.Contains(err.Error(), "cleanup denied") {
		t.Fatalf("cleanup-only error = %v", err)
	}
	if names := directoryNames(t, wikiDir); len(names) != 1 || !strings.HasPrefix(names[0], ".cogvault-access-check-") {
		t.Fatalf("residual sentinel inventory = %v", names)
	}
}

func TestAccessCheckReadbackMismatchStillCleansSentinel(t *testing.T) {
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
			return []byte("different bytes"), nil
		}
		return os.ReadFile(path)
	}
	_, _, err := executeCommand("access-check", "--config", configPath)
	if err == nil || !strings.Contains(err.Error(), "compare sentinel") || !strings.Contains(err.Error(), "content mismatch") {
		t.Fatalf("readback mismatch error = %v", err)
	}
	if names := directoryNames(t, wikiDir); len(names) != 0 {
		t.Fatalf("sentinel not cleaned after mismatch: %v", names)
	}
}

func TestAccessCheckWriteAndCloseFailuresCleanSentinel(t *testing.T) {
	for _, operation := range []string{"write", "close"} {
		t.Run(operation, func(t *testing.T) {
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
			injected := errors.New(operation + " sentinel")
			switch operation {
			case "write":
				defaultAccessCheckOps.write = func(file *os.File, _ []byte) (int, error) {
					if filepath.Dir(file.Name()) == wikiDir {
						return 0, injected
					}
					return 0, errors.New("unexpected write")
				}
			case "close":
				defaultAccessCheckOps.close = func(file *os.File) error {
					err := file.Close()
					if filepath.Dir(file.Name()) == wikiDir {
						return errors.Join(err, injected)
					}
					return err
				}
			}
			_, _, err := executeCommand("access-check", "--config", configPath)
			if err == nil || !strings.Contains(err.Error(), operation+" sentinel") || !strings.Contains(err.Error(), wikiDir) {
				t.Fatalf("%s error = %v", operation, err)
			}
			if names := directoryNames(t, wikiDir); len(names) != 0 {
				t.Fatalf("%s failure left sentinel: %v", operation, names)
			}
		})
	}
}

func TestAccessCheckFIFOReplacementReturnsWithoutBlocking(t *testing.T) {
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
	fifoPath := filepath.Join(sourceDir, "replacement.md")
	if err := os.WriteFile(originalPath, []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(fifoPath, 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(base, "config.yaml")
	writeAccessCheckConfig(t, configPath, wikiDir, filepath.Join(dbDir, "cogvault.db"), sourceDir)
	originalOps := defaultAccessCheckOps
	t.Cleanup(func() { defaultAccessCheckOps = originalOps })
	defaultAccessCheckOps.open = func(path string, flags int, mode uint32) (int, error) {
		if path == originalPath {
			return unix.Open(fifoPath, flags, mode)
		}
		return unix.Open(path, flags, mode)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := executeCommand("access-check", "--config", configPath)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), originalPath) {
			t.Fatalf("FIFO replacement error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("FIFO replacement blocked")
	}
}

func TestAccessCheckRejectsNonDirectoryWriteSurface(t *testing.T) {
	base := t.TempDir()
	wikiPath := filepath.Join(base, "wiki-file")
	dbDir := filepath.Join(base, "state")
	if err := os.WriteFile(wikiPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(base, "config.yaml")
	writeAccessCheckConfig(t, configPath, wikiPath, filepath.Join(dbDir, "cogvault.db"))
	_, _, err := executeCommand("access-check", "--config", configPath)
	if err == nil || !strings.Contains(err.Error(), "wiki_dir") || !strings.Contains(err.Error(), wikiPath) || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("non-directory error = %v", err)
	}
}

func TestAccessCheckSourceOperationErrorsNameBoundary(t *testing.T) {
	for _, operation := range []string{"read directory", "lstat", "open", "read"} {
		t.Run(operation, func(t *testing.T) {
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
			if err := os.WriteFile(accepted, []byte("body"), 0o644); err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(base, "config.yaml")
			writeAccessCheckConfig(t, configPath, wikiDir, filepath.Join(dbDir, "cogvault.db"), sourceDir)
			original := defaultAccessCheckOps
			t.Cleanup(func() { defaultAccessCheckOps = original })
			injected := errors.New("permission sentinel")
			switch operation {
			case "read directory":
				defaultAccessCheckOps.readDir = func(path string) ([]os.DirEntry, error) {
					if path == sourceDir {
						return nil, injected
					}
					return os.ReadDir(path)
				}
			case "lstat":
				defaultAccessCheckOps.lstat = func(path string) (os.FileInfo, error) {
					if path == accepted {
						return nil, injected
					}
					return os.Lstat(path)
				}
			case "open":
				defaultAccessCheckOps.open = func(path string, flags int, mode uint32) (int, error) {
					if path == accepted {
						return -1, injected
					}
					return unix.Open(path, flags, mode)
				}
			case "read":
				defaultAccessCheckOps.read = func(file *os.File, b []byte) (int, error) {
					if file.Name() == accepted {
						return 0, injected
					}
					return file.Read(b)
				}
			}
			_, _, err := executeCommand("access-check", "--config", configPath)
			if err == nil || !strings.Contains(err.Error(), "source "+sourceDir) || !strings.Contains(err.Error(), operation) || !strings.Contains(err.Error(), "permission sentinel") {
				t.Fatalf("%s error = %v", operation, err)
			}
			if operation != "read directory" && !strings.Contains(err.Error(), accepted) {
				t.Errorf("error missing exact file path: %v", err)
			}
		})
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
