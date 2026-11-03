package mcp_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	gomcp "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/teslamint/cogvault/internal/adapter/obsidian"
	"github.com/teslamint/cogvault/internal/archive"
	"github.com/teslamint/cogvault/internal/config"
	"github.com/teslamint/cogvault/internal/index"
	"github.com/teslamint/cogvault/internal/mcp"
	"github.com/teslamint/cogvault/internal/storage"
)

type archiveFixture struct {
	root    string // wiki storage root
	archive *archive.Archive
	aroot   string
	gate    *mcp.ArchiveGate
	srv     *server.MCPServer
}

func setupArchiveTools(t *testing.T, open bool) archiveFixture {
	t.Helper()
	root := t.TempDir()
	cfg := &config.Config{WikiDir: "_wiki", DBPath: ".cogvault.db", Exclude: []string{".obsidian", ".trash"}, ExcludeRead: []string{}, Adapter: "obsidian"}
	os.MkdirAll(filepath.Join(root, "_wiki"), 0o755)
	store := storage.NewFSStorage(root, cfg)
	idx, err := index.NewSQLiteIndex(root, filepath.Join(root, cfg.DBPath), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })
	aroot := filepath.Join(t.TempDir(), "archive")
	a, err := archive.Open(aroot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	gate := &mcp.ArchiveGate{}
	if open {
		gate.Open()
	}
	s := mcp.NewServer(root, cfg, store, idx, obsidian.New())
	s.AddTools(mcp.ArchiveTools(a, gate, idx, store)...)
	return archiveFixture{root: root, archive: a, aroot: aroot, gate: gate, srv: s}
}

func objectCount(t *testing.T, aroot string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(aroot, "objects", "sha256"))
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

