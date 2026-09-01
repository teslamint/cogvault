package extract

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// fake returns a Commands.CommandContext that runs sh snippets, records argv,
// and can record per-command invocations. No host tools are needed.
type recorder struct{ calls *[]string }

func shOut(s string) *exec.Cmd {
	// Pass output via environment variable to survive sh quoting for any byte
	// sequence without Go's %q producing \n escapes sh does not interpret.
	return exec.Command("sh", "-c", `printf '%s' "$OUT"`)
}

func envCmd(s string, name string, args ...string) *exec.Cmd {
	cmd := exec.Command("sh", "-c", `printf '%s' "$OUT"`)
	cmd.Env = append(os.Environ(), "OUT="+s)
	return cmd
}

func TestExtractUsableTextLayer(t *testing.T) {
	text := strings.Repeat("abcdefgh", 100)
	var seen []string
	cmds := Commands{CommandContext: func(ctx context.Context, name string, args ...string) *exec.Cmd {
		seen = append(seen, name)
		return envCmd(text, name, args...)
	}}
	e := NewPDFExtractor(10000, cmds)
	out, err := e.Extract(context.Background(), "f.pdf")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if out != text {
		t.Fatalf("text mismatch: %q", short(out))
	}
	for _, c := range seen {
		if c != "pdftotext" {
			t.Fatalf("unexpected command %q during text-layer extraction", c)
		}
	}
}

func TestExtractWhitespaceTrimmed(t *testing.T) {
	text := "  \n\t " + strings.Repeat("abcdefgh", 12) + "  \n "
	cmds := Commands{CommandContext: fakeCmdWithOutput(text)}
	e := NewPDFExtractor(10000, cmds)
	out, err := e.Extract(context.Background(), "f.pdf")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if out != strings.TrimSpace(text) {
		t.Fatalf("not trimmed: %q", short(out))
	}
}

func fakeCmdWithOutput(s string) func(context.Context, string, ...string) *exec.Cmd {
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return envCmd(s, name, args...)
	}
}

func TestExtractInvalidUTF8(t *testing.T) {
	bad := "\xff\xfe" + strings.Repeat("abcdefgh", 15)
	cmds := Commands{CommandContext: fakeCmdWithOutput(bad)}
	e := NewPDFExtractor(10000, cmds)
	_, err := e.Extract(context.Background(), "f.pdf")
	if err == nil || strings.Contains(err.Error(), "transient") {
		t.Fatalf("want permanent encoding error, got %v", err)
	}
}

func TestExtract79vs80Runes(t *testing.T) {
	below := strings.Repeat("a", 79)
	at := strings.Repeat("a", 80)

	mk := func(s string) Commands {
		return Commands{CommandContext: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			if name == "pdftotext" {
				return fakeCmdWithOutput(s)(ctx, name, args...)
			}
			// Any non-pdftotext invocation proves OCR fallback was attempted.
			return envCmd("FALLBACK-INVOKED", name, args...)
		}}
	}

	e1 := NewPDFExtractor(10000, mk(below))
	_, err := e1.Extract(context.Background(), "f.pdf")
	if err == nil || !strings.Contains(err.Error(), "pdfinfo") {
		t.Fatalf("79 runes should route to OCR fallback (pdfinfo preflight), got %v", err)
	}
	e2 := NewPDFExtractor(10000, mk(at))
	if _, err := e2.Extract(context.Background(), "f.pdf"); err != nil {
		t.Fatalf("80 runes: %v", err)
	}
}

func TestExtractLetterlessTextFallsBackToOCR(t *testing.T) {
	sym := strings.Repeat("!? ", 5000)
	var seen []string
	cmds := Commands{CommandContext: func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "pdftotext" {
			return fakeCmdWithOutput(sym)(ctx, name, args...)
		}
		seen = append(seen, name)
		return ocrFixture(name, args)
	}}
	e := NewPDFExtractor(1000, cmds)
	out, err := e.Extract(context.Background(), "f.pdf")
	joined := strings.Join(seen, ",")
	// Complete-only contract: an over-cap letterless text layer is drained and
	// discarded; OCR then produces the retained result.
	if err != nil {
		t.Fatalf("OCR fallback after letterless candidate: %v", err)
	}
	if !strings.Contains(joined, "pdfinfo") || !strings.Contains(joined, "tesseract") {
		t.Fatalf("OCR fallback not reached: %q", joined)
	}
	if !strings.Contains(out, "page1-text") {
		t.Fatalf("expected OCR result, got %q", short(out))
	}
}

