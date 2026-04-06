package obsidian

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/adrg/frontmatter"

	"github.com/teslamint/cogvault/internal/adapter"
	cverr "github.com/teslamint/cogvault/internal/errors"
)

var (
	wikilinkRe  = regexp.MustCompile(`\[\[([^\]\n]+)\]\]`)
	inlineTagRe = regexp.MustCompile(`(?:^|\s)#([a-zA-Z\p{L}][\w\p{L}/\-]*)`)
	dataviewRe  = regexp.MustCompile(`(?m)^(\w+)::\s*(.+)$`)
)

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

func (a *ObsidianAdapter) Parse(root, relPath string, includeContent bool) (*adapter.Source, error) {
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
	links, attachments := extractWikilinks(string(body))
	tags := extractTags(fm, string(body))
	dvFields := extractDataview(string(body))
	aliases := adapter.ExtractAliases(fm)

	src := &adapter.Source{
		Path:           cleaned,
		Title:          title,
		Frontmatter:    fm,
		Links:          links,
		Attachments:    attachments,
		Tags:           tags,
		DataviewFields: dvFields,
		Aliases:        aliases,
		SourceType:     "obsidian",
	}
	if includeContent {
		src.Content = string(body)
	}
	return src, nil
}

func extractWikilinks(body string) ([]string, []string) {
	matches := wikilinkRe.FindAllStringSubmatchIndex(body, -1)
	linkSeen := map[string]bool{}
	attachSeen := map[string]bool{}
	var links, attachments []string

	for _, loc := range matches {
		matchStart := loc[0]
		captured := body[loc[2]:loc[3]]

		target := captured
		if idx := strings.Index(target, "|"); idx >= 0 {
			target = target[:idx]
		}
		if idx := strings.Index(target, "#"); idx >= 0 {
			target = target[:idx]
		}
		target = strings.TrimSpace(target)
		if target == "" {
			continue
		}

		isEmbed := matchStart > 0 && body[matchStart-1] == '!'
		if isEmbed {
			if !attachSeen[target] {
				attachments = append(attachments, target)
				attachSeen[target] = true
			}
		} else {
			if !linkSeen[target] {
				links = append(links, target)
				linkSeen[target] = true
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

func extractTags(fm map[string]any, body string) []string {
	tags := adapter.ExtractFrontmatterTags(fm)
	seen := map[string]bool{}
	for _, t := range tags {
		seen[t] = true
	}

	for _, m := range inlineTagRe.FindAllStringSubmatch(body, -1) {
		tag := m[1]
		if !seen[tag] {
			tags = append(tags, tag)
			seen[tag] = true
		}
	}

	return tags
}

func extractDataview(body string) map[string]string {
	fields := map[string]string{}
	for _, m := range dataviewRe.FindAllStringSubmatch(body, -1) {
		fields[m[1]] = strings.TrimSpace(m[2])
	}
	return fields
}
