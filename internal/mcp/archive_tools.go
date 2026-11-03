package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/teslamint/cogvault/internal/archive"
	cverr "github.com/teslamint/cogvault/internal/errors"
	"github.com/teslamint/cogvault/internal/index"
	"github.com/teslamint/cogvault/internal/terms"
)

// ArchiveMaxTextBytes bounds one text capture through MCP. It is a
// prototype bound; the production maximum object size is ADR 0027 open
// question 6.
const ArchiveMaxTextBytes = 1 << 20

// ArchiveGate is the boundary for every archive tool (ADR 0027 item 12).
// Removing a tool from the list is only a hint to clients: mcp-go runs a
// handler it looked up before DeleteTools, and clients need not refresh
// their lists. Each handler therefore enters the gate first; a closed gate
// refuses the call before anything is stored.
type ArchiveGate struct {
	mu       sync.Mutex
	open     bool
	inflight int
	drained  chan struct{}
}

// Enter admits one call. ok is false when the gate is closed; otherwise
// release must be called when the call ends.
func (g *ArchiveGate) Enter() (release func(), ok bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.open {
		return nil, false
	}
	g.inflight++
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			g.inflight--
			if g.inflight == 0 && g.drained != nil {
				close(g.drained)
				g.drained = nil
			}
		})
	}, true
}

// Open admits new calls.
func (g *ArchiveGate) Open() {
	g.mu.Lock()
	g.open = true
	g.mu.Unlock()
}

// Close refuses new calls at once. The returned channel is closed when the
// calls admitted before Close have finished; Close never cancels them.
func (g *ArchiveGate) Close() <-chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.open = false
	if g.inflight == 0 {
		done := make(chan struct{})
		close(done)
		return done
	}
	if g.drained == nil {
		g.drained = make(chan struct{})
	}
	return g.drained
}

// IsOpen reports whether new calls are admitted.
func (g *ArchiveGate) IsOpen() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.open
}

// InFlight reports how many admitted calls have not finished.
func (g *ArchiveGate) InFlight() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.inflight
}

const archiveDisabled = "archive capture is disabled on this server"

// ArchiveToolNames lists every archive tool, for DeleteTools.
var ArchiveToolNames = []string{"archive_capture_text", "archive_capture_report", "archive_capture_summary", "archive_status", "archive_page_links"}

// PageChecker answers whether a wiki-relative path is an existing file and
// reads it. storage.FSStorage satisfies it; evidence validation uses the
// same IsFile check.
type PageChecker interface {
	IsFile(path string) (bool, error)
	Read(path string) ([]byte, error)
}

// ArchiveTools builds the archive tools. They are separate from wiki_*:
// they take no host path, never delete or overwrite, and write only
// through the archive store. idx and pages answer, live and never stored,
// whether an observed source page is in the search index and whether it
// exists in the wiki (so a research_evidence ref to it resolves).
func ArchiveTools(a *archive.Archive, gate *ArchiveGate, idx index.Index, pages PageChecker) []server.ServerTool {
	// mcp-go does not enforce additionalProperties at call time, so the
	// wrapper refuses any argument the tool does not declare.
	gated := func(tool mcp.Tool, h server.ToolHandlerFunc) server.ServerTool {
		tool = noExtra(tool)
		return server.ServerTool{Tool: tool, Handler: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			release, ok := gate.Enter()
			if !ok {
				return mcp.NewToolResultError(archiveDisabled), nil
			}
			defer release()
			for k := range req.GetArguments() {
				if _, declared := tool.InputSchema.Properties[k]; !declared {
					return mcp.NewToolResultError(fmt.Sprintf("unknown parameter: %s", k)), nil
				}
			}
			return h(ctx, req)
		}}
	}
	return []server.ServerTool{
		gated(archiveCaptureTextTool(), handleArchiveCaptureText(a)),
		gated(archiveCaptureReportTool(), handleArchiveCaptureReport(a)),
		gated(archiveCaptureSummaryTool(), handleArchiveCaptureSummary(a)),
		gated(archiveStatusTool(), handleArchiveStatus(a, idx, pages)),
		gated(archivePageLinksTool(), handleArchivePageLinks(a, pages)),
	}
}

