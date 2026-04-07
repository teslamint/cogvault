package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/teslamint/cogvault/internal/adapter"
	"github.com/teslamint/cogvault/internal/config"
	cverr "github.com/teslamint/cogvault/internal/errors"
	"github.com/teslamint/cogvault/internal/index"
	"github.com/teslamint/cogvault/internal/storage"
)

func mapError(err error, path string) *mcp.CallToolResult {
	switch {
	case errors.Is(err, cverr.ErrNotFound):
		return mcp.NewToolResultError(fmt.Sprintf("not found: %s", path))
	case errors.Is(err, cverr.ErrPermission):
		return mcp.NewToolResultError(fmt.Sprintf("access denied: %s", path))
	case errors.Is(err, cverr.ErrTraversal):
		return mcp.NewToolResultError(fmt.Sprintf("invalid path: %s", path))
	case errors.Is(err, cverr.ErrSymlink):
		return mcp.NewToolResultError(fmt.Sprintf("invalid path: %s", path))
	case errors.Is(err, cverr.ErrNotMarkdown):
		return mcp.NewToolResultError(fmt.Sprintf("not a markdown file: %s", path))
	default:
		return mcp.NewToolResultError(fmt.Sprintf("internal error: %s", err.Error()))
	}
}

func handleWikiRead(store storage.Storage) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		path, err := req.RequireString("path")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: path"), nil
		}
		data, err := store.Read(path)
		if err != nil {
			return mapError(err, path), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

func handleWikiWrite(root string, cfg *config.Config, store storage.Storage, idx index.Index, adpt adapter.Adapter) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		path, err := req.RequireString("path")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: path"), nil
		}
		content, err := req.RequireString("content")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: content"), nil
		}

		if err := store.Write(path, []byte(content)); err != nil {
			return mapError(err, path), nil
		}

		if strings.HasSuffix(path, ".md") {
			src, parseErr := adpt.Parse(root, path, false)
			if parseErr == nil {
				_ = idx.Add(path, content, index.BuildMeta(src))
			}
		}

		result := map[string]any{
			"status":   "written",
			"path":     path,
			"bytes":    len(content),
			"warnings": []string{},
		}
		return mcp.NewToolResultJSON(result)
	}
}

func handleWikiList(cfg *config.Config, store storage.Storage, idx index.Index, adpt adapter.Adapter) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		prefix := req.GetString("prefix", "")
		if prefix == "" {
			prefix = "."
		}

		if _, _, _, err := idx.CheckConsistency(store, adpt, false); err != nil {
			if errors.Is(err, index.ErrConsistencySystemic) {
				return mapError(err, ""), nil
			}
			slog.Warn("consistency check: per-file errors", "error", err)
		}

		entries, err := store.List(prefix)
		if err != nil {
			return mapError(err, prefix), nil
		}

		results := make([]map[string]any, len(entries))
		for i, e := range entries {
			r := map[string]any{
				"path":   e.Path,
				"name":   e.Name,
				"is_dir": e.IsDir,
				"title":  "",
				"type":   "",
			}
			if !e.IsDir {
				if meta, metaErr := idx.GetMeta(e.Path); metaErr == nil {
					r["title"] = meta.Title
					r["type"] = meta.Type
				}
			}
			results[i] = r
		}
		return mcp.NewToolResultJSON(results)
	}
}

func handleWikiSearch(cfg *config.Config, store storage.Storage, idx index.Index, adpt adapter.Adapter) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query, err := req.RequireString("query")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: query"), nil
		}
		limit := req.GetInt("limit", 10)
		if limit < 1 {
			limit = 1
		}
		if limit > 100 {
			limit = 100
		}
		scope := req.GetString("scope", "all")

		if _, _, _, err := idx.CheckConsistency(store, adpt, false); err != nil {
			if errors.Is(err, index.ErrConsistencySystemic) {
				return mapError(err, ""), nil
			}
			slog.Warn("consistency check: per-file errors", "error", err)
		}

		results, err := idx.Search(query, limit, scope)
		if err != nil {
			return mapError(err, ""), nil
		}
		return mcp.NewToolResultJSON(results)
	}
}

func handleWikiScan(root string, cfg *config.Config, adpt adapter.Adapter) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		dir := req.GetString("dir", "")

		if dir != "" {
			if filepath.IsAbs(dir) {
				return mcp.NewToolResultError(fmt.Sprintf("invalid path: %s", dir)), nil
			}
			if adapter.ContainsDotDot(dir) {
				return mcp.NewToolResultError(fmt.Sprintf("invalid path: %s", dir)), nil
			}
			absDir := filepath.Join(root, filepath.Clean(dir))
			info, statErr := os.Stat(absDir)
			if statErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("not found: %s", dir)), nil
			}
			if !info.IsDir() {
				return mcp.NewToolResultError(fmt.Sprintf("not found: %s", dir)), nil
			}
		}

		var paths []string
		scanErr := adpt.Scan(root, cfg.AllExcluded(), func(path string) error {
			if dir != "" {
				cleanDir := filepath.Clean(dir)
				if !strings.HasPrefix(path, cleanDir+"/") && path != cleanDir {
					return nil
				}
			}
			paths = append(paths, path)
			return nil
		})
		if scanErr != nil {
			return mapError(scanErr, dir), nil
		}
		return mcp.NewToolResultJSON(paths)
	}
}

func handleWikiParse(root string, adpt adapter.Adapter) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		path, err := req.RequireString("path")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: path"), nil
		}
		includeContent := req.GetBool("include_content", false)

		if !strings.HasSuffix(strings.ToLower(path), ".md") {
			return mcp.NewToolResultError(fmt.Sprintf("not a markdown file: %s", path)), nil
		}

		src, err := adpt.Parse(root, path, includeContent)
		if err != nil {
			return mapError(err, path), nil
		}

		if !includeContent {
			src.Content = ""
		}

		return mcp.NewToolResultJSON(src)
	}
}
