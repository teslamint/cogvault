package main

import (
	"context"
	"fmt"
	"log/slog"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"github.com/teslamint/cogvault/internal/index"
	"github.com/teslamint/cogvault/internal/llm"
	"github.com/teslamint/cogvault/internal/storage"
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

	if err := idx.InitEmbeddingsTable(); err != nil {
		return err
	}

	if _, _, _, ccErr := idx.CheckConsistency(store, adpt, true); ccErr != nil {
		cmd.PrintErrln("warning: consistency check:", ccErr)
	}

	stale, err := idx.StalePaths(cfg.LLM.EmbeddingModel)
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

	result := batchEmbed(cmd.Context(), idx, store, embedder, cfg.LLM.EmbeddingModel, stale, batchSize)
	cmd.Printf("embed: %d embedded, %d skipped, %d failed, %d total\n", result.embedded, result.skipped, result.failed, len(stale))
	if result.failed > 0 {
		return fmt.Errorf("%d embedding(s) failed", result.failed)
	}
	return nil
}

type embedResult struct {
	embedded int
	skipped  int
	failed   int
}

func batchEmbed(ctx context.Context, sqIdx *index.SQLiteIndex, store *storage.FSStorage, embedder llm.Embedder, model string, stale []index.StaleEntry, batchSize int) embedResult {
	var res embedResult

	for i := 0; i < len(stale); i += batchSize {
		end := i + batchSize
		if end > len(stale) {
			end = len(stale)
		}
		batch := stale[i:end]

		var texts []string
		var valid []index.StaleEntry
		for _, entry := range batch {
			text, err := buildEmbedText(store, entry.Path)
			if err != nil {
				slog.Warn("embed: read failed, skipping", "path", entry.Path, "error", err)
				res.skipped++
				continue
			}
			texts = append(texts, text)
			valid = append(valid, entry)
		}

		if len(texts) == 0 {
			continue
		}

		vecs, err := embedder.Embed(ctx, texts)
		if err != nil {
			slog.Error("embed batch failed", "start", i, "error", err)
			res.failed += len(valid)
			continue
		}

		for j, entry := range valid {
			if err := sqIdx.StoreEmbedding(entry.Path, entry.ContentHash, model, vecs[j]); err != nil {
				slog.Error("store embedding failed", "path", entry.Path, "error", err)
				res.failed++
				continue
			}
			res.embedded++
		}
	}
	return res
}

func buildEmbedText(store *storage.FSStorage, path string) (string, error) {
	data, err := store.Read(path)
	if err != nil {
		return "", err
	}
	content := string(data)

	title := path
	if t := findTitle(content); t != "" {
		title = t
	}

	runes := []rune(content)
	if utf8.RuneCount(data) > embedTextRuneCap {
		runes = runes[:embedTextRuneCap]
	}

	return title + "\n\n" + string(runes), nil
}

func findTitle(content string) string {
	for i := 0; i < len(content); {
		j := i
		for j < len(content) && content[j] != '\n' {
			j++
		}
		line := content[i:j]
		if len(line) > 7 && line[:7] == "title: " {
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
