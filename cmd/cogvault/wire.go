package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/teslamint/cogvault/internal/adapter"
	"github.com/teslamint/cogvault/internal/adapter/markdown"
	"github.com/teslamint/cogvault/internal/adapter/obsidian"
	"github.com/teslamint/cogvault/internal/config"
	"github.com/teslamint/cogvault/internal/index"
	"github.com/teslamint/cogvault/internal/storage"
)

// resolveConfigPath returns the --config flag value, or the default config path
// when the flag is empty. No filesystem stat is performed here; config.Load
// reports a clear error (naming the path) when the file is missing.
func resolveConfigPath(cmd *cobra.Command) (string, error) {
	path, _ := cmd.Flags().GetString("config")
	if path != "" {
		return path, nil
	}
	return config.DefaultConfigPath()
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

// bootstrap loads config and creates storage, index, and adapter, all rooted at
// the absolute wiki_dir/db_path in the config. Caller must defer idx.Close() on
// success. If bootstrap returns a non-nil error, all resources are cleaned up.
func bootstrap(configPath string) (*config.Config, *storage.FSStorage, *index.SQLiteIndex, adapter.Adapter, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	adpt, err := newAdapter(cfg.Adapter)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	store := storage.NewFSStorage(cfg.WikiDir, cfg)

	idx, err := index.NewSQLiteIndex(cfg.WikiDir, cfg.DBPath, cfg)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	return cfg, store, idx, adpt, nil
}
