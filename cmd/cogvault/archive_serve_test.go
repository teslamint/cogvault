package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"github.com/teslamint/cogvault/internal/archive"
	"github.com/teslamint/cogvault/internal/config"
	cogmcp "github.com/teslamint/cogvault/internal/mcp"
)

// archiveServeFixture is a temp vault with an archive root and a config
// file the test rewrites between reloads. Nothing outside t.TempDir() is
// touched.
type archiveServeFixture struct {
	configPath, wikiDir, dbPath, archiveRoot, base string
	srv                                            *server.MCPServer
	rt                                             *archiveRuntime
	logs                                           *syncBuffer
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func allowActivation(*config.Config) error { return nil }

func (f *archiveServeFixture) writeConfig(t *testing.T, enabled bool, extra string) {
	t.Helper()
	body := "wiki_dir: " + f.wikiDir + "\ndb_path: " + f.dbPath + "\nadapter: obsidian\n" + extra +
		"archive:\n  root: " + f.archiveRoot + "\n  capture_enabled: " + map[bool]string{true: "true", false: "false"}[enabled] + "\n"
	if err := os.WriteFile(f.configPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func newArchiveServeFixture(t *testing.T, enabled bool, activation func(*config.Config) error) *archiveServeFixture {
	t.Helper()
	base := t.TempDir()
	f := &archiveServeFixture{
		base: base, configPath: filepath.Join(base, "config.yaml"), wikiDir: filepath.Join(base, "wiki"),
		dbPath: filepath.Join(base, "index.db"), archiveRoot: filepath.Join(base, "archive"), logs: &syncBuffer{},
	}
	os.MkdirAll(f.wikiDir, 0o755)
	f.writeConfig(t, enabled, "")
	cfg, store, idx, adpt, err := bootstrap(f.configPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })
	f.srv = cogmcp.NewServer(cfg.WikiDir, cfg, store, idx, adpt)
	f.rt, err = newArchiveRuntime(cfg, f.configPath, f.srv, idx, store, activation, slog.New(slog.NewTextHandler(f.logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.rt.Close() })
	return f
}

func (f *archiveServeFixture) hasTools() bool {
	return f.srv.GetTool("archive_capture_text") != nil
}

func TestArchiveReloadTogglesOnlyCaptureEnabled(t *testing.T) {
	f := newArchiveServeFixture(t, false, allowActivation)
	if f.hasTools() || f.rt.gate.IsOpen() {
		t.Fatal("capture visible while disabled")
	}
	f.writeConfig(t, true, "llm:\n  timeout_seconds: 7\n")
	if err := f.rt.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !f.hasTools() || !f.rt.gate.IsOpen() {
		t.Fatal("enable reload did not expose capture")
	}
	if !strings.Contains(f.logs.String(), "not applied until a restart") || !strings.Contains(f.logs.String(), "llm") {
		t.Fatalf("changed llm key not reported as restart-only:\n%s", f.logs.String())
	}
	f.writeConfig(t, false, "")
	if err := f.rt.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.hasTools() || f.rt.gate.IsOpen() {
		t.Fatal("disable reload left capture exposed")
	}
	if _, ok := f.rt.gate.Enter(); ok {
		t.Fatal("closed gate admitted a call")
	}
}

func TestArchiveReloadRejectionsKeepStateAndLogNoValues(t *testing.T) {
	const secret = "SECRET-VALUE-7f3a"
	cases := []struct {
		name, extra, wantCode, wantKey string
		enabled                        bool
		edit                           func(f *archiveServeFixture)
		activation                     func(*config.Config) error
	}{
		{name: "yaml syntax", wantCode: "config_invalid", enabled: true, edit: func(f *archiveServeFixture) {
			os.WriteFile(f.configPath, []byte("wiki_dir: ["+secret+"\n"), 0o600)
		}},
		{name: "validation error", extra: "auth:\n  mode: oauth\n  oauth:\n    issuer: http://" + secret + ".example\n", wantCode: "config_invalid", wantKey: "auth.oauth.issuer", enabled: true},
		{name: "restart-only auth", extra: "auth:\n  mode: none\n  max_stream_seconds: 77\n", wantCode: "restart_only_changed", wantKey: "auth", enabled: true},
		{name: "restart-only root", wantCode: "restart_only_changed", wantKey: "archive.root", enabled: true, edit: func(f *archiveServeFixture) {
			f.archiveRoot = filepath.Join(f.base, secret)
			f.writeConfig(t, true, "")
		}},
		{name: "production activation", wantCode: "deployment_settings_missing", wantKey: "archive.deployment.backup_target", enabled: true, activation: productionActivation},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			activation := allowActivation
			if tc.activation != nil {
				activation = tc.activation
			}
			f := newArchiveServeFixture(t, false, activation)
			if tc.edit != nil {
				tc.edit(f)
			} else {
				f.writeConfig(t, tc.enabled, tc.extra)
			}
			err := f.rt.Reload(context.Background())
			if err == nil {
				t.Fatal("reload accepted")
			}
			if f.hasTools() || f.rt.gate.IsOpen() {
				t.Fatal("a rejected reload changed capture")
			}
			logs := f.logs.String()
			if !strings.Contains(logs, tc.wantCode) || (tc.wantKey != "" && !strings.Contains(logs, tc.wantKey)) {
				t.Fatalf("log lacks code %q / key %q:\n%s", tc.wantCode, tc.wantKey, logs)
			}
			for _, leak := range []string{secret, f.configPath, f.base} {
				if strings.Contains(logs, leak) || strings.Contains(err.Error(), leak) {
					t.Fatalf("reload log or error exposes %q:\n%s\n%v", leak, logs, err)
				}
			}
		})
	}
}

// TestProductionActivationChecksAttestationsOnly walks the activation check
// through each refusal. Every refusal names keys, never values. A pass
// means only that the attestations are present and the narrow file checks
// hold; it does not prove the deployment.
func TestProductionActivationChecksAttestationsOnly(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "archive")
	a, err := archive.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	res, err := a.CaptureSource(context.Background(), archive.SourceRequest{Content: strings.NewReader("x"), OriginalName: "x.md"})
	if err != nil {
		t.Fatal(err)
	}
	im, _ := a.ImpactOf(context.Background(), res.ObjectID)
	if err := a.Quarantine(context.Background(), res.ObjectID, im.Digest); err != nil {
		t.Fatal(err)
	}
	a.Close()

	target := filepath.Join(base, "SECRET-offsite")
	os.MkdirAll(target, 0o700)
	dep := config.ArchiveDeployment{
		BackupTarget: target, BackupSchedule: "daily", PermissionsOwner: "o", HandoffMount: "m",
		RestoreTestReport: filepath.Join(base, "SECRET-report.json"),
	}
	cfg := &config.Config{Archive: config.ArchiveConfig{Root: root, Deployment: dep}}
	check := func(want string) {
		t.Helper()
		err := productionActivation(cfg)
		if want == "" {
			if err != nil {
				t.Fatalf("activation refused: %v", err)
			}
			return
		}
		if err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "SECRET") {
			t.Fatalf("want %s, err = %v", want, err)
		}
	}
	check("archive.deployment.deletion_log_replica") // replica not configured
	cfg.Archive.Deployment.DeletionLogReplica = filepath.Join(base, "SECRET-replica.jsonl")
	check("restore_test_not_passed")
	report := func(backup string) {
		body := `{"version":1,"passed":true,"backup":"` + backup + `","backup_created_at":"","checked_at":"","objects_verified":1,"deletions_reapplied":0,"resurrected":[],"errors":[]}`
		os.WriteFile(dep.RestoreTestReport, []byte(body), 0o600)
	}
	report(filepath.Join(base, "elsewhere", "b1"))
	check("restore_report_not_for_backup_target")
	report(filepath.Join(target, "b1"))
	check("deletion_log_replica_not_current") // replica file missing
	os.WriteFile(cfg.Archive.Deployment.DeletionLogReplica, nil, 0o600)
	check("deletion_log_replica_not_current") // replica behind the live log
	live, _ := os.ReadFile(filepath.Join(root, "deletions", "log.jsonl"))
	os.WriteFile(cfg.Archive.Deployment.DeletionLogReplica, live, 0o600)
	check("restore_report_without_replica") // a live-log-only report

	// A real restore test that re-applied the replica satisfies the gate.
	bdir := filepath.Join(target, "b2")
	b, err := archive.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Backup(context.Background(), bdir); err != nil {
		t.Fatal(err)
	}
	b.Close()
	os.Remove(dep.RestoreTestReport)
	if _, _, err := executeCommand("archive", "--root", root, "restore-test", "--backup", bdir, "--report", dep.RestoreTestReport, "--deletion-log", cfg.Archive.Deployment.DeletionLogReplica, "--scratch", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	check("")
}

