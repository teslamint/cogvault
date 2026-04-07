package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "cogvault",
		Short:         "MCP tool server for building wiki knowledge bases on Obsidian vaults",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.PersistentFlags().String("vault", "", "path to vault root (default: current directory)")
	cmd.AddCommand(newInitCmd())
	cmd.AddCommand(newSearchCmd())
	cmd.AddCommand(newServeCmd())
	return cmd
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
