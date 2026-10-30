package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/teslamint/cogvault/internal/wikilink"
)

func newGraphCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "graph",
		Short: "Output the wiki link graph as JSON (pages and edges)",
		RunE:  runGraph,
	}
}

type graphNode struct {
	Path  string `json:"path"`
	Title string `json:"title"`
	Type  string `json:"type"`
}

type graphEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

type graphOutput struct {
	Nodes []graphNode `json:"nodes"`
	Edges []graphEdge `json:"edges"`
}

func runGraph(cmd *cobra.Command, args []string) error {
	configPath, err := resolveConfigPath(cmd)
	if err != nil {
		return err
	}

	cfg, store, idx, adpt, err := bootstrap(configPath)
	if err != nil {
		return err
	}
	defer idx.Close()

	if _, _, _, ccErr := idx.CheckConsistency(store, adpt, true); ccErr != nil {
		cmd.PrintErrln("warning: consistency check:", ccErr)
	}

	var nodes []graphNode
	var edges []graphEdge
	var pages []string

	if err := adpt.Scan(cfg.WikiDir, cfg.AllExcluded(), func(path string) error {
		pages = append(pages, path)
		title := path
		typ := ""
		if meta, metaErr := idx.GetMeta(path); metaErr == nil && meta != nil {
			if meta.Title != "" {
				title = meta.Title
			}
			typ = meta.Type
		}
		nodes = append(nodes, graphNode{Path: path, Title: title, Type: typ})
		return nil
	}); err != nil {
		return fmt.Errorf("scan wiki: %w", err)
	}

	resolver := wikilink.NewSet(pages)
	for _, node := range nodes {
		src, parseErr := adpt.Parse(cfg.WikiDir, node.Path, false)
		if parseErr != nil {
			continue
		}
		for _, link := range src.Links {
			target := resolver.Resolve(link)
			if target != "" {
				edges = append(edges, graphEdge{Source: node.Path, Target: target})
			}
		}
	}

	out := graphOutput{Nodes: nodes, Edges: edges}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal graph: %w", err)
	}
	cmd.Println(string(data))
	return nil
}