func toolJSON(t *testing.T, r *gomcp.CallToolResult, v any) {
	t.Helper()
	if r.IsError {
		t.Fatalf("tool error: %s", extractText(t, r))
	}
	if err := json.Unmarshal([]byte(extractText(t, r)), v); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveToolsRefuseWhileGateIsClosed(t *testing.T) {
	f := setupArchiveTools(t, false)
	for name, args := range map[string]map[string]any{
		"archive_capture_text":    {"text": "x"},
		"archive_capture_report":  {"text": "x"},
		"archive_capture_summary": {"text": "x", "url": "https://example.org/x"},
		"archive_status":          {"capture_id": "cap_00000000000000000000000000000000"},
		"archive_page_links":      {"wiki_page": "sources/p.md"},
	} {
		r := callTool(t, f.srv, name, args)
		if !r.IsError || !strings.Contains(extractText(t, r), "disabled") {
			t.Fatalf("%s with a closed gate: %+v", name, r)
		}
	}
	if n := objectCount(t, f.aroot); n != 0 {
		t.Fatalf("closed gate stored %d objects", n)
	}
}

func TestArchiveToolsCaptureWithinBounds(t *testing.T) {
	f := setupArchiveTools(t, true)
	var res archive.Result
	toolJSON(t, callTool(t, f.srv, "archive_capture_text", map[string]any{"text": "full source text\n", "name": "paper.md", "url": "https://example.org/p", "title": "P"}), &res)
	if !strings.HasPrefix(res.CaptureID, "cap_") {
		t.Fatalf("capture = %+v", res)
	}
	var c archive.Capture
	toolJSON(t, callTool(t, f.srv, "archive_status", map[string]any{"capture_id": res.CaptureID}), &c)
	if c.Completeness != "unknown" || c.Ingest.State != "held" || c.OriginalName != "paper.md" {
		t.Fatalf("status = %+v", c)
	}
	var rep archive.Result
	toolJSON(t, callTool(t, f.srv, "archive_capture_report", map[string]any{"text": "report", "cites": []any{res.CaptureID, "https://example.org/q"}}), &rep)
	var sum archive.Result
	toolJSON(t, callTool(t, f.srv, "archive_capture_summary", map[string]any{"text": "summary", "summary_of": res.CaptureID}), &sum)
	toolJSON(t, callTool(t, f.srv, "archive_status", map[string]any{"capture_id": res.CaptureID}), &c)
	if len(c.CitedBy) != 1 || len(c.Summaries) != 1 || c.Completeness != "unknown" {
		t.Fatalf("relations = %+v", c)
	}

	before := objectCount(t, f.aroot)
	for _, args := range []map[string]any{
		{"text": "x", "name": "../escape.md"},
		{"text": "x", "name": "/etc/passwd.md"},
		{"text": "x", "name": "a.pdf"},
		{"text": "x", "name": ".hidden.md"},
		{"text": "x", "completeness": "verified"},
		{"text": "x", "url": "file:///etc/passwd"},
		{"text": strings.Repeat("x", mcp.ArchiveMaxTextBytes+1)},
		{"text": "x", "path": "/tmp/x"},
		{"text": "x", "name": 123},
		{"text": "x", "name": nil},
		{"text": "x", "url": []any{"https://example.org/"}},
	} {
		r := callTool(t, f.srv, "archive_capture_text", args)
		if !r.IsError {
			t.Fatalf("capture %v accepted", truncate(args))
		}
	}
	if after := objectCount(t, f.aroot); after != before {
		t.Fatalf("rejected captures stored %d objects", after-before)
	}
	r := callTool(t, f.srv, "archive_capture_report", map[string]any{"text": "r", "cites": []any{rep.CaptureID}})
	if !r.IsError {
		t.Fatal("a report citing a report was accepted")
	}
}

func truncate(args map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range args {
		if s, ok := v.(string); ok && len(s) > 40 {
			v = s[:40] + "..."
		}
		out[k] = v
	}
	return out
}

func TestArchiveToolListHasNoAdminOrDeleteTool(t *testing.T) {
	f := setupArchiveTools(t, true)
	for name := range f.srv.ListTools() {
		if strings.HasPrefix(name, "archive_") && !contains(mcp.ArchiveToolNames, name) {
			t.Fatalf("unexpected archive tool %s", name)
		}
		for _, bad := range []string{"reload", "delete", "purge", "quarantine", "restore", "enable", "admin"} {
			if strings.HasPrefix(name, "archive_") && strings.Contains(name, bad) {
				t.Fatalf("tool %s exposes an operator action", name)
			}
		}
	}
	for _, name := range mcp.ArchiveToolNames {
		tool := f.srv.GetTool(name)
		if tool == nil {
			t.Fatalf("%s not registered", name)
		}
		if props := tool.Tool.InputSchema.Properties; props["path"] != nil || props["file"] != nil {
			t.Fatalf("%s takes a host path", name)
		}
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// TestArchiveStaleHandlerIsStoppedByGate pins the real boundary: mcp-go
// copies a handler under its lock and runs it later, so a call looked up
// before DeleteTools still runs. The gate inside the handler must refuse it.
func TestArchiveStaleHandlerIsStoppedByGate(t *testing.T) {
	f := setupArchiveTools(t, true)
	stale := f.srv.GetTool("archive_capture_text")
	<-f.gate.Close()
	f.srv.DeleteTools(mcp.ArchiveToolNames...)
	r, err := stale.Handler(context.Background(), gomcp.CallToolRequest{Params: gomcp.CallToolParams{Name: "archive_capture_text", Arguments: map[string]any{"text": "late"}}})
	if err != nil || !r.IsError {
		t.Fatalf("stale handler result = %+v, %v", r, err)
	}
	if n := objectCount(t, f.aroot); n != 0 {
		t.Fatalf("stale handler stored %d objects", n)
	}
}

func TestArchiveGateDrainsAdmittedCalls(t *testing.T) {
	var g mcp.ArchiveGate
	if _, ok := g.Enter(); ok {
		t.Fatal("zero gate admitted a call")
	}
	g.Open()
	release, ok := g.Enter()
	if !ok {
		t.Fatal("open gate refused a call")
	}
	drained := g.Close()
	if _, ok := g.Enter(); ok {
		t.Fatal("closed gate admitted a new call")
	}
	select {
	case <-drained:
		t.Fatal("drained before the admitted call finished")
	case <-time.After(20 * time.Millisecond):
	}
	release()
	release() // a second release is a no-op
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("not drained after the admitted call finished")
	}
	if g.InFlight() != 0 {
		t.Fatalf("in flight = %d", g.InFlight())
	}
}

func TestArchiveGateConcurrentTogglesAndCalls(t *testing.T) {
	f := setupArchiveTools(t, true)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; ; j++ {
				select {
				case <-stop:
					return
				default:
				}
				h := f.srv.GetTool("archive_capture_text")
				if h == nil {
					continue
				}
				h.Handler(context.Background(), gomcp.CallToolRequest{Params: gomcp.CallToolParams{Arguments: map[string]any{"text": strings.Repeat("y", i+1) + string(rune('a'+j%26))}}})
			}
		}(i)
	}
	for range 50 {
		<-f.gate.Close()
		f.srv.DeleteTools(mcp.ArchiveToolNames...)
		if f.gate.InFlight() != 0 {
			t.Fatal("calls in flight after drain")
		}
		f.gate.Open()
		f.srv.AddTools(mcp.ArchiveTools(f.archive, f.gate, nil, nil)...)
	}
	close(stop)
	wg.Wait()
}

