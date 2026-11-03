package extract

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func writeSource(t *testing.T, name string, content []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExtractorReturnsCompleteTextSources(t *testing.T) {
	content := "---\ntitle: 노트\n---\n\n# 본문\n\n한국어와 English.\n"
	e := NewExtractor(len([]rune(content)), Commands{})
	for _, name := range []string{"a.md", "b.markdown", "c.txt", "D.MD"} {
		out, err := e.Extract(context.Background(), writeSource(t, name, []byte(content)))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if out != content {
			t.Fatalf("%s: text = %q, want verbatim source", name, out)
		}
	}
}

func TestExtractorRejectsTextOverLimitInsteadOfTruncating(t *testing.T) {
	e := NewExtractor(5, Commands{})
	out, err := e.Extract(context.Background(), writeSource(t, "a.md", []byte("가나다라마바")))
	if err == nil || !strings.Contains(err.Error(), "exceeds input limit") {
		t.Fatalf("out=%q err=%v, want input limit error", out, err)
	}
	if _, err := e.Extract(context.Background(), writeSource(t, "b.md", []byte("가나다라마"))); err != nil {
		t.Fatalf("text at the limit: %v", err)
	}
}

func TestExtractorRejectsInvalidUTF8Text(t *testing.T) {
	e := NewExtractor(100, Commands{})
	_, err := e.Extract(context.Background(), writeSource(t, "a.txt", []byte("caf\xe9")))
	if err == nil || !strings.Contains(err.Error(), "not valid UTF-8") {
		t.Fatalf("err=%v, want UTF-8 error", err)
	}
}

func TestExtractorTextReadIsBoundedAndRefusesSpecialFiles(t *testing.T) {
	e := NewExtractor(3, Commands{})
	// A large ASCII file fails on the byte bound (3 runes * 4 bytes + 1).
	if _, err := e.Extract(context.Background(), writeSource(t, "big.md", []byte(strings.Repeat("x", 1<<20)))); err == nil || !strings.Contains(err.Error(), "exceeds input limit") {
		t.Fatalf("large file: %v", err)
	}
	dir := t.TempDir()
	target := writeSource(t, "real.md", []byte("ok"))
	link := filepath.Join(dir, "link.md")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Extract(context.Background(), link); err == nil {
		t.Fatal("followed a symlink")
	}
	fifo := filepath.Join(dir, "pipe.md")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	done := make(chan error, 1)
	go func() { _, err := e.Extract(context.Background(), fifo); done <- err }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("FIFO: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reading a FIFO blocked")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.Extract(ctx, target); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context: %v", err)
	}
}

func TestExtractorRejectsUnsupportedType(t *testing.T) {
	e := NewExtractor(100, Commands{})
	_, err := e.Extract(context.Background(), writeSource(t, "a.html", []byte("<p>x</p>")))
	if err == nil || !strings.Contains(err.Error(), "unsupported source type") {
		t.Fatalf("err=%v, want unsupported type error", err)
	}
}

func TestExtractorSendsPDFToPDFExtraction(t *testing.T) {
	text := strings.Repeat("abcdefgh", 100)
	var seen []string
	cmds := Commands{CommandContext: func(ctx context.Context, name string, args ...string) *exec.Cmd {
		seen = append(seen, name)
		return envCmd(text, name, args...)
	}}
	out, err := NewExtractor(10000, cmds).Extract(context.Background(), "f.PDF")
	if err != nil || out != text || len(seen) == 0 || seen[0] != "pdftotext" {
		t.Fatalf("out=%q err=%v seen=%v", short(out), err, seen)
	}
}
