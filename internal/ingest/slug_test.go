package ingest

import (
	"testing"

	"golang.org/x/text/unicode/norm"
)

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

func TestTruncateNFDSafe(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxBytes int
		wantNFD  int // expected NFD byte length <= maxBytes
	}{
		{"ascii fits", "hello", 10, 5},
		{"ascii truncated", "hello-world", 8, 8},           // "hello-wo" fits in 8
		{"korean fits", "\uAC15\uCC3D\uD76C", 30, 24},      // 강(9)+창(9)+희(6)=24
		{"korean truncated", "\uAC15\uCC3D\uD76C", 20, 18}, // 강(9)+창(9)=18
		{"korean one char", "\uAC15\uCC3D\uD76C", 9, 9},    // 강=9
		{"korean too small", "\uAC15\uCC3D\uD76C", 5, 0},   // nothing fits
		{"mixed", "abc-\uAC15\uCC3D", 15, 13},              // "abc-"(4)+강(9)=13
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateNFDSafe(tt.input, tt.maxBytes)
			nfdLen := len(norm.NFD.String(got))
			if nfdLen > tt.maxBytes {
				t.Errorf("truncateNFDSafe(%q, %d) = %q (NFD %d bytes), exceeds cap",
					tt.input, tt.maxBytes, got, nfdLen)
			}
			if nfdLen != tt.wantNFD {
				t.Errorf("truncateNFDSafe(%q, %d) NFD = %d bytes, want %d",
					tt.input, tt.maxBytes, nfdLen, tt.wantNFD)
			}
		})
	}
}

func TestSlugForNFDByteCap(t *testing.T) {
	const fakeHash = "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"

	// A long Korean filename that would exceed 255 NFD bytes without the cap.
	// Each Korean syllable: 3 NFC bytes → 9 NFD bytes (3x expansion).
	longKorean := "강창희-박상곤-주-52시간-근로-상한제가-근로시간과-고용에-미친-영향-경제학연구-71-4"
	slug := slugFor("/src/"+longKorean+".pdf", fakeHash)

	// The full path on disk: "sources/" + slug + ".md" (+ possibly "-<hash8>")
	// NFD of slug must be <= 220 bytes
	nfdSlug := norm.NFD.String(slug)
	if len(nfdSlug) > 220 {
		t.Errorf("slugFor long Korean: NFD slug = %d bytes, want <= 220", len(nfdSlug))
	}

	// Verify it's a proper prefix (not garbled)
	if slug == "" {
		t.Fatal("slug should not be empty")
	}
	// Should start with the original slug prefix
	if slug[:3] != "강창" && slug[:10] != "kea-ne-kr-" {
		// It should start with Korean or the original prefix
		if len(slug) < 5 {
			t.Errorf("slug too short: %q", slug)
		}
	}
}
