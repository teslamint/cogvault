package markdown

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"

	cverr "github.com/teslamint/cogvault/internal/errors"
)

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanBasic(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "notes", "a.md"), "# A")
	mustWriteFile(t, filepath.Join(root, "readme.txt"), "skip")

	a := New()
	var paths []string
	err := a.Scan(root, nil, func(path string) error {
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(paths) != 1 || paths[0] != filepath.Join("notes", "a.md") {
		t.Fatalf("Scan() got %v, want [notes/a.md]", paths)
	}
}

func TestScanExclude(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, ".hidden", "a.md"), "")
	mustWriteFile(t, filepath.Join(root, "notes", "b.md"), "")

	a := New()
	var paths []string
	err := a.Scan(root, []string{".hidden"}, func(path string) error {
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(paths) != 1 || paths[0] != filepath.Join("notes", "b.md") {
		t.Fatalf("Scan() got %v, want [notes/b.md]", paths)
	}
}

func TestScanExcludeFile(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "good.md"), "")
	mustWriteFile(t, filepath.Join(root, "private", "secret.md"), "")

	a := New()
	var paths []string
	err := a.Scan(root, []string{"private"}, func(path string) error {
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(paths) != 1 || paths[0] != "good.md" {
		t.Fatalf("Scan() got %v, want [good.md]", paths)
	}
}

func TestScanRootNotDirectory(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file.txt")
	mustWriteFile(t, file, "content")

	a := New()
	err := a.Scan(file, nil, func(path string) error { return nil })
	if !errors.Is(err, cverr.ErrNotFound) {
		t.Fatalf("Scan(file) error = %v, want ErrNotFound", err)
	}
}

func TestScanMultipleFiles(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "a.md"), "")
	mustWriteFile(t, filepath.Join(root, "sub", "b.md"), "")

	a := New()
	var paths []string
	err := a.Scan(root, nil, func(path string) error {
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	sort.Strings(paths)
	if len(paths) != 2 {
		t.Fatalf("Scan() got %d paths, want 2: %v", len(paths), paths)
	}
}

func TestParseLinksInternal(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "test.md"),
		"---\n---\n[Note](note.md) and [Relative](./note.md) and [Parent](../dir/note.md)")

	a := New()
	src, err := a.Parse(root, "test.md", false)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	want := []string{"note.md", "./note.md", "../dir/note.md"}
	if len(src.Links) != len(want) {
		t.Fatalf("Links = %v, want %v", src.Links, want)
	}
	for i, w := range want {
		if src.Links[i] != w {
			t.Errorf("Links[%d] = %q, want %q", i, src.Links[i], w)
		}
	}
}

func TestParseLinksExternalScheme(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "test.md"),
		"---\n---\n[Google](https://example.com) and [Mail](mailto:a@b.com) and [FTP](ftp://host/path)")

	a := New()
	src, err := a.Parse(root, "test.md", false)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(src.Links) != 0 {
		t.Fatalf("Links = %v, want empty (external URLs excluded)", src.Links)
	}
}

func TestParseLinksAbsolutePath(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "test.md"),
		"---\n---\n[Abs](/abs/path.md)")

	a := New()
	src, err := a.Parse(root, "test.md", false)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(src.Links) != 0 {
		t.Fatalf("Links = %v, want empty (absolute path excluded)", src.Links)
	}
}

func TestParseLinksProtocolRelative(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "test.md"),
		"---\n---\n[Proto](//host/path)")

	a := New()
	src, err := a.Parse(root, "test.md", false)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(src.Links) != 0 {
		t.Fatalf("Links = %v, want empty (protocol-relative excluded)", src.Links)
	}
}

func TestParseLinksHeadingOnly(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "test.md"),
		"---\n---\n[Section](#heading)")

	a := New()
	src, err := a.Parse(root, "test.md", false)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(src.Links) != 0 {
		t.Fatalf("Links = %v, want empty (heading-only anchor excluded)", src.Links)
	}
}

