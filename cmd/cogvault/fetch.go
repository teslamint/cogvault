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

	name, _ := cmd.Flags().GetString("name")
	if name == "" {
		name = slugFromURL(rawURL)
	}
	if !strings.HasSuffix(name, ".md") {
		name += ".md"
	}

	outPath := filepath.Join(srcDir, name)
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		return fmt.Errorf("create source dir: %w", err)
	}

	content := formatFetchedContent(rawURL, body)
	if err := os.WriteFile(outPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outPath, err)
	}

	cmd.Printf("saved %s (%d bytes) → run cogvault ingest to digest\n", outPath, len(content))
	return nil
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
	if len(path) > 80 {
		path = path[:80]
	}
	return "web-" + path
}

func formatFetchedContent(sourceURL string, body []byte) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString(fmt.Sprintf("source_url: %s\n", sourceURL))
	b.WriteString(fmt.Sprintf("fetched_at: %s\n", time.Now().UTC().Format("2006-01-02")))
	b.WriteString("---\n\n")
	b.Write(body)
	return b.String()
}
