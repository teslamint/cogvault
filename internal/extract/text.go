package extract

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Extractor selects complete-only extraction by source file extension.
// Markdown and plain-text sources are returned verbatim; PDFs use
// PDFExtractor. Text over the input limit fails instead of being truncated,
// matching the PDF contract.
type Extractor struct {
	pdf           *PDFExtractor
	maxInputChars int
}

func NewExtractor(maxInputChars int, commands Commands) *Extractor {
	return &Extractor{pdf: NewPDFExtractor(maxInputChars, commands), maxInputChars: maxInputChars}
}

func (e *Extractor) Extract(ctx context.Context, path string) (string, error) {
	switch ext := strings.ToLower(filepath.Ext(path)); ext {
	case ".pdf":
		return e.pdf.Extract(ctx, path)
	case ".md", ".markdown", ".txt":
		return e.text(path)
	default:
		return "", fmt.Errorf("unsupported source type %q", ext)
	}
}

func (e *Extractor) text(path string) (string, error) {
	if e.maxInputChars <= 0 {
		return "", fmt.Errorf("invalid input character limit: %d", e.maxInputChars)
	}
	// Ingest skips files over max_file_size_mb before extraction, so reading
	// the whole file is bounded.
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(b) {
		return "", fmt.Errorf("text source is not valid UTF-8")
	}
	if utf8.RuneCount(b) > e.maxInputChars {
		return "", fmt.Errorf("complete text exceeds input limit")
	}
	return string(b), nil
}
