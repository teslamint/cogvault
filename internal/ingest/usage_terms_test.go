package ingest

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/teslamint/cogvault/internal/llm"
)

func TestRunPreservesGeneratedUsageTermsWithoutForgingReview(t *testing.T) {
	const fictionalQuote = "FICTIONAL TERMS ONLY: synthetic example, not rights evidence."

	for _, tc := range []struct {
		name       string
		usageTerms string
		want       bool
	}{
		{
			name: "generated candidate",
			usageTerms: `usage_terms:
  status: recorded
  purpose: fictional testing
  conditions: synthetic only
  evidence:
    reference: https://fictional.invalid/terms
    quote: "` + fictionalQuote + `"
  reviewed_by: Fictional Reviewer
  reviewed_at: "2026-10-20"
`,
			want: true,
		},
		{name: "no candidate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pageContent := "---\ntitle: Fictional Example\ntype: source\ntags:\n  - synthetic\n" + tc.usageTerms + "---\n\nFictional body must remain unchanged.\n"
			m := &mockLLM{fn: func(llm.DigestRequest) (*llm.DigestResult, error) {
				return &llm.DigestResult{PageContent: pageContent}, nil
			}}
			h := newHarness(t, []string{"md"}, m)
			h.write(t, "fictional-terms.md", "fictional source")

			report, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive"})
			if err != nil || report.Digested != 1 {
				t.Fatalf("Run: report=%+v err=%v", report, err)
			}
			stored, err := h.store.Read("sources/fictional-terms.md")
			if err != nil {
				t.Fatal(err)
			}
			frontmatter, _, _, ok := parsePage(string(stored))
			if !ok {
				t.Fatalf("stored page is invalid: %s", stored)
			}
			if !strings.Contains(string(stored), "\n\nFictional body must remain unchanged.\n") {
				t.Fatalf("stored body changed: %q", stored)
			}
			if frontmatter["title"] != "Fictional Example" || frontmatter["type"] != "source" {
				t.Fatalf("unrelated metadata changed: %#v", frontmatter)
			}
			got, ok := frontmatter["usage_terms"].(map[string]any)
			if !ok || got["status"] != "unverified" {
				t.Fatalf("stored usage_terms = %#v, want unverified", frontmatter["usage_terms"])
			}
			if tc.want {
				if got["conditions"] != "synthetic only" || got["purpose"] != "fictional testing" {
					t.Fatalf("generated conditions were lost: %#v", got)
				}
				if strings.Contains(string(stored), "Fictional Reviewer") || strings.Contains(string(stored), "reviewed_at") {
					t.Fatalf("generated review claim remained in stored page: %s", stored)
				}
			} else if len(got) != 1 {
				t.Fatalf("absent record fabricated terms: %#v", got)
			}
			results, err := h.idx.Search("unverified", 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(results) == 0 || results[0].Path != "sources/fictional-terms.md" {
				t.Fatalf("index does not expose normalized stored content: %+v", results)
			}
		})
	}
}

func TestRunPrioritizesSourceUsageTermsOverModelOutput(t *testing.T) {
	const quote = "Synthetic permission notice: retain this exact quotation."
	source := "---\nusage_terms:\n  purpose: fictional testing\n  conditions: synthetic use only\n  scope: this synthetic file\n  evidence:\n    quote: \"" + quote + "\"\n---\n\nSource body.\n"
	for _, tc := range []struct {
		name      string
		generated string
	}{
		{name: "model omission"},
		{name: "model contradiction", generated: "usage_terms:\n  purpose: hallucinated purpose\n  conditions: hallucinated conditions\n"},
		{name: "model malformed declaration", generated: "usage_terms: unknown\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &mockLLM{fn: func(llm.DigestRequest) (*llm.DigestResult, error) {
				return &llm.DigestResult{PageContent: "---\ntitle: Fictional Example\ntype: source\n" + tc.generated + "---\n\nGenerated body.\n"}, nil
			}}
			h := newHarness(t, []string{"md"}, m)
			h.write(t, "fictional-source.md", source)

			report, err := h.runner.Run(context.Background(), RunOptions{Origin: "test"})
			if err != nil || report.Digested != 1 {
				t.Fatalf("Run: report=%+v err=%v", report, err)
			}
			stored, err := h.store.Read("sources/fictional-source.md")
			if err != nil {
				t.Fatal(err)
			}
			frontmatter, _, _, ok := parsePage(string(stored))
			if !ok {
				t.Fatalf("stored page is invalid: %s", stored)
			}
			got, ok := frontmatter["usage_terms"].(map[string]any)
			if !ok || got["purpose"] != "fictional testing" || got["conditions"] != "synthetic use only" || got["scope"] != "this synthetic file" || got["status"] != "unverified" {
				t.Fatalf("stored usage_terms = %#v, want source declaration marked unverified", frontmatter["usage_terms"])
			}
			evidence, ok := got["evidence"].(map[string]any)
			if !ok || evidence["quote"] != quote {
				t.Fatalf("stored evidence = %#v, want exact source quote", got["evidence"])
			}
			results, err := h.idx.Search(quote, 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(results) == 0 || results[0].Path != "sources/fictional-source.md" {
				t.Fatalf("index does not expose source evidence: %+v", results)
			}
			meta, err := h.idx.GetMeta("sources/fictional-source.md")
			if err != nil || meta.ContentHash != fmt.Sprintf("%x", sha256.Sum256(stored)) {
				t.Fatalf("index differs from preserved evidence: meta=%+v error=%v", meta, err)
			}
		})
	}
}

