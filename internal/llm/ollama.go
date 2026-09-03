package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Ollama struct {
	baseURL string
	model   string
	client  *http.Client
}

// Option customizes an LLM adapter at construction.
type Option func(*adapterOptions)

type adapterOptions struct {
	timeout time.Duration
}

// WithTimeout bounds one digest call (HTTP round trip for ollama, process
// run for claudecode). Zero or negative keeps the per-backend default
// (5 minutes).
func WithTimeout(d time.Duration) Option {
	return func(o *adapterOptions) {
		if d > 0 {
			o.timeout = d
		}
	}
}

func NewOllama(baseURL, model string, opts ...Option) *Ollama {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	ao := adapterOptions{timeout: 5 * time.Minute}
	for _, opt := range opts {
		opt(&ao)
	}
	return &Ollama{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		client:  &http.Client{Timeout: ao.timeout},
	}
}

func (o *Ollama) Name() string { return "ollama" }

func (o *Ollama) InputMode() InputMode { return PathInput }

func (o *Ollama) Digest(ctx context.Context, req DigestRequest) (*DigestResult, error) {
	prompt := buildPrompt(req)

	body, err := json.Marshal(map[string]any{
		"model":  o.model,
		"prompt": prompt,
		"stream": false,
	})
	if err != nil {
		return nil, fmt.Errorf("llm.Digest %s: %w", req.SourcePath, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", o.baseURL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("llm.Digest %s: %w", req.SourcePath, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("llm.Digest %s: %w: %w", req.SourcePath, err, ErrTransient)
	}
	defer resp.Body.Close()

	code := resp.StatusCode
	// Server-side failures (5xx) and explicit retry signals (408, 429) are
	// transient: Ollama returns 500/503 while a model is loading or under
	// load, and burning the ledger's bounded attempts on those would
	// permanently exhaust files over blips the server recovers from on its
	// own. This mirrors the claudecode backend, which classifies equivalent
	// CLI failures as transient. Remaining 4xx codes are client errors
	// (e.g. a missing model name) that no retry can fix, so they stay
	// permanent and consume attempts.
	if code == http.StatusRequestTimeout || code == http.StatusTooManyRequests || code >= 500 {
		return nil, fmt.Errorf("llm.Digest %s: HTTP %d: %w", req.SourcePath, code, ErrTransient)
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("llm.Digest %s: HTTP %d", req.SourcePath, code)
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("llm.Digest %s: read response: %w", req.SourcePath, err)
	}

	var result struct {
		Response string `json:"response"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("llm.Digest %s: parse response: %w", req.SourcePath, err)
	}

	page := strings.TrimSpace(stripFence(result.Response))
	if page == "" {
		return nil, fmt.Errorf("llm.Digest %s: empty response", req.SourcePath)
	}

	return &DigestResult{PageContent: page}, nil
}

var _ Adapter = (*Ollama)(nil)
