package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/teslamint/cogvault/internal/config"
	"github.com/teslamint/cogvault/internal/ingest"
)

type statusOutput struct {
	Attention []ingest.AttentionRow `json:"attention"`
	Model     string                `json:"model"`
}

func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show ingest items that need attention",
		RunE:  runStatus,
	}
	cmd.Flags().Bool("json", false, "output status as JSON")
	return cmd
}

func runStatus(cmd *cobra.Command, args []string) error {
	configPath, err := resolveConfigPath(cmd)
	if err != nil {
		return err
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	rows, err := ingest.AttentionRows(cfg.DBPath, cfg.LLM.Model)
	if err != nil {
		return err
	}

	jsonOutput, _ := cmd.Flags().GetBool("json")
	if jsonOutput {
		if rows == nil {
			rows = []ingest.AttentionRow{}
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(statusOutput{
			Attention: rows,
			Model:     cfg.LLM.Model,
		})
	}

	if len(rows) == 0 {
		cmd.Println("주의 필요 항목 없음.")
		return nil
	}

	cmd.Printf("주의 필요: %d건\n", len(rows))
	for _, row := range rows {
		attemptedAt, err := time.Parse(time.RFC3339Nano, row.LastAttempt)
		if err != nil {
			return fmt.Errorf("invalid last_attempt %q for %s: %w", row.LastAttempt, row.Path, err)
		}
		cmd.Printf(
			"  %s  %s  %s  (%s)\n",
			row.Status,
			filepath.Base(row.Path),
			row.Error,
			attemptedAt.In(time.Local).Format("2006-01-02 15:04"),
		)
	}
	return nil
}
