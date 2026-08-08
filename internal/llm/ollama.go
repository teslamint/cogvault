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

func NewOllama(baseURL, model string) *Ollama {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	return &Ollama{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		client:  &http.Client{Timeout: 5 * time.Minute},
	}
}

func (o *Ollama) Name() string { return "ollama" }

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

	if resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("llm.Digest %s: HTTP %d: %w", req.SourcePath, resp.StatusCode, ErrTransient)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("llm.Digest %s: HTTP %d", req.SourcePath, resp.StatusCode)
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

	page := strings.TrimSpace(result.Response)
	if page == "" {
		return nil, fmt.Errorf("llm.Digest %s: empty response", req.SourcePath)
	}

	return &DigestResult{PageContent: page}, nil
}

var _ Adapter = (*Ollama)(nil)