const textName = "Base file name with extension .md, .markdown, or .txt (default capture.md). Never a path."

func archiveCaptureTextTool() mcp.Tool {
	return mcp.NewTool("archive_capture_text",
		mcp.WithDescription("Store the full text of a source original in the research archive. Records where it came from; it is not a fact check. completeness may be unknown (default) or incomplete; only an operator can record verified. Text over 1 MiB is rejected, never truncated. Returns capture_id."),
		mcp.WithString("text", mcp.Required(), mcp.Description("Complete source text as UTF-8")),
		mcp.WithString("name", mcp.Description(textName)),
		mcp.WithString("url", mcp.Description("Absolute http(s) URL the text came from")),
		mcp.WithString("title", mcp.Description("Source title")),
		mcp.WithString("supplier", mcp.Description("Who supplied the text")),
		mcp.WithString("completeness", mcp.Description("unknown (default) or incomplete")),
		mcp.WithString("key", mcp.Description("Optional idempotency key")),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
	)
}

func archiveCaptureReportTool() mcp.Tool {
	return mcp.NewTool("archive_capture_report",
		mcp.WithDescription("Store a finished research report and the sources it cites, in report order. A cite is an archive capture_id of a source or an http(s) URL; a URL cite does not claim the original was acquired."),
		mcp.WithString("text", mcp.Required(), mcp.Description("Report text as UTF-8")),
		mcp.WithString("name", mcp.Description(textName)),
		mcp.WithString("title", mcp.Description("Report title")),
		mcp.WithString("supplier", mcp.Description("Who produced the report")),
		mcp.WithArray("cites", mcp.WithStringItems(), mcp.Description("Cited capture IDs or URLs, in report order")),
		mcp.WithString("key", mcp.Description("Optional idempotency key")),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
	)
}

func archiveCaptureSummaryTool() mcp.Tool {
	return mcp.NewTool("archive_capture_summary",
		mcp.WithDescription("Store a finished summary of a source. A summary is never recorded as the original and never changes the summarized source's acquisition or completeness. Name the summarized source by summary_of (a source capture_id), url, or both."),
		mcp.WithString("text", mcp.Required(), mcp.Description("Summary text as UTF-8")),
		mcp.WithString("name", mcp.Description(textName)),
		mcp.WithString("summary_of", mcp.Description("Summarized source capture_id")),
		mcp.WithString("url", mcp.Description("Summarized http(s) URL")),
		mcp.WithString("title", mcp.Description("Summary title")),
		mcp.WithString("supplier", mcp.Description("Who wrote the summary")),
		mcp.WithString("key", mcp.Description("Optional idempotency key")),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
	)
}

func archiveStatusTool() mcp.Tool {
	return mcp.NewTool("archive_status",
		mcp.WithDescription("Show one archive capture: provenance, completeness, citations, summaries, recovery attempts, and ingest state. Ingest results are observations copied from the ingest host's ledger, labeled with their time; search visibility is a live index query."),
		mcp.WithString("capture_id", mcp.Required(), mcp.Description("Archive capture ID (cap_...)")),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	)
}

func archivePageLinksTool() mcp.Tool {
	return mcp.NewTool("archive_page_links",
		mcp.WithDescription("List the archive captures linked to one wiki source page. The archive's link records are the full list; a page's archive_capture_ids frontmatter is only a snapshot from its digest. Each link comes from an ingest-host receipt; page_exists is a live check, and a missing page is never reported as a usable evidence ref."),
		mcp.WithString("wiki_page", mcp.Required(), mcp.Description("Wiki-root-relative .md path, as in research_evidence.sources[].ref")),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	)
}

