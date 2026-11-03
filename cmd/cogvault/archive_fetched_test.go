package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/teslamint/cogvault/internal/archive"
)

// TestSplitFetchFileRoundTripsRealFetchOutput feeds the exact bytes fetch
// writes (formatFetchedContent) and checks that the body comes back byte
// for byte, including bodies that look like frontmatter or carry their own
// usage_terms.
func TestSplitFetchFileRoundTripsRealFetchOutput(t *testing.T) {
	for name, body := range map[string]string{
		"html":                 "<!doctype html><html><body>x</body></html>\n",
		"markdown":             "# Title\n\nText with --- inside.\n",
		"own frontmatter":      "---\ntitle: inner\n---\nbody\n",
		"supplied usage terms": "---\nusage_terms:\n  status: allowed\n  scope: [quote]\n---\nterms body\n",
		"no trailing newline":  "plain",
	} {
		t.Run(name, func(t *testing.T) {
			file, err := formatFetchedContent("https://example.org/a?b=1", []byte(body))
			if err != nil {
				t.Fatal(err)
			}
			u, at, got, err := splitFetchFile([]byte(file))
			if err != nil {
				t.Fatalf("split: %v\n%s", err, file)
			}
			if u != "https://example.org/a?b=1" || at == "" || string(got) != body {
				t.Fatalf("split = %q %q %q", u, at, got)
			}
		})
	}
}

func TestSplitFetchFileRefusesOtherFiles(t *testing.T) {
	good, err := formatFetchedContent("https://example.org/a", []byte("body\n"))
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"no frontmatter":   "body only\n",
		"extra key":        strings.Replace(good, "source_url:", "title: x\nsource_url:", 1),
		"missing url":      "---\nfetched_at: \"2026-01-01\"\n---\nbody\n",
		"not re-encodable": strings.Replace(good, "source_url: ", "source_url:   ", 1),
		"number date":      "---\nfetched_at: 5\nsource_url: https://e.org\n---\nb\n",
	} {
		if _, _, _, err := splitFetchFile([]byte(data)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestArchiveCLIImportFetchedAndReassess(t *testing.T) {
	root := filepath.Join(t.TempDir(), "archive")
	file, err := formatFetchedContent("https://example.org/page", []byte("<html>page</html>\n"))
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "web-page.md")
	os.WriteFile(p, []byte(file), 0o644)
	out, _, err := executeCommand("archive", "--root", root, "import-fetched", "--file", p)
	if err != nil || !strings.Contains(out, "recorded as derived") {
		t.Fatalf("import-fetched: %q %v", out, err)
	}
	capID := strings.Fields(out)[1]
	if after, _ := os.ReadFile(p); string(after) != file {
		t.Fatal("import-fetched changed the fetch file")
	}
	out, _, _ = executeCommand("archive", "--root", root, "status", capID, "--json")
	var c archive.Capture
	if err := json.Unmarshal([]byte(out), &c); err != nil {
		t.Fatal(err)
	}
	if c.OriginalName != "web-page.response" || len(c.Derived) != 1 || c.Derived[0].Name != "web-page.md" {
		t.Fatalf("status = %+v", c)
	}

	bad := filepath.Join(t.TempDir(), "edited.md")
	os.WriteFile(bad, []byte(strings.Replace(file, "source_url:", "note: edited\nsource_url:", 1)), 0o644)
	if _, _, err := executeCommand("archive", "--root", root, "import-fetched", "--file", bad); err == nil {
		t.Fatal("edited fetch file accepted")
	}

	src := filepath.Join(t.TempDir(), "doc.md")
	os.WriteFile(src, []byte("complete doc\n"), 0o644)
	out, _, _ = executeCommand("archive", "--root", root, "capture", "--file", src)
	held := strings.Fields(out)[1]
	out, _, err = executeCommand("archive", "--root", root, "reassess", held, "--basis", "declared length matches")
	if err != nil || !strings.Contains(out, "your stated basis") {
		t.Fatalf("reassess: %q %v", out, err)
	}
	out, _, _ = executeCommand("archive", "--root", root, "status", held)
	if !strings.Contains(out, "recovered as") {
		t.Fatalf("held status:\n%s", out)
	}
}
