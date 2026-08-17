package llm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"
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

func TestDigestStructuredTransientDiagnostic(t *testing.T) {
	c, _, _ := newFake(t, "custom_exit1")
	t.Setenv("CLAUDE_FAKE_STDOUT", `[{"type":"result","subtype":"error_during_execution","is_error":true,"result":"You've hit your weekly limit · resets Aug 18 at 12pm (Asia/Seoul)"}]`)

	_, err := c.Digest(context.Background(), DigestRequest{SourcePath: "notes/x.pdf"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrTransient) || errors.Is(err, ErrRefused) {
		t.Fatalf("weekly limit should be transient, not refused: %v", err)
	}
	if !strings.Contains(err.Error(), "resets Aug 18 at 12pm (Asia/Seoul)") {
		t.Errorf("error should contain actionable reset detail, got %q", err)
	}
}

func TestDigestGenericAPIErrorNotRefused(t *testing.T) {
	for _, tc := range []struct {
		name       string
		isError    bool
		wantDetail bool
	}{
		{"persistable", true, true},
		{"classification_only", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _, _ := newFake(t, "custom_exit0")
			t.Setenv("CLAUDE_FAKE_STDOUT", fmt.Sprintf(`[{"type":"result","subtype":"error_during_execution","is_error":%t,"terminal_reason":"api_error","result":"API Error: upstream overloaded; SECRET-DETAIL"}]`, tc.isError))

			_, err := c.Digest(context.Background(), DigestRequest{SourcePath: "notes/x.pdf"})
			if err == nil {
				t.Fatal("expected error")
			}
			if !errors.Is(err, ErrTransient) || errors.Is(err, ErrRefused) {
				t.Fatalf("generic API error should be transient, not refused: %v", err)
			}
			if got := strings.Contains(err.Error(), "SECRET-DETAIL"); got != tc.wantDetail {
				t.Errorf("diagnostic detail presence = %v, want %v: %q", got, tc.wantDetail, err)
			}
		})
	}
}

func TestDigestDiagnosticEventEligibility(t *testing.T) {
	tests := []struct {
		name       string
		event      string
		wantResult bool
	}{
		{"is_error_false", `{"type":"result","subtype":"error_during_execution","is_error":false,"result":"SECRET-INELIGIBLE"}`, false},
		{"completed", `{"type":"result","subtype":"success","is_error":true,"terminal_reason":"completed","result":"SECRET-INELIGIBLE"}`, false},
		{"execution_error", `{"type":"result","subtype":"error_during_execution","is_error":true,"result":"eligible execution detail"}`, true},
		{"api_error", `{"type":"result","subtype":"success","is_error":true,"terminal_reason":"api_error","result":"eligible API detail"}`, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, _, _ := newFake(t, "custom_exit1")
			t.Setenv("CLAUDE_FAKE_STDOUT", "["+tc.event+"]")
			t.Setenv("CLAUDE_FAKE_STDERR", "safe stderr fallback")
			_, err := c.Digest(context.Background(), DigestRequest{SourcePath: "notes/x.pdf"})
			if err == nil || !errors.Is(err, ErrTransient) {
				t.Fatalf("expected transient error, got %v", err)
			}
			gotResult := strings.Contains(err.Error(), "eligible ")
			if gotResult != tc.wantResult {
				t.Errorf("result eligibility = %v, want %v: %q", gotResult, tc.wantResult, err)
			}
			if strings.Contains(err.Error(), "SECRET-INELIGIBLE") {
				t.Errorf("ineligible result leaked: %q", err)
			}
		})
	}
}

func TestDigestDiagnosticShape(t *testing.T) {
	tests := []struct {
		name   string
		result string
	}{
		{"frontmatter", "---\ntitle: SECRET-PAGE"},
		{"heading", "# SECRET-PAGE"},
		{"fence", "```markdown SECRET-PAGE"},
		{"multiline", "partial detail\nSECRET-MULTILINE"},
		{"empty", "   "},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, _, _ := newFake(t, "custom_exit1")
			t.Setenv("CLAUDE_FAKE_STDOUT", `[{"type":"result","subtype":"error_during_execution","is_error":true,"result":`+strconv.Quote(tc.result)+`}]`)
			t.Setenv("CLAUDE_FAKE_STDERR", "safe stderr fallback")
			_, err := c.Digest(context.Background(), DigestRequest{SourcePath: "notes/x.pdf"})
			if err == nil || !strings.Contains(err.Error(), "safe stderr fallback") {
				t.Fatalf("unsafe result should fall back to stderr, got %v", err)
			}
			if strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "partial detail") {
				t.Errorf("unsafe result leaked: %q", err)
			}
		})
	}
}

