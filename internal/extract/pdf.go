// Package extract provides bounded, complete-only PDF text extraction.
package extract

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

var ErrTransient = errors.New("transient PDF extraction failure")

const (
	maxPages      = 50
	maxPixels     = 16 * 1000 * 1000
	maxImageBytes = 32 * 1024 * 1024

	// ExtractionContractVersion identifies the complete-only text extraction
	// behavior that contributes to text-mode digest identity.
	ExtractionContractVersion = "pdf-text-v1"
)

// RequiredTools lists the executables and Tesseract language data required by
// the OpenAI PDF ingestion path.
var RequiredTools = []string{"pdftotext", "pdfinfo", "pdftoppm", "tesseract", "eng", "kor"}

// ValidatePrerequisites checks that every required executable or language
// resource is available. lookup is injected by command tests; production
// callers should provide a lookup that handles both executable names and
// language data names.
func ValidatePrerequisites(lookup func(string) (string, error)) error {
	if lookup == nil {
		return fmt.Errorf("validate PDF prerequisites: nil lookup")
	}
	for _, name := range RequiredTools {
		if _, err := lookup(name); err != nil {
			return fmt.Errorf("PDF ingestion prerequisite %q not found: %w", name, err)
		}
	}
	return nil
}

type Commands struct {
	CommandContext func(context.Context, string, ...string) *exec.Cmd
}

type PDFExtractor struct {
	maxInputChars int
	commands      Commands
}

func NewPDFExtractor(maxInputChars int, commands Commands) *PDFExtractor {
	if commands.CommandContext == nil {
		commands.CommandContext = exec.CommandContext
	}
	return &PDFExtractor{maxInputChars: maxInputChars, commands: commands}
}

func (e *PDFExtractor) Extract(ctx context.Context, path string) (string, error) {
	if e.maxInputChars <= 0 {
		return "", fmt.Errorf("invalid input character limit: %d", e.maxInputChars)
	}
	text, textUsable, textOverflow, err := e.textLayer(ctx, path)
	if err != nil {
		return "", err
	}
	if textUsable {
		if textOverflow || utf8.RuneCountInString(text) > e.maxInputChars {
			return "", fmt.Errorf("complete PDF text exceeds input limit")
		}
		return text, nil
	}
	return e.ocr(ctx, path)
}

func (e *PDFExtractor) textLayer(ctx context.Context, path string) (string, bool, bool, error) {
	cmd := e.commands.CommandContext(ctx, "pdftotext", "-layout", path, "-")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return "", false, false, transient(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return "", false, false, transient(err)
	}
	collector := newStreamCollector(e.maxInputChars)
	_, readErr := io.Copy(collector, out)
	finishErr := collector.Finish()
	waitErr := cmd.Wait()
	if readErr != nil {
		return "", false, false, readErr
	}
	if finishErr != nil {
		return "", false, false, finishErr
	}
	if err := classifyProcess(ctx, waitErr, stderr.String(), "pdftotext"); err != nil {
		return "", false, false, err
	}
	return collector.String(), collector.Usable(), collector.Overflow(), nil
}

func (e *PDFExtractor) ocr(ctx context.Context, path string) (string, error) {
	dir, err := os.MkdirTemp("", "cogvault-pdf-")
	if err != nil {
		return "", transient(err)
	}
	defer os.RemoveAll(dir)
	pages, err := e.pageCount(ctx, path)
	if err != nil {
		return "", err
	}
	if pages < 1 {
		return "", fmt.Errorf("PDF has no pages")
	}
	if pages > maxPages {
		return "", fmt.Errorf("PDF exceeds %d-page limit", maxPages)
	}
	collector := newStreamCollector(e.maxInputChars)
	for page := 1; page <= pages; page++ {
		w, h, err := e.pageDimensions(ctx, path, page)
		if err != nil {
			return "", err
		}
		pxW, pxH := math.Ceil(w*200/72), math.Ceil(h*200/72)
		if pxW <= 0 || pxH <= 0 || pxW*pxH > maxPixels {
			return "", fmt.Errorf("PDF page %d exceeds pixel limit", page)
		}
		imagePath := filepath.Join(dir, fmt.Sprintf("page-%d.png", page))
		if err := e.render(ctx, path, page, imagePath); err != nil {
			return "", err
		}
		if page > 1 {
			_, _ = collector.Write([]byte{'\n'})
		}
		if err := e.ocrPageInto(ctx, imagePath, collector); err != nil {
			os.Remove(imagePath)
			return "", err
		}
		os.Remove(imagePath)
		if collector.Overflow() {
			return "", fmt.Errorf("complete OCR text exceeds input limit")
		}
	}
	if err := collector.Finish(); err != nil {
		return "", err
	}
	if !collector.Usable() {
		return "", fmt.Errorf("OCR produced no usable text")
	}
	return collector.String(), nil
}

func (e *PDFExtractor) pageCount(ctx context.Context, path string) (int, error) {
	output, err := e.run(ctx, "pdfinfo", path)
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Pages:") {
			n, x := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Pages:")))
			if x != nil {
				return 0, fmt.Errorf("invalid PDF page count")
			}
			return n, nil
		}
	}
	return 0, fmt.Errorf("pdfinfo did not report page count")
}

