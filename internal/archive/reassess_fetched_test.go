package archive

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReassessAddsLinkedVerifiedCaptureAndKeepsProvenance(t *testing.T) {
	a, _, _ := handoffFixture(t)
	held := mustCapture(t, a, SourceRequest{Content: strings.NewReader("whole document\n"), OriginalName: "doc.md", URL: "https://example.org/doc", Title: "Doc", Supplier: "research agent"})
	before := mustStatus(t, a, held.CaptureID)

	res, err := a.Reassess(ctx, held.CaptureID, "page count 12 matches the publisher's table of contents")
	if err != nil {
		t.Fatal(err)
	}
	if res.CaptureID == held.CaptureID || res.ObjectID != held.ObjectID || res.Existing {
		t.Fatalf("reassess result = %+v", res)
	}
	v := mustStatus(t, a, res.CaptureID)
	if v.Completeness != "verified" || v.Supplier != "research agent" || v.URL != before.URL || v.Title != before.Title || v.OriginalName != before.OriginalName {
		t.Fatalf("reassessed capture lost provenance: %+v", v)
	}
	after := mustStatus(t, a, held.CaptureID)
	if after.Completeness != "unknown" || after.Supplier != before.Supplier {
		t.Fatalf("held capture changed: %+v", after)
	}
	if len(after.Attempts) != 1 || !strings.Contains(after.Attempts[0].Tried, "not an independent check") {
		t.Fatalf("attempts = %+v", after.Attempts)
	}
	if after.Ingest.State != "held" || len(after.Ingest.RecoveredAs) != 1 || after.Ingest.RecoveredAs[0] != res.CaptureID {
		t.Fatalf("held view = %+v", after.Ingest)
	}
	out, err := a.Handoff(ctx, DefaultHandoffTypes)
	if err != nil {
		t.Fatal(err)
	}
	got := statuses(out)
	if got[res.CaptureID].Status != "written" || got[held.CaptureID].Status != "held" {
		t.Fatalf("handoff after reassess = %+v", out)
	}

	again, err := a.Reassess(ctx, held.CaptureID, "page count 12 matches the publisher's table of contents")
	if err != nil || again.CaptureID != res.CaptureID || !again.Existing {
		t.Fatalf("repeat = %+v, %v", again, err)
	}
	if n := len(mustStatus(t, a, held.CaptureID).Attempts); n != 1 {
		t.Fatalf("repeat added attempts: %d", n)
	}
}

// TestReassessLinksACaptureStoredByAnInterruptedRun: an earlier run stored
// the verified capture and stopped before linking. Running the same
// reassessment again links it exactly once.
func TestReassessLinksACaptureStoredByAnInterruptedRun(t *testing.T) {
	a, _ := openTest(t)
	held := mustCapture(t, a, SourceRequest{Content: strings.NewReader("bytes\n"), OriginalName: "b.md", Supplier: "s"})
	basis := "declared length 6 matches"
	orphan := mustCapture(t, a, SourceRequest{Content: strings.NewReader("bytes\n"), OriginalName: "b.md", Supplier: "s", MediaType: mustStatus(t, a, held.CaptureID).MediaType, Completeness: "verified", Basis: basis})
	if n := len(mustStatus(t, a, held.CaptureID).Attempts); n != 0 {
		t.Fatalf("fixture has %d attempts", n)
	}
	res, err := a.Reassess(ctx, held.CaptureID, basis)
	if err != nil || res.CaptureID != orphan.CaptureID {
		t.Fatalf("reassess = %+v, %v; want the stored capture %s", res, err, orphan.CaptureID)
	}
	if _, err := a.Reassess(ctx, held.CaptureID, basis); err != nil {
		t.Fatal(err)
	}
	if n := len(mustStatus(t, a, held.CaptureID).Attempts); n != 1 {
		t.Fatalf("attempts = %d, want 1", n)
	}
}

