package llm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeClaude(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("testdata", "bin", "claude"))
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}
	return p
}

func newFake(t *testing.T, mode string) (*ClaudeCode, string, string) {
	t.Helper()
	argvFile := filepath.Join(t.TempDir(), "argv")
	stdinFile := filepath.Join(t.TempDir(), "stdin")
	t.Setenv("CLAUDE_FAKE_MODE", mode)
	t.Setenv("CLAUDE_FAKE_ARGV_FILE", argvFile)
	t.Setenv("CLAUDE_FAKE_STDIN_FILE", stdinFile)
	return NewClaudeCode(fakeClaude(t)), argvFile, stdinFile
}

func TestClaudeCodeName(t *testing.T) {
	if got := NewClaudeCode("claude").Name(); got != "claudecode" {
		t.Errorf("Name() = %q, want claudecode", got)
	}
}

func TestDigestHappy(t *testing.T) {
	c, argvFile, stdinFile := newFake(t, "ok")
	req := DigestRequest{SourcePath: "notes/x.pdf", SchemaText: "SCHEMA-MARKER", PageSlug: "x"}

	res, err := c.Digest(context.Background(), req)
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	if !strings.HasPrefix(res.PageContent, "---") || !strings.Contains(res.PageContent, "# Test Page") {
		t.Errorf("unexpected page content: %q", res.PageContent)
	}

	argv, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("read argv: %v", err)
	}
	for _, want := range []string{"--print", "--output-format", "json", "--allowedTools", "Read"} {
		if !strings.Contains(string(argv), want) {
			t.Errorf("argv %q missing %q", argv, want)
		}
	}

	stdin, err := os.ReadFile(stdinFile)
	if err != nil {
		t.Fatalf("read stdin: %v", err)
	}
	if !strings.Contains(string(stdin), "SCHEMA-MARKER") {
		t.Errorf("stdin missing schema text: %q", stdin)
	}
	if !strings.Contains(string(stdin), "notes/x.pdf") {
		t.Errorf("stdin missing source path: %q", stdin)
	}
}

func TestDigestFencedStripped(t *testing.T) {
	c, _, _ := newFake(t, "okfenced")

	res, err := c.Digest(context.Background(), DigestRequest{SourcePath: "notes/x.pdf"})
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	if strings.Contains(res.PageContent, "```") {
		t.Errorf("fence not stripped: %q", res.PageContent)
	}
	if !strings.HasPrefix(res.PageContent, "---") {
		t.Errorf("stripped content should start at frontmatter: %q", res.PageContent)
	}
}

func TestDigestExecutionErrorTransient(t *testing.T) {
	c, _, _ := newFake(t, "execerr")

	_, err := c.Digest(context.Background(), DigestRequest{SourcePath: "notes/x.pdf"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrTransient) {
		t.Errorf("error_during_execution should be transient, got %v", err)
	}
}

func TestDigestRateLimitTransient(t *testing.T) {
	c, _, _ := newFake(t, "ratelimit")

	_, err := c.Digest(context.Background(), DigestRequest{SourcePath: "notes/x.pdf"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrTransient) {
		t.Errorf("nonzero exit should be transient, got %v", err)
	}
}

func TestDigestGarbagePermanent(t *testing.T) {
	c, _, _ := newFake(t, "garbage")

	_, err := c.Digest(context.Background(), DigestRequest{SourcePath: "notes/x.pdf"})
	if err == nil {
		t.Fatal("expected error")
	}
	if errors.Is(err, ErrTransient) {
		t.Errorf("malformed JSON should be permanent, got transient: %v", err)
	}
}

func TestDigestMissingBinaryTransient(t *testing.T) {
	c := NewClaudeCode(filepath.Join(t.TempDir(), "does-not-exist"))

	_, err := c.Digest(context.Background(), DigestRequest{SourcePath: "notes/x.pdf"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrTransient) {
		t.Errorf("missing binary should be transient, got %v", err)
	}
}

func TestDigestTimeoutTransient(t *testing.T) {
	c, _, _ := newFake(t, "sleep")
	c.timeout = 100 * time.Millisecond

	_, err := c.Digest(context.Background(), DigestRequest{SourcePath: "notes/x.pdf"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrTransient) {
		t.Errorf("timeout should be transient, got %v", err)
	}
}

func TestDigestWrapsSourcePath(t *testing.T) {
	c, _, _ := newFake(t, "garbage")

	_, err := c.Digest(context.Background(), DigestRequest{SourcePath: "notes/x.pdf"})
	if err == nil || !strings.Contains(err.Error(), "llm.Digest notes/x.pdf") {
		t.Errorf("error should carry source path, got %v", err)
	}
}

var _ Adapter = (*ClaudeCode)(nil)
