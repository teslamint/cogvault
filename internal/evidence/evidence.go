package evidence

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/teslamint/cogvault/internal/terms"
)

const (
	SeverityError   = "error"
	SeverityWarning = "warning"
)

type Diagnostic struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	ClaimID  string `json:"claim_id"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	FixHint  string `json:"fix_hint"`
}

type FileChecker interface {
	IsFile(path string) (bool, error)
}

type ClaimSummary struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"`
	Checks     []string `json:"checks"`
	Gaps       []string `json:"gaps"`
	Limitation string   `json:"limitation"`
}

type Summary struct {
	Status     string         `json:"status"`
	ClaimCount int            `json:"claim_count"`
	Claims     []ClaimSummary `json:"claims"`
	Truncated  bool           `json:"truncated"`
	Reason     string         `json:"reason"`
}

func (d Diagnostic) Warning() string {
	prefix := "[" + d.ID + "] "
	if d.ClaimID != "" {
		prefix += d.ClaimID + ": "
	}
	return prefix + d.Message + "; fix: " + d.FixHint
}

func (d Diagnostic) Text() string {
	message := d.Message
	if d.ClaimID != "" {
		message = d.ClaimID + ": " + message
	}
	return message + " (fix: " + d.FixHint + ")"
}

// parse returns the metadata and whether a frontmatter block was present.
func parse(content []byte) (map[string]any, bool, error) {
	metadata, _, err := terms.Parse(content)
	if err != nil {
		return nil, false, err
	}
	return metadata, len(metadata) > 0, nil
}

func Validate(page string, content []byte, files FileChecker) []Diagnostic {
	metadata, present, err := parse(content)
	if err != nil || !present {
		return nil
	}
	return validateMetadata(page, metadata, files)
}

