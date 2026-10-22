package terms

import (
	"bytes"
	"testing"
)

func TestParseAndEncodePreserveBody(t *testing.T) {
	content := []byte("---\nusage_terms:\n  purpose: research\n---\n\n# Body\n")
	metadata, body, err := Parse(content)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got := metadata["usage_terms"].(map[string]any)["purpose"]; got != "research" {
		t.Fatalf("purpose = %#v, want research", got)
	}
	if !bytes.Equal(body, []byte("\n# Body\n")) {
		t.Fatalf("body = %q", body)
	}
	encoded, err := Encode(metadata, body)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	parsed, roundTripBody, err := Parse(encoded)
	if err != nil {
		t.Fatalf("Parse(encoded) error = %v", err)
	}
	if !bytes.Equal(roundTripBody, body) || parsed["usage_terms"].(map[string]any)["purpose"] != "research" {
		t.Fatalf("round trip = (%#v, %q)", parsed, roundTripBody)
	}
}

func TestParseWithoutFrontmatterReturnsOriginalBody(t *testing.T) {
	content := []byte("plain extracted text\n")
	metadata, body, err := Parse(content)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(metadata) != 0 || !bytes.Equal(body, content) {
		t.Fatalf("Parse() = (%#v, %q), want empty metadata and unchanged body", metadata, body)
	}
}

func TestAutomaticPreservesSelectedTermsAndClearsReviewClaims(t *testing.T) {
	source := map[string]any{"title": "source", "usage_terms": map[string]any{
		"purpose": "research", "conditions": []any{"retain notice"},
		"evidence": map[string]any{"quote": "Keep notice.", "reference": "LICENSE"},
		"status":   "approved", "reviewed_by": "reviewer", "reviewed_at": "today",
	}}
	previous := map[string]any{"usage_terms": map[string]any{"purpose": "old"}}
	generated := map[string]any{"summary": "generated", "usage_terms": map[string]any{"purpose": "model guess"}}
	got := Automatic(source, previous, generated)
	record := got["usage_terms"].(map[string]any)
	if record["purpose"] != "research" || record["status"] != "unverified" || record["conditions"].([]any)[0] != "retain notice" {
		t.Fatalf("selected record = %#v", record)
	}
	if _, ok := record["reviewed_by"]; ok {
		t.Fatal("reviewed_by was retained")
	}
	if _, ok := record["reviewed_at"]; ok {
		t.Fatal("reviewed_at was retained")
	}
	if got["summary"] != "generated" {
		t.Fatalf("unrelated generated metadata lost: %#v", got)
	}
}

func TestCheckOverwriteRequiresExistingEvidenceLeaves(t *testing.T) {
	existing := []byte("---\nusage_terms:\n  purpose: research\n  conditions: notice\n  scope: article\n  evidence:\n    quote: Keep notice.\n    reference: LICENSE\n---\nold\n")
	for name, incoming := range map[string]string{
		"missing conditions":         "---\nusage_terms:\n  purpose: research\n  scope: article\n  evidence:\n    quote: Keep notice.\n    reference: LICENSE\n---\nnew\n",
		"missing evidence reference": "---\nusage_terms:\n  purpose: research\n  conditions: notice\n  scope: article\n  evidence:\n    quote: Keep notice.\n---\nnew\n",
		"null terms":                 "---\nusage_terms: null\n---\nnew\n",
		"scalar terms":               "---\nusage_terms: invalid\n---\nnew\n",
		"malformed incoming":         "---\nusage_terms: [\n---\nnew\n",
	} {
		t.Run(name, func(t *testing.T) {
			if err := CheckOverwrite(existing, []byte(incoming)); err == nil {
				t.Fatal("CheckOverwrite() error = nil, want loss rejection")
			}
		})
	}
}

