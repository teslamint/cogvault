package terms

import "strings"

// Unknown marks absent or unusable summary text; it never means unrestricted use.
const Unknown = "unverified"

type Summary struct {
	Scope      string          `json:"scope"`
	Where      string          `json:"where"`
	Level      string          `json:"level"`
	Conditions string          `json:"conditions"`
	Evidence   SummaryEvidence `json:"evidence"`
	Status     string          `json:"status"`
	Reasons    []string        `json:"reasons"`
}

type SummaryEvidence struct {
	Reference string `json:"reference"`
}

// Summarize projects declared values without inferring permission from a license.
func Summarize(content []byte) Summary {
	metadata, _, err := Parse(content)
	if err != nil {
		return unknownSummary("invalid YAML frontmatter; usage_terms could not be read")
	}
	record, _ := metadata["usage_terms"].(map[string]any)
	evidence, _ := record["evidence"].(map[string]any)
	status, reasons := Inspect(metadata["usage_terms"])
	return Summary{
		Scope:      summaryText(record["scope"]),
		Where:      summaryText(record["where"]),
		Level:      summaryText(record["level"]),
		Conditions: summaryText(record["conditions"]),
		Evidence:   SummaryEvidence{Reference: summaryText(evidence["reference"])},
		Status:     status,
		Reasons:    reasons,
	}
}

// UnavailableSummary does not expose partial data from a failed storage read.
func UnavailableSummary() Summary {
	return unknownSummary("page could not be read; usage_terms is unavailable")
}

func unknownSummary(reason string) Summary {
	return Summary{
		Scope: Unknown, Where: Unknown, Level: Unknown, Conditions: Unknown,
		Evidence: SummaryEvidence{Reference: Unknown},
		Status:   "unverified", Reasons: []string{reason},
	}
}

func summaryText(value any) string {
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return Unknown
	}
	return text
}
