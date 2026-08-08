package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newSimilarCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "similar <path>",
		Short: "Find pages similar to a given wiki page",
		Args:  cobra.ExactArgs(1),
		RunE:  runSimilar,
	}
	cmd.Flags().Int("limit", 5, "maximum number of results")
	return cmd
}

func runSimilar(cmd *cobra.Command, args []string) error {
	configPath, err := resolveConfigPath(cmd)
	if err != nil {
		return err
	}

	cfg, store, idx, adpt, err := bootstrap(configPath)
	if err != nil {
		return err
	}
	defer idx.Close()

	if _, _, _, ccErr := idx.CheckConsistency(store, adpt, true); ccErr != nil {
		cmd.PrintErrln("warning: consistency check:", ccErr)
	}

	if cfg.LLM.EmbeddingModel != "" {
		if err := idx.InitEmbeddingsTable(); err != nil {
			cmd.PrintErrln("warning: embeddings table:", err)
		}
		idx.SetEmbeddingModel(cfg.LLM.EmbeddingModel)
	}

	limit, _ := cmd.Flags().GetInt("limit")
	results, err := idx.SearchSimilar(args[0], limit)
	if err != nil {
		return fmt.Errorf("search similar: %w", err)
	}

	if len(results) == 0 {
		cmd.Println("No similar pages found.")
		return nil
	}

	for _, r := range results {
		title := r.Title
		if title == "" {
			title = r.Path
		}
		cmd.Printf("%.4f  %s  (%s)\n", r.Score, title, r.Path)
	}
	return nil
}