// TestServeRefusesCaptureWithoutDeploymentSettings runs the real serve
// command on loopback behind bearer auth, the shape a tunneled production
// deployment has. No config key relaxes the activation check, so serve
// refuses to start with capture enabled.
func TestServeRefusesCaptureWithoutDeploymentSettings(t *testing.T) {
	t.Setenv(bearerTokenEnvVar, strings.Repeat("k", 40))
	f := &archiveServeFixture{}
	base := t.TempDir()
	f.base, f.configPath, f.wikiDir, f.dbPath, f.archiveRoot = base, filepath.Join(base, "c.yaml"), filepath.Join(base, "wiki"), filepath.Join(base, "i.db"), filepath.Join(base, "archive")
	os.MkdirAll(f.wikiDir, 0o755)
	f.writeConfig(t, true, "auth:\n  mode: bearer\n")
	_, _, err := executeCommand("serve", "--config", f.configPath, "--transport", "http", "--addr", "127.0.0.1:0")
	if err == nil || !strings.Contains(err.Error(), "deployment_settings_missing") {
		t.Fatalf("serve with capture enabled and no deployment settings: err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.archiveRoot, "registry.db")); !os.IsNotExist(err) {
		t.Fatal("a refused serve still opened the archive")
	}
}

func TestArchiveReloadDrainsAdmittedCallsAndStopsOnShutdown(t *testing.T) {
	f := newArchiveServeFixture(t, true, allowActivation)
	f.rt.drainTimeout = time.Minute
	release, ok := f.rt.gate.Enter()
	if !ok {
		t.Fatal("gate closed at start")
	}
	f.writeConfig(t, false, "")
	done := make(chan error, 1)
	go func() { done <- f.rt.Reload(context.Background()) }()
	deadline := time.Now().Add(time.Second)
	for f.rt.gate.IsOpen() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if f.rt.gate.IsOpen() {
		t.Fatal("gate still open during a disabling reload")
	}
	if _, ok := f.rt.gate.Enter(); ok {
		t.Fatal("a new call was admitted while draining")
	}
	select {
	case <-done:
		t.Fatal("reload returned before the admitted call finished")
	case <-time.After(50 * time.Millisecond):
	}
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("reload did not return after the drain")
	}

	f.writeConfig(t, true, "")
	if err := f.rt.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	release, _ = f.rt.gate.Enter()
	defer release()
	f.writeConfig(t, false, "")
	ctx, cancel := context.WithCancel(context.Background())
	go func() { done <- f.rt.Reload(ctx) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not end the drain wait")
	}
	if f.rt.gate.IsOpen() {
		t.Fatal("gate reopened after an interrupted disable")
	}
}

