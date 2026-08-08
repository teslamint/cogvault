package storage

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/teslamint/cogvault/internal/config"
	cverr "github.com/teslamint/cogvault/internal/errors"
)

func TestReadWriteAndExists(t *testing.T) {
	root := t.TempDir()
	store := newTestStorage(t, root, &config.Config{
		Exclude:     []string{".obsidian"},
		ExcludeRead: []string{"private"},
	})

	if err := store.Write("sources/test.md", []byte("hello")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	data, err := store.Read("sources/test.md")
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("Read() data = %q, want %q", string(data), "hello")
	}

	ok, err := store.Exists("sources/test.md")
	if err != nil {
		t.Fatalf("Exists() error = %v", err)
	}
	if !ok {
		t.Fatalf("Exists() = false, want true")
	}

	ok, err = store.Exists("sources/missing.md")
	if err != nil {
		t.Fatalf("Exists() missing error = %v", err)
	}
	if ok {
		t.Fatalf("Exists() missing = true, want false")
	}

	_, err = store.Read("sources/missing.md")
	if !errors.Is(err, cverr.ErrNotFound) {
		t.Fatalf("Read() missing error = %v, want ErrNotFound", err)
	}
}

func TestWriteOverwrite(t *testing.T) {
	root := t.TempDir()
	store := newTestStorage(t, root, &config.Config{})

	if err := store.Write("test.md", []byte("first")); err != nil {
		t.Fatalf("Write() first error = %v", err)
	}
	if err := store.Write("test.md", []byte("second")); err != nil {
		t.Fatalf("Write() second error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, "test.md"))
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	if string(data) != "second" {
		t.Fatalf("file data = %q, want %q", string(data), "second")
	}
}

func TestWriteAtRootLevelSucceeds(t *testing.T) {
	root := t.TempDir()
	store := newTestStorage(t, root, &config.Config{})

	if err := store.Write("sources/page.md", []byte("ok")); err != nil {
		t.Fatalf("Write() root-relative error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, "sources", "page.md"))
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	if string(data) != "ok" {
		t.Fatalf("file data = %q, want %q", string(data), "ok")
	}
}

func TestWriteNestedNewDirectoriesCreatesParents(t *testing.T) {
	root := t.TempDir()
	store := newTestStorage(t, root, &config.Config{})

	if err := store.Write("a/b/c/deep.md", []byte("deep")); err != nil {
		t.Fatalf("Write() nested error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, "a", "b", "c", "deep.md"))
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	if string(data) != "deep" {
		t.Fatalf("file data = %q, want %q", string(data), "deep")
	}
}

func TestWriteRejectsSchema(t *testing.T) {
	root := t.TempDir()
	store := newTestStorage(t, root, &config.Config{})

	err := store.Write("_schema.md", []byte("x"))
	if !errors.Is(err, cverr.ErrPermission) {
		t.Fatalf("Write() schema error = %v, want ErrPermission", err)
	}
}

func TestStat(t *testing.T) {
	root := t.TempDir()
	store := newTestStorage(t, root, &config.Config{
		ExcludeRead: []string{"private"},
	})

	before := time.Now().Add(-time.Second)
	if err := store.Write("sources/page.md", []byte("hello world")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	after := time.Now().Add(time.Second)

	size, mtime, err := store.Stat("sources/page.md")
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if size != int64(len("hello world")) {
		t.Fatalf("Stat() size = %d, want %d", size, len("hello world"))
	}
	if mtime.Before(before) || mtime.After(after) {
		t.Fatalf("Stat() mtime = %v, want within [%v, %v]", mtime, before, after)
	}

	_, _, err = store.Stat("sources/missing.md")
	if !errors.Is(err, cverr.ErrNotFound) {
		t.Fatalf("Stat() missing error = %v, want ErrNotFound", err)
	}

	mustWriteFile(t, filepath.Join(root, "private", "secret.md"), "secret")
	_, _, err = store.Stat("private/secret.md")
	if !errors.Is(err, cverr.ErrPermission) {
		t.Fatalf("Stat() exclude_read error = %v, want ErrPermission", err)
	}
}

func TestStatDirectory(t *testing.T) {
	root := t.TempDir()
	store := newTestStorage(t, root, &config.Config{})

	mustMkdirAll(t, filepath.Join(root, "sources"))

	size, mtime, err := store.Stat("sources")
	if err != nil {
		t.Fatalf("Stat() dir error = %v", err)
	}
	if mtime.IsZero() {
		t.Fatalf("Stat() dir mtime is zero")
	}
	if size < 0 {
		t.Fatalf("Stat() dir size = %d, want >= 0", size)
	}
}

func TestStatSymlinkRejected(t *testing.T) {
	root := t.TempDir()
	store := newTestStorage(t, root, &config.Config{})

	realDir := filepath.Join(root, "real")
	mustMkdirAll(t, realDir)
	mustWriteFile(t, filepath.Join(realDir, "page.md"), "content")

	if err := os.Symlink(filepath.Join(realDir, "page.md"), filepath.Join(root, "link.md")); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}

	_, _, err := store.Stat("link.md")
	if !errors.Is(err, cverr.ErrSymlink) {
		t.Fatalf("Stat() symlink error = %v, want ErrSymlink", err)
	}
}

func TestListBehavior(t *testing.T) {
	root := t.TempDir()
	store := newTestStorage(t, root, &config.Config{
		Exclude:     []string{".obsidian"},
		ExcludeRead: []string{"private"},
	})

	mustWriteFile(t, filepath.Join(root, "note.md"), "note")
	mustMkdirAll(t, filepath.Join(root, "subdir"))
	mustWriteFile(t, filepath.Join(root, ".obsidian", "ignore.md"), "ignore")
	mustWriteFile(t, filepath.Join(root, "private", "secret.md"), "secret")

	entries, err := store.List(".")
	if err != nil {
		t.Fatalf("List(.) error = %v", err)
	}
	assertListEntry(t, entries, "note.md", "note.md", false)
	assertListEntry(t, entries, "subdir/", "subdir", true)
	if hasEntry(entries, ".obsidian") || hasEntry(entries, ".obsidian/") {
		t.Fatalf("List(.) included excluded entry: %#v", entries)
	}
	if hasEntry(entries, "private") || hasEntry(entries, "private/") {
		t.Fatalf("List(.) included exclude_read entry: %#v", entries)
	}

	entries, err = store.List("subdir")
	if err != nil {
		t.Fatalf("List(subdir) error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("List(subdir) len = %d, want 0", len(entries))
	}

	_, err = store.List("note.md")
	if !errors.Is(err, cverr.ErrNotFound) {
		t.Fatalf("List(file) error = %v, want ErrNotFound", err)
	}
}

func TestExcludeAndExcludeReadSemantics(t *testing.T) {
	root := t.TempDir()
	store := newTestStorage(t, root, &config.Config{
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
	store := newTestStorage(t, root, &config.Config{})

	for _, path := range []string{"../etc/passwd", "sources/../../secret.md", "/tmp/x", "/etc/passwd"} {
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
		_, _, err = store.Stat(path)
		if !errors.Is(err, cverr.ErrTraversal) {
			t.Fatalf("Stat(%q) error = %v, want ErrTraversal", path, err)
		}
	}
}

func TestSymlinkRejected(t *testing.T) {
	root := t.TempDir()
	store := newTestStorage(t, root, &config.Config{})

	outside := filepath.Join(root, "outside")
	mustMkdirAll(t, outside)
	mustMkdirAll(t, filepath.Join(root, "sub"))
	if err := os.Symlink(outside, filepath.Join(root, "sub", "out")); err != nil {
		t.Fatalf("os.Symlink() ancestor error = %v", err)
	}

	err := store.Write("sub/out/file.md", []byte("x"))
	if !errors.Is(err, cverr.ErrSymlink) {
		t.Fatalf("Write() ancestor symlink error = %v, want ErrSymlink", err)
	}

	if err := os.Remove(filepath.Join(root, "sub", "out")); err != nil {
		t.Fatalf("os.Remove() error = %v", err)
	}
	mustWriteFile(t, filepath.Join(root, "sub", "target.md"), "x")
	if err := os.Symlink(filepath.Join(root, "sub", "target.md"), filepath.Join(root, "sub", "link.md")); err != nil {
		t.Fatalf("os.Symlink() leaf error = %v", err)
	}

	_, err = store.Read("sub/link.md")
	if !errors.Is(err, cverr.ErrSymlink) {
		t.Fatalf("Read() leaf symlink error = %v, want ErrSymlink", err)
	}
}

func TestResolvePathPropagatesNonENOENTLstatError(t *testing.T) {
	root := t.TempDir()
	store := newTestStorage(t, root, &config.Config{})

	mustMkdirAll(t, filepath.Join(root, "sub"))
	mustWriteFile(t, filepath.Join(root, "sub", "file.md"), "x")

	_, err := store.Read("sub/file.md/child")
	if err == nil {
		t.Fatalf("Read() error = nil, want non-nil")
	}
	if errors.Is(err, cverr.ErrNotFound) || errors.Is(err, cverr.ErrTraversal) || errors.Is(err, cverr.ErrPermission) || errors.Is(err, cverr.ErrSymlink) {
		t.Fatalf("Read() error = %v, want raw fs error", err)
	}
}

func TestEmptyPathRejected(t *testing.T) {
	root := t.TempDir()
	store := newTestStorage(t, root, &config.Config{})

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
	_, _, err = store.Stat("")
	if !errors.Is(err, cverr.ErrNotFound) {
		t.Fatalf("Stat(\"\") error = %v, want ErrNotFound", err)
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
	store := newTestStorage(t, root, &config.Config{})

	var wg sync.WaitGroup
	values := []string{"first", "second", "third", "fourth"}
	for _, value := range values {
		wg.Add(1)
		go func(v string) {
			defer wg.Done()
			if err := store.Write("race.md", []byte(v)); err != nil {
				t.Errorf("Write() error = %v", err)
			}
		}(value)
	}
	wg.Wait()

	data, err := os.ReadFile(filepath.Join(root, "race.md"))
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

func TestWriteExcludeReadBlocked(t *testing.T) {
	root := t.TempDir()
	store := newTestStorage(t, root, &config.Config{
		Exclude:     []string{},
		ExcludeRead: []string{"private"},
	})

	err := store.Write("private/secret.md", []byte("should fail"))
	if !errors.Is(err, cverr.ErrPermission) {
		t.Fatalf("Write to exclude_read dir: expected ErrPermission, got %v", err)
	}

	// Verify non-excluded write still works
	err = store.Write("notes/ok.md", []byte("should succeed"))
	if err != nil {
		t.Fatalf("Write to non-excluded dir: %v", err)
	}
}

func TestMoveHappyPath(t *testing.T) {
	root := t.TempDir()
	store := newTestStorage(t, root, &config.Config{})

	if err := store.Write("sources/a.md", []byte("content")); err != nil {
		t.Fatal(err)
	}

	if err := store.Move("sources/a.md", "sources/_archived/a-abcd1234.md"); err != nil {
		t.Fatalf("Move() error = %v", err)
	}

	if _, err := store.Read("sources/a.md"); !errors.Is(err, cverr.ErrNotFound) {
		t.Fatalf("src should be gone, got err = %v", err)
	}

	data, err := store.Read("sources/_archived/a-abcd1234.md")
	if err != nil {
		t.Fatalf("dst Read() error = %v", err)
	}
	if string(data) != "content" {
		t.Fatalf("dst content = %q, want %q", string(data), "content")
	}
}

func TestMoveMissingSrc(t *testing.T) {
	root := t.TempDir()
	store := newTestStorage(t, root, &config.Config{})

	err := store.Move("sources/nope.md", "sources/_archived/nope.md")
	if !errors.Is(err, cverr.ErrNotFound) {
		t.Fatalf("Move missing src: expected ErrNotFound, got %v", err)
	}
}

func TestMoveTraversalRejected(t *testing.T) {
	root := t.TempDir()
	store := newTestStorage(t, root, &config.Config{})

	if err := store.Write("sources/a.md", []byte("content")); err != nil {
		t.Fatal(err)
	}

	err := store.Move("../escape", "sources/b.md")
	if !errors.Is(err, cverr.ErrTraversal) {
		t.Fatalf("Move traversal src: expected ErrTraversal, got %v", err)
	}

	err = store.Move("sources/a.md", "../escape")
	if !errors.Is(err, cverr.ErrTraversal) {
		t.Fatalf("Move traversal dst: expected ErrTraversal, got %v", err)
	}
}

func TestMoveSymlinkRejected(t *testing.T) {
	root := t.TempDir()
	store := newTestStorage(t, root, &config.Config{})

	real := filepath.Join(root, "real.md")
	if err := os.WriteFile(real, []byte("real"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.md")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	err := store.Move("link.md", "dst.md")
	if !errors.Is(err, cverr.ErrSymlink) {
		t.Fatalf("Move symlink src: expected ErrSymlink, got %v", err)
	}
}

func TestMoveAutoMkdir(t *testing.T) {
	root := t.TempDir()
	store := newTestStorage(t, root, &config.Config{})

	if err := store.Write("a.md", []byte("content")); err != nil {
		t.Fatal(err)
	}

	if err := store.Move("a.md", "deep/nested/dir/a.md"); err != nil {
		t.Fatalf("Move auto-mkdir error = %v", err)
	}

	data, err := store.Read("deep/nested/dir/a.md")
	if err != nil {
		t.Fatalf("Read after move error = %v", err)
	}
	if string(data) != "content" {
		t.Fatalf("content = %q, want %q", string(data), "content")
	}
}

func TestMoveRejectsSchema(t *testing.T) {
	root := t.TempDir()
	store := newTestStorage(t, root, &config.Config{})

	if err := store.Write("a.md", []byte("content")); err != nil {
		t.Fatal(err)
	}

	err := store.Move("a.md", "_schema.md")
	if !errors.Is(err, cverr.ErrPermission) {
		t.Fatalf("Move to schema: expected ErrPermission, got %v", err)
	}
}

func TestMoveRejectsExcludeReadSource(t *testing.T) {
	root := t.TempDir()
	store := newTestStorage(t, root, &config.Config{ExcludeRead: []string{"private"}})

	mustWriteFile(t, filepath.Join(root, "private", "secret.md"), "secret")

	err := store.Move("private/secret.md", "archive/secret.md")
	if !errors.Is(err, cverr.ErrPermission) {
		t.Fatalf("Move exclude_read src: expected ErrPermission, got %v", err)
	}
}

func TestMoveRejectsExcludeReadDestination(t *testing.T) {
	root := t.TempDir()
	store := newTestStorage(t, root, &config.Config{ExcludeRead: []string{"private"}})

	if err := store.Write("notes/secret.md", []byte("secret")); err != nil {
		t.Fatal(err)
	}

	err := store.Move("notes/secret.md", "private/secret.md")
	if !errors.Is(err, cverr.ErrPermission) {
		t.Fatalf("Move exclude_read dst: expected ErrPermission, got %v", err)
	}
}

func TestMoveMissingProtectedSourceReturnsPermission(t *testing.T) {
	root := t.TempDir()
	store := newTestStorage(t, root, &config.Config{ExcludeRead: []string{"private"}})

	err := store.Move("private/missing.md", "archive/missing.md")
	if !errors.Is(err, cverr.ErrPermission) {
		t.Fatalf("Move missing protected src: expected ErrPermission, got %v", err)
	}
}

func TestMovePreservesOccupiedDestination(t *testing.T) {
	root := t.TempDir()
	store := newTestStorage(t, root, &config.Config{})

	if err := store.Write("sources/a.md", []byte("source")); err != nil {
		t.Fatal(err)
	}
	if err := store.Write("archive/a.md", []byte("existing")); err != nil {
		t.Fatal(err)
	}

	err := store.Move("sources/a.md", "archive/a.md")
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("Move occupied dst: expected os.ErrExist, got %v", err)
	}

	srcData, err := store.Read("sources/a.md")
	if err != nil {
		t.Fatalf("Read src after failed move error = %v", err)
	}
	if string(srcData) != "source" {
		t.Fatalf("src content after failed move = %q, want %q", string(srcData), "source")
	}

	dstData, err := store.Read("archive/a.md")
	if err != nil {
		t.Fatalf("Read dst after failed move error = %v", err)
	}
	if string(dstData) != "existing" {
		t.Fatalf("dst content after failed move = %q, want %q", string(dstData), "existing")
	}
}

func TestMoveMissingSourceWinsOverOccupiedDestination(t *testing.T) {
	root := t.TempDir()
	store := newTestStorage(t, root, &config.Config{})

	if err := store.Write("archive/a.md", []byte("existing")); err != nil {
		t.Fatal(err)
	}

	err := store.Move("sources/missing.md", "archive/a.md")
	if !errors.Is(err, cverr.ErrNotFound) {
		t.Fatalf("Move missing src with occupied dst: expected ErrNotFound, got %v", err)
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
