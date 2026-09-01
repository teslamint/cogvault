package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/teslamint/cogvault/internal/config"
	cogmcp "github.com/teslamint/cogvault/internal/mcp"
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

// testVaultWithAuth is like testVault but appends authYAML (e.g.
// "auth:\n  mode: bearer\n") to the generated config.
func testVaultWithAuth(t *testing.T, authYAML string) (configPath, wikiDir, dbPath string) {
	t.Helper()
	base := t.TempDir()
	wikiDir = filepath.Join(base, "wiki")
	dbPath = filepath.Join(base, "index.db")
	configPath = filepath.Join(base, "config.yaml")
	if err := os.MkdirAll(wikiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "wiki_dir: %s\n", wikiDir)
	fmt.Fprintf(&b, "db_path: %s\n", dbPath)
	b.WriteString("adapter: obsidian\n")
	b.WriteString(authYAML)
	if err := os.WriteFile(configPath, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
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
	configPath, srcDir, wikiDir, dbPath := setupIngestVault(t)
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

// --- serve command ---

func TestServeUnknownTransportErrors(t *testing.T) {
	configPath, _, _ := testVault(t)

	_, _, err := executeCommand("serve", "--config", configPath, "--transport", "bogus")
	if err == nil {
		t.Fatal("expected error for unknown --transport value")
	}
	if !strings.Contains(err.Error(), `"bogus"`) {
		t.Errorf("expected error to name the rejected value, got: %v", err)
	}
}

// TestServeStdioUnchanged (Covers S4) proves stdio keeps its pre-existing
// behavior: no flags or auth guards are consulted, even when auth.mode is
// "oauth" (which would refuse to start either network transport without
// --public-url). Redirecting os.Stdin to a closed pipe delivers immediate
// EOF, which mcp-go's stdio server treats as a clean, non-error shutdown, so
// the test terminates rather than blocking on real stdin.
func TestServeStdioUnchanged(t *testing.T) {
	configPath, _, _ := testVaultWithAuth(t, "auth:\n  mode: oauth\n  oauth:\n    issuer: https://issuer.example.com\n")

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	origStdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = origStdin })
	if err := w.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}

	if _, _, err := executeCommand("serve", "--config", configPath); err != nil {
		t.Fatalf("stdio serve failed: %v", err)
	}
}

// TestServeBindGuard (Covers S5) asserts mode "none" refuses to start on a
// non-loopback address, for both network transports. ":8080" is tested
// explicitly, not only "0.0.0.0:8080": an empty host binds every interface
// too, and is easy to mistake for loopback.
func TestServeBindGuard(t *testing.T) {
	for _, transport := range []string{"http", "sse"} {
		for _, addr := range []string{":8080", "0.0.0.0:8080"} {
			t.Run(transport+"/"+addr, func(t *testing.T) {
				configPath, _, _ := testVault(t) // auth.mode defaults to "none"
				_, _, err := executeCommand("serve", "--config", configPath, "--transport", transport, "--addr", addr)
				if err == nil {
					t.Fatalf("expected bind guard error for --transport %s --addr %s", transport, addr)
				}
			})
		}
	}
}

func TestServeBindGuardAllowsLoopback(t *testing.T) {
	// isLoopbackAddr itself, exercised through the guard: a loopback host
	// must not trip the "none" mode guard. The transport still needs a real
	// listener to fully start, so this only checks that the guard error
	// specifically does not fire by asserting the failure (if any) is not a
	// bind-guard error; TestSSERequiresAuth and TestServeEndpointPathAgreesWithResource
	// exercise the full started-handler path.
	configPath, _, _ := testVault(t)
	cfg, store, idx, adpt, err := bootstrap(configPath)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer idx.Close()
	mcpSrv := cogmcp.NewServer(cfg.WikiDir, cfg, store, idx, adpt)

	for _, addr := range []string{"localhost:0", "127.0.0.1:0", "[::1]:0"} {
		if _, err := buildServeHandler(cfg, mcpSrv, serveFlags{transport: "http", addr: addr, endpointPath: "/mcp"}); err != nil {
			t.Errorf("buildServeHandler(addr=%q): unexpected error: %v", addr, err)
		}
	}
}

