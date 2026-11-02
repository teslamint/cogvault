package evidence

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

type fakeFiles map[string]bool

func (f fakeFiles) IsFile(name string) (bool, error) {
	return f[name], nil
}

func page(record string) []byte { return []byte("---\n" + record + "\n---\nbody\n") }
func claim(extra string) string {
	return "  - id: claim-1\n    text: 주장\n    kind: empirical\n    sources:\n      - ref: sources/a.md\n" + extra
}
func hasID(ds []Diagnostic, id string) bool {
	for _, d := range ds {
		if d.ID == id {
			return true
		}
	}
	return false
}

func TestValidateAndSummarizeValidRecord(t *testing.T) {
	content := page("title: sample\nresearch_evidence:\n  version: 1\n  claims:\n    - id: claim-1\n      text: 한글 주장\n      kind: empirical\n      sources:\n        - ref: sources/a.md\n          locator: 표 2\n        - ref: sources/b.md\n      checks:\n        - kind: bibliography\n          result: 저자와 제목 대조\n        - kind: table\n          result: 표의 수치 대조\n          locator: 표 2\n      gaps:\n        - kind: replication_pending\n          detail: 재계산하지 않음\n          next_step: 입력을 고정해 재현\n        - kind: applicability_unknown\n          detail: 적용 범위가 다름\n          next_step: 차이를 검토\n      limitation: 원래 집계 범위 밖 적용에는 추가 가정이 필요함")
	ds := Validate("notes/page.md", content, fakeFiles{"sources/a.md": true, "sources/b.md": true})
	if len(ds) != 0 {
		t.Fatalf("Validate() = %#v", ds)
	}
	s := Summarize(content)
	if s.Status != "recorded" || s.ClaimCount != 1 || s.Truncated || len(s.Claims) != 1 {
		t.Fatalf("Summary = %#v", s)
	}
	got := s.Claims[0]
	if got.ID != "claim-1" || got.Kind != "empirical" || strings.Join(got.Checks, ",") != "bibliography,table" || strings.Join(got.Gaps, ",") != "replication_pending,applicability_unknown" || got.Limitation == "" {
		t.Fatalf("ClaimSummary = %#v", got)
	}
}

func TestValidateAndSummarizeIgnoreNonStringKeys(t *testing.T) {
	tests := []struct {
		name    string
		content string
		files   FileChecker
		summary bool
	}{
		{
			name:    "claim extra key",
			content: "research_evidence:\n  version: 1\n  claims:\n    - id: claim-1\n      text: claim\n      kind: empirical\n      sources: [{ref: sources/a.md}]\n      5: extra",
			summary: true,
		},
		{
			name:    "block extra key",
			content: "research_evidence:\n  version: 1\n  1: z\n  claims: []",
		},
		{
			name:    "source extra key",
			content: "research_evidence:\n  version: 1\n  claims:\n    - id: claim-1\n      text: claim\n      kind: empirical\n      sources:\n        - ref: sources/a.md\n          true: x",
			files:   fakeFiles{"sources/a.md": true},
			summary: true,
		},
		{
			name:    "check extra key",
			content: "research_evidence:\n  version: 1\n  claims:\n    - id: claim-1\n      text: claim\n      kind: empirical\n      sources: [{ref: sources/a.md}]\n      checks:\n        - kind: bibliography\n          result: checked\n          7: y",
			summary: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			content := page(tc.content)
			if ds := Validate("p.md", content, tc.files); len(ds) != 0 {
				t.Fatalf("Validate() = %#v", ds)
			}
			if tc.summary {
				if got := Summarize(content); got.Status != "recorded" {
					t.Fatalf("Summarize() = %#v", got)
				}
			}
		})
	}
}

