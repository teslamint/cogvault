package obsidian

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	cverr "github.com/teslamint/cogvault/internal/errors"
)

func TestParseWikilinks(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "test.md"), "---\n---\n[[target1]] and [[target2]]")

	a := New()
	src, err := a.Parse(root, "test.md", false)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(src.Links) != 2 || src.Links[0] != "target1" || src.Links[1] != "target2" {
		t.Fatalf("Links = %v, want [target1, target2]", src.Links)
	}
}

func TestParseWikilinkWithDisplay(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "test.md"), "---\n---\n[[target|display text]]")

	a := New()
	src, err := a.Parse(root, "test.md", false)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(src.Links) != 1 || src.Links[0] != "target" {
		t.Fatalf("Links = %v, want [target]", src.Links)
	}
}

func TestParseWikilinkWithHeading(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "test.md"), "---\n---\n[[target#heading]]")

	a := New()
	src, err := a.Parse(root, "test.md", false)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(src.Links) != 1 || src.Links[0] != "target" {
		t.Fatalf("Links = %v, want [target]", src.Links)
	}
}

func TestParseWikilinkWithHeadingAndDisplay(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "test.md"), "---\n---\n[[target#heading|display]]")

	a := New()
	src, err := a.Parse(root, "test.md", false)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(src.Links) != 1 || src.Links[0] != "target" {
		t.Fatalf("Links = %v, want [target]", src.Links)
	}
}

func TestParseEmbeds(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "test.md"), "---\n---\n![[image.png]]")

	a := New()
	src, err := a.Parse(root, "test.md", false)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(src.Attachments) != 1 || src.Attachments[0] != "image.png" {
		t.Fatalf("Attachments = %v, want [image.png]", src.Attachments)
	}
	if len(src.Links) != 0 {
		t.Fatalf("Links = %v, want empty (embed should not be in Links)", src.Links)
	}
}

func TestParseEmbedWithSize(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "test.md"), "---\n---\n![[diagram.svg|500]]")

	a := New()
	src, err := a.Parse(root, "test.md", false)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(src.Attachments) != 1 || src.Attachments[0] != "diagram.svg" {
		t.Fatalf("Attachments = %v, want [diagram.svg]", src.Attachments)
	}
}

func TestParseTitleFromFrontmatter(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "test.md"), "---\ntitle: My Title\n---\n# Heading\n")

	a := New()
	src, err := a.Parse(root, "test.md", false)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if src.Title != "My Title" {
		t.Fatalf("Title = %q, want %q", src.Title, "My Title")
	}
}

func TestParseTitleFromHeading(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "test.md"), "---\n---\n# First Heading\n\nBody")

	a := New()
	src, err := a.Parse(root, "test.md", false)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if src.Title != "First Heading" {
		t.Fatalf("Title = %q, want %q", src.Title, "First Heading")
	}
}

func TestParseTitleFromFilename(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "my-note.md"), "---\n---\nBody without heading")

	a := New()
	src, err := a.Parse(root, "my-note.md", false)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if src.Title != "my-note" {
		t.Fatalf("Title = %q, want %q", src.Title, "my-note")
	}
}

func TestParseBrokenFrontmatter(t *testing.T) {
	root := fixtureRoot(t)
	a := New()
	src, err := a.Parse(root, filepath.Join("edge", "broken-frontmatter.md"), false)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(src.Frontmatter) != 0 {
		t.Fatalf("Frontmatter = %v, want empty", src.Frontmatter)
	}
}

func TestParseNoFrontmatter(t *testing.T) {
	root := fixtureRoot(t)
	a := New()
	src, err := a.Parse(root, filepath.Join("edge", "no-frontmatter.md"), false)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if src.Title != "Just a Heading" {
		t.Fatalf("Title = %q, want %q", src.Title, "Just a Heading")
	}
	if len(src.Links) != 1 || src.Links[0] != "some-link" {
		t.Fatalf("Links = %v, want [some-link]", src.Links)
	}
}

