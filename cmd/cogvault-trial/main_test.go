package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/teslamint/cogvault/internal/trial"
)

func trialEnv(t *testing.T, over ...string) (environ []string, root string) {
	t.Helper()
	parent := filepath.Join(t.TempDir(), "trial")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	root = filepath.Join(parent, "data")
	environ = []string{
		trial.EnvAck + "=" + trial.AckPhrase, trial.EnvAcceptedBy + "=tester 2026-11-07",
		trial.EnvAllowedHosts + "=www.rfc-editor.org", trial.EnvRoot + "=" + root,
	}
	return append(environ, over...), root
}

func do(t *testing.T, environ []string, args ...string) (string, string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err := run(args, environ, &out, &errOut)
	return out.String(), errOut.String(), err
}

func TestCommandsRefuseWithoutTheAcknowledgementAndHaveNoDelete(t *testing.T) {
	environ, _ := trialEnv(t)
	for _, bad := range [][]string{{trial.EnvAck + "=no"}, {trial.EnvAcceptedBy + "="}, {trial.EnvAllowedHosts + "="}, {"COGVAULT_TRIAL_TYPO=1"}} {
		e := append(append([]string(nil), environ...), bad...)
		// Later entries win in the map built from the slice, as in the process environment.
		if _, _, err := do(t, e, "check"); err == nil {
			t.Errorf("check accepted %v", bad)
		}
	}
	for _, cmd := range []string{"cleanup", "delete", "purge", "reset", "wipe"} {
		if _, _, err := do(t, environ, cmd); err == nil || !strings.Contains(err.Error(), "unknown command") {
			t.Errorf("%s: %v, want an unknown command", cmd, err)
		}
	}
	if out, _, err := do(t, environ, "version"); err != nil || strings.TrimSpace(out) == "" {
		t.Fatalf("version = %q, %v", out, err)
	}
	if _, _, err := do(t, nil, "version"); err != nil {
		t.Fatalf("version needs no policy: %v", err)
	}
}

// check creates nothing; submit, status, pause and resume work through the CLI
// and a second invocation (a restart) reads the same job from the same root.
func TestSubmitStatusPauseAndRestart(t *testing.T) {
	environ, root := trialEnv(t)
	if out, _, err := do(t, environ, "check"); err != nil || !strings.Contains(out, "no backup is assumed") {
		t.Fatalf("check = %q, %v", out, err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("check created the root")
	}
	for _, bad := range []string{"http://www.rfc-editor.org/rfc/rfc9110.pdf", "https://example.org/a.pdf", "https://www.rfc-editor.org:8443/a.pdf", "https://www.rfc-editor.org/rfc/rfc9110.pdf#x"} {
		if _, _, err := do(t, environ, "submit", bad); err == nil {
			t.Errorf("submit accepted %s", bad)
		}
	}
	out, _, err := do(t, environ, "submit", "--label", "smoke", "https://www.rfc-editor.org/rfc/rfc9110.pdf")
	id := regexp.MustCompile(`job_[0-9a-f]+`).FindString(out)
	if err != nil || id == "" {
		t.Fatalf("submit = %q, %v", out, err)
	}
	if again, _, err := do(t, environ, "submit", "https://www.rfc-editor.org/rfc/rfc9110.pdf"); err != nil || !strings.Contains(again, "already queued "+id) {
		t.Fatalf("a repeated submit = %q, %v", again, err)
	}
	// "Restart": a fresh invocation, same root.
	st, _, err := do(t, environ, "status", id)
	if err != nil || !strings.Contains(st, id+": queued") || !strings.Contains(st, "rfc9110.pdf") {
		t.Fatalf("status = %q, %v", st, err)
	}
	if b, _, err := do(t, environ, "status"); err != nil || !strings.Contains(b, "waiting or running 1/10") {
		t.Fatalf("budget = %q, %v", b, err)
	}
	// Pause blocks a single pass; resume lets it through to the end of the queue check.
	if _, _, err := do(t, environ, "pause"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, trial.PauseName)); err != nil {
		t.Fatalf("pause file: %v", err)
	}
	if _, _, err := do(t, environ, "run", "--once"); err == nil {
		t.Fatal("a paused run --once returned no error")
	}
	if st, _, _ := do(t, environ, "status", id); !strings.Contains(st, "queued") || !strings.Contains(st, "attempts 0 of 3") {
		t.Fatalf("a paused worker touched the job: %q", st)
	}
	if _, _, err := do(t, environ, "resume"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, trial.PauseName)); !os.IsNotExist(err) {
		t.Fatal("resume left the pause file")
	}
}

// A root that overlaps a protected path, or holds anything that is not a trial,
// stops every command that would open it, before anything is written.
func TestCommandsRefuseAProductionLookingRoot(t *testing.T) {
	environ, root := trialEnv(t)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "registry.db"), []byte("prod"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range [][]string{{"check"}, {"submit", "https://www.rfc-editor.org/rfc/rfc9110.pdf"}, {"status"}, {"run", "--once"}, {"pause"}} {
		if _, _, err := do(t, environ, cmd...); err == nil || !strings.Contains(err.Error(), "trial root refused") {
			t.Errorf("%v: %v, want a refused root", cmd, err)
		}
	}
	if entries, _ := os.ReadDir(root); len(entries) != 1 {
		t.Fatalf("a refused command wrote to the directory: %v", entries)
	}
}