func TestDigestRefusalCanonicalization(t *testing.T) {
	for _, diagnostic := range []string{
		"policy refusal: disallowed",
		"PoLiCy ReFuSaL:\t disallowed",
		"\x1b[31mAPI Error:\x1b[0m\tREFUSED by policy",
		"API Error: safeguards\u00a0flagged",
		"API Error: Fable 5's safeguards flagged this message",
	} {
		t.Run(diagnostic, func(t *testing.T) {
			c, _, _ := newFake(t, "custom_exit1")
			t.Setenv("CLAUDE_FAKE_STDOUT", diagnostic)
			_, err := c.Digest(context.Background(), DigestRequest{SourcePath: "notes/x.pdf"})
			if err == nil || !errors.Is(err, ErrRefused) || errors.Is(err, ErrTransient) {
				t.Errorf("known envelope should be refused, got %v", err)
			}
		})
	}
}

func TestDigestRefusalMutationNegatives(t *testing.T) {
	for _, diagnostic := range []string{
		"API Error: not safeguards flagged",
		`API Error: "safeguards flagged"`,
		`API Error: response contained "safeguards flagged"`,
		"warning: API Error: refused",
		"request failed; policy refusal: disallowed",
		`"policy refusal: disallowed"`,
		"not policy refusal: disallowed",
		"connection refused",
		"API Error: authentication failed",
		"the provider's safeguards flagged",
	} {
		t.Run(diagnostic, func(t *testing.T) {
			c, _, _ := newFake(t, "custom_exit1")
			t.Setenv("CLAUDE_FAKE_STDOUT", diagnostic)
			_, err := c.Digest(context.Background(), DigestRequest{SourcePath: "notes/x.pdf"})
			if err == nil || !errors.Is(err, ErrTransient) || errors.Is(err, ErrRefused) {
				t.Errorf("mutation should remain transient, got %v", err)
			}
		})
	}

	t.Run("stale_result_event", func(t *testing.T) {
		c, _, _ := newFake(t, "custom_exit1")
		t.Setenv("CLAUDE_FAKE_STDOUT", `[{"type":"result","subtype":"error_during_execution","is_error":true,"result":"policy refusal: stale"},{"type":"result","subtype":"error_during_execution","is_error":true,"result":"current transient detail"}]`)
		_, err := c.Digest(context.Background(), DigestRequest{SourcePath: "notes/x.pdf"})
		if err == nil || !errors.Is(err, ErrTransient) || errors.Is(err, ErrRefused) {
			t.Fatalf("only the final result may classify, got %v", err)
		}
		if !strings.Contains(err.Error(), "current transient detail") || strings.Contains(err.Error(), "stale") {
			t.Errorf("error should use only the final result: %q", err)
		}
	})
}

func TestDigestNonzeroExitDiagnosticFallbacks(t *testing.T) {
	tests := []struct {
		name       string
		stdout     string
		stderr     string
		want       string
		notContain string
	}{
		{"malformed_json", `[{"type":"result"`, "safe stderr", "safe stderr", `[{`},
		{"wrong_shape", `{"result":"RAW-JSON"}`, "safe stderr", "safe stderr", "RAW-JSON"},
		{"no_result", `[{"type":"system","result":"RAW-JSON"}]`, "safe stderr", "safe stderr", "RAW-JSON"},
		{"plain_prefers_stderr", "plain stdout", "preferred stderr", "preferred stderr", "plain stdout"},
		{"plain_without_stderr", "plain stdout", "", "plain stdout", ""},
		{"json_without_stderr", `[{"type":`, "", "exit status 1", `[{`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, _, _ := newFake(t, "custom_exit1")
			t.Setenv("CLAUDE_FAKE_STDOUT", tc.stdout)
			t.Setenv("CLAUDE_FAKE_STDERR", tc.stderr)
			_, err := c.Digest(context.Background(), DigestRequest{SourcePath: "notes/x.pdf"})
			if err == nil || !errors.Is(err, ErrTransient) {
				t.Fatalf("expected transient error, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q missing %q", err, tc.want)
			}
			if tc.notContain != "" && strings.Contains(err.Error(), tc.notContain) {
				t.Errorf("error leaked %q: %q", tc.notContain, err)
			}
		})
	}
}

func TestNormalizeCLIDiagnostic(t *testing.T) {
	input := "\x1b]0;secret title\x07\x1b[31mred\x1b[0m\t spaced\u00a0text\u200bformat\x00end"
	got := normalizeCLIDiagnostic(input)
	want := "red spaced text�format�end"
	if got != want {
		t.Errorf("normalizeCLIDiagnostic() = %q, want %q", got, want)
	}
	for _, r := range got {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			t.Errorf("normalized diagnostic retains control/format rune %U", r)
		}
	}

	long := strings.Repeat("x", 2001)
	got = normalizeCLIDiagnostic(long)
	if utf8.RuneCountInString(got) != 2000 || !strings.HasSuffix(got, "…") {
		t.Errorf("long diagnostic not truncated to 1,999 runes plus ellipsis: len=%d suffix=%q", utf8.RuneCountInString(got), got[len(got)-3:])
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