func TestValidateAndSummarizeEntryStates(t *testing.T) {
	cases := []struct {
		name           string
		content        []byte
		status, reason string
		wantID         string
	}{
		{"no frontmatter", []byte("body"), "absent", "", ""},
		{"no key", page("title: page"), "absent", "", ""},
		{"broken yaml", page("research_evidence: ["), "unavailable", "frontmatter could not be parsed", ""},
		{"version missing", page("research_evidence:\n  claims: []"), "unavailable", "unsupported research_evidence version", "EVID001"},
		{"version string", page("research_evidence:\n  version: \"1\"\n  claims: []"), "unavailable", "unsupported research_evidence version", "EVID001"},
		{"version 2", page("research_evidence:\n  version: 2\n  claims: []"), "unavailable", "unsupported research_evidence version", "EVID001"},
		{"version float", page("research_evidence:\n  version: 1.0\n  claims: []"), "unavailable", "unsupported research_evidence version", "EVID001"},
		{"scalar block", page("research_evidence: scalar"), "unavailable", "unsupported research_evidence version", "EVID001"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := Summarize(tc.content)
			if s.Status != tc.status || s.Reason != tc.reason || s.Claims == nil {
				t.Fatalf("Summary = %#v", s)
			}
			ds := Validate("p.md", tc.content, nil)
			if tc.wantID == "" && len(ds) != 0 {
				t.Fatalf("diagnostics = %#v", ds)
			}
			if tc.wantID != "" && (len(ds) != 1 || ds[0].ID != tc.wantID) {
				t.Fatalf("diagnostics = %#v", ds)
			}
		})
	}
}

func TestValidateDiagnosticClasses(t *testing.T) {
	cases := []struct{ name, claimFields, want string }{
		{"claims not array", "research_evidence:\n  version: 1\n  claims: {}", "EVID002"},
		{"claim not mapping", "research_evidence:\n  version: 1\n  claims: [hello]", "EVID002"},
		{"missing id", "research_evidence:\n  version: 1\n  claims:\n    - text: x\n      kind: empirical\n      sources: [{ref: sources/a.md}]", "EVID002"},
		{"missing text", "research_evidence:\n  version: 1\n  claims:\n    - id: x\n      kind: empirical\n      sources: [{ref: sources/a.md}]", "EVID002"},
		{"missing kind", "research_evidence:\n  version: 1\n  claims:\n    - id: x\n      text: y\n      sources: [{ref: sources/a.md}]", "EVID002"},
		{"numeric id", "research_evidence:\n  version: 1\n  claims:\n    - id: 42\n      text: y\n      kind: empirical\n      sources: [{ref: sources/a.md}]", "EVID002"},
		{"duplicate id", "research_evidence:\n  version: 1\n  claims:\n    - {id: x, text: a, kind: empirical, sources: [{ref: sources/a.md}]}\n    - {id: x, text: b, kind: empirical, sources: [{ref: sources/a.md}]}", "EVID002"},
		{"checks not array", "research_evidence:\n  version: 1\n  claims:\n" + claim("    checks: nope\n"), "EVID003"},
		{"check kind missing", "research_evidence:\n  version: 1\n  claims:\n" + claim("    checks: [{}]\n"), "EVID003"},
		{"check kind empty", "research_evidence:\n  version: 1\n  claims:\n" + claim("    checks: [{kind: ' ', result: x}]\n"), "EVID003"},
		{"check kind integer", "research_evidence:\n  version: 1\n  claims:\n" + claim("    checks: [{kind: 1}]\n"), "EVID003"},
		{"check kind unknown", "research_evidence:\n  version: 1\n  claims:\n" + claim("    checks: [{kind: bogus}]\n"), "EVID003"},
		{"gap kind unknown", "research_evidence:\n  version: 1\n  claims:\n" + claim("    gaps: [{kind: bogus}]\n"), "EVID003"},
		{"claim kind unknown", "research_evidence:\n  version: 1\n  claims:\n    - {id: x, text: y, kind: bogus, sources: [{ref: sources/a.md}]}\n", "EVID003"},
		{"limitation integer", "research_evidence:\n  version: 1\n  claims:\n" + claim("    limitation: 42\n"), "EVID003"},
		{"limitation list", "research_evidence:\n  version: 1\n  claims:\n" + claim("    limitation: [a, b]\n"), "EVID003"},
		{"check result empty", "research_evidence:\n  version: 1\n  claims:\n" + claim("    checks: [{kind: bibliography, result: '  '}]\n"), "EVID005"},
		{"table no locator", "research_evidence:\n  version: 1\n  claims:\n" + claim("    checks: [{kind: table, result: values}]\n"), "EVID005"},
		{"gap no next step", "research_evidence:\n  version: 1\n  claims:\n" + claim("    gaps: [{kind: replication_pending, detail: pending}]\n"), "EVID007"},
		{"reslt typo", "research_evidence:\n  version: 1\n  claims:\n" + claim("    checks: [{kind: bibliography, reslt: value}]\n"), "EVID005"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ds := Validate("p.md", page(tc.claimFields), nil)
			if !hasID(ds, tc.want) {
				t.Fatalf("wanted %s in %#v", tc.want, ds)
			}
			for _, d := range ds {
				if d.ID == "EVID006" {
					t.Fatal("reserved diagnostic emitted")
				}
			}
		})
	}
}

