package main

import (
	"fmt"

	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"
	cogmcp "github.com/teslamint/cogvault/internal/mcp"
)

func newServeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the MCP server (stdio or SSE mode)",
		RunE:  runServe,
	}
	cmd.Flags().String("transport", "stdio", "transport mode: stdio or sse")
	cmd.Flags().String("addr", "localhost:8080", "listen address for SSE transport")
	return cmd
}

func runServe(cmd *cobra.Command, args []string) error {
	configPath, err := resolveConfigPath(cmd)
	if err != nil {
		return err
	}

	cfg, store, idx, adpt, err := bootstrap(configPath)
	if err != nil {
		return err
	}
	defer idx.Close()

	_, _, _, ccErr := idx.CheckConsistency(store, adpt, true)
	if err := handleConsistencyResult(cmd, ccErr); err != nil {
		return err
	}

	mcpSrv := cogmcp.NewServer(cfg.WikiDir, cfg, store, idx, adpt)

	transport, _ := cmd.Flags().GetString("transport")
	switch transport {
	case "sse":
		addr, _ := cmd.Flags().GetString("addr")
		sseSrv := server.NewSSEServer(mcpSrv, server.WithBaseURL(fmt.Sprintf("http://%s", addr)))
		cmd.Printf("SSE server listening on %s\n", addr)
		return sseSrv.Start(addr)
	default:
		return server.ServeStdio(mcpSrv)
	}
}