func TestServeOAuthRequiresPublicURL(t *testing.T) {
	// "sse" is deliberately not exercised here: TestServeOAuthRejectsSSE
	// covers it, and oauth+sse is now refused before the public-url check
	// ever runs (C2), so it would no longer report a public-url error.
	configPath, _, _ := testVaultWithAuth(t, "auth:\n  mode: oauth\n  oauth:\n    issuer: https://issuer.example.com\n")

	_, _, err := executeCommand("serve", "--config", configPath, "--transport", "http", "--addr", "127.0.0.1:0")
	if err == nil {
		t.Fatalf("expected error for oauth mode without --public-url")
	}
	if !strings.Contains(err.Error(), "public-url") {
		t.Errorf("expected error to name public-url, got: %v", err)
	}
}

// TestServeOAuthRejectsSSE (Covers C2) asserts "auth.mode: oauth" combined
// with "--transport sse" refuses to start. SSE always serves fixed /sse and
// /message paths, so the RFC 9728 protected resource metadata it would
// advertise can never equal the URL a conformant OAuth client requested
// (RFC 9728 §3.3): the combination is structurally unusable, not merely
// undocumented, so it fails the same way the other startup guards do.
func TestServeOAuthRejectsSSE(t *testing.T) {
	configPath, _, _ := testVaultWithAuth(t, "auth:\n  mode: oauth\n  oauth:\n    issuer: https://issuer.example.com\n")

	_, _, err := executeCommand("serve", "--config", configPath, "--transport", "sse", "--addr", "127.0.0.1:0", "--public-url", "https://mcp.example.com")
	if err == nil {
		t.Fatal("expected error for oauth mode with --transport sse")
	}
	if !strings.Contains(err.Error(), "transport") {
		t.Errorf("expected error to name transport, got: %v", err)
	}
}

// TestServeNoneRejectsPublicURL (Covers C1) asserts "auth.mode: none"
// combined with a non-empty "--public-url" refuses to start. A public URL
// has no legitimate function in "none" mode; its presence signals
// tunnel-exposure intent the code cannot see directly but can see the
// contradictory config for.
func TestServeNoneRejectsPublicURL(t *testing.T) {
	configPath, _, _ := testVault(t) // auth.mode defaults to "none"

	_, _, err := executeCommand("serve", "--config", configPath, "--transport", "http", "--addr", "localhost:0", "--public-url", "https://mcp.example.com")
	if err == nil {
		t.Fatal("expected error for auth.mode none with --public-url set")
	}
	if !strings.Contains(err.Error(), "public-url") {
		t.Errorf("expected error to name public-url, got: %v", err)
	}
}

func TestServeBearerRequiresToken(t *testing.T) {
	configPath, _, _ := testVaultWithAuth(t, "auth:\n  mode: bearer\n")

	t.Run("unset", func(t *testing.T) {
		_, _, err := executeCommand("serve", "--config", configPath, "--transport", "http", "--addr", "127.0.0.1:0")
		if err == nil {
			t.Fatal("expected error when COGVAULT_BEARER_TOKEN is unset")
		}
		if !strings.Contains(err.Error(), "COGVAULT_BEARER_TOKEN") {
			t.Errorf("expected error to name COGVAULT_BEARER_TOKEN, got: %v", err)
		}
	})

	t.Run("too short", func(t *testing.T) {
		t.Setenv("COGVAULT_BEARER_TOKEN", strings.Repeat("a", 31))
		_, _, err := executeCommand("serve", "--config", configPath, "--transport", "http", "--addr", "127.0.0.1:0")
		if err == nil {
			t.Fatal("expected error when COGVAULT_BEARER_TOKEN is under 32 bytes")
		}
	})

	t.Run("32 bytes accepted", func(t *testing.T) {
		t.Setenv("COGVAULT_BEARER_TOKEN", strings.Repeat("a", 32))
		cfg, store, idx, adpt, err := bootstrap(configPath)
		if err != nil {
			t.Fatalf("bootstrap: %v", err)
		}
		defer idx.Close()
		mcpSrv := cogmcp.NewServer(cfg.WikiDir, cfg, store, idx, adpt)
		if _, err := buildServeHandler(cfg, mcpSrv, serveFlags{transport: "http", addr: "127.0.0.1:0", endpointPath: "/mcp"}); err != nil {
			t.Errorf("unexpected error with a 32-byte token: %v", err)
		}
	})
}