func validateMetadata(page string, metadata map[string]any, files FileChecker) []Diagnostic {
	raw, exists := metadata["research_evidence"]
	if !exists {
		return nil
	}
	block, ok := mapping(raw)
	if !ok || !integerOne(block["version"]) {
		return []Diagnostic{diag("EVID001", page, "", SeverityError, "research_evidence must have integer version 1", "set version to the integer 1")}
	}
	var out []Diagnostic
	claims, ok := block["claims"].([]any)
	if !ok {
		out = append(out, diag("EVID002", page, "", SeverityError, "claims must be an array", "provide a claims array"))
		return out
	}
	seen := make(map[string]bool)
	for _, item := range claims {
		claim, ok := mapping(item)
		if !ok {
			out = append(out, diag("EVID002", page, "", SeverityError, "claim must be a mapping", "provide each claim as a mapping"))
			continue
		}
		id, idOK := stringValue(claim["id"])
		_, textOK := stringValue(claim["text"])
		kind, kindOK := stringValue(claim["kind"])
		claimID := id
		if !idOK || !textOK || !kindOK {
			out = append(out, diag("EVID002", page, claimID, SeverityError, "claim id, text, and kind must be non-empty strings", "provide non-empty string id, text, and kind"))
		}
		if idOK {
			if seen[id] {
				out = append(out, diag("EVID002", page, claimID, SeverityError, fmt.Sprintf("duplicate claim id %q", id), "use a unique claim id"))
			}
			seen[id] = true
		}
		if kindOK && !oneOf(kind, "empirical", "causal", "normative") {
			out = append(out, diag("EVID003", page, claimID, SeverityError, fmt.Sprintf("unknown claim kind %q", kind), "use empirical, causal, or normative"))
		}
		out = append(out, validateSources(page, claimID, claim["sources"], hasKey(claim, "sources"), files)...)
		out = append(out, validateChecks(page, claimID, claim["checks"], hasKey(claim, "checks"))...)
		out = append(out, validateGaps(page, claimID, claim["gaps"], hasKey(claim, "gaps"))...)
		if limitation, present := claim["limitation"]; present {
			if _, ok := limitation.(string); !ok {
				out = append(out, diag("EVID003", page, claimID, SeverityError, "limitation must be a string", "write limitation as a string"))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.ClaimID != b.ClaimID {
			return a.ClaimID < b.ClaimID
		}
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return a.Message < b.Message
	})
	return out
}

func validateSources(page, claimID string, raw any, present bool, files FileChecker) []Diagnostic {
	if !present {
		return []Diagnostic{sourceDiag(page, claimID, "claim sources must be a non-empty array")}
	}
	items, ok := raw.([]any)
	if !ok || len(items) == 0 {
		return []Diagnostic{sourceDiag(page, claimID, "claim sources must be a non-empty array")}
	}
	var out []Diagnostic
	for _, item := range items {
		source, ok := mapping(item)
		if !ok {
			out = append(out, sourceDiag(page, claimID, "source must be a mapping"))
			continue
		}
		ref, ok := stringValue(source["ref"])
		if !ok {
			out = append(out, sourceDiag(page, claimID, "source ref must be a non-empty string"))
			continue
		}
		if !validRef(ref) {
			out = append(out, sourceDiag(page, claimID, fmt.Sprintf("source ref %q is not a safe wiki-relative .md path", ref)))
			continue
		}
		if files != nil {
			isFile, err := files.IsFile(ref)
			if err != nil {
				out = append(out, sourceDiag(page, claimID, fmt.Sprintf("ref path rejected: %v", err)))
			} else if !isFile {
				out = append(out, sourceDiag(page, claimID, fmt.Sprintf("source ref %q not found", ref)))
			}
		}
	}
	return out
}

func validateChecks(page, claimID string, raw any, present bool) []Diagnostic {
	if !present {
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		return []Diagnostic{diag("EVID003", page, claimID, SeverityError, "checks must be an array", "provide checks as an array")}
	}
	var out []Diagnostic
	for _, item := range items {
		check, ok := mapping(item)
		if !ok {
			out = append(out, diag("EVID003", page, claimID, SeverityError, "check must be a mapping", "provide each check as a mapping"))
			continue
		}
		kind, ok := stringValue(check["kind"])
		if !ok || !oneOf(kind, "bibliography", "fulltext", "table", "replication") {
			out = append(out, diag("EVID003", page, claimID, SeverityError, "check kind is missing or unknown", "use bibliography, fulltext, table, or replication"))
			continue
		}
		if _, ok := stringValue(check["result"]); !ok {
			out = append(out, diag("EVID005", page, claimID, SeverityWarning, "check result must be a non-empty string", "record the result of the check"))
		}
		if oneOf(kind, "fulltext", "table", "replication") {
			if _, ok := stringValue(check["locator"]); !ok {
				out = append(out, diag("EVID005", page, claimID, SeverityWarning, fmt.Sprintf("%s check requires a locator", kind), "provide a non-empty locator"))
			}
		}
		// Unknown fields are intentionally ignored in v1.
	}
	return out
}

func validateGaps(page, claimID string, raw any, present bool) []Diagnostic {
	if !present {
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		return []Diagnostic{diag("EVID003", page, claimID, SeverityError, "gaps must be an array", "provide gaps as an array")}
	}
	var out []Diagnostic
	for _, item := range items {
		gap, ok := mapping(item)
		if !ok {
			out = append(out, diag("EVID003", page, claimID, SeverityError, "gap must be a mapping", "provide each gap as a mapping"))
			continue
		}
		kind, ok := stringValue(gap["kind"])
		if !ok || !oneOf(kind, "source_unavailable", "verification_pending", "replication_pending", "applicability_unknown", "additional_evidence_needed") {
			out = append(out, diag("EVID003", page, claimID, SeverityError, "gap kind is missing or unknown", "use a supported gap kind"))
			continue
		}
		if _, ok := stringValue(gap["detail"]); !ok {
			out = append(out, diag("EVID007", page, claimID, SeverityWarning, "gap detail must be a non-empty string", "describe the unresolved gap"))
		}
		if _, ok := stringValue(gap["next_step"]); !ok {
			out = append(out, diag("EVID007", page, claimID, SeverityWarning, "gap next_step must be a non-empty string", "record a concrete next step"))
		}
	}
	return out
}

func Summarize(content []byte) Summary {
	metadata, present, err := parse(content)
	if err != nil {
		return UnavailableSummary("frontmatter could not be parsed")
	}
	if !present {
		return absentSummary()
	}
	raw, exists := metadata["research_evidence"]
	if !exists {
		return absentSummary()
	}
	block, ok := mapping(raw)
	if !ok || !integerOne(block["version"]) {
		return UnavailableSummary("unsupported research_evidence version")
	}
	diagnostics := validateMetadata("", metadata, nil)
	for _, d := range diagnostics {
		if d.ID == "EVID002" || d.ID == "EVID003" {
			return UnavailableSummary("research_evidence structure is invalid")
		}
	}
	claimsRaw, ok := block["claims"].([]any)
	if !ok {
		return UnavailableSummary("research_evidence structure is invalid")
	}
	result := Summary{Status: "recorded", ClaimCount: len(claimsRaw), Claims: make([]ClaimSummary, 0, min(3, len(claimsRaw)))}
	if len(claimsRaw) > 3 {
		result.Truncated = true
	}
	for i, rawClaim := range claimsRaw {
		if i >= 3 {
			break
		}
		claim, ok := mapping(rawClaim)
		if !ok {
			continue
		}
		id, _ := stringValue(claim["id"])
		kind, _ := stringValue(claim["kind"])
		cs := ClaimSummary{ID: id, Kind: kind, Checks: make([]string, 0), Gaps: make([]string, 0)}
		if items, ok := claim["checks"].([]any); ok {
			if len(items) > 4 {
				result.Truncated = true
			}
			for j, item := range items {
				if j >= 4 {
					break
				}
				if m, ok := mapping(item); ok {
					if k, ok := stringValue(m["kind"]); ok {
						cs.Checks = append(cs.Checks, k)
					}
				}
			}
		}
		if items, ok := claim["gaps"].([]any); ok {
			if len(items) > 4 {
				result.Truncated = true
			}
			for j, item := range items {
				if j >= 4 {
					break
				}
				if m, ok := mapping(item); ok {
					if k, ok := stringValue(m["kind"]); ok {
						cs.Gaps = append(cs.Gaps, k)
					}
				}
			}
		}
		if limitation, ok := stringValue(claim["limitation"]); ok {
			if utf8.RuneCountInString(limitation) > 200 {
				limitation = string([]rune(limitation)[:200])
				result.Truncated = true
			}
			cs.Limitation = limitation
		}
		result.Claims = append(result.Claims, cs)
	}
	return result
}

func UnavailableSummary(reason string) Summary {
	return Summary{Status: "unavailable", Claims: make([]ClaimSummary, 0), Reason: reason}
}

func absentSummary() Summary { return Summary{Status: "absent", Claims: make([]ClaimSummary, 0)} }
func diag(id, page, claimID, severity, message, fix string) Diagnostic {
	return Diagnostic{ID: id, Path: page, ClaimID: claimID, Severity: severity, Message: message, FixHint: fix}
}
func sourceDiag(page, claimID, message string) Diagnostic {
	return diag("EVID004", page, claimID, SeverityWarning, message, "point ref at an existing wiki-relative .md file")
}
func hasKey(m map[string]any, key string) bool { _, ok := m[key]; return ok }
func mapping(value any) (map[string]any, bool) {
	if m, ok := value.(map[string]any); ok {
		return m, true
	}
	m, ok := value.(map[any]any)
	if !ok {
		return nil, false
	}
	result := make(map[string]any, len(m))
	for key, value := range m {
		if key, ok := key.(string); ok {
			result[key] = value
		}
	}
	return result, true
}
func stringValue(value any) (string, bool) {
	s, ok := value.(string)
	return s, ok && strings.TrimSpace(s) != ""
}
func integerOne(value any) bool { n, ok := value.(int); return ok && n == 1 }
func oneOf(value string, choices ...string) bool {
	for _, choice := range choices {
		if value == choice {
			return true
		}
	}
	return false
}
func validRef(ref string) bool {
	if ref == "" || strings.TrimSpace(ref) != ref || strings.HasPrefix(ref, "/") || strings.Contains(ref, `\`) || strings.Contains(ref, "://") || strings.HasPrefix(ref, "[[") || strings.Contains(ref, "]]") || strings.Contains(ref, "#") || !strings.EqualFold(path.Ext(ref), ".md") || path.Clean(ref) != ref {
		return false
	}
	for _, segment := range strings.Split(ref, "/") {
		if segment == ".." {
			return false
		}
	}
	if len(ref) >= 2 && ((ref[0] >= 'A' && ref[0] <= 'Z') || (ref[0] >= 'a' && ref[0] <= 'z')) && ref[1] == ':' {
		return false
	}
	return true
}
