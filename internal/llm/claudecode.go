package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const defaultTimeout = 5 * time.Minute

const maxDiagnosticRunes = 2000

const maxStdoutBytes = 4 << 20 // 4 MiB
const maxStderrBytes = 1 << 20 // 1 MiB

var (
	ansiSGRPattern = regexp.MustCompile(`\x1b\[[0-9;?]*m`)
	ansiOSCPattern = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
	refusalPattern = regexp.MustCompile(`(?i)^api error:\s*(?:refused\b|safeguards flagged\b|fable 5(?:'s|’s) safeguards flagged\b)`)
)

type cappedWriter struct {
	buf  bytes.Buffer
	max  int
	over bool
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	if w.over {
		return len(p), nil
	}
	if w.buf.Len()+len(p) > w.max {
		w.over = true
		return len(p), nil
	}
	return w.buf.Write(p)
}

func (w *cappedWriter) Bytes() []byte    { return w.buf.Bytes() }
func (w *cappedWriter) String() string   { return w.buf.String() }
func (w *cappedWriter) Overflowed() bool { return w.over }

type ClaudeCode struct {
	binPath   string
	model     string
	timeout   time.Duration // 0 => defaultTimeout; overridden in tests
	maxStdout int           // 0 => maxStdoutBytes; overridden in tests
	maxStderr int           // 0 => maxStderrBytes; overridden in tests
}

func NewClaudeCode(binPath, model string, opts ...Option) *ClaudeCode {
	ao := adapterOptions{}
	for _, opt := range opts {
		opt(&ao)
	}
	return &ClaudeCode{binPath: binPath, model: model, timeout: ao.timeout}
}

func (c *ClaudeCode) Name() string { return "claudecode" }

func (c *ClaudeCode) InputMode() InputMode { return PathInput }

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

	args := []string{"--print", "--output-format", "json", "--allowedTools", "Read"}
	if c.model != "" {
		args = append(args, "--model", c.model)
	}
	cmd := exec.CommandContext(ctx, c.binPath, args...)
	// Bound cleanup once the deadline fires: an orphaned descendant holding the
	// output pipes open must not keep Digest blocked past its timeout.
	cmd.WaitDelay = 2 * time.Second
	cmd.Stdin = strings.NewReader(buildPrompt(req))
	stdoutCap := c.maxStdout
	if stdoutCap == 0 {
		stdoutCap = maxStdoutBytes
	}
	stderrCap := c.maxStderr
	if stderrCap == 0 {
		stderrCap = maxStderrBytes
	}
	stdout := &cappedWriter{max: stdoutCap}
	stderr := &cappedWriter{max: stderrCap}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	runErr := cmd.Run()
	if errors.Is(ctx.Err(), context.Canceled) {
		return nil, fmt.Errorf("context cancelled: %w", ErrTransient)
	}

	inspection := inspectCLIOutput(stdout.Bytes())
	if (inspection.hasFinal && classificationEligible(inspection.final) && isRefusalText(inspection.final.Result)) ||
		isRefusalText(stderr.String()) || isRefusalText(inspection.plainStdout) {
		return nil, fmt.Errorf("claude policy refusal: %w", ErrRefused)
	}

	if stdout.Overflowed() {
		return nil, fmt.Errorf("stdout exceeded %d bytes", stdoutCap)
	}

	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("timeout after %s: %w", timeout, ErrTransient)
	}

	if runErr != nil {
		msg := failureDiagnostic(inspection, stderr.String(), runErr)
		return nil, fmt.Errorf("claude cli: %s: %w", msg, ErrTransient)
	}

	if inspection.hasFinal && (inspection.final.IsError || inspection.final.Subtype == "error_during_execution" || inspection.final.TerminalReason == "api_error") {
		msg := failureDiagnostic(inspection, stderr.String(), errors.New("claude execution error"))
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
	s = normalizeCLIDiagnostic(s)
	return strings.HasPrefix(strings.ToLower(s), "policy refusal:") || refusalPattern.MatchString(s)
}

type cliOutputInspection struct {
	final       resultEvent
	hasFinal    bool
	plainStdout string
}