// pdftoppmCmd emulates `pdftoppm ... -singlefile <path> <prefix>` by writing a
// nonzero PNG to "<prefix>.png". The real render() stats that owned file
// instead of reading pdftoppm's stdout, which is empty on some Poppler builds.
func pdftoppmCmd(args []string) *exec.Cmd {
	prefix := args[len(args)-1]
	cmd := exec.Command("sh", "-c", `printf 'PNGDATA' > "$OUT.png"`)
	cmd.Env = append(os.Environ(), "OUT="+prefix)
	return cmd
}

func ocrFixture(name string, args []string) *exec.Cmd {
	switch name {
	case "pdfinfo":
		if len(args) > 0 && args[0] == "-f" {
			return envCmd("Page size: 612 x 792 pts (letter)", name, args...)
		}
		return envCmd("Pages: 2", name, args...)
	case "pdftoppm":
		return pdftoppmCmd(args)
	case "tesseract":
		page := 1
		for _, a := range args {
			if strings.Contains(a, "page-2") {
				page = 2
			}
		}
		return envCmd(fmt.Sprintf("page%d-text %s", page, strings.Repeat("x", 90)), name, args...)
	}
	return envCmd("", name, args...)
}

func TestExtractTrimmedTextIgnoresWhitespaceBeyondRetentionCap(t *testing.T) {
	text := strings.Repeat(" \n\t", 1000) + strings.Repeat("a", 80) + strings.Repeat(" \n\t", 1000)
	e := NewPDFExtractor(80, Commands{CommandContext: fakeCmdWithOutput(text)})
	out, err := e.Extract(context.Background(), "f.pdf")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if out != strings.Repeat("a", 80) {
		t.Fatalf("unexpected trimmed text: %q", short(out))
	}
}

func TestExtractTextLayerAlphanumericAfterRetentionCapIsOverLimit(t *testing.T) {
	text := strings.Repeat("!", 400) + strings.Repeat("a", 80)
	e := NewPDFExtractor(80, Commands{CommandContext: fakeCmdWithOutput(text)})
	_, err := e.Extract(context.Background(), "f.pdf")
	if err == nil || !strings.Contains(err.Error(), "complete PDF text exceeds input limit") {
		t.Fatalf("want permanent over-cap text-layer error, got %v", err)
	}
}

func TestExtractAggregateOCRCapWhileTesseractStreams(t *testing.T) {
	var tesseractCalls int
	cmds := Commands{CommandContext: func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "pdftotext" {
			return exec.Command("sh", "-c", "printf ''")
		}
		switch name {
		case "pdfinfo":
			if len(args) > 0 && args[0] == "-f" {
				return exec.Command("sh", "-c", "printf '%s' 'Page size: 612 x 792 pts'")
			}
			return exec.Command("sh", "-c", "printf '%s' 'Pages: 2'")
		case "pdftoppm":
			return pdftoppmCmd(args)
		case "tesseract":
			tesseractCalls++
			return exec.Command("sh", "-c", "printf 'ok '; dd if=/dev/zero bs=1m count=8 2>/dev/null | tr '\\0' x")
		}
		return exec.Command("sh", "-c", "true")
	}}
	e := NewPDFExtractor(1000, cmds)
	_, err := e.Extract(context.Background(), "f.pdf")
	if err == nil || strings.Contains(err.Error(), "transient") {
		t.Fatalf("want permanent aggregate-cap error, got %v", err)
	}
	if tesseractCalls != 1 {
		t.Fatalf("aggregate cap should stop after first over-cap page, got %d tesseract calls", tesseractCalls)
	}
}

func TestExtractOverCapUsableText(t *testing.T) {
	text := strings.Repeat("abcdefgh", 1000)
	cmds := Commands{CommandContext: fakeCmdWithOutput(text)}
	e := NewPDFExtractor(1000, cmds)
	_, err := e.Extract(context.Background(), "f.pdf")
	if err == nil || strings.Contains(err.Error(), "transient") {
		t.Fatalf("want permanent over-cap, got %v", err)
	}
}

func TestExtractOcrPageOrder(t *testing.T) {
	var seen []string
	cmds := Commands{CommandContext: func(ctx context.Context, name string, args ...string) *exec.Cmd {
		seen = append(seen, name+" "+strings.Join(args, " "))
		if name == "pdftotext" {
			return exec.Command("sh", "-c", "printf ''")
		}
		return ocrFixture(name, args)
	}}
	e := NewPDFExtractor(100000, cmds)
	out, err := e.Extract(context.Background(), "f.pdf")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	i1, i2 := strings.Index(out, "page1-text"), strings.Index(out, "page2-text")
	if i1 < 0 || i2 < 0 || i1 > i2 {
		t.Fatalf("page order wrong: %q", short(out))
	}
	_ = seen
	for _, s := range seen {
		if strings.HasPrefix(s, "tesseract ") && !strings.Contains(s, "eng+kor") {
			t.Fatalf("tesseract not eng+kor: %q", s)
		}
	}
}

