package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func newLintCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "lint",
		Short: "Check wiki for broken links, orphan pages, and frontmatter issues",
		RunE:  runLint,
	}
}

type lintIssue struct {
	Path    string
	Kind    string
	Message string
}

func runLint(cmd *cobra.Command, args []string) error {
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

	allPages := map[string]bool{}
	if err := adpt.Scan(cfg.WikiDir, cfg.AllExcluded(), func(path string) error {
		allPages[path] = true
		return nil
	}); err != nil {
		return fmt.Errorf("scan wiki: %w", err)
	}

	var issues []lintIssue
	linked := map[string]bool{}

	for page := range allPages {
		src, parseErr := adpt.Parse(cfg.WikiDir, page, false)
		if parseErr != nil {
			issues = append(issues, lintIssue{page, "parse", parseErr.Error()})
			continue
		}

		if src.Title == "" {
			issues = append(issues, lintIssue{page, "frontmatter", "missing title"})
		}
		if _, ok := src.Frontmatter["type"]; !ok {
			issues = append(issues, lintIssue{page, "frontmatter", "missing type field"})
		}
		typ, _ := src.Frontmatter["type"].(string)
		if typ == "source" {
			for _, field := range []string{"source_path", "ingested_at"} {
				if _, ok := src.Frontmatter[field]; !ok {
					issues = append(issues, lintIssue{page, "frontmatter", "source page missing " + field})
				}
			}
		}

		for _, link := range src.Links {
			target := resolveWikilink(link, allPages)
			if target != "" {
				linked[target] = true
			} else {
				issues = append(issues, lintIssue{page, "broken-link", fmt.Sprintf("[[%s]] not found", link)})
			}
		}
	}

	for page := range allPages {
		if page == "_schema.md" || page == "_index.md" {
			continue
		}
		if !linked[page] {
			meta, metaErr := idx.GetMeta(page)
			typ := ""
			if metaErr == nil && meta != nil {
				typ = meta.Type
			}
			if typ == "source" {
				continue
			}
			issues = append(issues, lintIssue{page, "orphan", "no incoming links"})
		}
	}

	if len(issues) == 0 {
		cmd.Println("lint: no issues found")
		return nil
	}

	for _, issue := range issues {
		cmd.Printf("%-15s %-40s %s\n", issue.Kind, issue.Path, issue.Message)
	}
	cmd.Printf("\n%d issue(s) found\n", len(issues))
	return nil
}

func resolveWikilink(link string, allPages map[string]bool) string {
	candidates := []string{
		link + ".md",
		filepath.Join("sources", link+".md"),
		link,
	}
	for _, c := range candidates {
		if allPages[c] {
			return c
		}
	}
	for page := range allPages {
		base := strings.TrimSuffix(filepath.Base(page), ".md")
		if strings.EqualFold(base, link) {
			return page
		}
	}
	return ""
}
