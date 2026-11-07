package trialmcp

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/teslamint/cogvault/internal/trial"
)

func registerReportTools(srv *server.MCPServer, s *trial.Service) {
	importTool := mcp.NewTool("trial_import_manifest", mcp.WithDescription("Store a strict selected-source JSON manifest, at most 8 KiB and 5 sources. This neither fetches URLs nor imports files, report bodies or research activity logs. URL policy is controlled separately by the operator."), mcp.WithString("manifest", mcp.Required(), mcp.MinLength(1), mcp.MaxLength(8192)), mcp.WithReadOnlyHintAnnotation(false), mcp.WithDestructiveHintAnnotation(false))
	importTool.InputSchema.AdditionalProperties = false
	srv.AddTool(importTool, func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		a, ok := arguments(r, "manifest")
		if !ok {
			return failure("invalid_arguments")
		}
		raw, ok := a["manifest"].(string)
		if !ok || len(raw) == 0 || len(raw) > 8192 {
			return failure("invalid_arguments")
		}
		res, err := s.ImportManifest(ctx, raw)
		if err != nil {
			return safeError(err)
		}
		return result(res)
	})
	statusTool := mcp.NewTool("trial_report_status", mcp.WithDescription("Read a bounded selected-source report inventory. Shows separate captures, their completeness and provenance, missing sources and references to copies outside this trial. Does not read arbitrary files or return source bodies."), mcp.WithString("report_id", mcp.Required(), mcp.MinLength(1), mcp.MaxLength(96)), mcp.WithReadOnlyHintAnnotation(true))
	statusTool.InputSchema.AdditionalProperties = false
	srv.AddTool(statusTool, func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		a, ok := arguments(r, "report_id")
		if !ok {
			return failure("invalid_arguments")
		}
		id, ok := a["report_id"].(string)
		if !ok || len(id) == 0 || len(id) > 96 {
			return failure("invalid_arguments")
		}
		res, err := s.ReportStatus(ctx, id)
		if err != nil {
			return safeError(err)
		}
		return result(res)
	})
}