// TestServeWithReloadCollapsesAndWaitsForReload drives the reload loop
// through an injected channel, so no real signal reaches the test process.
// It must run the reload, keep serving, collapse repeated requests, and
// return only after the reload worker has finished, so the caller can close
// the archive safely.
func TestServeWithReloadCollapsesAndWaitsForReload(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	var finished atomic.Bool
	started := make(chan struct{}, 4)
	onReload := func(ctx context.Context) {
		calls.Add(1)
		started <- struct{}{}
		<-ctx.Done()
		time.Sleep(20 * time.Millisecond) // work after cancel must still finish before return
		finished.Store(true)
	}
	hup := make(chan os.Signal, 3)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- serveWithReload(ctx, ln, http.NotFoundHandler(), onReload, hup) }()
	waitForServing(t, ln.Addr().String())
	for range 3 {
		hup <- syscall.SIGHUP
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("a reload request did not start a reload")
	}
	if resp, err := http.Get("http://" + ln.Addr().String()); err != nil {
		t.Fatalf("server stopped serving after a reload request: %v", err)
	} else {
		resp.Body.Close()
	}
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serveWithReload did not return")
	}
	if !finished.Load() {
		t.Fatal("serveWithReload returned while the reload was still running")
	}
	if n := calls.Load(); n < 1 || n > 2 {
		t.Fatalf("reload ran %d times for 3 requests during one reload; want 1 or 2", n)
	}
}

const sighupChildEnv = "COGVAULT_TEST_SIGHUP_CHILD"

