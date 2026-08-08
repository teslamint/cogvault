package main

import (
	"errors"
	"fmt"
	"os/exec"

	"github.com/spf13/cobra"
	"github.com/teslamint/cogvault/internal/ingest"
	"github.com/teslamint/cogvault/internal/llm"
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
	return nil
}
