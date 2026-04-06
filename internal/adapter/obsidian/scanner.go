package obsidian

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/teslamint/cogvault/internal/adapter"
	cverr "github.com/teslamint/cogvault/internal/errors"
)

type ObsidianAdapter struct{}

func New() *ObsidianAdapter {
	return &ObsidianAdapter{}
}

func (a *ObsidianAdapter) Name() string {
	return "obsidian"
}

func (a *ObsidianAdapter) Scan(root string, exclude []string, fn func(path string) error) error {
	info, err := os.Stat(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("adapter.Scan %s: %w", root, cverr.ErrNotFound)
		}
		return fmt.Errorf("adapter.Scan %s: %w", root, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("adapter.Scan %s: %w", root, cverr.ErrNotFound)
	}

	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}

		if d.IsDir() {
			if adapter.IsExcluded(rel, exclude) {
				return fs.SkipDir
			}
			return nil
		}

		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}

		if !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}

		if adapter.IsExcluded(rel, exclude) {
			return nil
		}

		return fn(rel)
	})
}
