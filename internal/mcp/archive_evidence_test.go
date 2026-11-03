package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/teslamint/cogvault/internal/adapter/markdown"
	"github.com/teslamint/cogvault/internal/archive"
	"github.com/teslamint/cogvault/internal/config"
	"github.com/teslamint/cogvault/internal/index"
	"github.com/teslamint/cogvault/internal/storage"
)

// TestArchiveCaptureToSourcePageToEvidenceRef walks the chain with the
// existing contracts only: the ingest host's ledger (through a receipt)
// names the source page; archive_status offers it as the evidence ref and
// reports whether a file exists at that path; a research_evidence ref to it
// gets EVID004 until the page exists. Path existence is all that is
// checked: nothing here shows the page describes this capture.
func TestArchiveCaptureToSourcePageToEvidenceRef(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	wiki := filepath.Join(root, "wiki")
	os.MkdirAll(wiki, 0o755)
	cfg := &config.Config{WikiDir: wiki, DBPath: filepath.Join(root, "index.db"), Adapter: "markdown", Exclude: []string{}, ExcludeRead: []string{}}
	store := storage.NewFSStorage(wiki, cfg)
	idx, err := index.NewSQLiteIndex(wiki, cfg.DBPath, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	aroot := filepath.Join(root, "archive")
	a, err := archive.Open(aroot)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	res, err := a.CaptureSource(ctx, archive.SourceRequest{Content: strings.NewReader("paper text\n"), OriginalName: "paper.md", Completeness: "verified", Basis: "checked"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Handoff(ctx, archive.DefaultHandoffTypes); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "src")
	os.MkdirAll(dest, 0o755)
	if _, err := archive.Pickup(aroot, dest, archive.DefaultHandoffTypes); err != nil {
		t.Fatal(err)
	}
	ledger := func(string, string) (archive.LedgerEntry, bool, error) {
		return archive.LedgerEntry{Status: "success", WikiPage: "sources/paper.md"}, true, nil
	}
	if _, err := archive.WriteReceipts(aroot, dest, ledger, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.CollectReceipts(ctx); err != nil {
		t.Fatal(err)
	}

	gate := &ArchiveGate{}
	gate.Open()
	var status func() archiveStatusView
	for _, tool := range ArchiveTools(a, gate, idx, store) {
		if tool.Tool.Name == "archive_status" {
			h := tool.Handler
			status = func() archiveStatusView {
				r, err := h(ctx, makeReq(map[string]any{"capture_id": res.CaptureID}))
				if err != nil {
					t.Fatal(err)
				}
				var v archiveStatusView
				if err := json.Unmarshal(callToolJSON(t, r), &v); err != nil {
					t.Fatal(err)
				}
				return v
			}
		}
	}
	write := handleWikiWrite(wiki, cfg, store, idx, markdown.New())
	cite := func() []string {
		content := "---\ntitle: Note\nresearch_evidence:\n  version: 1\n  claims:\n    - id: claim-1\n      text: A claim.\n      kind: empirical\n      sources:\n        - ref: " + status().Ingest.WikiPage + "\n      checks: []\n      gaps: []\n---\nBody.\n"
		r, err := write(ctx, makeReq(map[string]any{"path": "notes/claim.md", "content": content}))
		if err != nil || r.IsError {
			t.Fatalf("wiki_write: %v %+v", err, r)
		}
		var out struct {
			Warnings []string `json:"warnings"`
		}
		json.Unmarshal(callToolJSON(t, r), &out)
		return out.Warnings
	}

	linkOK := func(v archiveStatusView, exists bool) {
		t.Helper()
		if len(v.PageLinks) != 1 {
			t.Fatalf("page links = %+v", v.PageLinks)
		}
		l := v.PageLinks[0]
		want := ""
		if exists {
			want = "sources/paper.md"
		}
		if l.WikiPage != "sources/paper.md" || !l.Current || l.PageExists == nil || *l.PageExists != exists || l.EvidenceRef != want {
			t.Fatalf("link with page exists=%v: %+v", exists, l)
		}
		// The ingest view's evidence_ref follows the same rule.
		if v.Ingest.EvidenceRef != want || v.Ingest.WikiPage != "sources/paper.md" {
			t.Fatalf("ingest view with page exists=%v: %+v", exists, v.Ingest)
		}
	}
	v := status()
	if v.Ingest.EvidenceRef != "" || v.Ingest.WikiPage != "sources/paper.md" || v.EvidenceRefPathExists == nil || *v.EvidenceRefPathExists {
		t.Fatalf("before the page exists: ingest %+v exists %v", v.Ingest, v.EvidenceRefPathExists)
	}
	linkOK(v, false)
	if !hasWarningPrefix(cite(), "[EVID004] claim-1:") {
		t.Fatal("a ref to the missing source page did not warn EVID004")
	}
	if r, err := write(ctx, makeReq(map[string]any{"path": "sources/paper.md", "content": "# Paper\n"})); err != nil || r.IsError {
		t.Fatalf("write source page: %v %+v", err, r)
	}
	if v := status(); v.EvidenceRefPathExists == nil || !*v.EvidenceRefPathExists || v.PageFrontmatterListsCapture == nil || *v.PageFrontmatterListsCapture {
		t.Fatalf("a page without archive keys: exists %v, lists %v", v.EvidenceRefPathExists, v.PageFrontmatterListsCapture)
	}
	// The link does not depend on the frontmatter: a page without archive
	// keys is still linked.
	linkOK(status(), true)
	if hasWarningPrefix(cite(), "[EVID004]") {
		t.Fatal("EVID004 after the source page exists")
	}
	v = status()
	page := func(capture, object string) {
		content := "---\ntitle: Paper\n" + archive.PageCaptureIDsKey + ":\n  - " + capture + "\n" + archive.PageObjectIDKey + ": " + object + "\n---\n# Paper\n"
		if r, err := write(ctx, makeReq(map[string]any{"path": "sources/paper.md", "content": content})); err != nil || r.IsError {
			t.Fatalf("write source page: %v %+v", err, r)
		}
	}
	for _, tc := range []struct {
		capture, object string
		want            bool
	}{
		{v.CaptureID, v.ObjectID, true},
		{"cap_" + strings.Repeat("0", 32), v.ObjectID, false},
		{v.CaptureID, "sha256:" + strings.Repeat("0", 64), false},
	} {
		page(tc.capture, tc.object)
		got := status()
		if got.PageFrontmatterListsCapture == nil || *got.PageFrontmatterListsCapture != tc.want {
			t.Fatalf("page lists %s/%s: %v, want %v", tc.capture, tc.object, got.PageFrontmatterListsCapture, tc.want)
		}
		linkOK(got, true)
	}

	// A deleted (or moved) page keeps the link record but is never offered
	// as an evidence ref.
	if err := os.Remove(filepath.Join(wiki, "sources", "paper.md")); err != nil {
		t.Fatal(err)
	}
	linkOK(status(), false)
}
