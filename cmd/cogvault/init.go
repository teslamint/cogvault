package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/teslamint/cogvault/internal/config"
	"github.com/teslamint/cogvault/internal/index"
	"github.com/teslamint/cogvault/internal/schema"
	"github.com/teslamint/cogvault/internal/storage"
)

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create the config file, then the wiki directory, schema, and database",
		Long: `Initialize cogvault in two steps.

First run (no config file yet): creates the config file at the configured path
with default placeholder values and exits. Edit wiki_dir, db_path, and sources
in that file, then run init again.

Second run (valid config): creates the wiki directory, writes _schema.md, and
builds the search index at the configured absolute locations.`,
		RunE: runInit,
	}
}

func runInit(cmd *cobra.Command, args []string) error {
	configPath, err := resolveConfigPath(cmd)
	if err != nil {
		return err
	}

	// 1. Config file. Track whether it already existed so we can distinguish a
	// fresh scaffold (guidance + success exit) from a real validation error.
	configExisted := fileExists(configPath)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return fmt.Errorf("init config dir: %w", err)
	}
	if err := config.Save(configPath); err != nil {
		return fmt.Errorf("init config: %w", err)
	}

	// 2. Load and validate.
	cfg, err := config.Load(configPath)
	if err != nil {
		if !configExisted {
			cmd.Printf("created %s; edit wiki_dir/db_path/sources, then re-run cogvault init\n", configPath)
			return nil
		}
		return err
	}

	// 3. Wiki directory (via storage for symlink/traversal security on schema).
	if err := os.MkdirAll(cfg.WikiDir, 0o755); err != nil {
		return fmt.Errorf("init wiki dir: %w", err)
	}

	fsStore := storage.NewFSStorage(cfg.WikiDir, cfg)
	if err := fsStore.WriteSchema([]byte(schema.DefaultContent)); err != nil {
		return fmt.Errorf("init schema: %w", err)
	}

	adpt, err := newAdapter(cfg.Adapter)
	if err != nil {
		return err
	}

	// 4. Database.
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		return fmt.Errorf("init db dir: %w", err)
	}
	dbExisted := fileExists(cfg.DBPath)

	idx, err := index.NewSQLiteIndex(cfg.WikiDir, cfg.DBPath, cfg)
	if err != nil {
		return fmt.Errorf("init database: %w", err)
	}
	defer idx.Close()

	if dbExisted {
		_, _, _, ccErr := idx.CheckConsistency(fsStore, adpt, true)
		if err := handleConsistencyResult(cmd, ccErr); err != nil {
			return fmt.Errorf("init consistency check: %w", err)
		}
	} else {
		if err := idx.Rebuild(fsStore, adpt); err != nil {
			return fmt.Errorf("init rebuild index: %w", err)
		}
	}

	cmd.Println("Initialized wiki at", cfg.WikiDir)
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
