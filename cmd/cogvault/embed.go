package main

import (
	"context"
	"fmt"
	"log/slog"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"github.com/teslamint/cogvault/internal/llm"
)

func newEmbedCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "embed",
		Short: "Compute embeddings for indexed wiki pages",
		RunE:  runEmbed,
	}
	cmd.Flags().Int("batch-size", 32, "number of texts per embedding request")
	return cmd
}

const embedTextRuneCap = 2000

func runEmbed(cmd *cobra.Command, args []string) error {
	configPath, err := resolveConfigPath(cmd)
	if err != nil {
		return err
	}

	cfg, store, idx, adpt, err := bootstrap(configPath)
	if err != nil {
		return err
	}
	defer idx.Close()

	if cfg.LLM.EmbeddingModel == "" {
		return fmt.Errorf("embedding_model not configured; set llm.embedding_model in config")
	}

	sqIdx := idx

	if err := sqIdx.InitEmbeddingsTable(); err != nil {
		return err
	}

	if _, _, _, ccErr := idx.CheckConsistency(store, adpt, true); ccErr != nil {
		cmd.PrintErrln("warning: consistency check:", ccErr)
	}

	stale, err := sqIdx.StalePaths(cfg.LLM.EmbeddingModel)
	if err != nil {
		return fmt.Errorf("find stale paths: %w", err)
	}

	if len(stale) == 0 {
		cmd.Println("embed: all embeddings up to date")
		return nil
	}

	baseURL := cfg.LLM.EmbeddingBaseURL
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	embedder := llm.NewOllamaEmbedder(baseURL, cfg.LLM.EmbeddingModel)

	batchSize, _ := cmd.Flags().GetInt("batch-size")
	if batchSize <= 0 {
		batchSize = 32
	}

	embedded := 0
	failed := 0
	ctx := context.Background()

	for i := 0; i < len(stale); i += batchSize {
		end := i + batchSize
		if end > len(stale) {
			end = len(stale)
		}
		batch := stale[i:end]

		texts := make([]string, len(batch))
		for j, entry := range batch {
			texts[j] = buildEmbedText(store, entry.Path)
		}

		vecs, err := embedder.Embed(ctx, texts)
		if err != nil {
			slog.Error("embed batch failed", "start", i, "error", err)
			failed += len(batch)
			continue
		}

		for j, entry := range batch {
			if err := sqIdx.StoreEmbedding(entry.Path, entry.ContentHash, cfg.LLM.EmbeddingModel, vecs[j]); err != nil {
				slog.Error("store embedding failed", "path", entry.Path, "error", err)
				failed++
				continue
			}
			embedded++
		}
	}

	cmd.Printf("embed: %d embedded, %d failed, %d total\n", embedded, failed, len(stale))
	if failed > 0 {
		return fmt.Errorf("%d embedding(s) failed", failed)
	}
	return nil
}

func buildEmbedText(store interface{ Read(string) ([]byte, error) }, path string) string {
	data, err := store.Read(path)
	if err != nil {
		return path
	}
	content := string(data)

	title := path
	if idx := findTitle(content); idx != "" {
		title = idx
	}

	runes := []rune(content)
	if utf8.RuneCount(data) > embedTextRuneCap {
		runes = runes[:embedTextRuneCap]
	}

	return title + "\n\n" + string(runes)
}

func findTitle(content string) string {
	for i := 0; i < len(content); {
		j := i
		for j < len(content) && content[j] != '\n' {
			j++
		}
		line := content[i:j]
		if len(line) > 8 && line[:7] == "title: " {
			return line[7:]
		}
		if len(line) > 2 && line[:2] == "# " {
			return line[2:]
		}
		if j < len(content) {
			j++
		}
		i = j
	}
	return ""
}
