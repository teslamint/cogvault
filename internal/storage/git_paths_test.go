package storage

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/teslamint/cogvault/internal/config"
	cverr "github.com/teslamint/cogvault/internal/errors"
)

func gitGuardStorage(t *testing.T) (*FSStorage, string) {
	t.Helper()
	root := t.TempDir()
	cfg := &config.Config{
		WikiDir:     root,
		Exclude:     []string{".obsidian", ".trash"},
		ExcludeRead: []string{},
	}
	return NewFSStorage(root, cfg), root
}

// gitControlledPaths are the paths that turn the 0024 auto-commit safety net
// into arbitrary command execution if a caller can write them: `git add`
// reads .gitattributes to decide which clean filter applies to a file, and
// reads .git/config to learn the shell command that filter runs.
//
// The case variants are not decoration. On a case-insensitive filesystem
// (APFS by default on macOS, this project's primary platform) a write to
// `.GIT/config` lands in the real `.git/config`, so a case-sensitive guard
// is no guard at all — that bypass was reproduced end-to-end through the
// live MCP tool surface, marker file and all, before the fix. These cases
// must keep failing on case-sensitive filesystems too: the boundary cannot
// depend on which host it runs on.
func gitControlledPaths() []string {
	return []string{
		".git/config",
		".git/hooks/pre-commit",
		".gitattributes",
		"notes/.gitattributes",
		"deep/nested/dir/.gitattributes",
		".gitmodules",
		".git",
		".GIT/config",
		".Git/config",
		".GitAttributes",
		".GITATTRIBUTES",
		"notes/.GitAttributes",
		".GitModules",
		".GIT",
	}
}

func TestWriteRejectsGitControlledPaths(t *testing.T) {
	for _, path := range gitControlledPaths() {
		t.Run(path, func(t *testing.T) {
			fs, root := gitGuardStorage(t)

			err := fs.Write(path, []byte("[filter \"evil\"]\n\tclean = touch /tmp/pwned\n"))
			if !errors.Is(err, cverr.ErrPermission) {
				t.Fatalf("Write(%q) error = %v, want ErrPermission", path, err)
			}
			if _, statErr := os.Stat(filepath.Join(root, path)); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("Write(%q) must not create the file; stat = %v", path, statErr)
			}
		})
	}
}

func TestDeleteRejectsGitControlledPaths(t *testing.T) {
	for _, path := range gitControlledPaths() {
		t.Run(path, func(t *testing.T) {
			fs, root := gitGuardStorage(t)
			abs := filepath.Join(root, path)
			if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
				t.Fatal(err)
			}
			// `.git` (in any casing) is a directory in a real repository;
			// the rest are files. Either way the delete must be refused
			// before it reaches the filesystem.
			if strings.EqualFold(path, ".git") {
				if err := os.MkdirAll(abs, 0o755); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(abs, []byte("fixture"), 0o644); err != nil {
				t.Fatal(err)
			}

			if err := fs.Delete(path); !errors.Is(err, cverr.ErrPermission) {
				t.Fatalf("Delete(%q) error = %v, want ErrPermission", path, err)
			}
			if _, statErr := os.Stat(abs); statErr != nil {
				t.Fatalf("Delete(%q) must leave the path intact; stat = %v", path, statErr)
			}
		})
	}
}

func TestMoveRejectsGitControlledPaths(t *testing.T) {
	t.Run("destination", func(t *testing.T) {
		fs, root := gitGuardStorage(t)
		if err := os.WriteFile(filepath.Join(root, "page.md"), []byte("# Page"), 0o644); err != nil {
			t.Fatal(err)
		}

		// Move is the flanking route to the same exploit: write innocuous
		// content to an allowed path, then rename it into place.
		if err := fs.Move("page.md", ".gitattributes"); !errors.Is(err, cverr.ErrPermission) {
			t.Fatalf("Move to .gitattributes error = %v, want ErrPermission", err)
		}
		if _, statErr := os.Stat(filepath.Join(root, ".gitattributes")); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("rejected Move must not create the destination; stat = %v", statErr)
		}
	})

	t.Run("source", func(t *testing.T) {
		fs, root := gitGuardStorage(t)
		if err := os.WriteFile(filepath.Join(root, ".gitattributes"), []byte("*.md filter=evil\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		if err := fs.Move(".gitattributes", "page.md"); !errors.Is(err, cverr.ErrPermission) {
			t.Fatalf("Move from .gitattributes error = %v, want ErrPermission", err)
		}
	})
}

// TestWriteAllowsGitAdjacentNames guards the guard: rejecting by substring
// rather than by path component would break ordinary wiki pages whose names
// merely start with the same letters, and .gitignore only selects paths — it
// never names a command to execute, so it stays writable.
func TestWriteAllowsGitAdjacentNames(t *testing.T) {
	for _, path := range []string{
		"github-notes.md",
		"notes/gitattributes-explained.md",
		".gitignore",
		"projects/git.md",
	} {
		t.Run(path, func(t *testing.T) {
			fs, root := gitGuardStorage(t)
			if err := fs.Write(path, []byte("# Page")); err != nil {
				t.Fatalf("Write(%q) = %v, want success", path, err)
			}
			if _, err := os.Stat(filepath.Join(root, path)); err != nil {
				t.Fatalf("Write(%q) did not land on disk: %v", path, err)
			}
		})
	}
}
