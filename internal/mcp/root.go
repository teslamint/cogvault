package mcp

import (
	"fmt"
	"os"
	"path/filepath"
)

func ResolveRoot(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("vault path must not be empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve vault path %q: %w", path, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("resolve vault path %q: %w", path, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("resolve vault path %q: not a directory", path)
	}
	return abs, nil
}