func TestReassessRefusesIncompleteVerifiedOrNonSource(t *testing.T) {
	a, _ := openTest(t)
	partial := mustCapture(t, a, SourceRequest{Content: strings.NewReader("half"), OriginalName: "p.md", Completeness: "incomplete"})
	done := mustCapture(t, a, verified("done\n", "d.md", ""))
	rep, err := a.ImportReport(ctx, ReportRequest{Content: strings.NewReader("r"), OriginalName: "r.md"})
	if err != nil {
		t.Fatal(err)
	}
	unknown := mustCapture(t, a, source("u\n", ""))
	for name, call := range map[string]func() error{
		"partial":  func() error { _, err := a.Reassess(ctx, partial.CaptureID, "b"); return err },
		"verified": func() error { _, err := a.Reassess(ctx, done.CaptureID, "b"); return err },
		"report":   func() error { _, err := a.Reassess(ctx, rep.CaptureID, "b"); return err },
		"no basis": func() error { _, err := a.Reassess(ctx, unknown.CaptureID, "  "); return err },
		"missing":  func() error { _, err := a.Reassess(ctx, "cap_"+strings.Repeat("0", 32), "b"); return err },
	} {
		if err := call(); err == nil {
			t.Errorf("%s: reassess accepted", name)
		}
	}
	if list, _ := a.List(ctx); len(list) != 4 {
		t.Fatalf("refused reassessments stored captures: %d", len(list))
	}
}

const fetchFile = "---\nfetched_at: \"2026-01-02\"\nsource_url: https://example.org/page\nusage_terms:\n    status: unverified\n---\n<html><body>page</body></html>\n"

func fetched(t *testing.T, a *Archive) Result {
	t.Helper()
	body := "<html><body>page</body></html>\n"
	res, err := a.ImportFetched(ctx, FetchedRequest{Body: []byte(body), URL: "https://example.org/page", FetchedAt: "2026-01-02", DerivedName: "web-page.md", Derived: []byte(fetchFile)})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestImportFetchedKeepsBodyAndDerivedFileSeparate(t *testing.T) {
	a, root, _ := handoffFixture(t)
	res := fetched(t, a)
	c := mustStatus(t, a, res.CaptureID)
	if c.OriginalName != "web-page.response" || c.Completeness != "unknown" || c.URL != "https://example.org/page" || !strings.Contains(c.Supplier, "as written in the file") {
		t.Fatalf("body capture = %+v", c)
	}
	if body, _ := os.ReadFile(filepath.Join(root, "objects", "sha256", hexOf(res.ObjectID))); string(body) != "<html><body>page</body></html>\n" {
		t.Fatalf("stored body = %q", body)
	}
	if len(c.Derived) != 1 || c.Derived[0].Name != "web-page.md" || c.Derived[0].Kind != "cogvault-fetch-md" || c.Derived[0].Size != int64(len(fetchFile)) {
		t.Fatalf("derived = %+v", c.Derived)
	}
	if stored, _ := os.ReadFile(filepath.Join(root, "objects", "sha256", c.Derived[0].SHA256)); string(stored) != fetchFile {
		t.Fatal("derived file bytes not kept as an object")
	}

	// The body's format is unknown (fetch keeps HTML as .md), so even a
	// reassessed copy is held, never handed off as Markdown.
	re, err := a.Reassess(ctx, res.CaptureID, "operator checked the page")
	if err != nil {
		t.Fatal(err)
	}
	out, _ := a.Handoff(ctx, HandoffExtensions)
	for _, r := range out {
		if r.Status != "held" {
			t.Fatalf("fetched body handed off: %+v", r)
		}
	}
	_ = re

	again := fetched(t, a)
	if !again.Existing || len(mustStatus(t, a, res.CaptureID).Derived) != 1 {
		t.Fatal("repeat import was not idempotent")
	}
	for _, bad := range []FetchedRequest{
		{Body: []byte("x"), URL: "https://e.org", FetchedAt: "d", DerivedName: "f.md", Derived: []byte("not ending with the body")},
		{Body: []byte("x"), URL: "https://e.org", FetchedAt: "d", DerivedName: "../f.md", Derived: []byte("x")},
		{Body: []byte("x"), URL: "", FetchedAt: "d", DerivedName: "f.md", Derived: []byte("x")},
	} {
		if _, err := a.ImportFetched(ctx, bad); err == nil {
			t.Fatalf("import %+v accepted", bad)
		}
	}
}

func TestDeletingFetchedBodyAlsoRemovesItsDerivedFile(t *testing.T) {
	a, root, _ := handoffFixture(t)
	clk := withClock(a)
	res := fetched(t, a)
	derived := mustStatus(t, a, res.CaptureID).Derived[0].ObjectID
	im, err := a.ImpactOf(ctx, res.ObjectID)
	if err != nil || len(im.DerivedObjects) != 1 || im.DerivedObjects[0] != derived {
		t.Fatalf("impact = %+v, %v", im, err)
	}
	bdir := filepath.Join(t.TempDir(), "b")
	if _, err := a.Backup(ctx, bdir); err != nil {
		t.Fatal(err)
	}
	if err := a.Quarantine(ctx, res.ObjectID, im.Digest); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{res.ObjectID, derived} {
		if _, err := os.Stat(filepath.Join(root, "objects", "sha256", hexOf(id))); !os.IsNotExist(err) {
			t.Fatalf("%s still active after quarantine", id)
		}
	}
	if _, err := a.ImportFetched(ctx, FetchedRequest{Body: []byte("<html><body>page</body></html>\n"), URL: "https://example.org/page", FetchedAt: "2026-01-03", DerivedName: "web-page.md", Derived: []byte(fetchFile)}); !errors.Is(err, ErrDeleted) {
		t.Fatalf("re-import of quarantined bytes: err = %v", err)
	}
	clk.t = clk.t.Add(QuarantineMinimum)
	if err := a.Purge(ctx, res.ObjectID, res.ObjectID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "quarantine", hexOf(derived))); !os.IsNotExist(err) {
		t.Fatal("derived file survived the purge")
	}
	rep, err := RestoreTest(ctx, bdir, t.TempDir(), []string{filepath.Join(root, "deletions", deletionLogName)}, time.Now())
	if err != nil || !rep.Passed || len(rep.Resurrected) != 0 {
		t.Fatalf("restore test = %+v, %v", rep, err)
	}
}

