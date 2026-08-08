package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
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
	allPages := map[string]bool{}

	if err := adpt.Scan(cfg.WikiDir, cfg.AllExcluded(), func(path string) error {
		allPages[path] = true
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

	for _, node := range nodes {
		src, parseErr := adpt.Parse(cfg.WikiDir, node.Path, false)
		if parseErr != nil {
			continue
		}
		for _, link := range src.Links {
			target := resolveWikilink(link, allPages)
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
