package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"

	"github.com/spf13/cobra"
	"github.com/teslamint/cogvault/internal/index"
	"github.com/teslamint/cogvault/internal/ingest"
	"github.com/teslamint/cogvault/internal/llm"
	"github.com/teslamint/cogvault/internal/storage"
)

func newIngestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ingest",
		Short: "Digest configured source files into wiki pages",
		RunE:  runIngest,
	}
	cmd.Flags().Bool("dry-run", false, "list files that would be digested without writing")
	cmd.Flags().Int("limit", 0, "max files to digest this run (0 = no limit)")
	cmd.Flags().Bool("scheduled", false, "mark this run as scheduled (used by the launchd plist)")
	return cmd
}

func runIngest(cmd *cobra.Command, args []string) error {
	configPath, err := resolveConfigPath(cmd)
	if err != nil {
		return err
	}

	cfg, store, idx, _, err := bootstrap(configPath)
	if err != nil {
		return err
	}
	defer idx.Close()

	dryRun, _ := cmd.Flags().GetBool("dry-run")

	var adpt llm.Adapter
	if !dryRun {
		switch cfg.LLM.Backend {
		case "ollama":
			adpt = llm.NewOllama(cfg.LLM.BaseURL, cfg.LLM.Model)
		default:
			binPath, err := exec.LookPath("claude")
			if err != nil {
				return fmt.Errorf("claude CLI not found in PATH; install Claude Code or add it to PATH")
			}
			adpt = llm.NewClaudeCode(binPath, cfg.LLM.Model)
		}
	}

	runner, err := ingest.New(cfg, store, idx, adpt, cfg.DBPath)
	if err != nil {
		return err
	}
	defer runner.Close()

	scheduled, _ := cmd.Flags().GetBool("scheduled")
	origin := "interactive"
	if scheduled {
		origin = "scheduled"
	}
	limit, _ := cmd.Flags().GetInt("limit")

	report, err := runner.Run(cmd.Context(), ingest.RunOptions{
		DryRun: dryRun,
		Limit:  limit,
		Origin: origin,
	})
	if report != nil {
		cmd.Print(report.String())
	}
	if err != nil {
		if errors.Is(err, ingest.ErrAlreadyRunning) {
			return fmt.Errorf("ingest already running (lock held)")
		}
		return err
	}

	if !dryRun && cfg.LLM.EmbeddingModel != "" && report != nil && report.Digested > 0 {
		postIngestEmbed(cmd, idx, store, cfg.LLM.EmbeddingModel, cfg.LLM.EmbeddingBaseURL)
	}
	return nil
}

func postIngestEmbed(cmd *cobra.Command, idx *index.SQLiteIndex, store *storage.FSStorage, model, baseURL string) {
	sqIdx := idx
	if err := sqIdx.InitEmbeddingsTable(); err != nil {
		slog.Error("post-ingest embed: init table", "error", err)
		return
	}

	stale, err := sqIdx.StalePaths(model)
	if err != nil {
		slog.Error("post-ingest embed: find stale", "error", err)
		return
	}
	if len(stale) == 0 {
		return
	}

	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	embedder := llm.NewOllamaEmbedder(baseURL, model)

	texts := make([]string, len(stale))
	for i, entry := range stale {
		texts[i] = buildEmbedText(store, entry.Path)
	}

	vecs, err := embedder.Embed(context.Background(), texts)
	if err != nil {
		slog.Error("post-ingest embed: embed call failed", "error", err)
		cmd.PrintErrln("warning: post-ingest embedding failed:", err)
		return
	}

	embedded := 0
	for i, entry := range stale {
		if err := sqIdx.StoreEmbedding(entry.Path, entry.ContentHash, model, vecs[i]); err != nil {
			slog.Error("post-ingest embed: store", "path", entry.Path, "error", err)
			continue
		}
		embedded++
	}
	cmd.Printf("post-ingest embed: %d/%d pages embedded\n", embedded, len(stale))
}
