package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/teslamint/cogvault/internal/adapter"
	"github.com/teslamint/cogvault/internal/config"
	"github.com/teslamint/cogvault/internal/evidence"
	"github.com/teslamint/cogvault/internal/index"
	"github.com/teslamint/cogvault/internal/storage"
	"github.com/teslamint/cogvault/internal/wikilink"
)

func newLintCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lint",
		Short: "Check wiki for broken links, orphan pages, and frontmatter issues",
		RunE:  runLint,
	}
	cmd.Flags().Bool("strict", false, "exit nonzero when issues are found (output is unchanged)")
	cmd.Flags().Bool("json", false, "print issues as JSON")
	return cmd
}

type lintIssue struct {
	Kind     string `json:"kind"`
	Path     string `json:"path"`
	Message  string `json:"message"`
	ID       string `json:"id"`
	ClaimID  string `json:"claim_id"`
	Severity string `json:"severity"`
	FixHint  string `json:"fix_hint"`
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

	issues, err := collectLintIssues(cfg, store, idx, adpt)
	if err != nil {
		return err
	}

	jsonMode, _ := cmd.Flags().GetBool("json")
	if jsonMode {
		if issues == nil {
			issues = []lintIssue{}
		}
		if err := json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
			Issues []lintIssue `json:"issues"`
		}{Issues: issues}); err != nil {
			return fmt.Errorf("encode lint issues: %w", err)
		}
	} else if len(issues) == 0 {
		cmd.Println("lint: no issues found")
	} else {
		for _, issue := range issues {
			message := issue.Message
			if issue.Kind == "evidence" {
				message = evidence.Diagnostic{
					ID: issue.ID, Path: issue.Path, ClaimID: issue.ClaimID,
					Severity: issue.Severity, Message: issue.Message, FixHint: issue.FixHint,
				}.Text()
			}
			cmd.Printf("%-15s %-40s %s\n", issue.Kind, issue.Path, message)
		}
		cmd.Printf("\n%d issue(s) found\n", len(issues))
	}

	if len(issues) > 0 {
		if strict, _ := cmd.Flags().GetBool("strict"); strict {
			return errLintIssuesFound
		}
	}
	return nil
}

// collectLintIssues takes storage as an interface so tests can inject read
// failures that the adapter's own file read does not see.
func collectLintIssues(cfg *config.Config, store storage.Storage, idx *index.SQLiteIndex, adpt adapter.Adapter) ([]lintIssue, error) {
	allPages := map[string]bool{}
	if err := adpt.Scan(cfg.WikiDir, cfg.AllExcluded(), func(path string) error {
		allPages[path] = true
		return nil
	}); err != nil {
		return nil, fmt.Errorf("scan wiki: %w", err)
	}

	var issues []lintIssue
	linked := map[string]bool{}
	pages := make([]string, 0, len(allPages))
	for page := range allPages {
		pages = append(pages, page)
	}
	resolver := wikilink.NewSet(pages)

	for page := range allPages {
		// A read failure skips only the evidence check: the page's links must
		// still count, or the pages it links to are reported as orphans.
		content, readErr := store.Read(page)
		if readErr != nil {
			issues = append(issues, lintIssue{Path: page, Kind: "parse", Message: readErr.Error()})
		} else {
			for _, diagnostic := range evidence.Validate(page, content, store) {
				issues = append(issues, lintIssue{
					Kind: "evidence", Path: diagnostic.Path, Message: diagnostic.Message,
					ID: diagnostic.ID, ClaimID: diagnostic.ClaimID, Severity: diagnostic.Severity, FixHint: diagnostic.FixHint,
				})
			}
		}

		src, parseErr := adpt.Parse(cfg.WikiDir, page, false)
		if parseErr != nil {
			if readErr == nil {
				issues = append(issues, lintIssue{Path: page, Kind: "parse", Message: parseErr.Error()})
			}
			continue
		}

		// _schema.md defines the page contract; it is not a page written under
		// it. It is write-protected, excluded from `cogvault index`, and already
		// exempt from the orphan check below, so holding it to the frontmatter
		// rules it specifies is a category error — and a fresh `cogvault init`
		// would otherwise never be lint-clean. Its own correctness is covered by
		// TestDefaultContentFitsMaxSchemaLen and the init test, not here. Links
		// in it are still checked: a genuinely broken one is still a defect.
		if page != "_schema.md" {
			if src.Title == "" {
				issues = append(issues, lintIssue{Path: page, Kind: "frontmatter", Message: "missing title"})
			}
			if _, ok := src.Frontmatter["type"]; !ok {
				issues = append(issues, lintIssue{Path: page, Kind: "frontmatter", Message: "missing type field"})
			}
			typ, _ := src.Frontmatter["type"].(string)
			if typ == "source" {
				for _, field := range []string{"source_path", "ingested_at"} {
					if _, ok := src.Frontmatter[field]; !ok {
						issues = append(issues, lintIssue{Path: page, Kind: "frontmatter", Message: "source page missing " + field})
					}
				}
			}
		}

		for _, link := range src.Links {
			target := resolver.Resolve(link)
			if target != "" {
				linked[target] = true
			} else {
				issues = append(issues, lintIssue{Path: page, Kind: "broken-link", Message: fmt.Sprintf("[[%s]] not found", link)})
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
			issues = append(issues, lintIssue{Path: page, Kind: "orphan", Message: "no incoming links"})
		}
	}

	sort.SliceStable(issues, func(i, j int) bool {
		left, right := issues[i], issues[j]
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		if left.ClaimID != right.ClaimID {
			return left.ClaimID < right.ClaimID
		}
		if left.ID != right.ID {
			return left.ID < right.ID
		}
		return left.Message < right.Message
	})
	return issues, nil
}

// errLintIssuesFound carries no count: the report on stdout already has it, and
// --strict is defined to change only the exit code.
var errLintIssuesFound = errors.New("lint: issues found")
