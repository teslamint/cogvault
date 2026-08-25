package main

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestRequireAddrHost(t *testing.T) {
	for _, addr := range []string{":8080", ""} {
		if err := requireAddrHost(addr); err == nil {
			t.Errorf("requireAddrHost(%q) = nil, want error", addr)
		}
	}
	for _, addr := range []string{"localhost:8080", "127.0.0.1:8080", "[::1]:8080"} {
		if err := requireAddrHost(addr); err != nil {
			t.Errorf("requireAddrHost(%q) = %v, want nil", addr, err)
		}
	}
}

func TestServeBindGuardRejectsEmptyHostInBearerMode(t *testing.T) {
	// bearer mode passes the loopback guard for a named host; ":8080" must
	// fail on the host-presence guard before anything binds.
	t.Setenv(bearerTokenEnvVar, "0123456789abcdef0123456789abcdef")
	configPath, _, _ := testVaultWithAuth(t, "auth:\n  mode: bearer\n")
	_, _, err := executeCommand("serve", "--config", configPath, "--transport", "http", "--addr", ":8080")
	if err == nil {
		t.Fatal("expected empty-host addr to be rejected")
	}
}

func TestServeListenerStopsOnContextCancel(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- serveListener(ctx, ln, http.NotFoundHandler())
	}()

	// Wait until the server answers, proving it was serving.
	waitForServing(t, ln.Addr().String())
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("serveListener: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serveListener did not return after context cancel")
	}
}

func TestServeListenerReturnsListenerError(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	// Closing the listener makes the in-flight Serve return a use-of-closed
	// error; serveListener must surface it rather than hang.
	ln.Close()

	errCh := make(chan error, 1)
	go func() {
		errCh <- serveListener(context.Background(), ln, http.NotFoundHandler())
	}()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected listener error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serveListener hung on a closed listener")
	}
}

func waitForServing(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("server never started listening on %s", addr)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
