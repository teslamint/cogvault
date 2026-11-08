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
	"github.com/teslamint/cogvault/internal/version"
)

const (
	serverName   = "cogvault"
	maxSchemaLen = 2000
)

const evidenceInstructions = `For optional research_evidence frontmatter (version 1):
- Record performed checks, remaining gaps, and next steps separately.
- Distinguish what a source actually covers from the claim's scope; record extension assumptions in limitation or gaps.
- Never mark a check complete without a result and, for fulltext/table/replication, a locator.
- Missing originals, code found, or failed reruns are not completed checks.
- Passing lint or wiki_write checks confirms structure only, not factual accuracy.
- Follow wiki_write warnings and review research_evidence summaries returned by wiki_search.`

// conflictInstructions is server-owned, like evidenceInstructions, so it
// reaches every MCP client regardless of a wiki's editable _schema.md and
// never enters the ingest digest prompt. Distilled from the claim-level
// provenance rules of anthropics/claude-quickstarts managed-agents/knowledge-wiki
// at 4a4224bd; see docs/research/2026-11-09-knowledge-wiki-upstream-study.md.
const conflictInstructions = `When pages disagree or a claim changes over time:
- Keep conflicting or superseded claims. Do not average, overwrite, or delete one merely to resolve the disagreement (a removal the user explicitly requests is not that); mark each CONFLICT (same measure, sources disagree) or SUPERSEDED (replaced by a later claim) and keep both sides. Figures on different bases are separate claims: record each with its basis and never merge them.
- Give every claim its own source: a wiki page, or the actual external source or the user when the claim does not come from a page (a user's own observation is its own source even if a page mentions the event). Add the as-of date (when the value applied) and basis (scope, definition); write "date unknown" if no date is stated. A footer list of sources does not attribute individual claims.
- A sources/ page is a record or summary generated at ingest (and possibly edited since), not the original and not an archived copy of its bytes; source_path is a local inbox path, not an origin. Cite an origin recorded on the page (url, source_url, canonical_url) or named in its text as recorded, not verified. Cite an origin the user supplied as user-provided, not verified. If neither exists, cite the page and say the origin is unrecorded.
- "Not in this source" is not "not true": scope an absence to the sources you checked.
- wiki_write replaces the whole file and has no compare-and-swap. Read the page right before editing, apply the changes you were asked to make, keep all other content verbatim, and re-read it afterwards to detect a lost edit. This narrows the race window and detects some losses; it does not prevent them.`

func NewServer(root string, cfg *config.Config, store storage.Storage, idx index.Index, adpt adapter.Adapter) *server.MCPServer {
	s := server.NewMCPServer(serverName, version.String(),
		server.WithInstructions(schemaInstructions(cfg, store)+"\n\n"+evidenceInstructions+"\n\n"+conflictInstructions),
		server.WithToolHandlerMiddleware(callLogMiddleware),
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

// synthesisSaveHint is appended to the read-side tool descriptions so the
// Q&A → wiki feedback loop reaches clients that ignore server instructions:
// a model that answers from several pages is told to file the answer back.
const synthesisSaveHint = " If you answer a question by combining several wiki pages, save that answer with wiki_write as a new page under synthesis/ whose frontmatter has `type: synthesis` and a `sources:` list of the pages used. Skip one-off lookups."

func wikiReadTool() mcp.Tool {
	return mcp.NewTool("wiki_read",
		mcp.WithDescription("Read a file from the wiki root. Returns the file content as text."+synthesisSaveHint),
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
		mcp.WithDescription("Full-text search across indexed files. Returns snippets and usage_terms summaries with declared scope, where, level, conditions, evidence reference, and review status. unverified means unknown or pending review; recorded is not legal permission."+synthesisSaveHint),
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