func (e *PDFExtractor) pageDimensions(ctx context.Context, path string, page int) (float64, float64, error) {
	output, err := e.run(ctx, "pdfinfo", "-f", strconv.Itoa(page), "-l", strconv.Itoa(page), path)
	if err != nil {
		return 0, 0, err
	}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		// pdfinfo emits "Page size: W x H pts" for a single-page probe and
		// "Page N size: W x H pts" for a -f/-l range probe. Match both by
		// splitting on the "size:" label rather than a fixed prefix.
		if !strings.HasPrefix(line, "Page") {
			continue
		}
		idx := strings.Index(line, "size:")
		if idx < 0 {
			continue
		}
		fields := strings.Fields(strings.TrimSpace(line[idx+len("size:"):]))
		if len(fields) < 3 {
			return 0, 0, fmt.Errorf("invalid PDF page dimensions")
		}
		w, x := strconv.ParseFloat(fields[0], 64)
		h, y := strconv.ParseFloat(fields[2], 64)
		if x != nil || y != nil {
			return 0, 0, fmt.Errorf("invalid PDF page dimensions")
		}
		return w, h, nil
	}
	return 0, 0, fmt.Errorf("pdfinfo did not report page dimensions")
}

func (e *PDFExtractor) render(ctx context.Context, path string, page int, imagePath string) error {
	// pdftoppm's stdout form (`-png <file> -`) writes nothing on some Poppler
	// builds (verified empty on Poppler 26.08.0), so render to an owned file
	// with `-singlefile`, which writes exactly <prefix>.png. The pixel
	// preflight in ocr() already rejects pages above maxPixels before we get
	// here; we additionally reject a rendered file above maxImageBytes.
	prefix := strings.TrimSuffix(imagePath, ".png")
	cmd := e.commands.CommandContext(ctx, "pdftoppm",
		"-f", strconv.Itoa(page), "-l", strconv.Itoa(page),
		"-r", "200", "-png", "-singlefile", path, prefix)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if err := classifyProcess(ctx, runErr, stderr.String(), "pdftoppm"); err != nil {
		os.Remove(imagePath)
		return err
	}
	info, statErr := os.Stat(imagePath)
	if statErr != nil {
		os.Remove(imagePath)
		return transient(statErr)
	}
	if info.Size() > maxImageBytes {
		os.Remove(imagePath)
		return fmt.Errorf("rendered PDF page exceeds image limit")
	}
	if info.Size() == 0 {
		os.Remove(imagePath)
		return fmt.Errorf("pdftoppm produced no image for page %d", page)
	}
	return nil
}

func (e *PDFExtractor) run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := e.commands.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", classifyProcess(ctx, err, stderr.String(), name)
	}
	return stdout.String(), nil
}

type streamCollector struct {
	limit        int
	maxBytes     int
	retained     []byte
	partial      []byte
	pending      []byte
	usableCount  int
	pendingRunes int
	content      bool
	runeCount    int
	alnum        bool
	overflow     bool
	invalid      bool
}

func newStreamCollector(limit int) *streamCollector {
	return &streamCollector{limit: limit, maxBytes: limit*4 + 1}
}
func (c *streamCollector) Write(p []byte) (int, error) {
	originalLen := len(p)
	if len(c.partial) > 0 {
		p = append(c.partial, p...)
		c.partial = nil
	}
	for len(p) > 0 {
		r, size := utf8.DecodeRune(p)
		if r == utf8.RuneError && size == 1 {
			if !utf8.FullRune(p) {
				c.partial = append(c.partial, p...)
				return originalLen, nil
			}
			c.invalid = true
			return originalLen, nil
		}
		c.consumeRune(r, p[:size])
		p = p[size:]
	}
	return originalLen, nil
}

func (c *streamCollector) consumeRune(r rune, encoded []byte) {
	if unicode.IsSpace(r) {
		if c.content {
			c.pendingRunes++
			if len(c.pending)+len(encoded) <= c.maxBytes {
				c.pending = append(c.pending, encoded...)
			}
		}
		return
	}
	if c.content {
		c.runeCount += c.pendingRunes
		c.appendBytes(c.pending)
	}
	c.pending = c.pending[:0]
	c.pendingRunes = 0
	c.usableCount++
	c.content = true
	c.runeCount++
	if unicode.IsLetter(r) || unicode.IsNumber(r) {
		c.alnum = true
	}
	c.appendBytes(encoded)
	if c.runeCount > c.limit {
		c.overflow = true
	}
}

func (c *streamCollector) appendBytes(p []byte) {
	if len(c.retained) >= c.maxBytes {
		return
	}
	remain := c.maxBytes - len(c.retained)
	if len(p) > remain {
		return
	}
	c.retained = append(c.retained, p...)
}

func (c *streamCollector) Finish() error {
	if len(c.partial) > 0 {
		c.invalid = true
	}
	if c.invalid {
		return fmt.Errorf("PDF text is not valid UTF-8")
	}
	return nil
}
func (c *streamCollector) String() string { return strings.TrimSpace(string(c.retained)) }
func (c *streamCollector) Usable() bool   { return c.usableCount >= 80 && c.alnum }
func (c *streamCollector) Overflow() bool { return c.overflow }

func (e *PDFExtractor) ocrPageInto(ctx context.Context, imagePath string, collector io.Writer) error {
	cmd := e.commands.CommandContext(ctx, "tesseract", imagePath, "stdout", "-l", "eng+kor")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return transient(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return transient(err)
	}
	_, readErr := io.Copy(collector, out)
	waitErr := cmd.Wait()
	if readErr != nil {
		return readErr
	}
	return classifyProcess(ctx, waitErr, stderr.String(), "tesseract")
}

func transient(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %v", ErrTransient, err)
}

func classifyProcess(ctx context.Context, err error, stderr, name string) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return transient(ctx.Err())
	}
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return transient(err)
	}
	if stderr != "" {
		return fmt.Errorf("%s failed: %s: %w", name, strings.TrimSpace(stderr), err)
	}
	return fmt.Errorf("%s failed: %w", name, err)
}