func TestImportFetchedRefusesBeforeStoringAndRecoversAPartialImport(t *testing.T) {
	a, _ := openTest(t)
	body := []byte("tail body\n")
	huge := append(make([]byte, MaxObjectBytes), body...)
	if _, err := a.ImportFetched(ctx, FetchedRequest{Body: body, URL: "https://e.org/x", FetchedAt: "d", DerivedName: "x.md", Derived: huge}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversize derived file: err = %v", err)
	}
	if list, _ := a.List(ctx); len(list) != 0 {
		t.Fatalf("a refused import stored the body: %+v", list)
	}

	// An earlier run stored the body capture and failed before recording
	// the derivation; the same import repeated completes it once.
	derived := []byte("---\nfetched_at: d\nsource_url: https://e.org/x\n---\n" + string(body))
	partial, err := a.captureSource(ctx, SourceRequest{Content: strings.NewReader(string(body)), OriginalName: "x.response", URL: "https://e.org/x", Supplier: FetchedSupplier}, "d")
	if err != nil {
		t.Fatal(err)
	}
	req := FetchedRequest{Body: body, URL: "https://e.org/x", FetchedAt: "d", DerivedName: "x.md", Derived: derived}
	res, err := a.ImportFetched(ctx, req)
	if err != nil || !res.Existing || res.CaptureID != partial.CaptureID {
		t.Fatalf("repeat after a partial import = %+v, %v", res, err)
	}
	if _, err := a.ImportFetched(ctx, req); err != nil {
		t.Fatal(err)
	}
	if d := mustStatus(t, a, res.CaptureID).Derived; len(d) != 1 {
		t.Fatalf("derivations = %+v, want exactly one", d)
	}
}