func TestExtractPageLimitExceeded(t *testing.T) {
	cmds := Commands{CommandContext: func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "pdftotext" {
			return exec.Command("sh", "-c", "printf ''")
		}
		if name == "pdfinfo" {
			return exec.Command("sh", "-c", "printf '%s' 'Pages: 51'")
		}
		t.Fatalf("unexpected %q after page-limit preflight", name)
		return nil
	}}
	e := NewPDFExtractor(10000, cmds)
	_, err := e.Extract(context.Background(), "f.pdf")
	if err == nil || strings.Contains(err.Error(), "transient") {
		t.Fatalf("want permanent page-limit error, got %v", err)
	}
}

func TestExtractLaterOversizePage(t *testing.T) {
	calls := []string{}
	cmds := Commands{CommandContext: func(ctx context.Context, name string, args ...string) *exec.Cmd {
		calls = append(calls, name)
		if name == "pdftotext" {
			return exec.Command("sh", "-c", "printf ''")
		}
		switch name {
		case "pdfinfo":
			if len(args) > 0 && args[0] == "-f" {
				if len(args) >= 2 && args[1] == "2" {
					return exec.Command("sh", "-c", "printf '%s' 'Page size: 6000 x 6000 pts'")
				}
				return exec.Command("sh", "-c", "printf '%s' 'Page size: 612 x 792 pts (letter)'")
			}
			return exec.Command("sh", "-c", "printf '%s' 'Pages: 2'")
		case "pdftoppm":
			return pdftoppmCmd(args)
		case "tesseract":
			return exec.Command("sh", "-c", "printf '%s' 'ok "+strings.Repeat("x", 90)+"'")
		}
		return exec.Command("sh", "-c", "true")
	}}
	e := NewPDFExtractor(100000, cmds)
	_, err := e.Extract(context.Background(), "f.pdf")
	if err == nil || strings.Contains(err.Error(), "transient") {
		t.Fatalf("want permanent oversize error, got %v", err)
	}
	pdftoppmCount := 0
	for _, c := range calls {
		if c == "pdftoppm" {
			pdftoppmCount++
		}
	}
	if pdftoppmCount != 1 {
		t.Fatalf("expected exactly page 1 rendered once, got %d pdftoppm calls (%v)", pdftoppmCount, calls)
	}
}

func TestExtractAggregateOCRCap(t *testing.T) {
	pageText := "ok " + strings.Repeat("x", 900)
	cmds := Commands{CommandContext: func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "pdftotext" {
			return exec.Command("sh", "-c", "printf ''")
		}
		switch name {
		case "pdfinfo":
			if len(args) > 0 && args[0] == "-f" {
				return exec.Command("sh", "-c", "printf '%s' 'Page size: 612 x 792 pts'")
			}
			return exec.Command("sh", "-c", "printf '%s' 'Pages: 2'")
		case "pdftoppm":
			return pdftoppmCmd(args)
		case "tesseract":
			return exec.Command("sh", "-c", fmt.Sprintf("printf %%s %q", pageText))
		}
		return exec.Command("sh", "-c", "true")
	}}
	e := NewPDFExtractor(1000, cmds)
	_, err := e.Extract(context.Background(), "f.pdf")
	if err == nil || strings.Contains(err.Error(), "transient") {
		t.Fatalf("want permanent aggregate-cap error, got %v", err)
	}
}

func TestExtractDeadlineTransient(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmds := Commands{CommandContext: func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "sleep 5")
	}}
	e := NewPDFExtractor(1000, cmds)
	_, err := e.Extract(ctx, "f.pdf")
	if !errors.Is(err, ErrTransient) {
		t.Fatalf("want ErrTransient, got %v", err)
	}
}