func TestParseEmptyFile(t *testing.T) {
	root := fixtureRoot(t)
	a := New()
	src, err := a.Parse(root, filepath.Join("edge", "empty.md"), false)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if src.Title != "empty" {
		t.Fatalf("Title = %q, want %q", src.Title, "empty")
	}
	if len(src.Links) != 0 {
		t.Fatalf("Links = %v, want empty", src.Links)
	}
}

func TestParseUTF8BOM(t *testing.T) {
	root := fixtureRoot(t)
	a := New()
	src, err := a.Parse(root, filepath.Join("edge", "utf8-bom.md"), false)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if src.Title != "BOM Test" {
		t.Fatalf("Title = %q, want %q", src.Title, "BOM Test")
	}
	if len(src.Tags) != 1 || src.Tags[0] != "bom-tag" {
		t.Fatalf("Tags = %v, want [bom-tag]", src.Tags)
	}
}

func TestParseNotMarkdown(t *testing.T) {
	root := fixtureRoot(t)
	a := New()
	_, err := a.Parse(root, filepath.Join("edge", "binary.png"), false)
	if !errors.Is(err, cverr.ErrNotMarkdown) {
		t.Fatalf("Parse(binary.png) error = %v, want ErrNotMarkdown", err)
	}
}

func TestParseNotFound(t *testing.T) {
	root := t.TempDir()
	a := New()
	_, err := a.Parse(root, "nonexistent.md", false)
	if !errors.Is(err, cverr.ErrNotFound) {
		t.Fatalf("Parse(nonexistent) error = %v, want ErrNotFound", err)
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

func TestParseAbsolutePath(t *testing.T) {
	a := New()
	_, err := a.Parse("/tmp", "/absolute/path.md", false)
	if !errors.Is(err, cverr.ErrTraversal) {
		t.Fatalf("Parse(absolute) error = %v, want ErrTraversal", err)
	}
}

func TestParseSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require elevated privileges on Windows")
	}
	root := t.TempDir()
	real := filepath.Join(root, "real.md")
	mustWriteFile(t, real, "# Real")
	link := filepath.Join(root, "link.md")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	a := New()
	_, err := a.Parse(root, "link.md", false)
	if !errors.Is(err, cverr.ErrSymlink) {
		t.Fatalf("Parse(symlink) error = %v, want ErrSymlink", err)
	}
}

func TestParseEmptyPath(t *testing.T) {
	root := t.TempDir()
	a := New()
	_, err := a.Parse(root, "", false)
	if !errors.Is(err, cverr.ErrNotFound) {
		t.Fatalf("Parse(empty) error = %v, want ErrNotFound", err)
	}
}

func TestParseDotPath(t *testing.T) {
	root := t.TempDir()
	a := New()
	_, err := a.Parse(root, ".", false)
	if !errors.Is(err, cverr.ErrNotFound) {
		t.Fatalf("Parse(dot) error = %v, want ErrNotFound", err)
	}
}

func TestParseTagsArray(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "test.md"), "---\ntags: [a, b]\n---\nBody")

	a := New()
	src, err := a.Parse(root, "test.md", false)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(src.Tags) != 2 || src.Tags[0] != "a" || src.Tags[1] != "b" {
		t.Fatalf("Tags = %v, want [a, b]", src.Tags)
	}
}

func TestParseTagsString(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "test.md"), "---\ntags: \"a, b\"\n---\nBody")

	a := New()
	src, err := a.Parse(root, "test.md", false)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(src.Tags) != 2 || src.Tags[0] != "a" || src.Tags[1] != "b" {
		t.Fatalf("Tags = %v, want [a, b]", src.Tags)
	}
}

func TestParseInlineTags(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "test.md"), "---\ntags: [fm-tag]\n---\nBody with #inline-tag")

	a := New()
	src, err := a.Parse(root, "test.md", false)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(src.Tags) != 2 {
		t.Fatalf("Tags = %v, want [fm-tag, inline-tag]", src.Tags)
	}
	if src.Tags[0] != "fm-tag" || src.Tags[1] != "inline-tag" {
		t.Fatalf("Tags = %v, want [fm-tag, inline-tag]", src.Tags)
	}
}

