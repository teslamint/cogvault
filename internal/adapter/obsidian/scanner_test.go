package obsidian

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
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
	mustWriteFile(t, filepath.Join(root, "notes", "sub", "b.md"), "# B")
	mustWriteFile(t, filepath.Join(root, "readme.txt"), "skip")
	mustWriteFile(t, filepath.Join(root, "image.png"), "skip")

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
	want := []string{
		filepath.Join("notes", "a.md"),
		filepath.Join("notes", "sub", "b.md"),
	}
	if len(paths) != len(want) {
		t.Fatalf("Scan() got %d paths, want %d: %v", len(paths), len(want), paths)
	}
	for i, p := range paths {
		if p != want[i] {
			t.Errorf("paths[%d] = %q, want %q", i, p, want[i])
		}
	}
}

func TestScanExclude(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, ".obsidian", "config.md"), "skip")
	mustWriteFile(t, filepath.Join(root, ".trash", "old.md"), "skip")
	mustWriteFile(t, filepath.Join(root, "notes", "good.md"), "# Good")

	a := New()
	var paths []string
	err := a.Scan(root, []string{".obsidian", ".trash"}, func(path string) error {
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(paths) != 1 || paths[0] != filepath.Join("notes", "good.md") {
		t.Fatalf("Scan() got %v, want [notes/good.md]", paths)
	}
}

func TestScanCallbackError(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "a.md"), "")
	mustWriteFile(t, filepath.Join(root, "b.md"), "")

	a := New()
	callbackErr := errors.New("stop")
	count := 0
	err := a.Scan(root, nil, func(path string) error {
		count++
		return callbackErr
	})
	if !errors.Is(err, callbackErr) {
		t.Fatalf("Scan() error = %v, want %v", err, callbackErr)
	}
	if count != 1 {
		t.Fatalf("callback called %d times, want 1", count)
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

func TestScanRootNotExist(t *testing.T) {
	a := New()
	err := a.Scan("/nonexistent/path", nil, func(path string) error { return nil })
	if !errors.Is(err, cverr.ErrNotFound) {
		t.Fatalf("Scan(nonexistent) error = %v, want ErrNotFound", err)
	}
}

func TestScanEmptyDirectory(t *testing.T) {
	root := t.TempDir()
	a := New()
	var paths []string
	err := a.Scan(root, nil, func(path string) error {
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(paths) != 0 {
		t.Fatalf("Scan() got %d paths, want 0", len(paths))
	}
}

func TestScanKoreanPath(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "한글폴더", "한글파일.md"), "# 한글")

	a := New()
	var paths []string
	err := a.Scan(root, nil, func(path string) error {
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	want := filepath.Join("한글폴더", "한글파일.md")
	if len(paths) != 1 || paths[0] != want {
		t.Fatalf("Scan() got %v, want [%s]", paths, want)
	}
}

func TestScanRelativePaths(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "deep", "nested", "file.md"), "")

	a := New()
	err := a.Scan(root, nil, func(path string) error {
		if filepath.IsAbs(path) {
			t.Errorf("expected relative path, got absolute: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
}

func TestScanExcludeFile(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "notes", "good.md"), "# Good")
	mustWriteFile(t, filepath.Join(root, "private", "secret.md"), "# Secret")
	mustWriteFile(t, filepath.Join(root, "toplevel-secret.md"), "# Top Secret")

	a := New()
	var paths []string
	err := a.Scan(root, []string{"private", "toplevel-secret.md"}, func(path string) error {
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(paths) != 1 || paths[0] != filepath.Join("notes", "good.md") {
		t.Fatalf("Scan() got %v, want [notes/good.md] (excluded files should be skipped)", paths)
	}
}

func TestScanSymlinkFileSkip(t *testing.T) {
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
	var paths []string
	err := a.Scan(root, nil, func(path string) error {
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(paths) != 1 || paths[0] != "real.md" {
		t.Fatalf("Scan() got %v, want [real.md] (symlink should be skipped)", paths)
	}
}

func TestScanSymlinkDirSkip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require elevated privileges on Windows")
	}
	root := t.TempDir()
	realDir := filepath.Join(root, "realdir")
	mustWriteFile(t, filepath.Join(realDir, "note.md"), "# Note")
	linkDir := filepath.Join(root, "linkdir")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Fatal(err)
	}

	a := New()
	var paths []string
	err := a.Scan(root, nil, func(path string) error {
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	want := filepath.Join("realdir", "note.md")
	if len(paths) != 1 || paths[0] != want {
		t.Fatalf("Scan() got %v, want [%s] (symlink dir should not be followed)", paths, want)
	}
}
