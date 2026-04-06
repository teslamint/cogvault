package adapter

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	cverr "github.com/teslamint/cogvault/internal/errors"
)

func ValidateRelPath(relPath string) (string, error) {
	if relPath == "" {
		return "", fmt.Errorf("adapter: %q: %w", relPath, cverr.ErrNotFound)
	}
	if filepath.IsAbs(relPath) {
		return "", fmt.Errorf("adapter: %s: %w", relPath, cverr.ErrTraversal)
	}
	if ContainsDotDot(relPath) {
		return "", fmt.Errorf("adapter: %s: %w", relPath, cverr.ErrTraversal)
	}
	cleaned := filepath.Clean(relPath)
	if cleaned == "." {
		return "", fmt.Errorf("adapter: %q: %w", relPath, cverr.ErrNotFound)
	}
	return cleaned, nil
}

func CheckSymlinks(root, cleaned string) error {
	parts := strings.Split(cleaned, string(os.PathSeparator))
	current := root
	for _, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				break
			}
			return fmt.Errorf("adapter: %s: %w", cleaned, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("adapter: %s: %w", cleaned, cverr.ErrSymlink)
		}
	}
	return nil
}

func HasPathPrefix(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+string(os.PathSeparator))
}

func IsExcluded(rel string, exclude []string) bool {
	for _, ex := range exclude {
		if HasPathPrefix(rel, filepath.Clean(ex)) {
			return true
		}
	}
	return false
}

func ContainsDotDot(path string) bool {
	for _, sep := range []string{"/", string(os.PathSeparator)} {
		for _, component := range strings.Split(path, sep) {
			if component == ".." {
				return true
			}
		}
	}
	return false
}
