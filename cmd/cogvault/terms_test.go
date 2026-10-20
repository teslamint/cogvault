package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// All terms in this file are fictional test settings, not evidence for real rights.
const fictionalTerms = `status: recorded
purpose: external submission of the synthetic summary
conditions: attribution required; original redistribution prohibited
evidence:
  reference: fictional-policy.md#summary
  quote: Synthetic summaries may be submitted with attribution.
reviewed_by: fictional-reviewer
reviewed_at: "2026-10-20"
`

func TestTermsInspection(t *testing.T) {
	cases := []struct {
		name, record, status, reason string
	}{
		{"complete record", fictionalTerms, "recorded", ""},
		{"absent", "", "unverified", "usage_terms"},
		{"explicitly unverified", "status: unverified\n", "unverified", "unverified"},
		{"URL alone", "status: recorded\nevidence:\n  reference: https://example.invalid/article\n", "unverified", "quote"},
		{"missing evidence quote", strings.ReplaceAll(fictionalTerms, "  quote: Synthetic summaries may be submitted with attribution.\n", ""), "unverified", "quote"},
		{"invalid date", strings.ReplaceAll(fictionalTerms, "2026-10-20", "2026-02-30"), "unverified", "reviewed_at"},
		{"wrong field type", strings.ReplaceAll(fictionalTerms, "fictional-reviewer", "[fictional-reviewer]"), "unverified", "reviewed_by"},
		{"unknown status", strings.ReplaceAll(fictionalTerms, "status: recorded", "status: allowed"), "unverified", "status"},
		{"scalar record", "free access", "unverified", "usage_terms"},
		{"complete but ambiguous", strings.ReplaceAll(fictionalTerms, "status: recorded", "status: unverified"), "unverified", "unverified"},
		{"whitespace evidence", strings.ReplaceAll(fictionalTerms, "Synthetic summaries may be submitted with attribution.", "\"  \""), "unverified", "quote"},
		{"malformed YAML", "status: [\n", "unverified", "invalid YAML"},
		{"non-string mapping key", "status: recorded\nevidence:\n  ? [a, b]\n  : synthetic\n", "unverified", "invalid YAML"},
		{"numeric-key malformed conditions", "status: unverified\nconditions:\n  1: Retain the fictional notice.\n", "unverified", "conditions"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configPath, wikiDir, dbPath := testVault(t)
			content := "---\ntitle: Fictional example\n"
			if tc.record != "" {
				if tc.name == "scalar record" {
					content += "usage_terms: " + tc.record + "\n"
				} else {
					content += "usage_terms:\n  " + strings.ReplaceAll(strings.TrimSuffix(tc.record, "\n"), "\n", "\n  ") + "\n"
				}
			}
			content += "---\nFictional terms for validation only; not evidence for real material.\n"
			page := filepath.Join(wikiDir, "example.md")
			if err := os.WriteFile(page, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			stdout, _, err := executeCommand("terms", "example.md", "--config", configPath)
			if err != nil {
				t.Fatalf("terms: %v", err)
			}
			var got struct {
				Status  string   `json:"status"`
				Record  any      `json:"record"`
				Reasons []string `json:"reasons"`
				Notice  string   `json:"notice"`
			}
			if err := json.Unmarshal([]byte(stdout), &got); err != nil {
				t.Fatal(err)
			}
			if got.Status != tc.status || (tc.reason != "" && !strings.Contains(strings.Join(got.Reasons, ";"), tc.reason)) {
				t.Fatalf("inspection = %s; want %s, reason containing %q", stdout, tc.status, tc.reason)
			}
			record, _ := got.Record.(map[string]any)
			if tc.status == "recorded" && (record["conditions"] != "attribution required; original redistribution prohibited" || record["purpose"] != "external submission of the synthetic summary") {
				t.Fatalf("lost human conditions or purpose: %s", stdout)
			}
			if !strings.Contains(got.Notice, "not permission") {
				t.Fatalf("missing non-permission notice: %s", stdout)
			}
			if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
				t.Fatalf("read-only inspector opened/created DB: %v", err)
			}
			after, err := os.ReadFile(page)
			if err != nil || string(after) != content {
				t.Fatalf("inspector modified page: %v", err)
			}
		})
	}
}

func TestTermsRespectsStorageBoundary(t *testing.T) {
	configPath, wikiDir, _ := testVault(t)
	outside := filepath.Join(filepath.Dir(wikiDir), "outside.md")
	if err := os.WriteFile(outside, []byte("private sentinel"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(wikiDir, "link.md")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../outside.md", outside, "link.md", "missing.md"} {
		out, _, err := executeCommand("terms", path, "--config", configPath)
		if err == nil || strings.Contains(out, "private sentinel") {
			t.Fatalf("terms %q: output=%q error=%v", path, out, err)
		}
	}
	f, err := os.OpenFile(configPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("exclude_read: [hidden.md]\n")
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wikiDir, "hidden.md"), []byte("private sentinel"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err := executeCommand("terms", "hidden.md", "--config", configPath)
	if err == nil || strings.Contains(out, "private sentinel") {
		t.Fatalf("excluded page: output=%q error=%v", out, err)
	}
}
