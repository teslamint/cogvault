package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/teslamint/cogvault/internal/config"
	"github.com/teslamint/cogvault/internal/storage"
	"github.com/teslamint/cogvault/internal/terms"
)

func newTermsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "terms <path>",
		Short: "Inspect recorded usage terms and review status, not legal permission",
		Args:  cobra.ExactArgs(1),
		RunE:  runTerms,
	}
}

func runTerms(cmd *cobra.Command, args []string) error {
	configPath, err := resolveConfigPath(cmd)
	if err != nil {
		return err
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	// Deliberately avoid bootstrap: inspection must not open or repair a DB.
	store := storage.NewFSStorage(cfg.WikiDir, cfg)
	data, err := store.Read(args[0])
	if err != nil {
		return err
	}
	fm, _, parseErr := terms.Parse(data)
	status, reasons := terms.Inspect(fm["usage_terms"])
	if parseErr != nil {
		status = "unverified"
		reasons = []string{"invalid YAML frontmatter; usage_terms could not be read"}
	}
	record, err := jsonValue(fm["usage_terms"])
	if err != nil {
		return err
	}
	result := struct {
		Path    string   `json:"path"`
		Status  string   `json:"status"`
		Record  any      `json:"record"`
		Reasons []string `json:"reasons"`
		Notice  string   `json:"notice"`
	}{
		Path: args[0], Status: status, Record: record, Reasons: reasons,
		Notice: "recorded means a complete human record, not permission or a legal determination. Evidence truth and applicability require human review. URLs, free access, and subscriptions do not establish permission.",
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

// jsonValue stringifies YAML mapping keys; Go 1.26 encoding/json rejects map[any]any.
// Distinct keys with the same text (1 and 1.0) are an error rather than a silent overwrite.
func jsonValue(value any) (any, error) {
	switch v := value.(type) {
	case map[any]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			text := fmt.Sprint(key)
			if _, exists := out[text]; exists {
				return nil, fmt.Errorf("usage_terms has distinct YAML keys that both print as %q", text)
			}
			converted, err := jsonValue(item)
			if err != nil {
				return nil, err
			}
			out[text] = converted
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			converted, err := jsonValue(item)
			if err != nil {
				return nil, err
			}
			out[key] = converted
		}
		return out, nil
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			converted, err := jsonValue(item)
			if err != nil {
				return nil, err
			}
			out[i] = converted
		}
		return out, nil
	}
	return value, nil
}
