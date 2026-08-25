package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"github.com/teslamint/cogvault/internal/config"
)

func newFetchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fetch <url>",
		Short: "Download a URL and save it as a source file for ingest",
		Args:  cobra.ExactArgs(1),
		RunE:  runFetch,
	}
	cmd.Flags().String("source-dir", "", "target source directory (defaults to first configured source path)")
	cmd.Flags().String("name", "", "output filename (default: derived from URL)")
	return cmd
}

func runFetch(cmd *cobra.Command, args []string) error {
	rawURL := args[0]
	if _, err := url.ParseRequestURI(rawURL); err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	configPath, err := resolveConfigPath(cmd)
	if err != nil {
		return err
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	srcDir, _ := cmd.Flags().GetString("source-dir")
	if srcDir == "" {
		if len(cfg.Sources) == 0 {
			return fmt.Errorf("no source directories configured; use --source-dir")
		}
		srcDir = cfg.Sources[0].Path
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(rawURL)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", rawURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch %s: HTTP %d", rawURL, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	// A URL that returns binary media (PDF, image) would otherwise be
	// deposited as a .md capture and pushed to the LLM digest as garbage.
	// Two gates: the declared Content-Type (when present) and a sniff of
	// the body itself — hosts that lie (`text/plain` over a PDF) or omit
	// the header must not slip binary through. httpDetectContentType
	// returns the sniffed type of the first 512 bytes.
	if ct := resp.Header.Get("Content-Type"); ct != "" && !isTextContentType(ct) {
		return fmt.Errorf("fetch %s: unsupported Content-Type %q; expected a text response", rawURL, ct)
	}
	if sniffed := http.DetectContentType(body); !isTextContentType(sniffed) {
		return fmt.Errorf("fetch %s: response body sniffs as %q; expected text", rawURL, sniffed)
	}
	if !utf8.Valid(body) {
		return fmt.Errorf("fetch %s: response body is not valid UTF-8", rawURL)
	}

	name, _ := cmd.Flags().GetString("name")
	if name == "" {
		name = slugFromURL(rawURL)
	}
	// The name ends up inside filepath.Join(srcDir, name); a user-supplied
	// name with separators or ".." could escape the source directory. The
	// CLI runs with local-user trust, unlike the MCP boundary, but staying
	// consistent with the repo's own storage-layer traversal protection
	// costs one check. Validated before the ".md" suffix is appended so a
	// bare ".." cannot become a legal-looking "...md".
	if name != filepath.Base(name) || name == "." || name == ".." {
		return fmt.Errorf("--name: %q must be a plain filename", name)
	}
	if !strings.HasSuffix(name, ".md") {
		name += ".md"
	}
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		return fmt.Errorf("create source dir: %w", err)
	}
	outPath := filepath.Join(srcDir, name)

	content := formatFetchedContent(rawURL, body)
	// Temp-file + rename: a crash mid os.WriteFile leaves a partial capture
	// in the source directory; atomic replacement prevents that.
	tmp := outPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, outPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write %s: %w", outPath, err)
	}

	cmd.Printf("saved %s (%d bytes) → run cogvault ingest to digest\n", outPath, len(content))
	return nil
}

// isTextContentType reports whether a Content-Type header names a text-ish
// type fetch is willing to store as a markdown capture. An absent header is
// allowed (some plain-file hosts omit it) and left to the UTF-8 gate.
func isTextContentType(ct string) bool {
	mt := strings.TrimSpace(strings.SplitN(ct, ";", 2)[0])
	mt = strings.ToLower(mt)
	switch mt {
	case "", "text/plain", "text/markdown", "text/x-markdown", "text/html",
		"application/json", "application/xml", "text/xml":
		return true
	}
	return strings.HasPrefix(mt, "text/")
}

func slugFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		h := sha256.Sum256([]byte(rawURL))
		return fmt.Sprintf("web-%x", h[:4])
	}
	path := strings.Trim(u.Path, "/")
	if path == "" {
		path = u.Host
	}
	path = strings.ReplaceAll(path, "/", "-")
	path = strings.ReplaceAll(path, " ", "-")
	path = truncateRunes(path, 80)
	return "web-" + path
}

// truncateRunes cuts s to at most n runes without splitting a multi-byte
// character; a byte-slice cut would produce invalid UTF-8 filenames from
// non-ASCII URLs.
func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

func formatFetchedContent(sourceURL string, body []byte) string {
	var b strings.Builder
	b.WriteString("---\n")
	// Quoted: a URL containing " #" would otherwise start a YAML comment
	// and truncate the value at parse time.
	fmt.Fprintf(&b, "source_url: %q\n", sourceURL)
	fmt.Fprintf(&b, "fetched_at: %s\n", time.Now().UTC().Format("2006-01-02"))
	b.WriteString("---\n\n")
	b.Write(body)
	return b.String()
}
