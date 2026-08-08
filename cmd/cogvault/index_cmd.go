package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newIndexCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "index",
		Short: "Generate a read-only _index.md listing all wiki pages",
		RunE:  runIndex,
	}
}

func runIndex(cmd *cobra.Command, args []string) error {
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

	var paths []string
	if err := adpt.Scan(cfg.WikiDir, cfg.AllExcluded(), func(path string) error {
		if path != "_schema.md" && path != "_index.md" {
			paths = append(paths, path)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("scan wiki: %w", err)
	}

	sort.Strings(paths)

	var b strings.Builder
	b.WriteString("---\ntitle: Index\ntype: index\n---\n\n")
	b.WriteString(fmt.Sprintf("# Wiki Index\n\n_Generated %s. %d pages._\n\n", time.Now().UTC().Format("2006-01-02"), len(paths)))

	for _, path := range paths {
		title := path
		if meta, metaErr := idx.GetMeta(path); metaErr == nil && meta.Title != "" {
			title = meta.Title
		}
		fmt.Fprintf(&b, "- [[%s|%s]]\n", strings.TrimSuffix(path, ".md"), title)
	}

	if err := store.Write("_index.md", []byte(b.String())); err != nil {
		return fmt.Errorf("write _index.md: %w", err)
	}

	_ = idx.Add("_index.md", b.String(), map[string]string{"title": "Index", "type": "index"})
	cmd.Printf("wrote _index.md (%d pages)\n", len(paths))
	return nil
}