func TestValidateSourceRefs(t *testing.T) {
	cases := []struct {
		name, ref string
		files     fakeFiles
		err       error
	}{
		{"missing", "sources/missing.md", fakeFiles{}, nil},
		{"directory", "sources/a.md", fakeFiles{"sources/a.md": false}, nil},
		{"basename only", "a.md", fakeFiles{"sources/a.md": true}, nil},
		{"absolute", "/abs.md", fakeFiles{}, nil},
		{"traversal", "../x.md", fakeFiles{}, nil},
		{"dot", "./x.md", fakeFiles{}, nil},
		{"url", "https://x/y.md", fakeFiles{}, nil},
		{"wikilink", "[[a]].md", fakeFiles{}, nil},
		{"fragment", "sources/a.md#h", fakeFiles{}, nil},
		{"no extension", "sources/a", fakeFiles{}, nil},
		{"empty sources", "", fakeFiles{}, nil},
		{"absent sources", "", fakeFiles{}, nil},
		{"existence error", "sources/a.md", fakeFiles{}, errors.New("unsafe path")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref := tc.ref
			if tc.name == "empty sources" || tc.name == "absent sources" {
				var content []byte
				if tc.name == "empty sources" {
					content = page("research_evidence:\n  version: 1\n  claims:\n    - {id: x, text: y, kind: empirical, sources: []}")
				} else {
					content = page("research_evidence:\n  version: 1\n  claims:\n    - {id: x, text: y, kind: empirical}")
				}
				if !hasID(Validate("p.md", content, tc.files), "EVID004") {
					t.Fatal("expected EVID004")
				}
				return
			}
			var files FileChecker = tc.files
			if tc.err != nil {
				files = errorFiles{err: tc.err}
			}
			content := page(fmt.Sprintf("research_evidence:\n  version: 1\n  claims:\n    - {id: x, text: y, kind: empirical, sources: [{ref: %q}]}", ref))
			ds := Validate("p.md", content, files)
			if !hasID(ds, "EVID004") {
				t.Fatalf("expected EVID004, got %#v", ds)
			}
			if tc.name == "missing" && ds[0].Message != `source ref "sources/missing.md" not found` {
				t.Fatalf("message = %q", ds[0].Message)
			}
		})
	}
}

type errorFiles struct{ err error }

func (e errorFiles) IsFile(string) (bool, error) { return false, e.err }

func TestValidateSkipRulesSortingAndNoContradiction(t *testing.T) {
	content := page("research_evidence:\n  version: 1\n  claims:\n    - {id: z, text: y, kind: empirical, sources: [{ref: sources/a.md}], checks: [{kind: replication, result: ran, locator: report}], gaps: [{kind: replication_pending, detail: pending, next_step: rerun}]}\n    - {id: a, text: y, kind: empirical, sources: []}")
	ds := Validate("p.md", content, nil)
	if len(ds) != 1 || ds[0].ClaimID != "a" {
		t.Fatalf("diagnostics = %#v", ds)
	}
	if Summarize(content).Status != "recorded" {
		t.Fatalf("replication pairing should remain recorded: %#v", Summarize(content))
	}
}

func TestValidateOrdersDiagnosticsByClaimID(t *testing.T) {
	content := page("research_evidence:\n  version: 1\n  claims:\n    - {id: z, text: y, kind: empirical, sources: []}\n    - {id: a, text: y, kind: empirical, sources: []}")
	ds := Validate("p.md", content, nil)
	if len(ds) != 2 || ds[0].ClaimID != "a" || ds[1].ClaimID != "z" {
		t.Fatalf("diagnostics = %#v, want claim a before z", ds)
	}
}

