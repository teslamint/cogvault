package ingest

import "testing"

func TestSlugFor(t *testing.T) {
	const fakeHash = "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"

	tests := []struct {
		name string
		path string
		want string
	}{
		{"ascii basic", "/src/notes-2024.pdf", "notes-2024"},
		{"ascii spaces", "/src/my notes file.pdf", "my-notes-file"},
		{"ascii uppercase", "/src/README.pdf", "readme"},
		{"ascii dots and underscores", "/src/v1.2_final.pdf", "v1.2_final"},
		{"ascii consecutive dashes", "/src/a---b.pdf", "a-b"},
		{"ascii leading trailing dash", "/src/-name-.pdf", "name"},
		{"korean only", "/src/판례.pdf", "판례"},
		{"korean mixed", "/src/판례_95도250.pdf", "판례_95도250"},
		{"korean with english", "/src/AI시대-뉴스.pdf", "ai시대-뉴스"},
		{"korean with spaces", "/src/한국어 파일명.pdf", "한국어-파일명"},
		{"cjk japanese", "/src/東京タワー.pdf", "東京タワー"},
		{"punctuation only", "/src/!!!.pdf", "src-" + fakeHash[:8]},
		{"empty after strip", "/src/.pdf", "src-" + fakeHash[:8]},
		{"mixed unicode punctuation", "/src/데이터(2024).pdf", "데이터2024"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := slugFor(tt.path, fakeHash)
			if got != tt.want {
				t.Errorf("slugFor(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}
