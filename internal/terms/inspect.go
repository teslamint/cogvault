package terms

import (
	"fmt"
	"strings"
	"time"
)

// Inspect reports record completeness, never permission to share a work.
func Inspect(value any) (string, []string) {
	record, ok := value.(map[string]any)
	if !ok {
		return "unverified", []string{"missing or malformed usage_terms record"}
	}
	reasons := []string{}
	status, _ := record["status"].(string)
	switch status {
	case "unverified":
		reasons = append(reasons, "usage_terms is explicitly unverified")
	case "recorded":
	default:
		reasons = append(reasons, "usage_terms.status must be unverified or recorded")
	}
	for _, key := range []string{"purpose", "conditions", "reviewed_by", "reviewed_at"} {
		value, ok := record[key].(string)
		if !ok || strings.TrimSpace(value) == "" {
			reasons = append(reasons, fmt.Sprintf("usage_terms.%s must be a non-empty string", key))
		}
	}
	if date, ok := record["reviewed_at"].(string); ok && strings.TrimSpace(date) != "" {
		if _, err := time.Parse("2006-01-02", date); err != nil {
			reasons = append(reasons, "usage_terms.reviewed_at must be a valid YYYY-MM-DD string")
		}
	}
	evidence, _ := record["evidence"].(map[string]any)
	for _, key := range []string{"reference", "quote"} {
		value, ok := evidence[key].(string)
		if !ok || strings.TrimSpace(value) == "" {
			reasons = append(reasons, fmt.Sprintf("usage_terms.evidence.%s must be a non-empty string", key))
		}
	}
	if len(reasons) != 0 {
		return "unverified", reasons
	}
	return "recorded", reasons
}