func TestCheckOverwriteAllowsCompleteReplacementAndNoRecord(t *testing.T) {
	existing := []byte("---\nusage_terms:\n  purpose: research\n  conditions: notice\n  scope: article\n  evidence:\n    quote: Keep notice.\n    reference: LICENSE\n---\nold\n")
	incoming := []byte("---\nusage_terms:\n  purpose: new purpose\n  conditions: new conditions\n  scope: new scope\n  evidence:\n    quote: new quote\n    reference: new reference\n---\nnew\n")
	if err := CheckOverwrite(existing, incoming); err != nil {
		t.Fatalf("CheckOverwrite(complete replacement) error = %v", err)
	}
	if err := CheckOverwrite([]byte("---\ntitle: page\n---\nold\n"), []byte("plain markdown\n")); err != nil {
		t.Fatalf("CheckOverwrite(no existing terms) error = %v", err)
	}
	if _, _, err := Parse([]byte("---\nx: [\n---\n")); err == nil {
		t.Fatal("Parse() error = nil, want malformed YAML error")
	}
}

func TestOverwriteCannotReplaceExplicitConditionWithEmptyOrNonTextData(t *testing.T) {
	existing := []byte("---\nusage_terms:\n  conditions: Preserve the fictional notice.\n---\n")
	for _, replacement := range []string{"false", "42", "[null]", "[\" \"]", "{other: value}"} {
		incoming := []byte("---\nusage_terms:\n  conditions: " + replacement + "\n---\n")
		if err := CheckOverwrite(existing, incoming); err == nil {
			t.Errorf("accepted a lost text condition replaced by %s", replacement)
		}
	}
}

func TestAutomaticAbsentTermsRemainUnverified(t *testing.T) {
	generated := map[string]any{"title": "No permission supplied"}
	result := Automatic(nil, nil, generated)
	record, ok := result["usage_terms"].(map[string]any)
	if !ok || len(record) != 1 || record["status"] != "unverified" {
		t.Fatalf("absent terms = %#v", result["usage_terms"])
	}
	if _, changed := generated["usage_terms"]; changed {
		t.Fatal("automatic normalization mutated input metadata")
	}
}

func TestOverwriteCanRepairMalformedExistingMetadata(t *testing.T) {
	repaired := []byte("---\nusage_terms:\n  conditions: Preserve the fictional notice.\n---\nRepaired page.\n")
	for _, existing := range []string{"---\nusage_terms: [\n---\n", "---\nusage_terms: null\n---\n", "---\nusage_terms: invalid\n---\n"} {
		if err := CheckOverwrite([]byte(existing), repaired); err != nil {
			t.Errorf("cannot repair malformed page: %v", err)
		}
	}
}

func TestParsePreservesDateAndTimestampText(t *testing.T) {
	input := []byte("---\ningested_at: 2026-10-20\nusage_terms:\n  reviewed_at: 2026-10-20\n  evidence:\n    quote: 2026-10-20T09:30:00+09:00\n---\nBody.\n")
	metadata, body, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	record := metadata["usage_terms"].(map[string]any)
	evidence := record["evidence"].(map[string]any)
	if metadata["ingested_at"] != "2026-10-20" || record["reviewed_at"] != "2026-10-20" || evidence["quote"] != "2026-10-20T09:30:00+09:00" {
		t.Fatalf("date or evidence text changed: %#v", metadata)
	}
	encoded, err := Encode(metadata, body)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, _, err := Parse(encoded)
	if err != nil || roundTrip["ingested_at"] != "2026-10-20" {
		t.Fatalf("stored date contract changed: %#v, %v", roundTrip, err)
	}
}

func TestBOMDeclarationCannotBeLostOnOverwrite(t *testing.T) {
	input := append([]byte{0xef, 0xbb, 0xbf}, []byte("---\nusage_terms:\n  conditions: Keep the fictional notice.\n---\nBody.\n")...)
	metadata, _, err := Parse(input)
	if err != nil || metadata["usage_terms"] == nil {
		t.Fatalf("BOM hid explicit declaration: %#v, %v", metadata, err)
	}
	if err := CheckOverwrite(input, []byte("# Replacement without conditions\n")); err == nil {
		t.Fatal("BOM bypassed permission retention")
	}
}

func TestOverwriteCannotDropSharingAudienceOrExtent(t *testing.T) {
	// Fictional declarations only, not evidence for any real work.
	for _, field := range []string{"where", "level"} {
		existing := []byte("---\nusage_terms:\n  " + field + ": fictional declared restriction\n---\nBody.\n")
		if err := CheckOverwrite(existing, []byte("---\nusage_terms:\n  status: unverified\n---\nUpdated body.\n")); err == nil {
			t.Errorf("overwrite silently dropped usage_terms.%s", field)
		}
	}
}
