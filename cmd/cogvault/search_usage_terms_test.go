package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchShowsSharingConditionsWithoutDetailLookup(t *testing.T) {
	config, wiki, _ := testVault(t)
	content, err := os.ReadFile(filepath.Join("..", "..", "testdata", "terms-review-demo", "wiki", "search-ready.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wiki, "example.md"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCommand("init", "--config", config); err != nil {
		t.Fatal(err)
	}
	out, _, err := executeCommand("search", "search-terms-demo", "--config", config)
	if err != nil {
		t.Fatal(err)
	}
	for _, information := range []string{
		"this fictional summary only; not the original",
		"external exercise reviewers",
		"synthetic summary only",
		"attribution required; redistribution of the fictional original prohibited",
		"fictional-policy.md#synthetic-summaries",
		"recorded",
	} {
		if !strings.Contains(out, information) {
			t.Errorf("search omitted %q without a detail lookup:\n%s", information, out)
		}
	}
}

func TestSearchMakesMissingSharingInformationVisible(t *testing.T) {
	config, wiki, _ := testVault(t)
	content := "---\ntitle: Fictional unknown\n---\nsearch-unknown-demo\nFictional validation only; not rights evidence.\n"
	if err := os.WriteFile(filepath.Join(wiki, "unknown.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCommand("init", "--config", config); err != nil {
		t.Fatal(err)
	}
	out, _, err := executeCommand("search", "search-unknown-demo", "--config", config)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "미확인") || !strings.Contains(out, "unverified") {
		t.Fatalf("search left unknown sharing information implicit:\n%s", out)
	}
}
