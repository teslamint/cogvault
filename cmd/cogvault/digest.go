package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newDigestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "digest",
		Short: "Generate a periodic summary page from recently ingested sources",
		RunE:  runDigest,
	}
	cmd.Flags().Int("days", 7, "include sources ingested within this many days")
	return cmd
}

func runDigest(cmd *cobra.Command, args []string) error {
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

	days, _ := cmd.Flags().GetInt("days")
	cutoff := time.Now().AddDate(0, 0, -days)

	var recentPages []string
	if err := adpt.Scan(cfg.WikiDir, cfg.AllExcluded(), func(path string) error {
		if !strings.HasPrefix(path, "sources/") {
			return nil
		}
		meta, metaErr := idx.GetMeta(path)
		if metaErr != nil || meta == nil {
			return nil
		}
		t, tErr := time.Parse(time.RFC3339, meta.IndexedAt)
		if tErr != nil {
			return nil
		}
		if t.After(cutoff) {
			recentPages = append(recentPages, path)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("scan wiki: %w", err)
	}

	if len(recentPages) == 0 {
		cmd.Println("no recent sources to summarize")
		return nil
	}

	sort.Strings(recentPages)
	today := time.Now().UTC().Format("2006-01-02")
	slug := fmt.Sprintf("digests/weekly-%s.md", today)

	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: Weekly Digest %s\n", today)
	b.WriteString("type: synthesis\n")
	fmt.Fprintf(&b, "generated_at: %s\n", today)
	b.WriteString("---\n\n")
	fmt.Fprintf(&b, "# Weekly Digest — %s\n\n", today)
	fmt.Fprintf(&b, "_Recent %d day(s), %d source(s)._\n\n", days, len(recentPages))

	for _, path := range recentPages {
		title := path
		if meta, metaErr := idx.GetMeta(path); metaErr == nil && meta != nil && meta.Title != "" {
			title = meta.Title
		}
		link := strings.TrimSuffix(path, ".md")
		fmt.Fprintf(&b, "- [[%s|%s]]\n", link, title)
	}

	if err := store.Write(slug, []byte(b.String())); err != nil {
		return fmt.Errorf("write digest: %w", err)
	}
	_ = idx.Add(slug, b.String(), map[string]string{"title": fmt.Sprintf("Weekly Digest %s", today), "type": "synthesis"})

	cmd.Printf("wrote %s (%d sources)\n", slug, len(recentPages))
	return nil
}
