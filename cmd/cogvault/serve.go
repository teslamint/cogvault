package main

import (
	"errors"

	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"
	cogmcp "github.com/teslamint/cogvault/internal/mcp"

	"github.com/teslamint/cogvault/internal/index"
)

func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Start the MCP server in stdio mode",
		RunE:  runServe,
	}
}

func runServe(cmd *cobra.Command, args []string) error {
	vaultRoot, err := resolveVaultRoot(cmd)
	if err != nil {
		return err
	}

	cfg, store, idx, adpt, err := bootstrap(vaultRoot)
	if err != nil {
		return err
	}
	defer idx.Close()

	_, _, _, ccErr := idx.CheckConsistency(store, adpt, true)
	if ccErr != nil {
		if errors.Is(ccErr, index.ErrConsistencySystemic) {
			return ccErr
		}
		cmd.PrintErrln("warning: some files had errors during consistency check:", ccErr)
	}

	mcpSrv := cogmcp.NewServer(vaultRoot, cfg, store, idx, adpt)
	return server.ServeStdio(mcpSrv)
}
