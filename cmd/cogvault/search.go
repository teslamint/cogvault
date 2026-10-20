package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/teslamint/cogvault/internal/terms"
)

func newSearchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Full-text search across indexed wiki files",
		Args:  cobra.MinimumNArgs(1),
		RunE:  runSearch,
	}
	cmd.Flags().Int("limit", 10, "max results (max 100)")
	return cmd
}

func runSearch(cmd *cobra.Command, args []string) error {
	configPath, err := resolveConfigPath(cmd)
	if err != nil {
		return err
	}

	_, store, idx, adpt, err := bootstrap(configPath)
	if err != nil {
		return err
	}
	defer idx.Close()

	// Bounded staleness — same policy as MCP handlers (0015-D3)
	_, _, _, ccErr := idx.CheckConsistency(store, adpt, false)
	if err := handleConsistencyResult(cmd, ccErr); err != nil {
		return fmt.Errorf("consistency check: %w", err)
	}

	query := strings.Join(args, " ")
	limit, _ := cmd.Flags().GetInt("limit")
	if limit <= 0 || limit > 100 {
		limit = 10
	}

	results, err := idx.Search(query, limit)
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}

	if len(results) == 0 {
		cmd.Println("No results found.")
		return nil
	}

	cmd.Println("공유 조건 기록입니다. recorded는 공유 허용 판정이 아닙니다.")
	for _, r := range results {
		typeStr := ""
		if r.Type != "" {
			if r.Category != "" {
				typeStr = fmt.Sprintf("  (%s/%s)", r.Type, r.Category)
			} else {
				typeStr = fmt.Sprintf("  (%s)", r.Type)
			}
		}
		cmd.Printf("%s  %s%s\n", r.Path, r.Title, typeStr)
		if r.Snippet != "" {
			cmd.Printf("  %s\n", r.Snippet)
		}
		content, readErr := store.Read(r.Path)
		var sharing terms.Summary
		if readErr != nil {
			sharing = terms.UnavailableSummary()
		} else {
			sharing = terms.Summarize(content)
		}
		cmd.Printf("  적용 대상: %s\n", sharingText(sharing.Scope))
		cmd.Printf("  어디까지: %s\n", sharingText(sharing.Where))
		cmd.Printf("  얼마나: %s\n", sharingText(sharing.Level))
		cmd.Printf("  어떻게: %s\n", sharingText(sharing.Conditions))
		cmd.Printf("  근거: %s\n", sharingText(sharing.Evidence.Reference))
		cmd.Printf("  검토: %s\n", sharing.Status)
		if len(sharing.Reasons) > 0 {
			cmd.Printf("  확인 사항: %s\n", strings.Join(sharing.Reasons, "; "))
		}
	}
	return nil
}

func sharingText(value string) string {
	if value == terms.Unknown {
		return "미확인"
	}
	return strings.Join(strings.Fields(value), " ")
}
