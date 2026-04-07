package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/teslamint/cogvault/internal/adapter"
	"github.com/teslamint/cogvault/internal/adapter/markdown"
	"github.com/teslamint/cogvault/internal/adapter/obsidian"
	"github.com/teslamint/cogvault/internal/config"
	"github.com/teslamint/cogvault/internal/index"
	"github.com/teslamint/cogvault/internal/storage"
)

func resolveVaultRoot(cmd *cobra.Command) (string, error) {
	vault, _ := cmd.Flags().GetString("vault")
	if vault == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve vault root: %w", err)
		}
		vault = wd
	}

	abs, err := filepath.Abs(vault)
	if err != nil {
		return "", fmt.Errorf("resolve vault root: %w", err)
	}

	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("vault root %q: %w", abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("vault root %q: not a directory", abs)
	}
	return abs, nil
}

func newAdapter(name string) (adapter.Adapter, error) {
	switch name {
	case "obsidian":
		return obsidian.New(), nil
	case "markdown":
		return markdown.New(), nil
	default:
		return nil, fmt.Errorf("unsupported adapter: %q", name)
	}
}

// bootstrap loads config and creates storage, index, and adapter.
// Caller must defer idx.Close() on success. If bootstrap returns a non-nil error,
// all resources are already cleaned up.
func bootstrap(vaultRoot string) (*config.Config, *storage.FSStorage, *index.SQLiteIndex, adapter.Adapter, error) {
	cfg, err := config.Load(vaultRoot)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	adpt, err := newAdapter(cfg.Adapter)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	store := storage.NewFSStorage(vaultRoot, cfg)

	dbPath := filepath.Join(vaultRoot, cfg.DBPath)
	idx, err := index.NewSQLiteIndex(vaultRoot, dbPath, cfg)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	return cfg, store, idx, adpt, nil
}
