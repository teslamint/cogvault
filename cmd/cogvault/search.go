package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newSearchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Full-text search across indexed vault files",
		Args:  cobra.MinimumNArgs(1),
		RunE:  runSearch,
	}
	cmd.Flags().String("scope", "all", "search scope: all, wiki, or vault")
	cmd.Flags().Int("limit", 10, "max results (max 100)")
	return cmd
}

func runSearch(cmd *cobra.Command, args []string) error {
	vaultRoot, err := resolveVaultRoot(cmd)
	if err != nil {
		return err
	}

	_, store, idx, adpt, err := bootstrap(vaultRoot)
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
	scope, _ := cmd.Flags().GetString("scope")
	limit, _ := cmd.Flags().GetInt("limit")
	if limit <= 0 || limit > 100 {
		limit = 10
	}

	results, err := idx.Search(query, limit, scope)
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}

	if len(results) == 0 {
		cmd.Println("No results found.")
		return nil
	}

	for _, r := range results {
		typeStr := ""
		if r.Type != "" {
			typeStr = fmt.Sprintf("  (%s)", r.Type)
		}
		cmd.Printf("%s  %s%s\n", r.Path, r.Title, typeStr)
		if r.Snippet != "" {
			cmd.Printf("  %s\n", r.Snippet)
		}
	}
	return nil
}