func TestExtractUnusableOCRFailsPermanent(t *testing.T) {
	cmds := Commands{CommandContext: func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "pdftotext" {
			return exec.Command("sh", "-c", "printf ''")
		}
		switch name {
		case "pdfinfo":
			if len(args) > 0 && args[0] == "-f" {
				return exec.Command("sh", "-c", "printf '%s' 'Page size: 612 x 792 pts'")
			}
			return exec.Command("sh", "-c", "printf '%s' 'Pages: 1'")
		case "pdftoppm":
			return pdftoppmCmd(args)
		case "tesseract":
			return exec.Command("sh", "-c", "printf ''")
		}
		return exec.Command("sh", "-c", "true")
	}}
	e := NewPDFExtractor(1000, cmds)
	_, err := e.Extract(context.Background(), "f.pdf")
	if err == nil || strings.Contains(err.Error(), "transient") {
		t.Fatalf("want permanent unusable-OCR error, got %v", err)
	}
}

func TestExtractCleansTempDir(t *testing.T) {
	var pngPath string
	cmds := Commands{CommandContext: func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "tesseract" {
			pngPath = args[0]
		}
		if name == "pdftotext" {
			return exec.Command("sh", "-c", "printf ''")
		}
		return ocrFixture(name, args)
	}}
	e := NewPDFExtractor(10000, cmds)
	_, _ = e.Extract(context.Background(), "f.pdf")
	if pngPath != "" {
		if _, err := os.Stat(pngPath); err == nil {
			t.Fatalf("temp PNG remains: %s", pngPath)
		}
	}
}

// TestExtractParsesRangedPdfinfoDimensions pins the real Poppler output shape:
// `pdfinfo -f N -l N` emits "Page N size: W x H pts", not "Page size: ...".
// A parser matching only the whole-document "Page size:" prefix fails every
// OCR fallback with "pdfinfo did not report page dimensions".
func TestExtractParsesRangedPdfinfoDimensions(t *testing.T) {
	ocrText := strings.Repeat("가나다라마바사아", 20)
	cmds := Commands{CommandContext: func(ctx context.Context, name string, args ...string) *exec.Cmd {
		switch name {
		case "pdftotext":
			return exec.Command("sh", "-c", "printf ''")
		case "pdfinfo":
			if len(args) > 0 && args[0] == "-f" {
				return envCmd("Page    1 size:  612 x 792 pts (letter)", name, args...)
			}
			return envCmd("Pages:           1", name, args...)
		case "pdftoppm":
			return pdftoppmCmd(args)
		case "tesseract":
			return envCmd(ocrText, name, args...)
		}
		t.Fatalf("unexpected command %q", name)
		return nil
	}}
	e := NewPDFExtractor(10000, cmds)
	out, err := e.Extract(context.Background(), "f.pdf")
	if err != nil {
		t.Fatalf("ranged pdfinfo dimensions should parse and OCR should succeed, got %v", err)
	}
	if out != ocrText {
		t.Fatalf("OCR text mismatch: %q", short(out))
	}
}

// TestExtractRendersToSinglefileNotStdout pins the render contract against a
// Poppler build whose `pdftoppm -png <path> -` stdout form writes nothing:
// render() must pass -singlefile and read the owned file, and an empty render
// must fail rather than hand tesseract a truncated image.
func TestExtractRendersToSinglefileNotStdout(t *testing.T) {
	var pdftoppmArgs []string
	stdoutOnly := Commands{CommandContext: func(ctx context.Context, name string, args ...string) *exec.Cmd {
		switch name {
		case "pdftotext":
			return exec.Command("sh", "-c", "printf ''")
		case "pdfinfo":
			if len(args) > 0 && args[0] == "-f" {
				return envCmd("Page 1 size: 612 x 792 pts", name, args...)
			}
			return envCmd("Pages: 1", name, args...)
		case "pdftoppm":
			pdftoppmArgs = append([]string{}, args...)
			// Emulate the broken host: emit bytes to stdout, write no file.
			return exec.Command("sh", "-c", "printf 'PNGDATA'")
		case "tesseract":
			return envCmd(strings.Repeat("a", 90), name, args...)
		}
		t.Fatalf("unexpected command %q", name)
		return nil
	}}
	e := NewPDFExtractor(10000, stdoutOnly)
	_, err := e.Extract(context.Background(), "f.pdf")
	if err == nil {
		t.Fatalf("an empty render must fail, not reach OCR with a missing image")
	}
	sawSinglefile := false
	for _, a := range pdftoppmArgs {
		if a == "-singlefile" {
			sawSinglefile = true
		}
		if a == "-" {
			t.Fatalf("render must not use pdftoppm stdout mode; argv=%v", pdftoppmArgs)
		}
	}
	if !sawSinglefile {
		t.Fatalf("render must pass -singlefile; argv=%v", pdftoppmArgs)
	}
}

func short(s string) string {
	if len(s) > 120 {
		return s[:120] + "..."
	}
	return s
}
