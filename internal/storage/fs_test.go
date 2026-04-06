package storage

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/teslamint/cogvault/internal/config"
	cverr "github.com/teslamint/cogvault/internal/errors"
)

func TestReadWriteAndExists(t *testing.T) {
	root := t.TempDir()
	store := newTestStorage(t, root, &config.Config{
		WikiDir:     "_wiki",
		Exclude:     []string{".obsidian"},
		ExcludeRead: []string{"private"},
	})

	if err := store.Write("_wiki/notes/test.md", []byte("hello")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	data, err := store.Read("_wiki/notes/test.md")
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("Read() data = %q, want %q", string(data), "hello")
	}

	ok, err := store.Exists("_wiki/notes/test.md")
	if err != nil {
		t.Fatalf("Exists() error = %v", err)
	}
	if !ok {
		t.Fatalf("Exists() = false, want true")
	}

	ok, err = store.Exists("_wiki/notes/missing.md")
	if err != nil {
		t.Fatalf("Exists() missing error = %v", err)
	}
	if ok {
		t.Fatalf("Exists() missing = true, want false")
	}

	_, err = store.Read("_wiki/notes/missing.md")
	if !errors.Is(err, cverr.ErrNotFound) {
		t.Fatalf("Read() missing error = %v, want ErrNotFound", err)
	}
}

