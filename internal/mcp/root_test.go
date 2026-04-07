package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveRoot(t *testing.T) {
	t.Run("valid directory", func(t *testing.T) {
		dir := t.TempDir()
		got, err := ResolveRoot(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		abs, _ := filepath.Abs(dir)
		if got != abs {
			t.Errorf("got %q, want %q", got, abs)
		}
	})

	t.Run("relative path resolved to absolute", func(t *testing.T) {
		dir := t.TempDir()
		rel, err := filepath.Rel(".", dir)
		if err != nil {
			t.Skip("cannot create relative path")
		}
		got, err := ResolveRoot(rel)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !filepath.IsAbs(got) {
			t.Errorf("expected absolute path, got %q", got)
		}
	})

	t.Run("empty path", func(t *testing.T) {
		_, err := ResolveRoot("")
		if err == nil {
			t.Fatal("expected error for empty path")
		}
	})

	t.Run("nonexistent path", func(t *testing.T) {
		_, err := ResolveRoot("/nonexistent/path/xyz")
		if err == nil {
			t.Fatal("expected error for nonexistent path")
		}
	})

	t.Run("file not directory", func(t *testing.T) {
		dir := t.TempDir()
		f := filepath.Join(dir, "file.txt")
		os.WriteFile(f, []byte("x"), 0644)
		_, err := ResolveRoot(f)
		if err == nil {
			t.Fatal("expected error for file path")
		}
	})
}
