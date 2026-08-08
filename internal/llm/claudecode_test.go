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
	return NewClaudeCode(fakeClaude(t), ""), argvFile, stdinFile
}

func TestClaudeCodeName(t *testing.T) {
	if got := NewClaudeCode("claude", "").Name(); got != "claudecode" {
		t.Errorf("Name() = %q, want claudecode", got)
	}
}

func TestDigestModelPassthrough(t *testing.T) {
	argvFile := filepath.Join(t.TempDir(), "argv")
	t.Setenv("CLAUDE_FAKE_MODE", "ok")
	t.Setenv("CLAUDE_FAKE_ARGV_FILE", argvFile)
	c := NewClaudeCode(fakeClaude(t), "opus")

	if _, err := c.Digest(context.Background(), DigestRequest{SourcePath: "notes/x.pdf"}); err != nil {
		t.Fatalf("Digest: %v", err)
	}
	argv, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("read argv: %v", err)
	}
	wantArgv := "--print --output-format json --allowedTools Read --model opus"
	if got := strings.TrimSpace(string(argv)); got != wantArgv {
		t.Errorf("argv = %q, want %q", got, wantArgv)
	}
}

func TestDigestNoModelOmitsFlag(t *testing.T) {
	argvFile := filepath.Join(t.TempDir(), "argv")
	t.Setenv("CLAUDE_FAKE_MODE", "ok")
	t.Setenv("CLAUDE_FAKE_ARGV_FILE", argvFile)
	c := NewClaudeCode(fakeClaude(t), "")

	if _, err := c.Digest(context.Background(), DigestRequest{SourcePath: "notes/x.pdf"}); err != nil {
		t.Fatalf("Digest: %v", err)
	}
	argv, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("read argv: %v", err)
	}
	if strings.Contains(string(argv), "--model") {
		t.Errorf("argv %q should not contain %q", argv, "--model")
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
	wantArgv := "--print --output-format json --allowedTools Read"
	if got := strings.TrimSpace(string(argv)); got != wantArgv {
		t.Errorf("argv = %q, want %q", got, wantArgv)
	}

	stdin, err := os.ReadFile(stdinFile)
	if err != nil {
		t.Fatalf("read stdin: %v", err)
	}
	for _, want := range []string{
		"SCHEMA-MARKER",
		"notes/x.pdf",
		"wiki page slug: x",
	} {
		if !strings.Contains(string(stdin), want) {
			t.Errorf("stdin missing %q in:\n%s", want, stdin)
		}
	}
	for _, want := range []string{"category:", "article", "legal", "reference"} {
		if !strings.Contains(string(stdin), want) {
			t.Errorf("stdin missing category instruction %q", want)
		}
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
	c := NewClaudeCode(filepath.Join(t.TempDir(), "does-not-exist"), "")

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

func TestDigestRefusalExit0(t *testing.T) {
	c, _, _ := newFake(t, "refusal_exit0")

	_, err := c.Digest(context.Background(), DigestRequest{SourcePath: "notes/x.pdf"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrRefused) {
		t.Errorf("api_error/safeguards refusal should be ErrRefused, got %v", err)
	}
	if errors.Is(err, ErrTransient) {
		t.Errorf("refusal must not be transient: %v", err)
	}
}

func TestDigestRefusalExitNStdout(t *testing.T) {
	c, _, _ := newFake(t, "refusal_exitN")

	_, err := c.Digest(context.Background(), DigestRequest{SourcePath: "notes/x.pdf"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrRefused) {
		t.Errorf("nonzero-exit refusal on stdout should be ErrRefused, got %v", err)
	}
	if errors.Is(err, ErrTransient) {
		t.Errorf("refusal must not be transient: %v", err)
	}
}

func TestDigestRefusalExitNStderr(t *testing.T) {
	c, _, _ := newFake(t, "refusal_exitN_stderr")

	_, err := c.Digest(context.Background(), DigestRequest{SourcePath: "notes/x.pdf"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrRefused) {
		t.Errorf("nonzero-exit refusal on stderr should be ErrRefused, got %v", err)
	}
	if errors.Is(err, ErrTransient) {
		t.Errorf("refusal must not be transient: %v", err)
	}
}

func TestDigestSuccessBodyNotRefused(t *testing.T) {
	c, _, _ := newFake(t, "notrefusal_success")

	res, err := c.Digest(context.Background(), DigestRequest{SourcePath: "notes/x.pdf"})
	if err != nil {
		t.Fatalf("ordinary body must not be flagged as refusal: %v", err)
	}
	if !strings.HasPrefix(res.PageContent, "---") {
		t.Errorf("expected a page, got %q", res.PageContent)
	}
}

func TestDigestEmptyResultPermanent(t *testing.T) {
	c, _, _ := newFake(t, "empty")

	_, err := c.Digest(context.Background(), DigestRequest{SourcePath: "notes/x.pdf"})
	if err == nil {
		t.Fatal("expected error")
	}
	if errors.Is(err, ErrTransient) {
		t.Errorf("empty result should be permanent, got transient: %v", err)
	}
	if errors.Is(err, ErrRefused) {
		t.Errorf("empty result should not be refused: %v", err)
	}
}

func TestDigestContextCancelledTransient(t *testing.T) {
	c, _, _ := newFake(t, "sleep")
	c.timeout = 10 * time.Second

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	_, err := c.Digest(ctx, DigestRequest{SourcePath: "notes/x.pdf"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrTransient) {
		t.Errorf("context cancellation should be transient, got %v", err)
	}
	if !strings.Contains(err.Error(), "context cancelled") {
		t.Errorf("error should mention context cancelled, got %q", err)
	}
}

func TestDigestWrapsSourcePath(t *testing.T) {
	c, _, _ := newFake(t, "garbage")

	_, err := c.Digest(context.Background(), DigestRequest{SourcePath: "notes/x.pdf"})
	if err == nil || !strings.Contains(err.Error(), "llm.Digest notes/x.pdf") {
		t.Errorf("error should carry source path, got %v", err)
	}
}

func TestSourceTypePhrase(t *testing.T) {
	cases := []struct {
		ext  string
		want string
	}{
		{".pdf", "Read the PDF file at path: "},
		{".PDF", "Read the PDF file at path: "},
		{".md", "Read the markdown file at path: "},
		{".markdown", "Read the markdown file at path: "},
		{".csv", "Read the CSV file at path: "},
		{".tsv", "Read the TSV file at path: "},
		{".xlsx", "Read the Excel spreadsheet at path: "},
		{".txt", "Read the file at path: "},
		{"", "Read the file at path: "},
	}
	for _, tc := range cases {
		if got := sourceTypePhrase(tc.ext); got != tc.want {
			t.Errorf("sourceTypePhrase(%q) = %q, want %q", tc.ext, got, tc.want)
		}
	}
}

func TestBuildPromptTypeAware(t *testing.T) {
	cases := []struct {
		ext      string
		contains string
	}{
		{".pdf", "Read the PDF file at path:"},
		{".md", "Read the markdown file at path:"},
		{".txt", "Read the file at path:"},
	}
	for _, tc := range cases {
		req := DigestRequest{SourcePath: "/src/doc" + tc.ext, SchemaText: "S", PageSlug: "doc", SourceExt: tc.ext}
		got := buildPrompt(req)
		if !strings.Contains(got, tc.contains) {
			t.Errorf("buildPrompt(ext=%q) missing %q in:\n%s", tc.ext, tc.contains, got)
		}
	}
}

var _ Adapter = (*ClaudeCode)(nil)
