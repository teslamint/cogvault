package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/teslamint/cogvault/internal/config"
)

func TestWriteRejectsMarkdownOverwriteThatDropsUsageTerms(t *testing.T) {
	root := t.TempDir()
	fs := newTestStorage(t, root, &config.Config{})
	page := "pages/article.md"
	original := []byte("---\nusage_terms:\n  status: unverified\n  purpose: research\n  conditions:\n    - retain copyright notice\n  scope: article\n  evidence:\n    quote: Keep this notice.\n    reference: LICENSE\n---\nOriginal body.\n")
	mustWriteFile(t, filepath.Join(root, page), string(original))

	err := fs.Write(page, []byte("---\nusage_terms:\n  status: unverified\n  purpose: research\n---\nReplacement body.\n"))
	if err == nil {
		t.Fatal("Write() error = nil, want rejection for dropped usage-term evidence")
	}
	got, readErr := os.ReadFile(filepath.Join(root, page))
	if readErr != nil {
		t.Fatalf("ReadFile() error = %v", readErr)
	}
	if string(got) != string(original) {
		t.Fatalf("page changed after rejected overwrite\n got: %q\nwant: %q", got, original)
	}
}

func TestWriteAllowsCompleteUsageTermsReplacement(t *testing.T) {
	root := t.TempDir()
	fs := newTestStorage(t, root, &config.Config{})
	page := "pages/article.md"
	mustWriteFile(t, filepath.Join(root, page), "---\nusage_terms:\n  purpose: old purpose\n  conditions: old condition\n  scope: old scope\n  evidence: old evidence\n---\nOld body.\n")
	incoming := []byte("---\nusage_terms:\n  purpose: new purpose\n  conditions: new condition\n  scope: new scope\n  evidence: new evidence\n---\nNew body.\n")

	if err := fs.Write(page, incoming); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, page))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(got) != string(incoming) {
		t.Fatalf("page = %q, want unchanged incoming bytes %q", got, incoming)
	}
}
