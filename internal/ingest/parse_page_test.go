package ingest

import (
	"strings"
	"testing"
)

const quotedHeadlineTitle = `"명태균, 과장은 좀 있지만... 오세훈, 운이 좋다" 김영선 전 의원`

func TestParsePageRepairsTitleThatIsNotValidYAML(t *testing.T) {
	// A headline that starts with a quote makes the model emit a quoted scalar
	// followed by more text, which YAML rejects. The title is recovered verbatim.
	content := "---\ntitle: " + quotedHeadlineTitle + "\ntype: source\nsource_path: \"/a.pdf\"\n---\n\n## 요약\n본문\n"
	fm, title, body, ok := parsePage(content)
	if !ok {
		t.Fatal("parsePage rejected a repairable title")
	}
	if title != quotedHeadlineTitle {
		t.Fatalf("title = %q, want %q", title, quotedHeadlineTitle)
	}
	if fm["type"] != "source" || fm["source_path"] != "/a.pdf" {
		t.Fatalf("other fields lost: %v", fm)
	}
	if !strings.Contains(string(body), "## 요약") {
		t.Fatalf("body lost: %q", body)
	}
}

func TestCheckPageReportsWhyAPageIsRejected(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"yaml error outside title", "---\ntitle: ok\ntags: [a\n---\nbody\n", "frontmatter YAML"},
		{"no frontmatter", "# heading\nbody\n", "missing frontmatter or title"},
		{"no title", "---\ntype: source\n---\nbody\n", "missing frontmatter or title"},
		{"blank title", "---\ntitle: \"  \"\n---\nbody\n", "missing frontmatter or title"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, err := checkPage(tt.content)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("checkPage error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}