func TestWriteOverwrite(t *testing.T) {
	root := t.TempDir()
	store := newTestStorage(t, root, &config.Config{WikiDir: "_wiki"})

	if err := store.Write("_wiki/test.md", []byte("first")); err != nil {
		t.Fatalf("Write() first error = %v", err)
	}
	if err := store.Write("_wiki/test.md", []byte("second")); err != nil {
		t.Fatalf("Write() second error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, "_wiki", "test.md"))
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	if string(data) != "second" {
		t.Fatalf("file data = %q, want %q", string(data), "second")
	}
}

func TestWriteRejectsOutsideWikiDirAndSchema(t *testing.T) {
	root := t.TempDir()
	store := newTestStorage(t, root, &config.Config{WikiDir: "_wiki"})

	err := store.Write("notes/outside.md", []byte("x"))
	if !errors.Is(err, cverr.ErrPermission) {
		t.Fatalf("Write() outside error = %v, want ErrPermission", err)
	}

	err = store.Write("_wiki_other/file.md", []byte("x"))
	if !errors.Is(err, cverr.ErrPermission) {
		t.Fatalf("Write() prefix collision error = %v, want ErrPermission", err)
	}

	err = store.Write("_wiki/_schema.md", []byte("x"))
	if !errors.Is(err, cverr.ErrPermission) {
		t.Fatalf("Write() schema error = %v, want ErrPermission", err)
	}
}

func TestWriteAllowsCanonicalWikiDirWithTrailingSlash(t *testing.T) {
	root := t.TempDir()
	store := newTestStorage(t, root, &config.Config{WikiDir: "_wiki/"})

	if err := store.Write("_wiki/file.md", []byte("ok")); err != nil {
		t.Fatalf("Write() with trailing slash wiki_dir error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, "_wiki", "file.md"))
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	if string(data) != "ok" {
		t.Fatalf("file data = %q, want %q", string(data), "ok")
	}
}

func TestListBehavior(t *testing.T) {
	root := t.TempDir()
	store := newTestStorage(t, root, &config.Config{
		WikiDir:     "_wiki",
		Exclude:     []string{".obsidian"},
		ExcludeRead: []string{"private"},
	})

	mustWriteFile(t, filepath.Join(root, "_wiki", "note.md"), "note")
	mustMkdirAll(t, filepath.Join(root, "_wiki", "subdir"))
	mustWriteFile(t, filepath.Join(root, ".obsidian", "ignore.md"), "ignore")
	mustWriteFile(t, filepath.Join(root, "private", "secret.md"), "secret")

	entries, err := store.List(".")
	if err != nil {
		t.Fatalf("List(.) error = %v", err)
	}
	assertListEntry(t, entries, "_wiki/", "_wiki", true)
	if hasEntry(entries, ".obsidian") || hasEntry(entries, ".obsidian/") {
		t.Fatalf("List(.) included excluded entry: %#v", entries)
	}
	if hasEntry(entries, "private") || hasEntry(entries, "private/") {
		t.Fatalf("List(.) included exclude_read entry: %#v", entries)
	}

	entries, err = store.List("_wiki")
	if err != nil {
		t.Fatalf("List(_wiki) error = %v", err)
	}
	assertListEntry(t, entries, "_wiki/note.md", "note.md", false)
	assertListEntry(t, entries, "_wiki/subdir/", "subdir", true)

	entries, err = store.List("_wiki/subdir")
	if err != nil {
		t.Fatalf("List(_wiki/subdir) error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("List(_wiki/subdir) len = %d, want 0", len(entries))
	}

	_, err = store.List("_wiki/note.md")
	if !errors.Is(err, cverr.ErrNotFound) {
		t.Fatalf("List(file) error = %v, want ErrNotFound", err)
	}
}

func TestExcludeAndExcludeReadSemantics(t *testing.T) {
	root := t.TempDir()
	store := newTestStorage(t, root, &config.Config{
		WikiDir:     "_wiki",
		Exclude:     []string{".obsidian"},
		ExcludeRead: []string{"private"},
	})

	mustWriteFile(t, filepath.Join(root, ".obsidian", "visible.md"), "visible")
	mustWriteFile(t, filepath.Join(root, "private", "secret.md"), "secret")
	mustWriteFile(t, filepath.Join(root, "private2", "allowed.md"), "allowed")

	data, err := store.Read(".obsidian/visible.md")
	if err != nil {
		t.Fatalf("Read(exclude) error = %v", err)
	}
	if string(data) != "visible" {
		t.Fatalf("Read(exclude) data = %q, want %q", string(data), "visible")
	}

	ok, err := store.Exists(".obsidian/visible.md")
	if err != nil {
		t.Fatalf("Exists(exclude) error = %v", err)
	}
	if !ok {
		t.Fatalf("Exists(exclude) = false, want true")
	}

	_, err = store.Read("private/secret.md")
	if !errors.Is(err, cverr.ErrPermission) {
		t.Fatalf("Read(exclude_read) error = %v, want ErrPermission", err)
	}

	ok, err = store.Exists("private/secret.md")
	if err != nil {
		t.Fatalf("Exists(exclude_read) error = %v", err)
	}
	if ok {
		t.Fatalf("Exists(exclude_read) = true, want false")
	}

	_, err = store.List("private")
	if !errors.Is(err, cverr.ErrPermission) {
		t.Fatalf("List(exclude_read dir) error = %v, want ErrPermission", err)
	}

	data, err = store.Read("private2/allowed.md")
	if err != nil {
		t.Fatalf("Read(prefix collision) error = %v", err)
	}
	if string(data) != "allowed" {
		t.Fatalf("Read(prefix collision) data = %q, want %q", string(data), "allowed")
	}
}

func TestTraversalAndAbsolutePathRejected(t *testing.T) {
	root := t.TempDir()
	store := newTestStorage(t, root, &config.Config{WikiDir: "_wiki"})

	for _, path := range []string{"../etc/passwd", "_wiki/../secret.md", "/tmp/x", "/etc/passwd"} {
		_, err := store.Read(path)
		if !errors.Is(err, cverr.ErrTraversal) {
			t.Fatalf("Read(%q) error = %v, want ErrTraversal", path, err)
		}
		err = store.Write(path, []byte("x"))
		if !errors.Is(err, cverr.ErrTraversal) {
			t.Fatalf("Write(%q) error = %v, want ErrTraversal", path, err)
		}
		_, err = store.List(path)
		if !errors.Is(err, cverr.ErrTraversal) {
			t.Fatalf("List(%q) error = %v, want ErrTraversal", path, err)
		}
		_, err = store.Exists(path)
		if !errors.Is(err, cverr.ErrTraversal) {
			t.Fatalf("Exists(%q) error = %v, want ErrTraversal", path, err)
		}
	}
}

func TestSymlinkRejected(t *testing.T) {
	root := t.TempDir()
	store := newTestStorage(t, root, &config.Config{WikiDir: "_wiki"})

	outside := filepath.Join(root, "outside")
	mustMkdirAll(t, outside)
	mustMkdirAll(t, filepath.Join(root, "_wiki"))
	if err := os.Symlink(outside, filepath.Join(root, "_wiki", "out")); err != nil {
		t.Fatalf("os.Symlink() ancestor error = %v", err)
	}

	err := store.Write("_wiki/out/file.md", []byte("x"))
	if !errors.Is(err, cverr.ErrSymlink) {
		t.Fatalf("Write() ancestor symlink error = %v, want ErrSymlink", err)
	}

	if err := os.Remove(filepath.Join(root, "_wiki", "out")); err != nil {
		t.Fatalf("os.Remove() error = %v", err)
	}
	mustWriteFile(t, filepath.Join(root, "_wiki", "target.md"), "x")
	if err := os.Symlink(filepath.Join(root, "_wiki", "target.md"), filepath.Join(root, "_wiki", "link.md")); err != nil {
		t.Fatalf("os.Symlink() leaf error = %v", err)
	}

	_, err = store.Read("_wiki/link.md")
	if !errors.Is(err, cverr.ErrSymlink) {
		t.Fatalf("Read() leaf symlink error = %v, want ErrSymlink", err)
	}
}

func TestResolvePathPropagatesNonENOENTLstatError(t *testing.T) {
	root := t.TempDir()
	store := newTestStorage(t, root, &config.Config{WikiDir: "_wiki"})

	mustMkdirAll(t, filepath.Join(root, "_wiki"))
	mustWriteFile(t, filepath.Join(root, "_wiki", "file.md"), "x")

	_, err := store.Read("_wiki/file.md/child")
	if err == nil {
		t.Fatalf("Read() error = nil, want non-nil")
	}
	if errors.Is(err, cverr.ErrNotFound) || errors.Is(err, cverr.ErrTraversal) || errors.Is(err, cverr.ErrPermission) || errors.Is(err, cverr.ErrSymlink) {
		t.Fatalf("Read() error = %v, want raw fs error", err)
	}
}

func TestEmptyPathRejected(t *testing.T) {
	root := t.TempDir()
	store := newTestStorage(t, root, &config.Config{WikiDir: "_wiki"})

	_, err := store.Read("")
	if !errors.Is(err, cverr.ErrNotFound) {
		t.Fatalf("Read(\"\") error = %v, want ErrNotFound", err)
	}
	err = store.Write("", []byte("x"))
	if !errors.Is(err, cverr.ErrNotFound) {
		t.Fatalf("Write(\"\") error = %v, want ErrNotFound", err)
	}
	_, err = store.List("")
	if !errors.Is(err, cverr.ErrNotFound) {
		t.Fatalf("List(\"\") error = %v, want ErrNotFound", err)
	}
	_, err = store.Exists("")
	if !errors.Is(err, cverr.ErrNotFound) {
		t.Fatalf("Exists(\"\") error = %v, want ErrNotFound", err)
	}
}

func TestContainsDotDot(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{path: "a/../b", want: true},
		{path: "..", want: true},
		{path: "a/b", want: false},
		{path: "...", want: false},
		{path: "a..b", want: false},
		{path: "a/..hidden", want: false},
	}

	for _, tt := range tests {
		if got := containsDotDot(tt.path); got != tt.want {
			t.Fatalf("containsDotDot(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestConcurrentWritesSamePath(t *testing.T) {
	root := t.TempDir()
	store := newTestStorage(t, root, &config.Config{WikiDir: "_wiki"})

	var wg sync.WaitGroup
	values := []string{"first", "second", "third", "fourth"}
	for _, value := range values {
		wg.Add(1)
		go func(v string) {
			defer wg.Done()
			if err := store.Write("_wiki/race.md", []byte(v)); err != nil {
				t.Errorf("Write() error = %v", err)
			}
		}(value)
	}
	wg.Wait()

	data, err := os.ReadFile(filepath.Join(root, "_wiki", "race.md"))
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}

	got := string(data)
	if got == "" {
		t.Fatalf("race file empty, want one of %v", values)
	}
	if !containsString(values, got) {
		t.Fatalf("race file = %q, want one of %v", got, values)
	}
}

func newTestStorage(t *testing.T, root string, cfg *config.Config) *FSStorage {
	t.Helper()
	if cfg.Exclude == nil {
		cfg.Exclude = []string{}
	}
	if cfg.ExcludeRead == nil {
		cfg.ExcludeRead = []string{}
	}
	return NewFSStorage(root, cfg)
}

func mustWriteFile(t *testing.T, path, data string) {
	t.Helper()
	mustMkdirAll(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", path, err)
	}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v", path, err)
	}
}

func assertListEntry(t *testing.T, entries []ListEntry, path, name string, isDir bool) {
	t.Helper()
	for _, entry := range entries {
		if entry.Path == path {
			if entry.Name != name {
				t.Fatalf("entry %q name = %q, want %q", path, entry.Name, name)
			}
			if entry.IsDir != isDir {
				t.Fatalf("entry %q isDir = %v, want %v", path, entry.IsDir, isDir)
			}
			if isDir && !strings.HasSuffix(entry.Path, string(os.PathSeparator)) {
				t.Fatalf("dir entry %q missing trailing separator", entry.Path)
			}
			if !isDir && strings.HasSuffix(entry.Path, string(os.PathSeparator)) {
				t.Fatalf("file entry %q has trailing separator", entry.Path)
			}
			return
		}
	}
	t.Fatalf("entry %q not found in %#v", path, entries)
}

func hasEntry(entries []ListEntry, path string) bool {
	for _, entry := range entries {
		if entry.Path == path {
			return true
		}
	}
	return false
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