func TestServePublicURLValidation(t *testing.T) {
	configPath, _, _ := testVault(t) // auth.mode "none", loopback addr

	tests := []struct {
		name string
		url  string
	}{
		{"no scheme", "mcp.example.com"},
		{"http not https", "http://mcp.example.com"},
		{"trailing slash", "https://mcp.example.com/"},
		{"trailing slash with path", "https://mcp.example.com/mcp/"},
		{"query", "https://mcp.example.com?x=1"},
		{"fragment", "https://mcp.example.com#frag"},
		{"empty host", "https:///mcp"},
		{"userinfo", "https://user:pass@mcp.example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := executeCommand("serve", "--config", configPath, "--transport", "http", "--addr", "localhost:0", "--public-url", tt.url)
			if err == nil {
				t.Fatalf("expected error for --public-url %q", tt.url)
			}
			if !strings.Contains(err.Error(), "public-url") {
				t.Errorf("expected error to name public-url, got: %v", err)
			}
		})
	}

	t.Run("path without trailing slash accepted", func(t *testing.T) {
		// buildServeHandler directly, not executeCommand: a successful
		// "serve" invocation blocks forever in http.Server.ListenAndServe,
		// which would hang this test. "bearer" mode, not "none": a
		// public-url has no legitimate function in "none" mode (C1) and
		// this subtest is about public-url path handling, not auth mode.
		t.Setenv("COGVAULT_BEARER_TOKEN", strings.Repeat("a", 32))
		bearerConfigPath, _, _ := testVaultWithAuth(t, "auth:\n  mode: bearer\n")
		cfg, store, idx, adpt, err := bootstrap(bearerConfigPath)
		if err != nil {
			t.Fatalf("bootstrap: %v", err)
		}
		defer idx.Close()
		mcpSrv := cogmcp.NewServer(cfg.WikiDir, cfg, store, idx, adpt)
		if _, err := buildServeHandler(cfg, mcpSrv, serveFlags{transport: "sse", addr: "localhost:0", endpointPath: "/mcp", publicURL: "https://mcp.example.com/sub/path"}); err != nil {
			t.Errorf("unexpected error for a valid --public-url with a path: %v", err)
		}
	})
}

// TestNewHTTPServerHasReadHeaderTimeout proves the *http.Server serving the
// sse and http transports bounds the pre-handler header-read phase: the
// httpauth stream deadline only starts once the handler runs, so nothing
// else stops a slowloris-style client from holding a connection open
// indefinitely on this deliberately public endpoint.
func TestNewHTTPServerHasReadHeaderTimeout(t *testing.T) {
	srv := newHTTPServer("localhost:0", http.NotFoundHandler())
	if srv.ReadHeaderTimeout <= 0 {
		t.Errorf("ReadHeaderTimeout = %v, want > 0", srv.ReadHeaderTimeout)
	}
	if srv.IdleTimeout <= 0 {
		t.Errorf("IdleTimeout = %v, want > 0", srv.IdleTimeout)
	}
	if srv.WriteTimeout != 0 {
		t.Errorf("WriteTimeout = %v, want 0 (unset): a non-zero value would cut off long-lived SSE/Streamable HTTP event streams", srv.WriteTimeout)
	}
}

// TestServeAudienceResolution unit-tests resolveAudience directly: the
// defaulting case and the mismatch-refuses-to-start case from the spec's
// "audience defaulting and equality" requirement. It needs no network JWKS
// fixture, since resolveAudience is pure.
func TestServeAudienceResolution(t *testing.T) {
	const resource = "https://mcp.example.com/mcp"

	t.Run("unset defaults to resource", func(t *testing.T) {
		got, err := resolveAudience("", resource)
		if err != nil {
			t.Fatalf("resolveAudience: %v", err)
		}
		if got != resource {
			t.Errorf("audience = %q, want %q", got, resource)
		}
	})

	t.Run("matching configured value accepted", func(t *testing.T) {
		got, err := resolveAudience(resource, resource)
		if err != nil {
			t.Fatalf("resolveAudience: %v", err)
		}
		if got != resource {
			t.Errorf("audience = %q, want %q", got, resource)
		}
	})

	t.Run("mismatched configured value refuses", func(t *testing.T) {
		_, err := resolveAudience("https://wrong.example.com/mcp", resource)
		if err == nil {
			t.Fatal("expected error for a mismatched audience")
		}
		if !strings.Contains(err.Error(), resource) || !strings.Contains(err.Error(), "wrong.example.com") {
			t.Errorf("expected error to name both values, got: %v", err)
		}
	})
}

