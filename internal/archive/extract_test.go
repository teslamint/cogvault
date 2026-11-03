package archive

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestExtractHTMLTextDropsActiveAndHiddenContent(t *testing.T) {
	src := `<!DOCTYPE html><html><head><title>Doc &amp; Title</title>
<style>body{color:red}</style><script>if (a < b && "</div>") { fetch("http://evil/") }</script>
<link rel="stylesheet" href="http://x/s.css"></head>
<body onload="steal()"><h1>Heading</h1><p>First <b>para</b>&nbsp;text.<br>Next line</p>
<ul><li>one</li><li>two</li></ul><img src="http://tracker/p.gif" alt="ALT">
<iframe src="http://x/"><p>IFRAME-TEXT</p></iframe><noscript>NOSCRIPT-TEXT</noscript>
<SCRIPT type="module">import x from "y"; const s = "SCRIPT-TEXT";</SCRIPT>
<svg><text>SVG-TEXT</text></svg><table><tr><td>a</td><td>b</td></tr></table><p>unclosed
</body></html>`
	got, err := ExtractHTMLText([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# Doc & Title", "# Heading", "First para", "text.", "Next line", "- one", "- two", "a b", "unclosed"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, bad := range []string{"color:red", "fetch", "evil", "SCRIPT-TEXT", "IFRAME-TEXT", "NOSCRIPT-TEXT", "SVG-TEXT", "steal", "tracker", "ALT", "s.css"} {
		if strings.Contains(got, bad) {
			t.Errorf("output contains %q:\n%s", bad, got)
		}
	}
	if _, err := ExtractHTMLText([]byte("<p>caf\xe9</p>")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("latin-1 input: err = %v", err)
	}
}

func htmlCapture(t *testing.T, a *Archive, body string) Result {
	t.Helper()
	return mustCapture(t, a, SourceRequest{Content: strings.NewReader(body), OriginalName: "page.html", MediaType: "text/html"})
}

func TestExtractTextKeepsOriginalAndRecordsVersion(t *testing.T) {
	a, root := openTest(t)
	body := "<html><body><p>Hello <i>world</i></p></body></html>"
	res := htmlCapture(t, a, body)
	e, err := a.ExtractText(ctx, res.CaptureID)
	if err != nil {
		t.Fatal(err)
	}
	if e.Extractor != HTMLTextExtractor || e.Status != "succeeded" || !strings.Contains(e.Detail, "no scripts run") {
		t.Fatalf("extraction = %+v", e)
	}
	out, _ := os.ReadFile(filepath.Join(root, "objects", "sha256", hexOf(e.OutputObjectID)))
	if string(out) != "Hello world\n" {
		t.Fatalf("output = %q", out)
	}
	if orig, _ := os.ReadFile(filepath.Join(root, "objects", "sha256", hexOf(res.ObjectID))); string(orig) != body {
		t.Fatal("original changed")
	}
	if again, err := a.ExtractText(ctx, res.CaptureID); err != nil || again.OutputObjectID != e.OutputObjectID {
		t.Fatalf("repeat = %+v, %v", again, err)
	}
	if c := mustStatus(t, a, res.CaptureID); len(c.Extractions) != 1 || c.Ingest.State != "held" {
		t.Fatalf("status = %+v", c)
	}

	bad := mustCapture(t, a, SourceRequest{Content: strings.NewReader("<p>caf\xe9</p>"), OriginalName: "l.html", MediaType: "text/html"})
	if e, err := a.ExtractText(ctx, bad.CaptureID); err == nil || e.Status != "failed" {
		t.Fatalf("non-UTF-8 = %+v, %v", e, err)
	}
	if c := mustStatus(t, a, bad.CaptureID); len(c.Extractions) != 1 || c.Extractions[0].Status != "failed" || c.Extractions[0].OutputObjectID != "" {
		t.Fatalf("failed extraction record = %+v", c.Extractions)
	}
	notHTML := mustCapture(t, a, source("# plain markdown\n", ""))
	if _, err := a.ExtractText(ctx, notHTML.CaptureID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("markdown: %v", err)
	}
	pdf := mustCapture(t, a, SourceRequest{Content: strings.NewReader("%PDF-1.4\x00\x01binary"), OriginalName: "x.html", MediaType: "text/html"})
	if _, err := a.ExtractText(ctx, pdf.CaptureID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("binary declared as HTML: %v", err)
	}
}

func TestExtractionFollowsSourceDeletion(t *testing.T) {
	a, root := openTest(t)
	res := htmlCapture(t, a, "<p>to delete</p>")
	quarantine(t, a, res.ObjectID)
	if _, err := a.ExtractText(ctx, res.CaptureID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("extract of a quarantined source: %v", err)
	}
	if n, _ := a.ids(ctx, `SELECT source_object_id FROM extractions`); len(n) != 0 {
		t.Fatal("extraction recorded for a quarantined source")
	}
	if err := a.Unquarantine(ctx, res.ObjectID); err != nil {
		t.Fatal(err)
	}
	e, err := a.ExtractText(ctx, res.CaptureID)
	if err != nil {
		t.Fatal(err)
	}
	quarantine(t, a, res.ObjectID)
	if _, err := os.Stat(filepath.Join(root, "objects", "sha256", hexOf(e.OutputObjectID))); !os.IsNotExist(err) {
		t.Fatal("extracted text stayed active after its source was quarantined")
	}
}

// TestExtractionAndQuarantineRaceLeavesNoActiveText runs both from two
// handles many times; afterwards a quarantined source never has active
// extracted text.
func TestExtractionAndQuarantineRaceLeavesNoActiveText(t *testing.T) {
	a, root := openTest(t)
	b, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	for i := range 20 {
		res := htmlCapture(t, a, "<p>race "+strings.Repeat("x", i)+"</p>")
		im, _ := a.ImpactOf(ctx, res.ObjectID)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _, _ = b.ExtractText(ctx, res.CaptureID) }()
		go func() { defer wg.Done(); _ = a.Quarantine(ctx, res.ObjectID, im.Digest) }()
		wg.Wait()
		state := ""
		a.db.QueryRow(`SELECT state FROM objects WHERE object_id = ?`, res.ObjectID).Scan(&state)
		outs, _ := a.ids(ctx, `SELECT output_object_id FROM extractions WHERE source_object_id = ? AND output_object_id IS NOT NULL`, res.ObjectID)
		for _, o := range outs {
			var os string
			a.db.QueryRow(`SELECT state FROM objects WHERE object_id = ?`, o).Scan(&os)
			if state != "active" && os == "active" {
				t.Fatalf("round %d: source %s, extracted text active", i, state)
			}
		}
	}
}