func TestArchiveStatusReportsLiveSearchVisibility(t *testing.T) {
	f := setupArchiveTools(t, true)
	ctx := context.Background()
	res, err := f.archive.CaptureSource(ctx, archive.SourceRequest{Content: strings.NewReader("verified text"), OriginalName: "v.md", Completeness: "verified", Basis: "checked"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.archive.Handoff(ctx, archive.DefaultHandoffTypes); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if _, err := archive.Pickup(f.aroot, dest, archive.DefaultHandoffTypes); err != nil {
		t.Fatal(err)
	}
	lookup := func(p, h string) (archive.LedgerEntry, bool, error) {
		return archive.LedgerEntry{Status: "success", WikiPage: "sources/v.md"}, true, nil
	}
	if _, err := archive.WriteReceipts(f.aroot, dest, lookup, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.archive.CollectReceipts(ctx); err != nil {
		t.Fatal(err)
	}
	var st struct {
		archive.Capture
		SearchVisible *bool `json:"search_visible"`
	}
	r := callTool(t, f.srv, "archive_status", map[string]any{"capture_id": res.CaptureID})
	raw := extractText(t, r)
	if strings.Contains(raw, dest) || strings.Contains(raw, "source_path") || strings.Contains(raw, f.aroot) {
		t.Fatalf("archive_status exposes a host path: %s", raw)
	}
	toolJSON(t, r, &st)
	if st.SearchVisible == nil || *st.SearchVisible || st.Ingest.EvidenceRef != "" || st.Ingest.WikiPage != "sources/v.md" || st.Ingest.LedgerStatus != "success" || st.Ingest.ObservedAt == "" {
		t.Fatalf("before the page exists: %+v visible=%v; evidence_ref must wait for the page", st.Ingest, st.SearchVisible)
	}
	type pageLinks struct {
		Links []struct {
			CaptureID   string `json:"capture_id"`
			PageExists  *bool  `json:"page_exists"`
			EvidenceRef string `json:"evidence_ref"`
			Current     bool   `json:"current"`
		} `json:"links"`
	}
	links := func() pageLinks {
		t.Helper()
		r := callTool(t, f.srv, "archive_page_links", map[string]any{"wiki_page": "sources/v.md"})
		if raw := extractText(t, r); strings.Contains(raw, dest) || strings.Contains(raw, f.aroot) {
			t.Fatalf("archive_page_links exposes a host path: %s", raw)
		}
		var out pageLinks
		toolJSON(t, r, &out)
		return out
	}
	if l := links().Links; len(l) != 1 || l[0].CaptureID != res.CaptureID || !l[0].Current || l[0].PageExists == nil || *l[0].PageExists || l[0].EvidenceRef != "" {
		t.Fatalf("links before the page exists = %+v", l)
	}
	callTool(t, f.srv, "wiki_write", map[string]any{"path": "sources/v.md", "content": "---\ntitle: V\n---\n# V\n"})
	toolJSON(t, callTool(t, f.srv, "archive_status", map[string]any{"capture_id": res.CaptureID}), &st)
	if st.SearchVisible == nil || !*st.SearchVisible || st.Ingest.EvidenceRef != "sources/v.md" {
		t.Fatalf("after the page is written: visible=%v ingest=%+v", st.SearchVisible, st.Ingest)
	}
	if l := links().Links; len(l) != 1 || l[0].PageExists == nil || !*l[0].PageExists || l[0].EvidenceRef != "sources/v.md" {
		t.Fatalf("links after the page is written = %+v", l)
	}
	if r := callTool(t, f.srv, "archive_page_links", map[string]any{"wiki_page": "../v.md"}); !r.IsError {
		t.Fatal("archive_page_links accepted a non-wiki path")
	}
}

func TestArchiveToolInternalErrorsAreGeneric(t *testing.T) {
	f := setupArchiveTools(t, true)
	f.archive.Close() // every registry call now fails inside database/sql
	for name, args := range map[string]map[string]any{
		"archive_capture_text": {"text": "x"},
		"archive_status":       {"capture_id": "cap_00000000000000000000000000000000"},
		"archive_page_links":   {"wiki_page": "sources/p.md"},
	} {
		r := callTool(t, f.srv, name, args)
		msg := extractText(t, r)
		if !r.IsError || strings.Contains(msg, "sql") || strings.Contains(msg, f.aroot) || !strings.Contains(msg, "internal archive error") {
			t.Fatalf("%s internal error message = %q", name, msg)
		}
	}
}
