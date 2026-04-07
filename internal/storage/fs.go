package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/teslamint/cogvault/internal/config"
	cverr "github.com/teslamint/cogvault/internal/errors"
)

type FSStorage struct {
	root string
	cfg  *config.Config
	mu   sync.Mutex
}

func NewFSStorage(root string, cfg *config.Config) *FSStorage {
	return &FSStorage{
		root: root,
		cfg:  cfg,
	}
}

func (fs *FSStorage) Read(path string) ([]byte, error) {
	absPath, cleaned, err := fs.resolvePath(path)
	if err != nil {
		return nil, err
	}
	if fs.isExcludeRead(cleaned) {
		return nil, fmt.Errorf("storage.Read %s: %w", path, cverr.ErrPermission)
	}

	data, err := os.ReadFile(absPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("storage.Read %s: %w", path, cverr.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("storage.Read %s: %w", path, err)
	}
	return data, nil
}

func (fs *FSStorage) Write(path string, data []byte) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	absPath, cleaned, err := fs.resolvePath(path)
	if err != nil {
		return err
	}

	if fs.isExcludeRead(cleaned) {
		return fmt.Errorf("storage.Write %s: %w", path, cverr.ErrPermission)
	}

	cleanedWikiDir := filepath.Clean(fs.cfg.WikiDir)
	wikiPrefix := cleanedWikiDir + string(os.PathSeparator)
	if !strings.HasPrefix(cleaned, wikiPrefix) {
		return fmt.Errorf("storage.Write %s: %w", path, cverr.ErrPermission)
	}
	if cleaned == filepath.Clean(fs.cfg.SchemaPath()) {
		return fmt.Errorf("storage.Write %s: %w", path, cverr.ErrPermission)
	}

	// TOCTOU: a race window exists between resolvePath (symlink check) and
	// MkdirAll/WriteFile. An external actor could replace a parent directory
	// with a symlink in between. Accepted for single-user local MVP.
	// See docs/decisions/0004-storage-toctou.md.
	parentDir := filepath.Dir(absPath)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		return fmt.Errorf("storage.Write %s: %w", path, err)
	}
	if err := os.WriteFile(absPath, data, 0o644); err != nil {
		return fmt.Errorf("storage.Write %s: %w", path, err)
	}
	return nil
}

// WriteSchema writes data to the configured schema path for init bootstrap.
// Idempotent: returns nil if the file already exists.
// Reuses resolvePath for symlink/traversal security.
func (fs *FSStorage) WriteSchema(data []byte) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	absPath, _, err := fs.resolvePath(fs.cfg.SchemaPath())
	if err != nil {
		return err
	}

	if info, err := os.Stat(absPath); err == nil {
		if info.IsDir() {
			return fmt.Errorf("storage.WriteSchema: schema path is a directory")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("storage.WriteSchema: %w", err)
	}

	parentDir := filepath.Dir(absPath)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		return fmt.Errorf("storage.WriteSchema: %w", err)
	}
	if err := os.WriteFile(absPath, data, 0o644); err != nil {
		return fmt.Errorf("storage.WriteSchema: %w", err)
	}
	return nil
}

func (fs *FSStorage) List(prefix string) ([]ListEntry, error) {
	absPath, cleaned, err := fs.resolvePath(prefix)
	if err != nil {
		return nil, err
	}
	if fs.isExcludeRead(cleaned) {
		return nil, fmt.Errorf("storage.List %s: %w", prefix, cverr.ErrPermission)
	}

	info, err := os.Stat(absPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("storage.List %s: %w", prefix, cverr.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("storage.List %s: %w", prefix, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("storage.List %s: %w", prefix, cverr.ErrNotFound)
	}

	entries, err := os.ReadDir(absPath)
	if err != nil {
		return nil, fmt.Errorf("storage.List %s: %w", prefix, err)
	}

	result := make([]ListEntry, 0, len(entries))
	for _, entry := range entries {
		childPath := filepath.Join(cleaned, entry.Name())
		if fs.isAllExcluded(childPath) {
			continue
		}

		pathField := childPath
		if entry.IsDir() {
			pathField += string(os.PathSeparator)
		}
		result = append(result, ListEntry{
			Path:  pathField,
			Name:  entry.Name(),
			IsDir: entry.IsDir(),
		})
	}
	return result, nil
}

func (fs *FSStorage) Exists(path string) (bool, error) {
	absPath, cleaned, err := fs.resolvePath(path)
	if err != nil {
		return false, err
	}
	if fs.isExcludeRead(cleaned) {
		return false, nil
	}

	_, err = os.Stat(absPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("storage.Exists %s: %w", path, err)
	}
	return true, nil
}

func (fs *FSStorage) resolvePath(path string) (string, string, error) {
	if path == "" {
		return "", "", fmt.Errorf("storage: %q: %w", path, cverr.ErrNotFound)
	}
	if filepath.IsAbs(path) {
		return "", "", fmt.Errorf("storage: %s: %w", path, cverr.ErrTraversal)
	}
	if containsDotDot(path) {
		return "", "", fmt.Errorf("storage: %s: %w", path, cverr.ErrTraversal)
	}

	cleaned := filepath.Clean(path)
	absPath := filepath.Join(fs.root, cleaned)
	parts := strings.Split(cleaned, string(os.PathSeparator))
	current := fs.root

	for _, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				break
			}
			return "", "", fmt.Errorf("storage: %s: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", "", fmt.Errorf("storage: %s: %w", path, cverr.ErrSymlink)
		}
	}

	return absPath, cleaned, nil
}

func (fs *FSStorage) isExcludeRead(path string) bool {
	cleanedPath := filepath.Clean(path)
	for _, entry := range fs.cfg.ExcludeRead {
		if hasPathPrefix(cleanedPath, filepath.Clean(entry)) {
			return true
		}
	}
	return false
}

func (fs *FSStorage) isAllExcluded(path string) bool {
	cleanedPath := filepath.Clean(path)
	for _, entry := range fs.cfg.AllExcluded() {
		if hasPathPrefix(cleanedPath, filepath.Clean(entry)) {
			return true
		}
	}
	return false
}

func hasPathPrefix(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+string(os.PathSeparator))
}

func containsDotDot(path string) bool {
	for _, sep := range []string{"/", string(os.PathSeparator)} {
		for _, component := range strings.Split(path, sep) {
			if component == ".." {
				return true
			}
		}
	}
	return false
}