// textInput reads text and name. The name must be a bare base name with a
// text extension, so no host path can enter the archive.
func textInput(req mcp.CallToolRequest) (string, string, *mcp.CallToolResult) {
	text, err := req.RequireString("text")
	if err != nil {
		return "", "", mcp.NewToolResultError("missing required parameter: text")
	}
	if len(text) > ArchiveMaxTextBytes {
		return "", "", mcp.NewToolResultError(fmt.Sprintf("text is %d bytes; the limit is %d and text is never truncated", len(text), ArchiveMaxTextBytes))
	}
	name := req.GetString("name", "capture.md")
	ext := strings.ToLower(name[strings.LastIndex(name, ".")+1:])
	if strings.ContainsAny(name, `/\`) || name == "." || name == ".." || strings.HasPrefix(name, ".") || !strings.Contains(name, ".") ||
		(ext != "md" && ext != "markdown" && ext != "txt") {
		return "", "", mcp.NewToolResultError("name must be a base file name ending in .md, .markdown, or .txt")
	}
	return text, name, nil
}

// archiveError maps archive errors to client messages. Request errors keep
// their text, which is built from the request. Everything else gets a fixed
// message and is logged here: remote clients must not see SQLite errors or
// host paths.
func archiveError(err error) *mcp.CallToolResult {
	switch {
	case errors.Is(err, archive.ErrInvalid), errors.Is(err, archive.ErrNotFound), errors.Is(err, archive.ErrConflict),
		errors.Is(err, archive.ErrTooLarge), errors.Is(err, archive.ErrDeleted):
		return mcp.NewToolResultError(err.Error())
	case errors.Is(err, archive.ErrIntegrity):
		slog.Error("archive integrity error", "err", err)
		return mcp.NewToolResultError("archive integrity error; an operator must check the archive")
	default:
		slog.Error("archive tool failed", "err", err)
		return mcp.NewToolResultError(archiveInternalError)
	}
}

const archiveInternalError = "internal archive error; an operator can find details in the server log"

func handleArchiveCaptureText(a *archive.Archive) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		text, name, bad := textInput(req)
		if bad != nil {
			return bad, nil
		}
		completeness := req.GetString("completeness", "unknown")
		if completeness != "unknown" && completeness != "incomplete" {
			return mcp.NewToolResultError("completeness must be unknown or incomplete; only an operator can record verified"), nil
		}
		res, err := a.CaptureSource(ctx, archive.SourceRequest{
			Key: req.GetString("key", ""), Content: strings.NewReader(text), OriginalName: name,
			URL: req.GetString("url", ""), Supplier: req.GetString("supplier", ""), Title: req.GetString("title", ""),
			MediaType: "text/plain; charset=utf-8", Completeness: completeness,
		})
		if err != nil {
			return archiveError(err), nil
		}
		return mcp.NewToolResultJSON(res)
	}
}

func handleArchiveCaptureReport(a *archive.Archive) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		text, name, bad := textInput(req)
		if bad != nil {
			return bad, nil
		}
		res, err := a.ImportReport(ctx, archive.ReportRequest{
			Key: req.GetString("key", ""), Content: strings.NewReader(text), OriginalName: name,
			Supplier: req.GetString("supplier", ""), Title: req.GetString("title", ""), Cites: req.GetStringSlice("cites", nil),
		})
		if err != nil {
			return archiveError(err), nil
		}
		return mcp.NewToolResultJSON(res)
	}
}

func handleArchiveCaptureSummary(a *archive.Archive) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		text, name, bad := textInput(req)
		if bad != nil {
			return bad, nil
		}
		res, err := a.CaptureSummary(ctx, archive.SummaryRequest{
			Key: req.GetString("key", ""), Content: strings.NewReader(text), OriginalName: name,
			SummaryOf: req.GetString("summary_of", ""), URL: req.GetString("url", ""),
			Supplier: req.GetString("supplier", ""), Title: req.GetString("title", ""),
		})
		if err != nil {
			return archiveError(err), nil
		}
		return mcp.NewToolResultJSON(res)
	}
}

// archiveStatusView is what archive_status returns. It is an explicit
// projection, not the whole archive.Capture: the ingest view leaves out the
// ingest host's absolute source path and keeps only the ledger status, its
// observation time, and the wiki page.
type archiveStatusView struct {
	CaptureID         string               `json:"capture_id"`
	Kind              string               `json:"kind"`
	ObjectID          string               `json:"object_id"`
	Size              int64                `json:"size"`
	MediaType         string               `json:"media_type"`
	OriginalName      string               `json:"original_name"`
	URL               string               `json:"url"`
	Supplier          string               `json:"supplier"`
	Title             string               `json:"title"`
	Acquisition       string               `json:"acquisition"`
	Completeness      string               `json:"completeness"`
	CompletenessBasis string               `json:"completeness_basis"`
	RecordedAt        string               `json:"recorded_at"`
	SummaryOf         string               `json:"summary_of,omitempty"`
	SameObject        []string             `json:"same_object"`
	CitedBy           []string             `json:"cited_by"`
	Citations         []archive.Citation   `json:"citations"`
	Summaries         []string             `json:"summaries"`
	Attempts          []archive.Attempt    `json:"attempts"`
	RecoveredBy       []archive.Attempt    `json:"recovered_by"`
	Ingest            archiveIngestView    `json:"ingest"`
	Derived           []archive.Derivation `json:"derived"`
	// Download is an Acquire response record. Acquire refuses URLs with
	// credentials, queries, or fragments on every hop, so its URLs carry none.
	Download      *archive.AcquisitionRecord `json:"download,omitempty"`
	Extractions   []archive.Extraction       `json:"extractions"`
	SearchVisible *bool                      `json:"search_visible,omitempty"`
	SearchChecked string                     `json:"search_checked_at,omitempty"`
	// EvidenceRefPathExists says only that a file exists at the evidence ref
	// path now, so a research_evidence ref to it passes the EVID004 path
	// check. It does not show that the page describes this capture or
	// edition, nor anything about a claim's support. The capture-to-page link
	// itself comes only from the ingest host's ledger, seen through a receipt.
	EvidenceRefPathExists *bool `json:"evidence_ref_path_exists,omitempty"`
	// PageFrontmatterListsCapture says the page at the evidence ref lists
	// this capture ID and its object ID in the frontmatter keys ingest writes
	// at digest time (archive_capture_ids, archive_object_id). That list is
	// a snapshot of the capture map when the page was digested, not the full
	// list: a capture of the same bytes added later is in PageLinks, not on
	// the page. wiki_write can edit the keys, so this is what the page
	// claims, not an independent check.
	PageFrontmatterListsCapture *bool `json:"page_frontmatter_lists_capture,omitempty"`
	// PageLinks are the archive's records linking this capture to wiki
	// pages, the authority for that association.
	PageLinks []archivePageLinkView `json:"page_links"`
}

// archivePageLinkView is one archive page link plus a live check of the
// page. EvidenceRef is set only when the link is current and the page
// exists now, so a moved or deleted page is never offered as a ref.
type archivePageLinkView struct {
	CaptureID    string `json:"capture_id"`
	WikiPage     string `json:"wiki_page"`
	ObjectID     string `json:"object_id"`
	ContentHash  string `json:"content_hash"`
	BasisReceipt string `json:"basis_receipt"`
	DigestedAt   string `json:"digested_at,omitempty"`
	ObservedAt   string `json:"observed_at"`
	RecordedAt   string `json:"recorded_at"`
	Current      bool   `json:"current"`
	// PageExists is a live check; nil when no wiki is attached.
	PageExists  *bool  `json:"page_exists,omitempty"`
	EvidenceRef string `json:"evidence_ref,omitempty"`
}

func linkViews(links []archive.PageLink, pages PageChecker) ([]archivePageLinkView, error) {
	out := make([]archivePageLinkView, 0, len(links))
	exists := map[string]bool{}
	for _, l := range links {
		v := archivePageLinkView{
			CaptureID: l.CaptureID, WikiPage: l.WikiPage, ObjectID: l.ObjectID, ContentHash: l.ContentHash,
			BasisReceipt: l.BasisReceipt, DigestedAt: l.DigestedAt, ObservedAt: l.ObservedAt, RecordedAt: l.RecordedAt,
			Current: l.Current,
		}
		if pages != nil {
			ok, seen := exists[l.WikiPage]
			if !seen {
				var err error
				ok, err = pages.IsFile(l.WikiPage)
				if err != nil && !errors.Is(err, cverr.ErrNotFound) {
					return nil, err
				}
				exists[l.WikiPage] = ok
			}
			v.PageExists = &ok
			if ok && l.Current {
				v.EvidenceRef = l.WikiPage
			}
		}
		out = append(out, v)
	}
	return out, nil
}

func handleArchivePageLinks(a *archive.Archive, pages PageChecker) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		page, err := req.RequireString("wiki_page")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: wiki_page"), nil
		}
		links, err := a.PageLinksFor(ctx, page)
		if err != nil {
			return archiveError(err), nil
		}
		views, err := linkViews(links, pages)
		if err != nil {
			return archiveError(err), nil
		}
		return mcp.NewToolResultJSON(struct {
			WikiPage string                `json:"wiki_page"`
			Links    []archivePageLinkView `json:"links"`
		}{page, views})
	}
}

type archiveIngestView struct {
	State        string `json:"state"`
	Reason       string `json:"reason,omitempty"`
	Source       string `json:"source,omitempty"`
	ObservedAt   string `json:"observed_at,omitempty"`
	LedgerStatus string `json:"ledger_status,omitempty"`
	// WikiPage is the page path the latest receipt observed in the ledger;
	// nothing checks that it exists.
	WikiPage string `json:"wiki_page,omitempty"`
	// EvidenceRef is set only when that observation is success and a file
	// exists at the path now (the same rule as a page link's evidence_ref).
	// Otherwise it is omitted and evidence_ref_path_exists says why.
	EvidenceRef string   `json:"evidence_ref,omitempty"`
	RecoveredAs []string `json:"recovered_as,omitempty"`
}

func statusView(c archive.Capture) archiveStatusView {
	return archiveStatusView{
		CaptureID: c.CaptureID, Kind: c.Kind, ObjectID: c.ObjectID, Size: c.Size, MediaType: c.MediaType,
		OriginalName: c.OriginalName, URL: c.URL, Supplier: c.Supplier, Title: c.Title,
		Acquisition: c.Acquisition, Completeness: c.Completeness, CompletenessBasis: c.CompletenessBasis,
		RecordedAt: c.RecordedAt, SummaryOf: c.SummaryOf, SameObject: c.SameObject, CitedBy: c.CitedBy,
		Citations: c.Citations, Summaries: c.Summaries, Attempts: c.Attempts, RecoveredBy: c.RecoveredBy,
		Derived: c.Derived, Download: c.Download, Extractions: c.Extractions,
		Ingest: archiveIngestView{
			State: c.Ingest.State, Reason: c.Ingest.Reason, Source: c.Ingest.Source, ObservedAt: c.Ingest.ObservedAt,
			LedgerStatus: c.Ingest.LedgerStatus, WikiPage: c.Ingest.WikiPage,
			RecoveredAs: c.Ingest.RecoveredAs,
		},
	}
}

func handleArchiveStatus(a *archive.Archive, idx index.Index, pages PageChecker) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, err := req.RequireString("capture_id")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: capture_id"), nil
		}
		c, err := a.Status(ctx, id)
		if err != nil {
			return archiveError(err), nil
		}
		out := statusView(c)
		if out.PageLinks, err = linkViews(c.PageLinks, pages); err != nil {
			return archiveError(err), nil
		}
		if page := c.Ingest.WikiPage; page != "" && idx != nil {
			_, err := idx.GetMeta(page)
			if err != nil && !errors.Is(err, cverr.ErrNotFound) {
				return archiveError(err), nil
			}
			visible := err == nil
			out.SearchVisible = &visible
			out.SearchChecked = time.Now().UTC().Format(time.RFC3339)
		}
		if ref := c.Ingest.EvidenceRef; ref != "" && pages != nil {
			exists, err := pages.IsFile(ref)
			if err != nil && !errors.Is(err, cverr.ErrNotFound) {
				return archiveError(err), nil
			}
			out.EvidenceRefPathExists = &exists
			if exists {
				out.Ingest.EvidenceRef = ref
				records := false
				if data, err := pages.Read(ref); err == nil {
					if meta, _, err := terms.Parse(data); err == nil {
						records = archive.PageRecordsCapture(meta, c.CaptureID, c.ObjectID)
					}
				} else if !errors.Is(err, cverr.ErrNotFound) {
					return archiveError(err), nil
				}
				out.PageFrontmatterListsCapture = &records
			}
		}
		return mcp.NewToolResultJSON(out)
	}
}