// TestServeAudienceMismatchRefusesToStart proves the wiring end-to-end
// through buildServeHandler: an explicitly configured auth.oauth.audience
// that disagrees with <public-url><endpoint-path> is a startup error.
func TestServeAudienceMismatchRefusesToStart(t *testing.T) {
	configPath, _, _ := testVaultWithAuth(t, "auth:\n  mode: oauth\n  oauth:\n    issuer: https://issuer.example.com\n    audience: https://wrong.example.com/mcp\n")

	_, _, err := executeCommand("serve", "--config", configPath, "--transport", "http", "--addr", "127.0.0.1:0", "--public-url", "https://mcp.example.com")
	if err == nil {
		t.Fatal("expected error for a mismatched auth.oauth.audience")
	}
	if !strings.Contains(err.Error(), "audience") {
		t.Errorf("expected error to name audience, got: %v", err)
	}
}

// TestServeEndpointPathAgreesWithResource proves that for a non-default
// --endpoint-path, the PRM document's "resource" and the path the http
// transport actually serves on are the same normalized string: a request to
// the endpoint path must not 404, and a request to any other path must.
func TestServeEndpointPathAgreesWithResource(t *testing.T) {
	// oauth mode: the PRM document, unauthenticated by design (RFC 9728),
	// must advertise a "resource" built from the same normalized endpoint
	// path the exact-path wrapper matches on.
	t.Run("PRM resource matches normalized endpoint path", func(t *testing.T) {
		configPath, _, _ := testVaultWithAuth(t, "auth:\n  mode: oauth\n  oauth:\n    issuer: https://issuer.example.com\n")
		cfg, store, idx, adpt, err := bootstrap(configPath)
		if err != nil {
			t.Fatalf("bootstrap: %v", err)
		}
		defer idx.Close()
		mcpSrv := cogmcp.NewServer(cfg.WikiDir, cfg, store, idx, adpt)

		handler, err := buildServeHandler(cfg, mcpSrv, serveFlags{
			transport:    "http",
			addr:         "127.0.0.1:0",
			endpointPath: "custom/mcp", // deliberately missing a leading slash
			publicURL:    "https://mcp.example.com",
		})
		if err != nil {
			t.Fatalf("buildServeHandler: %v", err)
		}

		ts := httptest.NewServer(handler)
		defer ts.Close()

		resp, err := http.Get(ts.URL + "/.well-known/oauth-protected-resource/custom/mcp")
		if err != nil {
			t.Fatalf("GET PRM: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("PRM status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
		var doc struct {
			Resource string `json:"resource"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
			t.Fatalf("decode PRM: %v", err)
		}
		const wantResource = "https://mcp.example.com/custom/mcp"
		if doc.Resource != wantResource {
			t.Fatalf("resource = %q, want %q", doc.Resource, wantResource)
		}
	})

	// mode "none": with no credential check in the way, the exact-path
	// wrapper's own routing is directly observable — it must 404 for any
	// path other than the configured endpoint path rather than silently
	// answering as MCP (mcp-go's StreamableHTTPServer.ServeHTTP ignores the
	// path entirely when used as an http.Handler), and it must not 404 the
	// endpoint path itself.
	t.Run("wrapper 404s off the endpoint path", func(t *testing.T) {
		configPath, _, _ := testVault(t)
		cfg, store, idx, adpt, err := bootstrap(configPath)
		if err != nil {
			t.Fatalf("bootstrap: %v", err)
		}
		defer idx.Close()
		mcpSrv := cogmcp.NewServer(cfg.WikiDir, cfg, store, idx, adpt)

		handler, err := buildServeHandler(cfg, mcpSrv, serveFlags{
			transport:    "http",
			addr:         "127.0.0.1:0",
			endpointPath: "custom/mcp",
		})
		if err != nil {
			t.Fatalf("buildServeHandler: %v", err)
		}

		ts := httptest.NewServer(handler)
		defer ts.Close()

		resp, err := http.Post(ts.URL+"/wrong/path", "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatalf("POST wrong path: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
		}

		// An "initialize" request avoids mcp-go's session-ID lookup path
		// (streamable_http.go's handlePost 404s a non-initialize POST with
		// no session header), which would otherwise produce a 404 of its
		// own and mask the signal this assertion is after.
		initBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`
		resp2, err := http.Post(ts.URL+"/custom/mcp", "application/json", strings.NewReader(initBody))
		if err != nil {
			t.Fatalf("POST endpoint path: %v", err)
		}
		defer resp2.Body.Close()
		if resp2.StatusCode == http.StatusNotFound {
			t.Fatal("the configured endpoint path itself 404s")
		}
	})
}

// TestSSERequiresAuth asserts an uncredentialed request to the SSE endpoint
// under "bearer" mode returns 401, proving SSE is not exempt from
// httpauth.Mount — the hole an earlier design review flagged.
func TestSSERequiresAuth(t *testing.T) {
	t.Setenv("COGVAULT_BEARER_TOKEN", strings.Repeat("a", 32))
	configPath, _, _ := testVaultWithAuth(t, "auth:\n  mode: bearer\n")
	cfg, store, idx, adpt, err := bootstrap(configPath)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer idx.Close()
	mcpSrv := cogmcp.NewServer(cfg.WikiDir, cfg, store, idx, adpt)

	handler, err := buildServeHandler(cfg, mcpSrv, serveFlags{transport: "sse", addr: "127.0.0.1:0", endpointPath: "/mcp"})
	if err != nil {
		t.Fatalf("buildServeHandler: %v", err)
	}

	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/sse")
	if err != nil {
		t.Fatalf("GET /sse: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

// TestServeSSEBaseURLUsesPublicURL (Decision — the SSE base URL) proves the
// message endpoint the client is told to use is built from --public-url
// when set, rather than the local bind address, which a remote client
// behind a tunnel cannot reach.
func TestServeSSEBaseURLUsesPublicURL(t *testing.T) {
	// "bearer" mode, not "none": a public-url has no legitimate function in
	// "none" mode and is now refused at startup (C1).
	t.Setenv("COGVAULT_BEARER_TOKEN", strings.Repeat("a", 32))
	configPath, _, _ := testVaultWithAuth(t, "auth:\n  mode: bearer\n")

	cfg, store, idx, adpt, err := bootstrap(configPath)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer idx.Close()
	mcpSrv := cogmcp.NewServer(cfg.WikiDir, cfg, store, idx, adpt)

	handler, err := buildServeHandler(cfg, mcpSrv, serveFlags{
		transport:    "sse",
		addr:         "localhost:0",
		endpointPath: "/mcp",
		publicURL:    "https://mcp.example.com",
	})
	if err != nil {
		t.Fatalf("buildServeHandler: %v", err)
	}

	ts := httptest.NewServer(handler)
	defer ts.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/sse", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 32))
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("GET /sse: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	event := string(buf[:n])
	wantPrefix := "data: https://mcp.example.com/message?sessionId="
	if !strings.Contains(event, wantPrefix) {
		t.Fatalf("endpoint event = %q, want it to contain %q", event, wantPrefix)
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

// TestSessionIdleTTLExceedsStreamLifetime pins the relationship the streamable
// HTTP session sweeper depends on. mcp-go only starts the sweeper when the
// idle TTL is positive, and it touches a session when a request arrives — a
// long-lived listen stream is touched at establishment and not again. So the
// TTL must outlast a stream that runs for its full MaxStreamSeconds, or the
// sweeper would reap the session out from under an active client.
func TestSessionIdleTTLExceedsStreamLifetime(t *testing.T) {
	for _, maxStreamSeconds := range []int{1, 60, 3600} {
		ttl := sessionIdleTTLFor(maxStreamSeconds)
		streamLifetime := time.Duration(maxStreamSeconds) * time.Second
		if ttl <= streamLifetime {
			t.Errorf("sessionIdleTTLFor(%d) = %v, want greater than the %v stream lifetime",
				maxStreamSeconds, ttl, streamLifetime)
		}
		if ttl <= 0 {
			t.Errorf("sessionIdleTTLFor(%d) = %v, want positive so mcp-go starts the sweeper at all",
				maxStreamSeconds, ttl)
		}
	}
}

// captureLogger records mcp-go's log lines so a test can assert on transport
// behavior that is otherwise invisible from the outside.
type captureLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *captureLogger) Infof(format string, v ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, v...))
}

func (l *captureLogger) Errorf(format string, v ...any) { l.Infof(format, v...) }

func (l *captureLogger) contains(substr string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range l.lines {
		if strings.Contains(line, substr) {
			return true
		}
	}
	return false
}

// TestSessionSweeperEvictsIdleSession discriminates the sweeper wiring, not
// just the TTL arithmetic. mcp-go starts the sweeper only when the transport
// is built with a positive idle TTL; drop WithSessionIdleTTL from the options
// and no sweep ever happens, which is the leak this guards against — a client
// that disconnects without DELETE otherwise keeps its session state for the
// process lifetime.
func TestSessionSweeperEvictsIdleSession(t *testing.T) {
	configPath, _, _ := testVault(t) // auth.mode "none", loopback addr
	cfg, store, idx, adpt, err := bootstrap(configPath)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer idx.Close()
	cfg.Auth.MaxStreamSeconds = 1 // TTL becomes 2s, sweep interval 1s

	logger := &captureLogger{}
	mcpSrv := cogmcp.NewServer(cfg.WikiDir, cfg, store, idx, adpt)
	h, err := buildServeHandler(cfg, mcpSrv, serveFlags{
		transport:    "http",
		addr:         "127.0.0.1:0",
		endpointPath: "/mcp",
		mcpLogger:    logger,
	})
	if err != nil {
		t.Fatalf("buildServeHandler: %v", err)
	}
	ts := httptest.NewServer(h)
	defer ts.Close()

	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
	resp, err := ts.Client().Post(ts.URL+"/mcp", "application/json", strings.NewReader(initialize))
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("initialize status = %d, want 200", resp.StatusCode)
	}

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if logger.contains("Sweeping expired session") {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("idle session was never swept; the transport was built without a positive session idle TTL")
}

// writeLintPage writes a well-formed wiki page: frontmatter with title and
// type, so the only issues a test sees are the ones it deliberately created.
func writeLintPage(t *testing.T, wikiDir, name, title, body string) {
	t.Helper()
	content := fmt.Sprintf("---\ntitle: %s\ntype: note\n---\n\n# %s\n\n%s\n", title, title, body)
	if err := os.WriteFile(filepath.Join(wikiDir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestLintStrictChangesOnlyTheExitCode pins both halves of F13's contract. The
// default must stay exit-0 with issues found, because `cogvault lint` shipped
// that way and any caller written against it would break otherwise; --strict
// must fail; and the two runs must print the same thing, because a flag that
// quietly reshaped the report would make the exit code the lesser surprise.
func TestLintStrictChangesOnlyTheExitCode(t *testing.T) {
	configPath, wikiDir, _ := testVault(t)
	if _, _, err := executeCommand("init", "--config", configPath); err != nil {
		t.Fatalf("init: %v", err)
	}
	writeLintPage(t, wikiDir, "a.md", "A", "links to [[nowhere]]")

	plainOut, _, plainErr := executeCommand("lint", "--config", configPath)
	if plainErr != nil {
		t.Fatalf("lint without --strict must exit 0 even with issues; got %v", plainErr)
	}
	if !strings.Contains(plainOut, "issue(s) found") {
		t.Fatalf("expected the fixture to produce issues, got: %q", plainOut)
	}

	strictOut, _, strictErr := executeCommand("lint", "--config", configPath, "--strict")
	if strictErr == nil {
		t.Fatal("lint --strict must exit nonzero when issues are found")
	}
	if strictOut != plainOut {
		t.Errorf("--strict changed stdout; it must change only the exit code\nwithout: %q\nwith:    %q", plainOut, strictOut)
	}
}

// TestLintStrictSucceedsOnCleanWiki discriminates the flag from an
// unconditional failure. Without this, a --strict that always returned an error
// would satisfy the test above. It keeps _schema.md in place: that file used to
// have to be deleted here, which is what F14 was about.
func TestLintStrictSucceedsOnCleanWiki(t *testing.T) {
	configPath, wikiDir, _ := testVault(t)
	if _, _, err := executeCommand("init", "--config", configPath); err != nil {
		t.Fatalf("init: %v", err)
	}
	writeLintPage(t, wikiDir, "a.md", "A", "see [[b]]")
	writeLintPage(t, wikiDir, "b.md", "B", "see [[a]]")

	stdout, _, err := executeCommand("lint", "--config", configPath, "--strict")
	if err != nil {
		t.Fatalf("lint --strict must exit 0 on a clean wiki: %v (output: %q)", err, stdout)
	}
	if !strings.Contains(stdout, "no issues found") {
		t.Fatalf("expected a clean report, got: %q", stdout)
	}
}

// TestFreshWikiIsLintClean pins F14 by name rather than leaving it to another
// test's setup: `cogvault init` followed by `cogvault lint --strict` must
// succeed on a wiki holding nothing but the shipped _schema.md. Before F14 it
// reported two issues — a missing type field, and the `[[링크]]` the schema uses
// to illustrate wikilink syntax read as a real link — so anyone wiring --strict
// into CI failed on an empty wiki.
func TestFreshWikiIsLintClean(t *testing.T) {
	configPath, wikiDir, _ := testVault(t)
	if _, _, err := executeCommand("init", "--config", configPath); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wikiDir, "_schema.md")); err != nil {
		t.Fatalf("expected init to write _schema.md: %v", err)
	}

	stdout, _, err := executeCommand("lint", "--config", configPath, "--strict")
	if err != nil {
		t.Fatalf("a freshly initialized wiki must pass lint --strict: %v\noutput:\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "no issues found") {
		t.Fatalf("expected a clean report, got: %q", stdout)
	}
}

func writeOpenAIConfig(t *testing.T, configPath, wikiDir, dbPath, srcDir string) {
	t.Helper()
	content := fmt.Sprintf("wiki_dir: %s\ndb_path: %s\nadapter: obsidian\nsources:\n  - path: %s\n    types: [pdf]\nllm:\n  backend: openai\n  model: test-model\n  base_url: http://127.0.0.1:12345/v1\n  max_input_chars: 1000\n", wikiDir, dbPath, srcDir)
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestIngestOpenAIMissingPDFPrerequisiteFailsBeforeLedger(t *testing.T) {
	configPath, srcDir, wikiDir, dbPath := setupIngestVault(t)
	writeOpenAIConfig(t, configPath, wikiDir, dbPath, srcDir)
	if _, _, err := executeCommand("init", "--config", configPath); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	originalLookup := ingestLookPath
	originalReady := ingestCheckOpenAIReady
	t.Cleanup(func() { ingestLookPath = originalLookup; ingestCheckOpenAIReady = originalReady })
	ingestCheckOpenAIReady = func(context.Context, string, string) error { return nil }
	ingestLookPath = func(name string) (string, error) {
		if name == "pdfinfo" {
			return "", errors.New("missing")
		}
		return "/fake/" + name, nil
	}
	_, _, err := executeCommand("ingest", "--config", configPath)
	if err == nil || !strings.Contains(err.Error(), "pdfinfo") {
		t.Fatalf("expected pdfinfo prerequisite error, got %v", err)
	}
	if ledgerTableExists(t, dbPath) {
		t.Fatal("prerequisite failure constructed the ledger")
	}
}

func TestIngestOpenAIReadinessFailsBeforeLedger(t *testing.T) {
	configPath, srcDir, wikiDir, dbPath := setupIngestVault(t)
	writeOpenAIConfig(t, configPath, wikiDir, dbPath, srcDir)
	if _, _, err := executeCommand("init", "--config", configPath); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	originalLookup := ingestLookPath
	originalReady := ingestCheckOpenAIReady
	t.Cleanup(func() { ingestLookPath = originalLookup; ingestCheckOpenAIReady = originalReady })
	ingestLookPath = func(name string) (string, error) { return "/fake/" + name, nil }
	ingestCheckOpenAIReady = func(context.Context, string, string) error { return errors.New("provider unavailable") }
	_, _, err := executeCommand("ingest", "--config", configPath)
	if err == nil || !strings.Contains(err.Error(), "provider unavailable") {
		t.Fatalf("expected readiness error, got %v", err)
	}
	if ledgerTableExists(t, dbPath) {
		t.Fatal("readiness failure constructed the ledger")
	}
}

func TestIngestClaudeDoesNotRequirePDFTools(t *testing.T) {
	configPath, srcDir, wikiDir, dbPath := setupIngestVault(t)
	writeConfigFile(t, configPath, wikiDir, dbPath, srcDir)
	originalLookup := ingestLookPath
	t.Cleanup(func() { ingestLookPath = originalLookup })
	ingestLookPath = func(name string) (string, error) {
		if name == "claude" {
			return "/fake/claude", nil
		}
		return "", errors.New("unexpected tool lookup")
	}
	t.Setenv("PATH", "")
	if _, _, err := executeCommand("ingest", "--config", configPath); err != nil {
		t.Fatalf("Claude path-mode ingest should construct without extraction tools: %v", err)
	}
}

func ledgerTableExists(t *testing.T, dbPath string) bool {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='ingest_ledger'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count != 0
}

func TestIngestOpenAIEachMissingPrerequisiteFailsBeforeLedger(t *testing.T) {
	for _, missing := range []string{"pdftotext", "pdfinfo", "pdftoppm", "tesseract", "eng", "kor"} {
		t.Run(missing, func(t *testing.T) {
			configPath, srcDir, wikiDir, dbPath := setupIngestVault(t)
			writeOpenAIConfig(t, configPath, wikiDir, dbPath, srcDir)
			if _, _, err := executeCommand("init", "--config", configPath); err != nil {
				t.Fatalf("init failed: %v", err)
			}
			oldLookup, oldReady := ingestLookPath, ingestCheckOpenAIReady
			t.Cleanup(func() { ingestLookPath, ingestCheckOpenAIReady = oldLookup, oldReady })
			ingestLookPath = func(name string) (string, error) {
				if name == missing {
					return "", errors.New("missing")
				}
				return "/fake/" + name, nil
			}
			ingestCheckOpenAIReady = func(context.Context, string, string) error { return nil }
			_, _, err := executeCommand("ingest", "--config", configPath)
			if err == nil || !strings.Contains(err.Error(), missing) {
				t.Fatalf("expected %s prerequisite error, got %v", missing, err)
			}
			if ledgerTableExists(t, dbPath) {
				t.Fatal("missing prerequisite constructed the ledger")
			}
		})
	}
}

func TestIngestOpenAIReadyConstructsAfterPreflight(t *testing.T) {
	configPath, srcDir, wikiDir, dbPath := setupIngestVault(t)
	writeOpenAIConfig(t, configPath, wikiDir, dbPath, srcDir)
	oldLookup, oldReady := ingestLookPath, ingestCheckOpenAIReady
	t.Cleanup(func() { ingestLookPath, ingestCheckOpenAIReady = oldLookup, oldReady })
	called := false
	ingestLookPath = func(name string) (string, error) { return "/fake/" + name, nil }
	ingestCheckOpenAIReady = func(context.Context, string, string) error { called = true; return nil }
	if _, _, err := executeCommand("ingest", "--config", configPath); err != nil {
		t.Fatalf("ready OpenAI ingest failed: %v", err)
	}
	if !called {
		t.Fatal("OpenAI readiness was not checked before construction")
	}
}

func TestLaunchdPATHKeepsStandardHomebrewAndClaudeDirs(t *testing.T) {
	data, err := os.ReadFile("../../deploy/com.teslamint.cogvault.ingest.plist")
	if err != nil {
		t.Fatal(err)
	}
	path := string(data)
	for _, entry := range []string{"/usr/bin", "/bin", "/usr/sbin", "/sbin", "/opt/homebrew/bin", "/Users/USERNAME/.local/bin"} {
		if !strings.Contains(path, entry) {
			t.Errorf("launchd PATH missing %s", entry)
		}
	}
}
