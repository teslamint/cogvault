package markdown

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/adrg/frontmatter"

	"github.com/teslamint/cogvault/internal/adapter"
	cverr "github.com/teslamint/cogvault/internal/errors"
)

var (
	mdLinkRe   = regexp.MustCompile(`!?\[([^\]]*)\]\(([^)]+)\)`)
	externalRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.\-]*:`)
	winAbsRe   = regexp.MustCompile(`^[A-Za-z]:[/\\]`)
)

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

type MarkdownAdapter struct{}

func New() *MarkdownAdapter {
	return &MarkdownAdapter{}
}

func (a *MarkdownAdapter) Name() string {
	return "markdown"
}

func (a *MarkdownAdapter) Scan(root string, exclude []string, fn func(path string) error) error {
	info, err := os.Stat(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("adapter.Scan %s: %w", root, cverr.ErrNotFound)
		}
		return fmt.Errorf("adapter.Scan %s: %w", root, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("adapter.Scan %s: %w", root, cverr.ErrNotFound)
	}

	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}

		if d.IsDir() {
			if adapter.IsExcluded(rel, exclude) {
				return fs.SkipDir
			}
			return nil
		}

		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}

		if !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}

		if adapter.IsExcluded(rel, exclude) {
			return nil
		}

		return fn(rel)
	})
}

func (a *MarkdownAdapter) Parse(root, relPath string, includeContent bool) (*adapter.Source, error) {
	cleaned, err := adapter.ValidateRelPath(relPath)
	if err != nil {
		return nil, err
	}

	if !strings.HasSuffix(strings.ToLower(cleaned), ".md") {
		return nil, fmt.Errorf("adapter.Parse %s: %w", cleaned, cverr.ErrNotMarkdown)
	}

	if err := adapter.CheckSymlinks(root, cleaned); err != nil {
		return nil, err
	}

	absPath := filepath.Join(root, cleaned)
	data, err := os.ReadFile(absPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("adapter.Parse %s: %w", cleaned, cverr.ErrNotFound)
		}
		return nil, fmt.Errorf("adapter.Parse %s: %w", cleaned, err)
	}

	data = bytes.TrimPrefix(data, utf8BOM)

	var fm map[string]any
	body, fmErr := frontmatter.Parse(bytes.NewReader(data), &fm)
	if fmErr != nil {
		fm = map[string]any{}
		body = data
	}
	if fm == nil {
		fm = map[string]any{}
	}

	title := adapter.ExtractTitle(fm, body, cleaned)
	links, attachments := extractMarkdownLinks(string(body))
	aliases := adapter.ExtractAliases(fm)
	tags := adapter.ExtractFrontmatterTags(fm)

	src := &adapter.Source{
		Path:           cleaned,
		Title:          title,
		Frontmatter:    fm,
		Links:          links,
		Attachments:    attachments,
		Tags:           tags,
		DataviewFields: map[string]string{},
		Aliases:        aliases,
		SourceType:     "markdown",
	}
	if includeContent {
		src.Content = string(body)
	}
	return src, nil
}

func extractMarkdownLinks(body string) ([]string, []string) {
	linkSeen := map[string]bool{}
	attachSeen := map[string]bool{}
	var links, attachments []string

	for _, m := range mdLinkRe.FindAllStringSubmatch(body, -1) {
		fullMatch := m[0]
		href := strings.TrimSpace(m[2])

		isImage := strings.HasPrefix(fullMatch, "!")

		if isExternalLink(href) {
			continue
		}
		if idx := strings.Index(href, "#"); idx >= 0 {
			href = href[:idx]
		}
		href = strings.TrimSpace(href)
		if href == "" {
			continue
		}

		if isImage {
			if !attachSeen[href] {
				attachments = append(attachments, href)
				attachSeen[href] = true
			}
		} else {
			if !linkSeen[href] {
				links = append(links, href)
				linkSeen[href] = true
			}
		}
	}

	if links == nil {
		links = []string{}
	}
	if attachments == nil {
		attachments = []string{}
	}
	return links, attachments
}

func isExternalLink(href string) bool {
	if externalRe.MatchString(href) {
		return true
	}
	if strings.HasPrefix(href, "//") {
		return true
	}
	if strings.HasPrefix(href, "/") {
		return true
	}
	if strings.HasPrefix(href, "#") {
		return true
	}
	if winAbsRe.MatchString(href) {
		return true
	}
	return false
}
