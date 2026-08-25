package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/teslamint/cogvault/internal/adapter/markdown"
)

func TestSlugFromURL(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"https://example.com/docs/Go Guide", "web-docs-Go-Guide"},
		{"https://example.com/", "web-example.com"},
		{"https://example.com", "web-example.com"},
		{"https://example.com/한글 문서/페이지", "web-한글-문서-페이지"},
	}
	for _, tt := range tests {
		got := slugFromURL(tt.raw)
		if got != tt.want {
			t.Errorf("slugFromURL(%q) = %q, want %q", tt.raw, got, tt.want)
		}
		if !utf8ValidString(got) {
			t.Errorf("slugFromURL(%q) produced invalid UTF-8: %q", tt.raw, got)
		}
	}
}

func TestSlugFromURLTruncatesOnRuneBoundary(t *testing.T) {
	long := "https://example.com/" + strings.Repeat("한", 100)
	got := slugFromURL(long)
	runes := []rune(strings.TrimPrefix(got, "web-"))
	if len(runes) != 80 {
		t.Fatalf("slug length = %d runes, want 80", len(runes))
	}
	if !utf8ValidString(got) {
		t.Fatalf("slug is not valid UTF-8: %q", got)
	}
}

func TestFormatFetchedContentQuotesURL(t *testing.T) {
	body := []byte("hello")
	got := formatFetchedContent("https://example.com/a b#c", body)
	if !strings.Contains(got, `source_url: "https://example.com/a b#c"`) {
		t.Fatalf("source_url not quoted:\n%s", got)
	}
	if !strings.HasSuffix(got, "hello") {
		t.Fatalf("body missing:\n%s", got)
	}
}

func TestIsTextContentType(t *testing.T) {
	yes := []string{"", "text/html", "text/html; charset=utf-8", "text/plain", "application/json", "TEXT/markdown"}
	no := []string{"application/pdf", "image/png", "application/octet-stream", "audio/mpeg"}
	for _, ct := range yes {
		if !isTextContentType(ct) {
			t.Errorf("isTextContentType(%q) = false, want true", ct)
		}
	}
	for _, ct := range no {
		if isTextContentType(ct) {
			t.Errorf("isTextContentType(%q) = true, want false", ct)
		}
	}
}

func TestRunFetchRejectsNonTextContent(t *testing.T) {
	configPath, _, _ := testVault(t)
	srcDir := t.TempDir()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.4 fake"))
	}))
	defer srv.Close()

	_, _, err := executeCommand("fetch", "--config", configPath, "--source-dir", srcDir, srv.URL)
	if err == nil || !strings.Contains(err.Error(), "unsupported Content-Type") {
		t.Fatalf("err = %v, want unsupported Content-Type", err)
	}
}

func TestRunFetchRejectsInvalidUTF8(t *testing.T) {
	configPath, _, _ := testVault(t)
	srcDir := t.TempDir()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		// Invalid UTF-8 that still sniffs as text (no NUL/BOM signature in
		// the first bytes DetectContentType inspects as binary).
		_, _ = w.Write([]byte("caf\xe9 r\xe9sum\xe9 with broken \xc3\x28 sequences"))
	}))
	defer srv.Close()

	_, _, err := executeCommand("fetch", "--config", configPath, "--source-dir", srcDir, srv.URL)
	if err == nil || !strings.Contains(err.Error(), "not valid UTF-8") {
		t.Fatalf("err = %v, want invalid UTF-8 error", err)
	}
}

func TestRunFetchSavesAndQuotesFrontmatter(t *testing.T) {
	configPath, _, _ := testVault(t)
	srcDir := t.TempDir()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html>hi</html>"))
	}))
	defer srv.Close()

	stdout, _, err := executeCommand("fetch", "--config", configPath, "--source-dir", srcDir, srv.URL+"/a b")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !strings.Contains(stdout, "saved") {
		t.Fatalf("stdout = %q, want saved message", stdout)
	}

	matches, _ := filepath.Glob(filepath.Join(srcDir, "web-a-b*.md"))
	if len(matches) != 1 {
		t.Fatalf("glob = %v, want one capture file", matches)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, `source_url: "`) {
		t.Fatalf("frontmatter source_url not quoted:\n%s", content)
	}
	// Frontmatter must parse: reuse the repo's own parser.
	src, err := markdown.New().Parse(srcDir, filepath.Base(matches[0]), false)
	if err != nil {
		t.Fatalf("parse captured file: %v", err)
	}
	if src.Frontmatter["source_url"] != srv.URL+"/a b" {
		t.Fatalf("source_url = %v", src.Frontmatter["source_url"])
	}
}

func TestRunFetchRejectsPathTraversalName(t *testing.T) {
	configPath, _, _ := testVault(t)
	srcDir := t.TempDir()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("x"))
	}))
	defer srv.Close()

	for _, name := range []string{"../evil.md", "sub/dir.md", "..", "."} {
		_, _, err := executeCommand("fetch", "--config", configPath, "--source-dir", srcDir, "--name", name, srv.URL)
		if err == nil || !strings.Contains(err.Error(), "plain filename") {
			t.Fatalf("name %q: err = %v, want plain filename error", name, err)
		}
	}
}

func utf8ValidString(s string) bool { return utf8.ValidString(s) }

func TestRunFetchSniffsMislabelledBinary(t *testing.T) {
	configPath, _, _ := testVault(t)
	srcDir := t.TempDir()

	// text/plain header over an actual PDF body: the sniff gate catches it.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("%PDF-1.7\n%âãÏÓ\n1 0 obj"))
	}))
	defer srv.Close()

	_, _, err := executeCommand("fetch", "--config", configPath, "--source-dir", srcDir, srv.URL)
	if err == nil || !strings.Contains(err.Error(), "sniffs as") {
		t.Fatalf("err = %v, want sniff rejection", err)
	}
}

func TestRunFetchAcceptsMissingContentType(t *testing.T) {
	configPath, _, _ := testVault(t)
	srcDir := t.TempDir()

	// No Content-Type header at all: allowed, body is text so it passes.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("plain body without header"))
	}))
	defer srv.Close()

	stdout, _, err := executeCommand("fetch", "--config", configPath, "--source-dir", srcDir, srv.URL)
	if err != nil || !strings.Contains(stdout, "saved") {
		t.Fatalf("err = %v stdout = %q, want success without header", err, stdout)
	}
}