func TestRunReadsUsageTermsFromExtractedText(t *testing.T) {
	const quote = "Synthetic extracted permission quote."
	m := &mockLLM{inputMode: llm.TextInput, fn: func(llm.DigestRequest) (*llm.DigestResult, error) {
		return &llm.DigestResult{PageContent: "---\ntitle: Extracted Example\ntype: source\n---\n\nGenerated body.\n"}, nil
	}}
	h := newHarness(t, []string{"pdf"}, m)
	h.write(t, "fictional.pdf", "binary input")
	h.runner.extractor = &mockExtractor{text: "---\nusage_terms:\n  conditions: extracted synthetic conditions\n  evidence:\n    quote: \"" + quote + "\"\n---\nExtracted body.\n"}
	report, err := h.runner.Run(context.Background(), RunOptions{Origin: "test"})
	if err != nil || report.Digested != 1 {
		t.Fatalf("Run: report=%+v err=%v", report, err)
	}
	stored, err := h.store.Read("sources/fictional.md")
	if err != nil {
		t.Fatal(err)
	}
	frontmatter, _, _, ok := parsePage(string(stored))
	if !ok {
		t.Fatalf("stored page is invalid: %s", stored)
	}
	got := frontmatter["usage_terms"].(map[string]any)
	evidence := got["evidence"].(map[string]any)
	if got["conditions"] != "extracted synthetic conditions" || got["status"] != "unverified" || evidence["quote"] != quote {
		t.Fatalf("stored extracted usage_terms = %#v", got)
	}
}

func TestRunRetainsPreviousUsageTermsOnRegeneration(t *testing.T) {
	const quote = "Synthetic retained evidence quote."
	m := &mockLLM{fn: func(llm.DigestRequest) (*llm.DigestResult, error) {
		return &llm.DigestResult{PageContent: "---\ntitle: Regenerated Example\ntype: source\n---\n\nRegenerated body.\n"}, nil
	}}
	h := newHarness(t, []string{"md"}, m)
	source := h.write(t, "regenerated.md", "---\nusage_terms:\n  conditions: retained synthetic conditions\n  evidence:\n    quote: \""+quote+"\"\n---\nOriginal body.\n")
	if report, err := h.runner.Run(context.Background(), RunOptions{Origin: "test"}); err != nil || report.Digested != 1 {
		t.Fatalf("first Run: report=%+v err=%v", report, err)
	}
	if err := os.WriteFile(source, []byte("Updated source without a terms record.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if report, err := h.runner.Run(context.Background(), RunOptions{Origin: "test"}); err != nil || report.Digested != 1 {
		t.Fatalf("regeneration Run: report=%+v err=%v", report, err)
	}
	stored, err := h.store.Read("sources/regenerated.md")
	if err != nil {
		t.Fatal(err)
	}
	frontmatter, _, _, ok := parsePage(string(stored))
	if !ok {
		t.Fatalf("stored page is invalid: %s", stored)
	}
	got := frontmatter["usage_terms"].(map[string]any)
	evidence := got["evidence"].(map[string]any)
	if got["conditions"] != "retained synthetic conditions" || got["status"] != "unverified" || evidence["quote"] != quote {
		t.Fatalf("regenerated usage_terms = %#v", got)
	}
}

func TestRunRetainsUsageTermsWhenContentChangesFallbackSlug(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	source := h.write(t, "😀.md", "---\nusage_terms:\n  conditions: Keep the fictional notice.\n  evidence:\n    quote: Synthetic permission requires this exact notice.\n---\nFirst body.\n")
	if report, err := h.runner.Run(context.Background(), RunOptions{}); err != nil || report.Digested != 1 {
		t.Fatalf("first ingest: %+v, %v", report, err)
	}
	oldHash, err := hashFile(source)
	if err != nil {
		t.Fatal(err)
	}
	oldPage := "sources/" + slugFor(source, oldHash) + ".md"
	if err := os.WriteFile(source, []byte("Updated source without a permission record.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if report, err := h.runner.Run(context.Background(), RunOptions{}); err != nil || report.Digested != 1 {
		t.Fatalf("second ingest: %+v, %v", report, err)
	}
	newHash, err := hashFile(source)
	if err != nil {
		t.Fatal(err)
	}
	newPage := "sources/" + slugFor(source, newHash) + ".md"
	if newPage == oldPage {
		t.Fatal("fixture did not change the hash-derived page path")
	}
	stored, err := h.store.Read(newPage)
	if err != nil {
		t.Fatal(err)
	}
	metadata, _, _, ok := parsePage(string(stored))
	if !ok {
		t.Fatalf("invalid regenerated page: %s", stored)
	}
	record, _ := metadata["usage_terms"].(map[string]any)
	evidence, _ := record["evidence"].(map[string]any)
	if record["conditions"] != "Keep the fictional notice." || evidence["quote"] != "Synthetic permission requires this exact notice." {
		t.Fatalf("hash-derived destination lost prior source terms: %#v", record)
	}
}
