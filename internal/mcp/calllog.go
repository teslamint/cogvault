package mcp

import (
	"context"
	"log/slog"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// loggedArgs is the allowlist of tool arguments copied into the call log.
// Anything not listed (notably wiki_write's content) is never logged; content
// is reduced to its length by callLogAttrs.
var loggedArgs = []string{"path", "prefix", "dir", "query", "limit", "include_content"}

// callLogMiddleware emits one Info line per tool call so operators can see
// which tools a remote client actually invoked. The transport's auth headers
// never reach a tool handler, so they cannot leak here.
func callLogMiddleware(next server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()
		res, err := next(ctx, req)
		attrs := callLogAttrs(req)
		attrs = append(attrs,
			"is_error", err != nil || (res != nil && res.IsError),
			"duration_ms", time.Since(start).Milliseconds(),
		)
		slog.Info("mcp: tool call", attrs...)
		return res, err
	}
}

func callLogAttrs(req mcp.CallToolRequest) []any {
	attrs := []any{"tool", req.Params.Name}
	args := req.GetArguments()
	for _, k := range loggedArgs {
		if v, ok := args[k]; ok {
			attrs = append(attrs, k, v)
		}
	}
	if c, ok := args["content"].(string); ok {
		attrs = append(attrs, "content_len", len(c))
	}
	return attrs
}