func TestParseLinksWindowsAbsolute(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "test.md"),
		"---\n---\n[Win](C:\\note.md) and [Win2](D:/other.md)")

	a := New()
	src, err := a.Parse(root, "test.md", false)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(src.Links) != 0 {
		t.Fatalf("Links = %v, want empty (Windows absolute paths excluded)", src.Links)
	}
}

func TestParseLinksHashStrip(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "test.md"),
		"---\n---\n[Ref](file.md#section)")

	a := New()
	src, err := a.Parse(root, "test.md", false)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(src.Links) != 1 || src.Links[0] != "file.md" {
		t.Fatalf("Links = %v, want [file.md] (# suffix stripped)", src.Links)
	}
}

func TestParseLinksWithWhitespace(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "test.md"),
		"---\n---\n[A]( https://example.com ) and [B]( #heading ) and [C]( /abs/path.md )")

	a := New()
	src, err := a.Parse(root, "test.md", false)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(src.Links) != 0 {
		t.Fatalf("Links = %v, want empty (whitespace-padded external links should be excluded)", src.Links)
	}
}

func TestParseImageToAttachments(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "test.md"),
		"---\n---\n![alt](image.png) and [link](note.md)")

	a := New()
	src, err := a.Parse(root, "test.md", false)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(src.Links) != 1 || src.Links[0] != "note.md" {
		t.Fatalf("Links = %v, want [note.md]", src.Links)
	}
	if len(src.Attachments) != 1 || src.Attachments[0] != "image.png" {
		t.Fatalf("Attachments = %v, want [image.png]", src.Attachments)
	}
}

func TestParseImageExternalSkipped(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "test.md"),
		"---\n---\n![alt](https://example.com/img.png)")

	a := New()
	src, err := a.Parse(root, "test.md", false)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(src.Attachments) != 0 {
		t.Fatalf("Attachments = %v, want empty (external image excluded)", src.Attachments)
	}
}

func TestParseSourceType(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "test.md"), "---\n---\nBody")

	a := New()
	src, err := a.Parse(root, "test.md", false)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if src.SourceType != "markdown" {
		t.Fatalf("SourceType = %q, want %q", src.SourceType, "markdown")
	}
}

func TestParseTitleFromFrontmatter(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "test.md"), "---\ntitle: My Title\n---\n# Heading")

	a := New()
	src, err := a.Parse(root, "test.md", false)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if src.Title != "My Title" {
		t.Fatalf("Title = %q, want %q", src.Title, "My Title")
	}
}

func TestParseNoWikilinks(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "test.md"), "---\n---\n[[wikilink]] should not be parsed")

	a := New()
	src, err := a.Parse(root, "test.md", false)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(src.Links) != 0 {
		t.Fatalf("Links = %v, want empty (wikilinks not supported in markdown adapter)", src.Links)
	}
}

func TestParseNotMarkdown(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "test.txt"), "content")

	a := New()
	_, err := a.Parse(root, "test.txt", false)
	if !errors.Is(err, cverr.ErrNotMarkdown) {
		t.Fatalf("Parse(txt) error = %v, want ErrNotMarkdown", err)
	}
}

func TestParseTraversal(t *testing.T) {
	root := t.TempDir()
	a := New()
	_, err := a.Parse(root, "../outside.md", false)
	if !errors.Is(err, cverr.ErrTraversal) {
		t.Fatalf("Parse(traversal) error = %v, want ErrTraversal", err)
	}
}

func TestParseTagsDedup(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "test.md"), "---\ntags: [a, a, b]\n---\nBody")

	a := New()
	src, err := a.Parse(root, "test.md", false)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(src.Tags) != 2 || src.Tags[0] != "a" || src.Tags[1] != "b" {
		t.Fatalf("Tags = %v, want [a, b] (deduplicated)", src.Tags)
	}
}
