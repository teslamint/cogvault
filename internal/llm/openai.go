package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	openAIReadyTimeout = 10 * time.Second
	openAIResponseCap  = 1 << 20
)

type OpenAI struct {
	baseURL string
	model   string
	client  *http.Client
}

func NewOpenAI(baseURL, model string, opts ...Option) *OpenAI {
	ao := adapterOptions{timeout: defaultTimeout}
	for _, opt := range opts {
		opt(&ao)
	}
	if ao.timeout <= 0 {
		ao.timeout = defaultTimeout
	}
	return &OpenAI{
		baseURL: canonicalBaseURL(baseURL),
		model:   model,
		client: &http.Client{
			Timeout:       ao.timeout,
			CheckRedirect: disableRedirects,
		},
	}
}

func (o *OpenAI) Name() string { return "openai" }

func (o *OpenAI) InputMode() InputMode { return TextInput }

func (o *OpenAI) Digest(ctx context.Context, req DigestRequest) (*DigestResult, error) {
	if err := validateLoopbackURL(o.baseURL); err != nil {
		return nil, fmt.Errorf("llm.Digest %s: %w", req.SourcePath, err)
	}
	if strings.TrimSpace(req.SourceText) == "" {
		return nil, fmt.Errorf("llm.Digest %s: source text is required", req.SourcePath)
	}
	body := map[string]any{
		"model": o.model,
		"messages": []map[string]string{
			{"role": "system", "content": req.SchemaText + "\n\nOutput ONLY a markdown wiki page (no preamble)."},
			{"role": "user", "content": "Source path: " + req.SourcePath + "\nPage slug: " + req.PageSlug + "\nSource text:\n" + req.SourceText},
		},
		"stream": false,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("llm.Digest %s: %w", req.SourcePath, err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("llm.Digest %s: %w", req.SourcePath, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := o.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("llm.Digest %s: %w: %w", req.SourcePath, err, ErrTransient)
	}
	defer resp.Body.Close()
	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, openAIResponseCap+1))
	if readErr != nil {
		return nil, fmt.Errorf("llm.Digest %s: read response: %w", req.SourcePath, readErr)
	}
	if len(respBody) > openAIResponseCap {
		return nil, fmt.Errorf("llm.Digest %s: response exceeds %d bytes", req.SourcePath, openAIResponseCap)
	}
	if resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return nil, fmt.Errorf("llm.Digest %s: HTTP %d: %w", req.SourcePath, resp.StatusCode, ErrTransient)
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(respBody, &e)
		message := strings.ToLower(e.Error.Message)
		if strings.Contains(message, "no model loaded") || strings.Contains(message, "model is not loaded") {
			return nil, fmt.Errorf("llm.Digest %s: %s: %w", req.SourcePath, e.Error.Message, ErrTransient)
		}
		return nil, fmt.Errorf("llm.Digest %s: HTTP %d", req.SourcePath, resp.StatusCode)
	}
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("llm.Digest %s: parse response: %w", req.SourcePath, err)
	}
	for _, choice := range result.Choices {
		if page := strings.TrimSpace(stripFence(choice.Message.Content)); page != "" {
			return &DigestResult{PageContent: page}, nil
		}
	}
	return nil, fmt.Errorf("llm.Digest %s: no usable response choice", req.SourcePath)
}

func CheckOpenAIReady(ctx context.Context, baseURL, model string) error {
	if err := validateLoopbackURL(baseURL); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, openAIReadyTimeout)
	defer cancel()
	client := &http.Client{CheckRedirect: disableRedirects}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, canonicalBaseURL(baseURL)+"/models", nil)
	if err != nil {
		return fmt.Errorf("openai readiness: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("openai readiness: %w: %w", err, ErrTransient)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, openAIResponseCap+1))
	if err != nil {
		return fmt.Errorf("openai readiness: read response: %w", err)
	}
	if len(body) > openAIResponseCap {
		return fmt.Errorf("openai readiness: response exceeds %d bytes", openAIResponseCap)
	}
	if resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return fmt.Errorf("openai readiness: HTTP %d: %w", resp.StatusCode, ErrTransient)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("openai readiness: HTTP %d", resp.StatusCode)
	}
	var result struct {
		Data []struct {
			ID     string `json:"id"`
			Loaded *bool  `json:"loaded"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("openai readiness: parse response: %w", err)
	}
	for _, m := range result.Data {
		if m.ID != model {
			continue
		}
		if m.Loaded != nil && !*m.Loaded {
			return fmt.Errorf("openai model %q is not loaded: %w", model, ErrTransient)
		}
		return nil
	}
	return fmt.Errorf("openai model %q is not listed", model)
}

func disableRedirects(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

func canonicalBaseURL(raw string) string { return strings.TrimRight(raw, "/") }

func validateLoopbackURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("openai base_url: must be an http loopback API root")
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("openai base_url: must be an http loopback API root")
	}
	return nil
}

var _ Adapter = (*OpenAI)(nil)
