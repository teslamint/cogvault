package mcp

import (
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/teslamint/cogvault/internal/adapter"
	"github.com/teslamint/cogvault/internal/config"
	"github.com/teslamint/cogvault/internal/index"
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

func defaultSchemaInstructions(cfg *config.Config) string {
	return fmt.Sprintf(`Wiki pages live under %q. Each page is a Markdown file with YAML frontmatter.
Use wiki_read/wiki_write/wiki_list/wiki_search/wiki_scan/wiki_parse to interact with the vault.
Read the schema with wiki_read(%q) for detailed formatting rules.`,
		cfg.WikiDir, cfg.SchemaPath())
}

func registerTools(s *server.MCPServer, root string, cfg *config.Config, store storage.Storage, idx index.Index, adpt adapter.Adapter) {
	s.AddTool(wikiReadTool(), handleWikiRead(store))
	s.AddTool(wikiWriteTool(), handleWikiWrite(root, cfg, store, idx, adpt))
	s.AddTool(wikiListTool(), handleWikiList(cfg, store, idx, adpt))
	s.AddTool(wikiSearchTool(), handleWikiSearch(cfg, store, idx, adpt))
	s.AddTool(wikiScanTool(), handleWikiScan(root, cfg, adpt))
	s.AddTool(wikiParseTool(), handleWikiParse(root, adpt))
}

func wikiReadTool() mcp.Tool {
	return mcp.NewTool("wiki_read",
		mcp.WithDescription("Read a file from the vault. Returns the file content as text."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Vault root-relative file path")),
	)
}

func wikiWriteTool() mcp.Tool {
	return mcp.NewTool("wiki_write",
		mcp.WithDescription("Write content to a file in the wiki directory. Creates intermediate directories as needed. Overwrites existing files."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Vault root-relative file path")),
		mcp.WithString("content", mcp.Required(), mcp.Description("File content to write")),
	)
}

func wikiListTool() mcp.Tool {
	return mcp.NewTool("wiki_list",
		mcp.WithDescription("List direct children of a directory. Returns path, name, is_dir, title, and type for each entry."),
		mcp.WithString("prefix", mcp.Description("Directory prefix to list (default: vault root)")),
	)
}

func wikiSearchTool() mcp.Tool {
	return mcp.NewTool("wiki_search",
		mcp.WithDescription("Full-text search across indexed files. Returns matching files with snippets."),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search query")),
		mcp.WithNumber("limit", mcp.Description("Max results (default 10, max 100)")),
		mcp.WithString("scope", mcp.Description("Search scope: all (default), wiki, or vault"), mcp.Enum("all", "wiki", "vault")),
	)
}

func wikiScanTool() mcp.Tool {
	return mcp.NewTool("wiki_scan",
		mcp.WithDescription("Recursively list all markdown file paths. Designed for vault discovery."),
		mcp.WithString("dir", mcp.Description("Directory to scan (default: entire vault)")),
	)
}

func wikiParseTool() mcp.Tool {
	return mcp.NewTool("wiki_parse",
		mcp.WithDescription("Parse a markdown file and extract metadata: title, frontmatter, links, tags, aliases, etc."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Vault root-relative .md file path")),
		mcp.WithBoolean("include_content", mcp.Description("Include full file content in response (default: false)")),
	)
}
