package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/adrg/frontmatter"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/teslamint/cogvault/internal/adapter"
	"github.com/teslamint/cogvault/internal/config"
	cverr "github.com/teslamint/cogvault/internal/errors"
	"github.com/teslamint/cogvault/internal/index"
	"github.com/teslamint/cogvault/internal/storage"
)

// gitCommitTimeout bounds each git add/commit subprocess so a wedged
// index.lock cannot block a tool call indefinitely (0024). A var, not a
// const, so tests can shrink it to exercise the timeout path without a
// multi-second sleep.
var gitCommitTimeout = 10 * time.Second

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

func newToolResultJSONText(data any) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("unable to marshal JSON: %w", err)
	}
	return mcp.NewToolResultText(string(b)), nil
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

		if strings.HasSuffix(strings.ToLower(path), ".md") {
			src, parseErr := adpt.Parse(root, path, false)
			if parseErr != nil {
				// The page is written; only the index update failed. The
				// interval-gated consistency check heals the drift, but the
				// operator should see why search may briefly miss it.
				slog.Warn("wiki_write: parse for indexing failed", "path", path, "error", parseErr)
			} else if err := idx.Add(path, content, index.BuildMeta(src)); err != nil {
				slog.Warn("wiki_write: index add failed", "path", path, "error", err)
			}
		}

		if cfg.Git.CommitsOnWrite() {
			gitAutoCommit(root, path, fmt.Sprintf("wiki: write %s", path))
		}

		var warnings []string
		if strings.HasSuffix(strings.ToLower(path), ".md") {
			warnings = validateFrontmatter(content)
		}
		if warnings == nil {
			warnings = []string{}
		}

		result := map[string]any{
			"status":   "written",
			"path":     path,
			"bytes":    len(content),
			"warnings": warnings,
		}
		return mcp.NewToolResultJSON(result)
	}
}

func handleWikiDelete(root string, store storage.Storage, idx index.Index) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		path, err := req.RequireString("path")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: path"), nil
		}

		if err := store.Delete(path); err != nil {
			return mapError(err, path), nil
		}

		if strings.HasSuffix(strings.ToLower(path), ".md") {
			_ = idx.Remove(path)
		}

		gitAutoCommit(root, path, fmt.Sprintf("wiki: delete %s", path))

		result := map[string]any{
			"status": "deleted",
			"path":   path,
		}
		return mcp.NewToolResultJSON(result)
	}
}

// gitAutoCommit best-effort `git add`s and `git commit`s a single path
// against root when root is inside a git repository. wiki_delete calls this
// unconditionally (its own delete-commit, unchanged since before 0024);
// wiki_write calls it only when cfg.Git.CommitsOnWrite() (0024, opt-in, off
// by default). `add` and `commit` each get their own independent
// gitCommitTimeout-bounded context — sharing one context across both would
// let a slow (not necessarily wedged) `git add` starve `git commit` of its
// own timeout budget, turning a merely slow add into a spurious commit
// failure. Failures log, never return a tool error — same contract as the
// pre-existing delete path.
func gitAutoCommit(root, path, message string) {
	addCtx, addCancel := context.WithTimeout(context.Background(), gitCommitTimeout)
	defer addCancel()
	absPath := filepath.Join(root, path)
	cmd := exec.CommandContext(addCtx, "git", "-C", root, "add", absPath)
	if err := cmd.Run(); err != nil {
		slog.Warn("git add failed", "path", path, "error", err)
		return
	}

	commitCtx, commitCancel := context.WithTimeout(context.Background(), gitCommitTimeout)
	defer commitCancel()
	commitCmd := exec.CommandContext(commitCtx, "git", "-C", root, "commit", "-m", message)
	if err := commitCmd.Run(); err != nil {
		slog.Warn("git commit failed", "path", path, "error", err)
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
				"path":     e.Path,
				"name":     e.Name,
				"is_dir":   e.IsDir,
				"title":    "",
				"type":     "",
				"category": "",
			}
			if !e.IsDir {
				if meta, metaErr := idx.GetMeta(e.Path); metaErr == nil {
					r["title"] = meta.Title
					r["type"] = meta.Type
					r["category"] = meta.Category
				}
			}
			results[i] = r
		}
		return newToolResultJSONText(results)
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

		if _, _, _, err := idx.CheckConsistency(store, adpt, false); err != nil {
			if errors.Is(err, index.ErrConsistencySystemic) {
				return mapError(err, ""), nil
			}
			slog.Warn("consistency check: per-file errors", "error", err)
		}

		results, err := idx.Search(query, limit)
		if err != nil {
			return mapError(err, ""), nil
		}
		return newToolResultJSONText(results)
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
		return newToolResultJSONText(paths)
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

func validateFrontmatter(content string) []string {
	if !strings.HasPrefix(strings.TrimSpace(content), "---") {
		return []string{"missing YAML frontmatter"}
	}
	var fm map[string]any
	if _, err := frontmatter.Parse(bytes.NewReader([]byte(content)), &fm); err != nil {
		return []string{"missing YAML frontmatter"}
	}
	if fm == nil {
		fm = map[string]any{}
	}

	var warnings []string
	if _, ok := fm["title"]; !ok {
		warnings = append(warnings, "missing frontmatter field: title")
	}

	typ, _ := fm["type"].(string)
	if typ == "source" {
		if _, ok := fm["source_path"]; !ok {
			warnings = append(warnings, "source page missing field: source_path")
		}
		if _, ok := fm["ingested_at"]; !ok {
			warnings = append(warnings, "source page missing field: ingested_at")
		}
	}
	return warnings
}