func inspectCLIOutput(stdout []byte) cliOutputInspection {
	trimmed := strings.TrimSpace(string(stdout))
	if trimmed == "" {
		return cliOutputInspection{}
	}
	if trimmed[0] != '{' && trimmed[0] != '[' {
		return cliOutputInspection{plainStdout: string(stdout)}
	}

	var events []resultEvent
	if err := json.Unmarshal(stdout, &events); err != nil {
		return cliOutputInspection{}
	}
	final, ok := lastResultEvent(events)
	return cliOutputInspection{final: final, hasFinal: ok}
}

func classificationEligible(event resultEvent) bool {
	return event.TerminalReason == "api_error" ||
		(event.IsError && event.Subtype == "error_during_execution")
}

func diagnosticEligible(event resultEvent) bool {
	return event.IsError &&
		(event.TerminalReason == "api_error" || event.Subtype == "error_during_execution")
}

func failureDiagnostic(inspection cliOutputInspection, stderr string, runErr error) string {
	if inspection.hasFinal && diagnosticEligible(inspection.final) && isDiagnosticShaped(inspection.final.Result) {
		return normalizeCLIDiagnostic(inspection.final.Result)
	}
	if msg := normalizeCLIDiagnostic(stderr); msg != "" {
		return msg
	}
	if msg := normalizeCLIDiagnostic(inspection.plainStdout); msg != "" {
		return msg
	}
	return normalizeCLIDiagnostic(runErr.Error())
}

func isDiagnosticShaped(s string) bool {
	withoutANSI := stripRecognizedANSI(s)
	trimmed := strings.TrimSpace(withoutANSI)
	if trimmed == "" || strings.ContainsAny(trimmed, "\r\n") {
		return false
	}
	shape := strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return -1
		}
		return r
	}, trimmed))
	return !strings.HasPrefix(shape, "---") &&
		!strings.HasPrefix(shape, "#") &&
		!strings.HasPrefix(shape, "```")
}

func normalizeCLIDiagnostic(s string) string {
	s = stripRecognizedANSI(s)
	var b strings.Builder
	b.Grow(len(s))
	spacePending := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			spacePending = b.Len() > 0
			continue
		}
		if spacePending {
			b.WriteByte(' ')
			spacePending = false
		}
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			b.WriteRune('\uFFFD')
			continue
		}
		b.WriteRune(r)
	}

	result := b.String()
	if utf8.RuneCountInString(result) <= maxDiagnosticRunes {
		return result
	}
	runes := []rune(result)
	return string(runes[:maxDiagnosticRunes-1]) + "…"
}

func stripRecognizedANSI(s string) string {
	s = ansiOSCPattern.ReplaceAllString(s, "")
	return ansiSGRPattern.ReplaceAllString(s, "")
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
	if classificationEligible(final) && isRefusalText(final.Result) {
		return "", fmt.Errorf("claude policy refusal: %w", ErrRefused)
	}
	if final.IsError || final.Subtype == "error_during_execution" || final.TerminalReason == "api_error" {
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

func sourceTypePhrase(ext string) string {
	switch strings.ToLower(ext) {
	case ".pdf":
		return "Read the PDF file at path: "
	case ".md", ".markdown":
		return "Read the markdown file at path: "
	case ".csv":
		return "Read the CSV file at path: "
	case ".tsv":
		return "Read the TSV file at path: "
	case ".xlsx":
		return "Read the Excel spreadsheet at path: "
	default:
		return "Read the file at path: "
	}
}

func buildPrompt(req DigestRequest) string {
	var b strings.Builder
	b.WriteString(req.SchemaText)
	b.WriteString("\n\n")
	b.WriteString(sourceTypePhrase(req.SourceExt))
	b.WriteString(req.SourcePath)
	b.WriteString("\n\nDigest it into the wiki page slug: ")
	b.WriteString(req.PageSlug)
	b.WriteString("\n\nOutput ONLY a markdown wiki page (no preamble). Begin with YAML frontmatter carrying the fields title, type: source, source_path: ")
	b.WriteString(req.SourcePath)
	b.WriteString(", ingested_at set to today's date in ISO 8601 (YYYY-MM-DD), and category: <one of article, legal, reference> based on the document content. ")
	b.WriteString("article = news, opinion, analysis, blogs, newsletters, reports. ")
	b.WriteString("legal = court rulings, legislation, terms of service, privacy policies, regulations. ")
	b.WriteString("reference = technical docs, API docs, framework guides, standards, manuals. ")
	b.WriteString("Default to article if uncertain.\n")
	return b.String()
}
