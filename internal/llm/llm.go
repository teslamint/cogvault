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

// ErrRefused marks an Acceptable Use Policy / provider policy refusal. Unlike
// ErrTransient, retrying will not help; the file is a permanent failure.
var ErrRefused = errors.New("claude policy refusal")
