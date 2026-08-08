package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newSynthesizeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "synthesize",
		Short: "Generate concept pages from cross-references between source pages",
		RunE:  runSynthesize,
	}
}

func runSynthesize(cmd *cobra.Command, args []string) error {
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

	linkIndex := map[string][]string{}
	tagIndex := map[string][]string{}

	if err := adpt.Scan(cfg.WikiDir, cfg.AllExcluded(), func(path string) error {
		src, parseErr := adpt.Parse(cfg.WikiDir, path, false)
		if parseErr != nil {
			return nil
		}
		for _, link := range src.Links {
			linkIndex[link] = append(linkIndex[link], path)
		}
		for _, tag := range src.Tags {
			tagIndex[tag] = append(tagIndex[tag], path)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("scan wiki: %w", err)
	}

	created := 0
	today := time.Now().UTC().Format("2006-01-02")

	for concept, pages := range linkIndex {
		if len(pages) < 2 {
			continue
		}
		slug := fmt.Sprintf("concepts/%s.md", strings.ToLower(strings.ReplaceAll(concept, " ", "-")))
		if exists, _ := store.Exists(slug); exists {
			continue
		}

		sort.Strings(pages)
		var b strings.Builder
		b.WriteString("---\n")
		fmt.Fprintf(&b, "title: %s\n", concept)
		b.WriteString("type: concept\n")
		fmt.Fprintf(&b, "generated_at: %s\n", today)
		b.WriteString("---\n\n")
		fmt.Fprintf(&b, "# %s\n\n", concept)
		fmt.Fprintf(&b, "_Referenced by %d pages._\n\n", len(pages))
		for _, page := range pages {
			title := page
			if meta, metaErr := idx.GetMeta(page); metaErr == nil && meta != nil && meta.Title != "" {
				title = meta.Title
			}
			fmt.Fprintf(&b, "- [[%s|%s]]\n", strings.TrimSuffix(page, ".md"), title)
		}

		if err := store.Write(slug, []byte(b.String())); err != nil {
			cmd.PrintErrln("warning: write", slug, err)
			continue
		}
		_ = idx.Add(slug, b.String(), map[string]string{"title": concept, "type": "concept"})
		created++
	}

	for tag, pages := range tagIndex {
		if len(pages) < 2 {
			continue
		}
		slug := fmt.Sprintf("concepts/tag-%s.md", strings.ToLower(strings.ReplaceAll(tag, "/", "-")))
		if exists, _ := store.Exists(slug); exists {
			continue
		}

		sort.Strings(pages)
		var b strings.Builder
		b.WriteString("---\n")
		fmt.Fprintf(&b, "title: \"#%s\"\n", tag)
		b.WriteString("type: concept\n")
		fmt.Fprintf(&b, "generated_at: %s\n", today)
		b.WriteString("---\n\n")
		fmt.Fprintf(&b, "# #%s\n\n", tag)
		fmt.Fprintf(&b, "_Tagged in %d pages._\n\n", len(pages))
		for _, page := range pages {
			title := page
			if meta, metaErr := idx.GetMeta(page); metaErr == nil && meta != nil && meta.Title != "" {
				title = meta.Title
			}
			fmt.Fprintf(&b, "- [[%s|%s]]\n", strings.TrimSuffix(page, ".md"), title)
		}

		if err := store.Write(slug, []byte(b.String())); err != nil {
			cmd.PrintErrln("warning: write", slug, err)
			continue
		}
		_ = idx.Add(slug, b.String(), map[string]string{"title": "#" + tag, "type": "concept"})
		created++
	}

	cmd.Printf("synthesize: created %d concept pages\n", created)
	return nil
}
