package extract

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"golang.org/x/sys/unix"
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
		return e.text(ctx, path)
	default:
		return "", fmt.Errorf("unsupported source type %q", ext)
	}
}

func (e *Extractor) text(ctx context.Context, path string) (string, error) {
	if e.maxInputChars <= 0 {
		return "", fmt.Errorf("invalid input character limit: %d", e.maxInputChars)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// The scan skipped symlinks, so a final symlink here means the entry was
	// swapped since; O_NONBLOCK keeps a FIFO from blocking the open.
	f, err := os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil {
		return "", err
	} else if !info.Mode().IsRegular() {
		return "", fmt.Errorf("text source is not a regular file")
	}
	// Read at most one byte past the largest valid input, so a file that
	// grew after the scan's size check cannot use unbounded memory.
	limit := int64(e.maxInputChars) * utf8.UTFMax
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return "", err
	}
	if int64(len(b)) > limit {
		return "", fmt.Errorf("complete text exceeds input limit")
	}
	if !utf8.Valid(b) {
		return "", fmt.Errorf("text source is not valid UTF-8")
	}
	if utf8.RuneCount(b) > e.maxInputChars {
		return "", fmt.Errorf("complete text exceeds input limit")
	}
	return string(b), nil
}
