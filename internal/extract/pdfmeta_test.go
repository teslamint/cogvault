package extract

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParsePDFMetadataKeepsOnlyDeclaredFieldsAndAnnotations(t *testing.T) {
	meta := parsePDFInfo("Title: A source\nCreator: Browser\nAuthor: \n")
	meta.Links = parsePDFLinks("Page  Type          URL\n" +
		"   1  Annotation    https://example.org/article\n" +
		"   2  Annotation    https://example.org/reference\n" +
		"   2  Annotation    https://example.org/reference\n" +
		"   2  Annotation    file:///private/notes\n" +
		"   3  Annotation    https://user:pass@example.org/private\n" +
		"   3  Annotation    https://example.org/has space\n")
	if meta.Title != "A source" || meta.Author != "" {
		t.Fatalf("unexpected PDF metadata: %+v", meta)
	}
	want := []string{"https://example.org/article", "https://example.org/reference"}
	if !reflect.DeepEqual(meta.Links, want) {
		t.Fatalf("links = %#v, want %#v", meta.Links, want)
	}
}

func TestPDFMetadataRejectsMalformedValuesAndCancellation(t *testing.T) {
	if got := parsePDFInfo("Title: bad\x00value\nAuthor: someone\x1belse\n"); got.Title != "" || got.Author != "" {
		t.Fatalf("control characters survived: %+v", got)
	}
	var rows strings.Builder
	for i := 0; i < 70; i++ {
		rows.WriteString("1 Annotation https://example.org/")
		rows.WriteString(strings.Repeat("x", i+1))
		rows.WriteByte('\n')
	}
	if got := parsePDFLinks(rows.String()); len(got) != 64 {
		t.Fatalf("links = %d, want bounded 64", len(got))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ReadPDFMetadata(ctx, "/nonexistent.pdf", Commands{}); err == nil {
		t.Fatal("cancelled metadata extraction succeeded")
	}
}

// A link-heavy PDF must not lose its declared title and author: the link list
// is cut at its bound instead of failing the whole read.
func TestReadPDFMetadataKeepsFieldsWhenLinkOutputExceedsTheCap(t *testing.T) {
	var links strings.Builder
	links.WriteString("Page  Type          URL\n")
	for i := 0; i < 3000; i++ {
		links.WriteString("   1  Annotation    https://example.org/")
		links.WriteString(strings.Repeat("x", 40))
		links.WriteString("/")
		links.WriteString(strings.Repeat("y", i%7+1))
		links.WriteString(string(rune('a' + i%26)))
		links.WriteString("\n")
	}
	if links.Len() <= 128<<10 {
		t.Fatalf("fixture too small: %d bytes", links.Len())
	}
	// A file, not envCmd: Linux refuses one env string over 128 KiB (E2BIG).
	fixture := filepath.Join(t.TempDir(), "links.txt")
	if err := os.WriteFile(fixture, []byte(links.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	cmds := Commands{CommandContext: func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if len(args) > 0 && args[0] == "-url" {
			return exec.CommandContext(ctx, "cat", fixture)
		}
		return envCmd("Title: Declared\nAuthor: Someone\n", name, args...)
	}}
	meta, err := ReadPDFMetadata(context.Background(), "f.pdf", cmds)
	if err != nil {
		t.Fatalf("ReadPDFMetadata: %v", err)
	}
	if meta.Title != "Declared" || meta.Author != "Someone" || len(meta.Links) == 0 || len(meta.Links) > 64 {
		t.Fatalf("metadata = title %q author %q links %d", meta.Title, meta.Author, len(meta.Links))
	}
}