func TestParseDataview(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "test.md"), "---\n---\nstatus:: draft\npriority:: high")

	a := New()
	src, err := a.Parse(root, "test.md", false)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if src.DataviewFields["status"] != "draft" || src.DataviewFields["priority"] != "high" {
		t.Fatalf("DataviewFields = %v, want {status:draft, priority:high}", src.DataviewFields)
	}
}

func TestParseAliases(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "test.md"), "---\naliases: [alias1, alias2]\n---\nBody")

	a := New()
	src, err := a.Parse(root, "test.md", false)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(src.Aliases) != 2 || src.Aliases[0] != "alias1" || src.Aliases[1] != "alias2" {
		t.Fatalf("Aliases = %v, want [alias1, alias2]", src.Aliases)
	}
}

func TestParseIncludeContentTrue(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "test.md"), "---\n---\nHello world")

	a := New()
	src, err := a.Parse(root, "test.md", true)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if src.Content == "" {
		t.Fatal("Content should not be empty when includeContent=true")
	}
}

func TestParseIncludeContentFalse(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "test.md"), "---\n---\nHello world")

	a := New()
	src, err := a.Parse(root, "test.md", false)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if src.Content != "" {
		t.Fatalf("Content = %q, want empty when includeContent=false", src.Content)
	}
}

func TestParseKorean(t *testing.T) {
	root := fixtureRoot(t)
	a := New()
	src, err := a.Parse(root, filepath.Join("obsidian", "korean-path", "한글노트.md"), false)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if src.Title != "한글 제목" {
		t.Fatalf("Title = %q, want %q", src.Title, "한글 제목")
	}
	if len(src.Links) != 1 || src.Links[0] != "다른노트" {
		t.Fatalf("Links = %v, want [다른노트]", src.Links)
	}
	if len(src.Tags) != 1 || src.Tags[0] != "한글태그" {
		t.Fatalf("Tags = %v, want [한글태그]", src.Tags)
	}
}

func TestParseCodeBlockFalsePositive(t *testing.T) {
	root := fixtureRoot(t)
	a := New()
	src, err := a.Parse(root, filepath.Join("obsidian", "code-block.md"), false)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(src.Links) < 2 {
		t.Fatalf("Links = %v, want at least 2 (MVP accepts false positives in code blocks)", src.Links)
	}
}

func TestParseDeduplication(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "test.md"), "---\n---\n[[same]] and [[same]] again")

	a := New()
	src, err := a.Parse(root, "test.md", false)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(src.Links) != 1 || src.Links[0] != "same" {
		t.Fatalf("Links = %v, want [same] (deduplicated)", src.Links)
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
	if src.SourceType != "obsidian" {
		t.Fatalf("SourceType = %q, want %q", src.SourceType, "obsidian")
	}
}

func TestParsePathNormalized(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "notes", "a.md"), "---\n---\nBody")

	a := New()
	src, err := a.Parse(root, "notes/./a.md", false)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	want := filepath.Join("notes", "a.md")
	if src.Path != want {
		t.Fatalf("Path = %q, want %q (normalized)", src.Path, want)
	}
}

func TestParseSameNameDifferentDirs(t *testing.T) {
	root := fixtureRoot(t)
	a := New()

	srcA, err := a.Parse(root, filepath.Join("obsidian", "same-name", "a", "note.md"), false)
	if err != nil {
		t.Fatalf("Parse(a/note.md) error = %v", err)
	}
	srcB, err := a.Parse(root, filepath.Join("obsidian", "same-name", "b", "note.md"), false)
	if err != nil {
		t.Fatalf("Parse(b/note.md) error = %v", err)
	}

	if srcA.Path == srcB.Path {
		t.Fatalf("same-name files should have different Path: %q == %q", srcA.Path, srcB.Path)
	}
	if srcA.Title != "Note A" || srcB.Title != "Note B" {
		t.Fatalf("titles: A=%q B=%q, want Note A / Note B", srcA.Title, srcB.Title)
	}
}

func fixtureRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join("..", "..", "..", "testdata", "fixtures")
	abs, err := filepath.Abs(root)
	if err != nil {
		t.Fatalf("could not resolve fixture root: %v", err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("fixture root not found: %s", abs)
	}
	return abs
}
