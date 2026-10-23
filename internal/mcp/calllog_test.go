package mcp

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func callReq(name string, args map[string]any) mcp.CallToolRequest {
	var req mcp.CallToolRequest
	req.Params.Name = name
	req.Params.Arguments = args
	return req
}

func TestCallLogMiddleware_WriteLogsLengthNotContent(t *testing.T) {
	buf := captureSlog(t)
	const secret = "BODY-MUST-NOT-APPEAR"
	h := callLogMiddleware(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	if _, err := h(context.Background(), callReq("wiki_write", map[string]any{
		"path": "synthesis/x.md", "content": secret,
	})); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, secret) {
		t.Errorf("log leaked content: %s", out)
	}
	for _, want := range []string{"tool=wiki_write", "path=synthesis/x.md", "content_len=20", "is_error=false", "duration_ms="} {
		if !strings.Contains(out, want) {
			t.Errorf("log missing %q: %s", want, out)
		}
	}
}

func TestCallLogMiddleware_ReportsErrors(t *testing.T) {
	for name, next := range map[string]func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error){
		"result": func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultError("nope"), nil
		},
		"go-error": func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return nil, errors.New("boom")
		},
	} {
		t.Run(name, func(t *testing.T) {
			buf := captureSlog(t)
			_, _ = callLogMiddleware(next)(context.Background(), callReq("wiki_search", map[string]any{"query": "q", "limit": 5}))
			out := buf.String()
			for _, want := range []string{"tool=wiki_search", "query=q", "limit=5", "is_error=true"} {
				if !strings.Contains(out, want) {
					t.Errorf("log missing %q: %s", want, out)
				}
			}
		})
	}
}