// TestServeListenerSIGHUPInChildProcess sends a real SIGHUP, but only to a
// child copy of the test binary, so no signal can reach this process or
// other tests. The child must reload instead of dying, and exit cleanly on
// SIGTERM after the reload worker has finished.
func TestServeListenerSIGHUPInChildProcess(t *testing.T) {
	if os.Getenv(sighupChildEnv) == "1" {
		runSIGHUPChild(t)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestServeListenerSIGHUPInChildProcess$", "-test.count=1")
	cmd.Env = append(os.Environ(), sighupChildEnv+"=1")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	lines := bufio.NewScanner(out)
	await := func(prefix string) string {
		t.Helper()
		for lines.Scan() {
			if strings.HasPrefix(lines.Text(), prefix) {
				return lines.Text()
			}
		}
		t.Fatalf("child ended before %q: %v", prefix, lines.Err())
		return ""
	}
	await("child serving")
	if err := cmd.Process.Signal(syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	await("child reload started")
	// The SIGHUP was handled, so none is left in flight when the child
	// stops trapping it.
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if got := await("child returned"); got != "child returned err=<nil> finished=true" {
		t.Fatalf("child: %s", got)
	}
	io.Copy(io.Discard, out)
	if err := cmd.Wait(); err != nil {
		t.Fatalf("child did not exit cleanly (SIGHUP must not kill it): %v", err)
	}
}

func runSIGHUPChild(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var finished atomic.Bool
	onReload := func(ctx context.Context) {
		fmt.Println("child reload started")
		<-ctx.Done()
		time.Sleep(20 * time.Millisecond)
		finished.Store(true)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- serveListener(context.Background(), ln, http.NotFoundHandler(), onReload) }()
	waitForServing(t, ln.Addr().String())
	fmt.Println("child serving")
	err = <-errCh
	fmt.Printf("child returned err=%v finished=%v\n", err, finished.Load())
}

func mcpPost(t *testing.T, url, session, token, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("POST", url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if session != "" {
		req.Header.Set("Mcp-Session-Id", session)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

const initBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`

// TestArchiveCaptureKeepsBearerAuth: enabling capture changes no
// authentication. Through the real serve handler in bearer mode, an archive
// call without the token is 401 and with it reaches the tool.
func TestArchiveCaptureKeepsBearerAuth(t *testing.T) {
	token := strings.Repeat("b", 40)
	t.Setenv(bearerTokenEnvVar, token)
	f := newArchiveServeFixture(t, false, allowActivation)
	f.writeConfig(t, true, "")
	if err := f.rt.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(f.configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Auth.Mode = "bearer"
	handler, err := buildServeHandler(cfg, f.srv, serveFlags{transport: "http", addr: "127.0.0.1:0", endpointPath: "/mcp"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(handler)
	defer ts.Close()
	call := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"archive_capture_text","arguments":{"text":"auth check"}}}`
	if resp := mcpPost(t, ts.URL+"/mcp", "", "", call); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: status %d", resp.StatusCode)
	}
	if resp := mcpPost(t, ts.URL+"/mcp", "", "wrong-token-wrong-token-wrong-token-xx", call); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token: status %d", resp.StatusCode)
	}
	init := mcpPost(t, ts.URL+"/mcp", "", token, initBody)
	session := init.Header.Get("Mcp-Session-Id")
	init.Body.Close()
	mcpPost(t, ts.URL+"/mcp", session, token, `{"jsonrpc":"2.0","method":"notifications/initialized"}`).Body.Close()
	resp := mcpPost(t, ts.URL+"/mcp", session, token, call)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "cap_") {
		t.Fatalf("authorized call: %d %s", resp.StatusCode, body)
	}
}

// TestArchiveReloadSendsListChanged observes the notification on a live
// streamable HTTP listen stream. Receiving it does not make a client
// refresh its list; the gate, not the list, is the boundary.
func TestArchiveReloadSendsListChanged(t *testing.T) {
	f := newArchiveServeFixture(t, false, allowActivation)
	ts := httptest.NewServer(server.NewStreamableHTTPServer(f.srv))
	defer ts.Close()
	init := mcpPost(t, ts.URL, "", "", initBody)
	session := init.Header.Get("Mcp-Session-Id")
	var initResp struct {
		Result struct {
			Capabilities struct {
				Tools struct {
					ListChanged bool `json:"listChanged"`
				} `json:"tools"`
			} `json:"capabilities"`
		} `json:"result"`
	}
	json.NewDecoder(init.Body).Decode(&initResp)
	init.Body.Close()
	if !initResp.Result.Capabilities.Tools.ListChanged {
		t.Fatal("initialize does not advertise tools.listChanged")
	}
	mcpPost(t, ts.URL, session, "", `{"jsonrpc":"2.0","method":"notifications/initialized"}`).Body.Close()
	req, _ := http.NewRequest("GET", ts.URL, nil)
	req.Header.Set("Mcp-Session-Id", session)
	req.Header.Set("Accept", "text/event-stream")
	stream, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	lines := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(stream.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()
	time.Sleep(50 * time.Millisecond)
	f.writeConfig(t, true, "")
	if err := f.rt.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(3 * time.Second)
	for {
		select {
		case l := <-lines:
			if strings.Contains(l, "notifications/tools/list_changed") {
				return
			}
		case <-deadline:
			t.Fatal("no tools/list_changed on the listen stream after enable")
		}
	}
}
