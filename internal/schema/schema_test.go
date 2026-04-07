package schema

import (
	"strings"
	"testing"
)

func TestDefaultContentNotEmpty(t *testing.T) {
	if DefaultContent == "" {
		t.Fatal("DefaultContent must not be empty")
	}
}

func TestDefaultContentStartsWithHeader(t *testing.T) {
	if !strings.HasPrefix(DefaultContent, "# Wiki Schema") {
		t.Errorf("DefaultContent should start with '# Wiki Schema', got prefix: %q", DefaultContent[:min(50, len(DefaultContent))])
	}
}

func TestDefaultContentContainsRequiredSections(t *testing.T) {
	for _, section := range []string{
		"## 규칙",
		"## 사용 가능한 도구",
		"## 페이지 타입",
		"## Ingest 워크플로우",
	} {
		if !strings.Contains(DefaultContent, section) {
			t.Errorf("DefaultContent missing required section: %s", section)
		}
	}
}
