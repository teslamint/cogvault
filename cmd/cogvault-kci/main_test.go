package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The browser worker has one start condition, the activation gate: it needs an
// explicit --root and --config, no flag skips the check, and a config that does
// not pass leaves the archive root unopened.
func TestWorkerNeedsTheGateAndHasNoBypass(t *testing.T) {
	root := filepath.Join(t.TempDir(), "archive")
	for _, args := range [][]string{{}, {"--root", root}, {"--config", "/x"}} {
		if err := run(append(args, "--once")); err == nil || !strings.Contains(err.Error(), "--config are required") {
			t.Errorf("run(%v) = %v, want the gate message", args, err)
		}
	}
	for _, flag := range []string{"--isolated-trial", "--skip-gate", "--no-gate"} {
		if err := run([]string{"--root", root, "--config", "/x", "--once", flag}); err == nil || !strings.Contains(err.Error(), "not defined") {
			t.Errorf("run with %s = %v, want an unknown flag", flag, err)
		}
	}
	// A config that does not enable capture blocks the one-shot run before the
	// archive is opened.
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	body := "wiki_dir: " + t.TempDir() + "\ndb_path: " + filepath.Join(t.TempDir(), "i.db") + "\nadapter: obsidian\narchive:\n  root: " + root + "\n  capture_enabled: false\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"--root", root, "--config", cfg, "--once"}); err == nil || !strings.Contains(err.Error(), "capture_disabled") {
		t.Fatalf("run with capture off = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "registry.db")); !os.IsNotExist(err) {
		t.Fatal("a gated browser worker opened the archive")
	}
}
