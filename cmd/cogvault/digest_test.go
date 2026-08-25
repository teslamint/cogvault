package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// seedWikiPage writes a minimal wiki page and indexes it via init.
func seedWikiPage(t *testing.T, wikiDir, relPath, title string) {
	t.Helper()
	abs := filepath.Join(wikiDir, relPath)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	page := "---\ntitle: " + title + "\ntype: source\nsource_path: x\ningested_at: " + time.Now().UTC().Format(time.RFC3339) + "\n---\n\n# " + title + "\n"
	if err := os.WriteFile(abs, []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDigestNoRecentSources(t *testing.T) {
	configPath, wikiDir, _ := testVault(t)
	seedWikiPage(t, wikiDir, "sources/old.md", "Old")
	if _, _, err := executeCommand("init", "--config", configPath); err != nil {
		t.Fatalf("init: %v", err)
	}

	// Nothing was ingested recently (indexed_at is now, but the page lives
	// under sources/ — indexed_at IS now, so backdate the index by writing
	// the page after a fake old init is not possible; instead use --days 0
	// with cutoff in the future? Simplest deterministic path: empty wiki.
	configPath2, _, _ := testVault(t)
	if _, _, err := executeCommand("init", "--config", configPath2); err != nil {
		t.Fatalf("init2: %v", err)
	}

	stdout, _, err := executeCommand("digest", "--config", configPath2)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	if !strings.Contains(stdout, "no recent sources to summarize") {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestDigestWritesWeeklyPage(t *testing.T) {
	configPath, wikiDir, _ := testVault(t)
	seedWikiPage(t, wikiDir, "sources/note.md", "Note")
	if _, _, err := executeCommand("init", "--config", configPath); err != nil {
		t.Fatalf("init: %v", err)
	}

	stdout, _, err := executeCommand("digest", "--config", configPath)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	today := time.Now().UTC().Format("2006-01-02")
	if !strings.Contains(stdout, "digests/weekly-"+today+".md") {
		t.Fatalf("stdout = %q, want weekly slug", stdout)
	}

	data, err := os.ReadFile(filepath.Join(wikiDir, "digests", "weekly-"+today+".md"))
	if err != nil {
		t.Fatalf("digest page missing: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "type: synthesis") {
		t.Fatalf("missing synthesis type:\n%s", content)
	}
	if !strings.Contains(content, "[[sources/note|Note]]") {
		t.Fatalf("missing wikilink to note:\n%s", content)
	}
}

func TestDigestOverwritesSameDay(t *testing.T) {
	configPath, wikiDir, _ := testVault(t)
	seedWikiPage(t, wikiDir, "sources/a.md", "A")
	if _, _, err := executeCommand("init", "--config", configPath); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, _, err := executeCommand("digest", "--config", configPath); err != nil {
		t.Fatalf("digest 1: %v", err)
	}
	if _, _, err := executeCommand("digest", "--config", configPath); err != nil {
		t.Fatalf("digest 2: %v", err)
	}
	// Exactly one file, and it lists the source exactly once.
	matches, _ := filepath.Glob(filepath.Join(wikiDir, "digests", "weekly-*.md"))
	if len(matches) != 1 {
		t.Fatalf("weekly files = %v, want 1", matches)
	}
	data, _ := os.ReadFile(matches[0])
	if got := strings.Count(string(data), "[[sources/a|A]]"); got != 1 {
		t.Fatalf("wikilink count = %d, want 1 (overwrite must not duplicate)", got)
	}
}

func TestSynthesizeCreatesConceptPages(t *testing.T) {
	configPath, wikiDir, _ := testVault(t)
	// Two pages linking the same concept + two sharing a tag.
	seedWikiPage(t, wikiDir, "sources/x.md", "X")
	seedWikiPage(t, wikiDir, "sources/y.md", "Y")
	pages := map[string]string{
		"sources/x.md": "---\ntitle: X\ntype: source\ntags: [golang]\n---\n\nX body\n\n[[shared]]\n",
		"sources/y.md": "---\ntitle: Y\ntype: source\ntags: [golang]\n---\n\nY body\n\n[[shared]]\n",
	}
	for rel, content := range pages {
		if err := os.WriteFile(filepath.Join(wikiDir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := executeCommand("init", "--config", configPath); err != nil {
		t.Fatalf("init: %v", err)
	}

	stdout, _, err := executeCommand("synthesize", "--config", configPath)
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	if !strings.Contains(stdout, "created 2 concept pages") {
		t.Fatalf("stdout = %q, want 2 created", stdout)
	}
	for _, slug := range []string{"concepts/shared.md", "concepts/tag-golang.md"} {
		if _, err := os.Stat(filepath.Join(wikiDir, slug)); err != nil {
			t.Fatalf("concept page %s missing: %v", slug, err)
		}
	}
}

func TestSynthesizeIdempotent(t *testing.T) {
	configPath, wikiDir, _ := testVault(t)
	seedWikiPage(t, wikiDir, "sources/x.md", "X")
	seedWikiPage(t, wikiDir, "sources/y.md", "Y")
	for rel, content := range map[string]string{
		"sources/x.md": "---\ntitle: X\ntype: source\n---\n\n[[dup]]\n",
		"sources/y.md": "---\ntitle: Y\ntype: source\n---\n\n[[dup]]\n",
	} {
		if err := os.WriteFile(filepath.Join(wikiDir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := executeCommand("init", "--config", configPath); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, _, err := executeCommand("synthesize", "--config", configPath); err != nil {
		t.Fatalf("synthesize 1: %v", err)
	}
	stdout, _, err := executeCommand("synthesize", "--config", configPath)
	if err != nil {
		t.Fatalf("synthesize 2: %v", err)
	}
	if !strings.Contains(stdout, "created 0 concept pages") {
		t.Fatalf("second run stdout = %q, want 0 created", stdout)
	}
}

func TestSynthesizeSingleReferenceSkipped(t *testing.T) {
	configPath, wikiDir, _ := testVault(t)
	seedWikiPage(t, wikiDir, "sources/solo.md", "Solo")
	if err := os.WriteFile(filepath.Join(wikiDir, "sources/solo.md"), []byte("---\ntitle: Solo\ntype: source\n---\n\n[[lonely]]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCommand("init", "--config", configPath); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, _, err := executeCommand("synthesize", "--config", configPath); err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wikiDir, "concepts", "lonely.md")); !os.IsNotExist(err) {
		t.Fatal("single-reference concept page must not be created")
	}
}
