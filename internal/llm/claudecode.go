package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const defaultTimeout = 5 * time.Minute

type ClaudeCode struct {
	binPath string
	timeout time.Duration // 0 => defaultTimeout; overridden in tests
}

func NewClaudeCode(binPath string) *ClaudeCode {
	return &ClaudeCode{binPath: binPath}
}

func (c *ClaudeCode) Name() string { return "claudecode" }

func (c *ClaudeCode) Digest(ctx context.Context, req DigestRequest) (*DigestResult, error) {
	res, err := c.digest(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("llm.Digest %s: %w", req.SourcePath, err)
	}
	return res, nil
}

func (c *ClaudeCode) digest(ctx context.Context, req DigestRequest) (*DigestResult, error) {
	timeout := c.timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.binPath, "--print", "--output-format", "json", "--allowedTools", "Read")
	// Bound cleanup once the deadline fires: an orphaned descendant holding the
	// output pipes open must not keep Digest blocked past its timeout.
	cmd.WaitDelay = 2 * time.Second
	cmd.Stdin = strings.NewReader(buildPrompt(req))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("timeout after %s: %w", timeout, ErrTransient)
	}
	if runErr != nil {
		if isRefusalText(stdout.String()) || isRefusalText(stderr.String()) {
			return nil, fmt.Errorf("claude policy refusal: %w", ErrRefused)
		}
		// Both a failed process launch and a nonzero exit are transport/quota
		// class; never inspect the exit code for digestion success (U1 spike).
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = runErr.Error()
		}
		return nil, fmt.Errorf("claude cli: %s: %w", msg, ErrTransient)
	}

	page, err := parseResult(stdout.Bytes())
	if err != nil {
		return nil, err
	}
	return &DigestResult{PageContent: page}, nil
}

type resultEvent struct {
	Type           string `json:"type"`
	Subtype        string `json:"subtype"`
	IsError        bool   `json:"is_error"`
	Result         string `json:"result"`
	TerminalReason string `json:"terminal_reason"`
}

func isRefusalText(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "API Error:") || strings.Contains(s, "safeguards flagged")
}

func parseResult(stdout []byte) (string, error) {
	var events []resultEvent
	if err := json.Unmarshal(stdout, &events); err != nil {
		return "", fmt.Errorf("parse claude output: %w", err)
	}

	final, ok := lastResultEvent(events)
	if !ok {
		return "", errors.New("no result event in claude output")
	}
	if final.TerminalReason == "api_error" || isRefusalText(final.Result) {
		return "", fmt.Errorf("claude policy refusal: %w", ErrRefused)
	}
	if final.IsError || final.Subtype == "error_during_execution" {
		return "", fmt.Errorf("claude execution error (subtype=%q): %w", final.Subtype, ErrTransient)
	}
	if final.Subtype != "success" {
		return "", fmt.Errorf("unexpected result subtype %q", final.Subtype)
	}

	page := stripFence(final.Result)
	if page == "" {
		return "", errors.New("claude returned empty result")
	}
	return page, nil
}

func lastResultEvent(events []resultEvent) (resultEvent, bool) {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == "result" {
			return events[i], true
		}
	}
	return resultEvent{}, false
}

// stripFence removes one optional leading fence line (``` or ```<lang>) and a
// matching trailing ``` line; the CLI wraps output inconsistently (U1 spike).
func stripFence(s string) string {
	s = strings.TrimSpace(s)
	lines := strings.Split(s, "\n")
	if len(lines) < 2 || !strings.HasPrefix(strings.TrimSpace(lines[0]), "```") {
		return s
	}
	lines = lines[1:]
	if n := len(lines); n > 0 && strings.TrimSpace(lines[n-1]) == "```" {
		lines = lines[:n-1]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func buildPrompt(req DigestRequest) string {
	var b strings.Builder
	b.WriteString(req.SchemaText)
	b.WriteString("\n\nRead the PDF file at path: ")
	b.WriteString(req.SourcePath)
	b.WriteString("\n\nDigest it into the wiki page slug: ")
	b.WriteString(req.PageSlug)
	b.WriteString("\n\nOutput ONLY a markdown wiki page (no preamble). Begin with YAML frontmatter carrying the fields title, type: source, source_path: ")
	b.WriteString(req.SourcePath)
	b.WriteString(", and ingested_at set to today's date in ISO 8601 (YYYY-MM-DD).\n")
	return b.String()
}
