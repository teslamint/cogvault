package mcp

import (
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/teslamint/cogvault/internal/adapter"
	"github.com/teslamint/cogvault/internal/config"
	"github.com/teslamint/cogvault/internal/index"
	"github.com/teslamint/cogvault/internal/schema"
	"github.com/teslamint/cogvault/internal/storage"
)

const (
	serverName    = "cogvault"
	serverVersion = "0.1.0"
	maxSchemaLen  = 2000
)

func NewServer(root string, cfg *config.Config, store storage.Storage, idx index.Index, adpt adapter.Adapter) *server.MCPServer {
	s := server.NewMCPServer(serverName, serverVersion,
		server.WithInstructions(schemaInstructions(cfg, store)),
	)
	registerTools(s, root, cfg, store, idx, adpt)
	return s
}

func schemaInstructions(cfg *config.Config, store storage.Storage) string {
	schemaPath := cfg.SchemaPath()
	data, err := store.Read(schemaPath)
	if err != nil {
		return defaultSchemaInstructions(cfg)
	}

	content := string(data)
	runes := []rune(content)
	if len(runes) <= maxSchemaLen {
		return content
	}
	return string(runes[:maxSchemaLen]) + fmt.Sprintf("\n\n[Full schema: wiki_read(%q)]", schemaPath)
}

func defaultSchemaInstructions(_ *config.Config) string {
	content := strings.Replace(schema.DefaultContent,
		"전문은 `wiki_read(\"_schema.md\")`로 읽는다.",
		"전문이 아래에 포함되어 있다.",
		1)
	runes := []rune(content)
	if len(runes) <= maxSchemaLen {
		return content
	}
	return string(runes[:maxSchemaLen])
}

func noExtra(t mcp.Tool) mcp.Tool {
	t.InputSchema.AdditionalProperties = false
	return t
}

func registerTools(s *server.MCPServer, root string, cfg *config.Config, store storage.Storage, idx index.Index, adpt adapter.Adapter) {
	s.AddTool(noExtra(wikiReadTool()), handleWikiRead(store))
	s.AddTool(noExtra(wikiWriteTool()), handleWikiWrite(root, cfg, store, idx, adpt))
	s.AddTool(noExtra(wikiDeleteTool()), handleWikiDelete(root, store, idx))
	s.AddTool(noExtra(wikiListTool()), handleWikiList(cfg, store, idx, adpt))
	s.AddTool(noExtra(wikiSearchTool()), handleWikiSearch(cfg, store, idx, adpt))
	s.AddTool(noExtra(wikiScanTool()), handleWikiScan(root, cfg, adpt))
	s.AddTool(noExtra(wikiParseTool()), handleWikiParse(root, adpt))
}

func wikiDeleteTool() mcp.Tool {
	return mcp.NewTool("wiki_delete",
		mcp.WithDescription("Delete a file from the wiki root. Auto-commits the deletion to git if the wiki is a git repository."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Wiki root-relative file path to delete")),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(true),
	)
}

func wikiReadTool() mcp.Tool {
	return mcp.NewTool("wiki_read",
		mcp.WithDescription("Read a file from the wiki root. Returns the file content as text."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Wiki root-relative file path")),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	)
}

func wikiWriteTool() mcp.Tool {
	return mcp.NewTool("wiki_write",
		mcp.WithDescription("Write content to a file in the wiki root. Creates intermediate directories as needed. Overwrites existing files."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Wiki root-relative file path")),
		mcp.WithString("content", mcp.Required(), mcp.Description("File content to write")),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(true),
	)
}

func wikiListTool() mcp.Tool {
	return mcp.NewTool("wiki_list",
		mcp.WithDescription("List direct children of a directory. Returns path, name, is_dir, title, and type for each entry."),
		mcp.WithString("prefix", mcp.Description("Directory prefix to list (default: wiki root)")),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	)
}

func wikiSearchTool() mcp.Tool {
	return mcp.NewTool("wiki_search",
		mcp.WithDescription("Full-text search across indexed files. Returns matching files with snippets."),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search query")),
		mcp.WithNumber("limit", mcp.Description("Max results (default 10, max 100)")),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	)
}

func wikiScanTool() mcp.Tool {
	return mcp.NewTool("wiki_scan",
		mcp.WithDescription("Recursively list all markdown file paths. Designed for wiki root discovery."),
		mcp.WithString("dir", mcp.Description("Directory to scan (default: entire wiki root)")),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	)
}

func wikiParseTool() mcp.Tool {
	return mcp.NewTool("wiki_parse",
		mcp.WithDescription("Parse a markdown file and extract metadata: title, frontmatter, links, tags, aliases, etc."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Wiki root-relative .md file path")),
		mcp.WithBoolean("include_content", mcp.Description("Include full file content in response (default: false)")),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	)
}
