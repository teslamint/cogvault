package terms

import "testing"

func TestSummaryPreservesKnownConditionsWithoutInferringSharingScope(t *testing.T) {
	// Fictional validation data only; root labels do not establish permission.
	page := []byte("---\nlicense: MIT\nsource_url: https://example.invalid/free\naccess: free\nsubscription: paid\nusage_terms:\n  status: unverified\n  where: [not, text]\n  conditions: Keep the fictional acknowledgement.\n  evidence:\n    reference: fictional-policy\n    quote: Synthetic acknowledgement is required.\n---\nBody.\n")
	got := Summarize(page)
	if got.Scope != "unverified" || got.Where != "unverified" || got.Level != "unverified" || got.Status != "unverified" {
		t.Fatalf("summary inferred unsupported sharing information: %+v", got)
	}
	if got.Conditions != "Keep the fictional acknowledgement." || got.Evidence.Reference != "fictional-policy" {
		t.Fatalf("unknown axes erased known conditions or evidence: %+v", got)
	}
}

func TestMalformedPageHasNoKnownSharingSummary(t *testing.T) {
	got := Summarize([]byte("---\nusage_terms: [\n---\n"))
	if got.Scope != "unverified" || got.Where != "unverified" || got.Level != "unverified" || got.Conditions != "unverified" || got.Evidence.Reference != "unverified" || got.Status != "unverified" {
		t.Fatalf("malformed page acquired a known declaration: %+v", got)
	}
}