func TestSummarizeRejectsNonStringLimitation(t *testing.T) {
	content := page("research_evidence:\n  version: 1\n  claims:\n" + claim("    limitation: 42\n"))
	if got := Summarize(content); got.Status != "unavailable" {
		t.Fatalf("Summarize = %#v, want unavailable", got)
	}
}

func TestSummaryTruncationAndTextFormats(t *testing.T) {
	var claims strings.Builder
	claims.WriteString("research_evidence:\n  version: 1\n  claims:\n")
	for i := 0; i < 4; i++ {
		fmt.Fprintf(&claims, "    - id: c%d\n      text: claim\n      kind: empirical\n      sources: [{ref: sources/a.md}]\n", i)
		if i == 0 {
			claims.WriteString("      checks: [{kind: bibliography, result: x}, {kind: fulltext, result: x, locator: x}, {kind: table, result: x, locator: x}, {kind: replication, result: x, locator: x}, {kind: table, result: x, locator: x}]\n")
			claims.WriteString("      gaps: [{kind: source_unavailable, detail: x, next_step: x}, {kind: verification_pending, detail: x, next_step: x}, {kind: replication_pending, detail: x, next_step: x}, {kind: applicability_unknown, detail: x, next_step: x}, {kind: additional_evidence_needed, detail: x, next_step: x}]\n")
			claims.WriteString("      limitation: " + strings.Repeat("한", 201) + "\n")
		}
	}
	s := Summarize(page(claims.String()))
	if s.Status != "recorded" || s.ClaimCount != 4 || len(s.Claims) != 3 || !s.Truncated {
		t.Fatalf("Summary = %#v", s)
	}
	if len(s.Claims[0].Checks) != 4 || len(s.Claims[0].Gaps) != 4 || len([]rune(s.Claims[0].Limitation)) != 200 {
		t.Fatalf("truncation = %#v", s.Claims[0])
	}
	if got := (Diagnostic{ID: "EVID005", ClaimID: "claim-1", Message: "bad result", FixHint: "add result"}).Warning(); got != "[EVID005] claim-1: bad result; fix: add result" {
		t.Fatalf("Warning = %q", got)
	}
	if got := (Diagnostic{ID: "EVID001", Message: "bad version", FixHint: "set version"}).Warning(); got != "[EVID001] bad version; fix: set version" {
		t.Fatalf("Warning = %q", got)
	}
	if got := (Diagnostic{ClaimID: "claim-1", Message: "bad result", FixHint: "add result"}).Text(); got != "claim-1: bad result (fix: add result)" {
		t.Fatalf("Text = %q", got)
	}
	if got := (Diagnostic{Message: "bad version", FixHint: "set version"}).Text(); got != "bad version (fix: set version)" {
		t.Fatalf("Text = %q", got)
	}
}
func TestUnknownFieldsAndInvalidKindSkip(t *testing.T) {
	content := page("research_evidence:\n  version: 1\n  claims:\n    - id: c\n      text: text\n      kind: empirical\n      sources: [{ref: sources/a.md}]\n      limitaton: ignored\n      extra: ignored\n      checks: [{kind: unknown, result: ''}]\n      gaps: [{kind: unknown, detail: ''}]\n    - id: d\n      text: text\n      kind: empirical\n      sources: [{ref: sources/a.md}]\n      checks: [{kind: bibliography, result: ok, reslt: ignored}]\n")
	ds := Validate("p.md", content, nil)
	if len(ds) != 2 || ds[0].ID != "EVID003" || ds[1].ID != "EVID003" {
		t.Fatalf("invalid kinds should only emit EVID003: %#v", ds)
	}
	if Summarize(content).Status != "unavailable" {
		t.Fatalf("invalid kind should make summary unavailable")
	}
	valid := page("research_evidence:\n  version: 1\n  claims:\n    - id: c\n      text: text\n      kind: empirical\n      sources: [{ref: sources/a.md}]\n      limitaton: ignored\n      extra: ignored\n      checks: [{kind: bibliography, result: ok, reslt: ignored}]\n")
	if ds := Validate("p.md", valid, nil); len(ds) != 0 {
		t.Fatalf("unknown extra fields should be ignored: %#v", ds)
	}
}
