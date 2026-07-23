package llm

import (
	"context"
	"errors"
)

type DigestRequest struct {
	SourcePath string
	SchemaText string
	PageSlug   string
}

type DigestResult struct {
	PageContent string
}

type Adapter interface {
	Digest(ctx context.Context, req DigestRequest) (*DigestResult, error)
	Name() string
}

// ErrTransient marks failures worth retrying (quota/rate limit, timeout, CLI
// transport, or execution errors). Everything else is a permanent failure.
var ErrTransient = errors.New("transient llm failure")

// ErrRefused signals a provider policy/AUP refusal — terminal under the same
// model, re-attempted only when the configured model changes, and never
// consuming a retry attempt (unlike a permanent failure, which does).
var ErrRefused = errors.New("claude policy refusal")
