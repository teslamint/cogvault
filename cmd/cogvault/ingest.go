package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"time"

	"github.com/spf13/cobra"
	"github.com/teslamint/cogvault/internal/gitutil"
	"github.com/teslamint/cogvault/internal/index"
	"github.com/teslamint/cogvault/internal/ingest"
	"github.com/teslamint/cogvault/internal/llm"
	"github.com/teslamint/cogvault/internal/storage"
)

type reportNotifier interface {
	Notify(*ingest.Report)
}

var runIngestNotify = notifyAfterRun

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
		timeout := time.Duration(cfg.LLM.TimeoutSeconds) * time.Second
		opts := []llm.Option{llm.WithTimeout(timeout)}
		switch cfg.LLM.Backend {
		case "ollama":
			adpt = llm.NewOllama(cfg.LLM.BaseURL, cfg.LLM.Model, opts...)
		default:
			binPath, err := exec.LookPath("claude")
			if err != nil {
				return fmt.Errorf("claude CLI not found in PATH; install Claude Code or add it to PATH")
			}
			adpt = llm.NewClaudeCode(binPath, cfg.LLM.Model, opts...)
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
	runIngestNotify(runner, report, scheduled, err)
	if err != nil {
		if errors.Is(err, ingest.ErrAlreadyRunning) {
			return fmt.Errorf("ingest already running (lock held)")
		}
		return err
	}

	if !dryRun && cfg.LLM.EmbeddingModel != "" && report != nil && report.Digested > 0 {
		postIngestEmbed(cmd, idx, store, cfg.LLM.EmbeddingModel, cfg.LLM.EmbeddingBaseURL)
	}
	if !dryRun && cfg.Git.CommitsOnIngest() && report != nil && report.Digested > 0 {
		postIngestGitCommit(cmd, cfg.WikiDir)
	}
	return nil
}

func notifyAfterRun(notifier reportNotifier, report *ingest.Report, scheduled bool, runErr error) {
	if scheduled && report != nil && runErr == nil {
		notifier.Notify(report)
	}
}

func postIngestEmbed(cmd *cobra.Command, idx *index.SQLiteIndex, store *storage.FSStorage, model, baseURL string) {
	if err := idx.InitEmbeddingsTable(); err != nil {
		slog.Error("post-ingest embed: init table", "error", err)
		return
	}

	stale, err := idx.StalePaths(model)
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

	res := batchEmbed(cmd.Context(), idx, store, embedder, model, stale, 32)
	cmd.Printf("post-ingest embed: %d embedded, %d skipped, %d failed\n", res.embedded, res.skipped, res.failed)
	if res.failed > 0 {
		cmd.PrintErrln("warning: some post-ingest embeddings failed")
	}
}

// postIngestGitCommit commits the whole wiki tree once after a successful
// ingest run that digested at least one file (git.auto_commit: write+ingest,
// 0024). Best-effort: failures log, never fail the ingest command — same
// contract as wiki_write/wiki_delete's per-file auto-commit.
//
// Serialization, per-command timeouts, and SIGTERM-with-grace termination
// live in internal/gitutil, shared with internal/mcp's per-file commits: a
// scheduled ingest can run while a `cogvault serve` process is handling
// wiki_write calls against the same repository, and git refuses concurrent
// index operations, so without a shared lock one side's commit would be
// silently dropped.
func postIngestGitCommit(cmd *cobra.Command, wikiDir string) {
	// -- . scopes the add to wikiDir's own working directory: without it, a
	// wikiDir nested inside a larger git repository (wikiDir is a plain
	// subdirectory, not its own git root, in that layout) would stage
	// changes anywhere in the enclosing repo's working tree, since `git -C
	// wikiDir add -A` still resolves against the repo root, not wikiDir.
	stage, err := gitutil.Commit(cmd.Context(), wikiDir, []string{"-A", "--", "."}, "wiki: ingest snapshot")
	if err == nil {
		return
	}
	switch stage {
	case gitutil.StageLock:
		slog.Warn("post-ingest git commit lock unavailable", "error", err)
	case gitutil.StageAdd:
		slog.Warn("post-ingest git add failed", "error", err)
	default:
		// A no-op ingest tree (all digests wrote identical content, or the
		// working tree already matched) makes "nothing to commit" exit
		// nonzero; that is expected, not a real failure, so it only logs.
		slog.Warn("post-ingest git commit failed", "error", err)
	}
}
