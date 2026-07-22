package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "cogvault",
		Short:         "personal knowledge pipeline: ingest sources, serve a searchable wiki over MCP",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.PersistentFlags().String("config", "", "path to config file (default ~/.config/cogvault/config.yaml)")
	cmd.AddCommand(newInitCmd())
	cmd.AddCommand(newSearchCmd())
	cmd.AddCommand(newServeCmd())
	cmd.AddCommand(newIngestCmd())
	cmd.SetOut(os.Stdout)
	return cmd
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
