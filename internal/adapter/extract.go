package adapter

import (
	"path/filepath"
	"regexp"
	"strings"
)

var headingRe = regexp.MustCompile(`(?m)^#\s+(.+)$`)

func ExtractTitle(fm map[string]any, body []byte, relPath string) string {
	if t, ok := fm["title"]; ok {
		if s, ok := t.(string); ok && s != "" {
			return s
		}
	}
	if m := headingRe.FindSubmatch(body); m != nil {
		return strings.TrimSpace(string(m[1]))
	}
	base := filepath.Base(relPath)
	return strings.TrimSuffix(base, ".md")
}

func ExtractAliases(fm map[string]any) []string {
	var aliases []string
	if a, ok := fm["aliases"]; ok {
		switch v := a.(type) {
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok && s != "" {
					aliases = append(aliases, strings.TrimSpace(s))
				}
			}
		case string:
			if s := strings.TrimSpace(v); s != "" {
				aliases = append(aliases, s)
			}
		}
	}
	if aliases == nil {
		aliases = []string{}
	}
	return aliases
}

func ExtractFrontmatterTags(fm map[string]any) []string {
	seen := map[string]bool{}
	var tags []string
	if t, ok := fm["tags"]; ok {
		switch v := t.(type) {
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok && s != "" {
					s = strings.TrimSpace(s)
					if !seen[s] {
						tags = append(tags, s)
						seen[s] = true
					}
				}
			}
		case string:
			for _, s := range strings.Split(v, ",") {
				s = strings.TrimSpace(s)
				if s != "" && !seen[s] {
					tags = append(tags, s)
					seen[s] = true
				}
			}
		}
	}
	if tags == nil {
		tags = []string{}
	}
	return tags
}
