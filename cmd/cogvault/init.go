package main

import (
	"errors"
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
		Short: "Initialize a vault with config, wiki directory, schema, and database",
		RunE:  runInit,
	}
}

func runInit(cmd *cobra.Command, args []string) error {
	vaultRoot, err := resolveVaultRoot(cmd)
	if err != nil {
		return err
	}

	// 1. Config file
	if err := config.Save(vaultRoot); err != nil {
		return fmt.Errorf("init config: %w", err)
	}

	cfg, err := config.Load(vaultRoot)
	if err != nil {
		return fmt.Errorf("init load config: %w", err)
	}

	// 2. Wiki directory
	wikiAbs := filepath.Join(vaultRoot, cfg.WikiDir)
	if err := os.MkdirAll(wikiAbs, 0o755); err != nil {
		return fmt.Errorf("init wiki dir: %w", err)
	}

	// 3. Schema file (via storage for symlink/traversal security)
	fsStore := storage.NewFSStorage(vaultRoot, cfg)
	if err := fsStore.WriteSchema([]byte(schema.DefaultContent)); err != nil {
		return fmt.Errorf("init schema: %w", err)
	}

	// 4. Adapter
	adpt, err := newAdapter(cfg.Adapter)
	if err != nil {
		return err
	}

	// 5. Database
	dbAbs := filepath.Join(vaultRoot, cfg.DBPath)
	dbExisted := fileExists(dbAbs)

	idx, err := index.NewSQLiteIndex(vaultRoot, dbAbs, cfg)
	if err != nil {
		return fmt.Errorf("init database: %w", err)
	}
	defer idx.Close()

	if dbExisted {
		_, _, _, ccErr := idx.CheckConsistency(fsStore, adpt, true)
		if ccErr != nil {
			if errors.Is(ccErr, index.ErrConsistencySystemic) {
				return fmt.Errorf("init consistency check: %w", ccErr)
			}
			cmd.PrintErrln("warning: some files had errors during consistency check:", ccErr)
		}
	} else {
		if err := idx.Rebuild(fsStore, adpt); err != nil {
			return fmt.Errorf("init rebuild index: %w", err)
		}
	}

	cmd.Println("Initialized vault at", vaultRoot)
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
