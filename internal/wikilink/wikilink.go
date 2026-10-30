// Package wikilink finds Obsidian [[wikilinks]] in page bodies and resolves
// them against the set of pages in the wiki. lint, graph, ingest and the MCP
// wiki_write tool share it so they agree on what a broken link is.
package wikilink

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/text/unicode/norm"
)

var linkRe = regexp.MustCompile(`\[\[([^\]\n]+)\]\]`)

// Match is one wikilink outside code. Start and End cover the brackets only;
// an embed's leading '!' sits at Start-1.
type Match struct {
	Start, End int
	// Target is the page name: the text before '|' and '#', trimmed.
	Target string
	// Display is the text a reader sees: the alias after '|', or the inner text.
	Display string
	Embed   bool
}

// Find returns the wikilinks in body in order, skipping fenced and inline code
// and links with an empty target.
func Find(body string) []Match {
	spans := codeSpans(body)
	var out []Match
	for _, loc := range linkRe.FindAllStringSubmatchIndex(body, -1) {
		if inCodeSpan(loc[0], spans) {
			continue
		}
		inner := body[loc[2]:loc[3]]
		target, display := inner, strings.TrimSpace(inner)
		if i := strings.Index(target, "|"); i >= 0 {
			display = strings.TrimSpace(target[i+1:])
			target = target[:i]
		}
		if i := strings.Index(target, "#"); i >= 0 {
			target = target[:i]
		}
		target = strings.TrimSpace(target)
		if target == "" {
			continue
		}
		out = append(out, Match{
			Start: loc[0], End: loc[1], Target: target, Display: display,
			Embed: loc[0] > 0 && body[loc[0]-1] == '!',
		})
	}
	return out
}

// Set resolves link targets to wiki page paths. Names are compared in Unicode
// NFC form, because macOS and iCloud may store Hangul file names decomposed
// (NFD) while page text uses the composed form.
type Set struct {
	exact map[string]string // NFC path -> path as scanned
	base  map[string]string // lower-case NFC basename without .md -> path
}

// NewSet builds a Set from wiki-relative page paths as returned by Scan.
func NewSet(paths []string) *Set {
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)
	s := &Set{exact: map[string]string{}, base: map[string]string{}}
	for _, p := range sorted {
		s.Add(p)
	}
	return s
}

// Add registers one more page path. An earlier path keeps a shared basename.
func (s *Set) Add(path string) {
	n := norm.NFC.String(path)
	if _, ok := s.exact[n]; !ok {
		s.exact[n] = path
	}
	b := strings.ToLower(strings.TrimSuffix(filepath.Base(n), ".md"))
	if _, ok := s.base[b]; !ok {
		s.base[b] = path
	}
}

// Resolve returns the page path link names, or "" when no page matches. It
// tries "<link>.md", "sources/<link>.md" and "<link>" verbatim, then a
// case-insensitive match on any page's basename.
func (s *Set) Resolve(link string) string {
	l := norm.NFC.String(link)
	for _, c := range []string{l + ".md", filepath.Join("sources", l+".md"), l} {
		if p, ok := s.exact[c]; ok {
			return p
		}
	}
	return s.base[strings.ToLower(l)]
}

// Unresolved returns the distinct non-embed link targets in body that resolve
// to no page, in order of first appearance.
func Unresolved(body string, s *Set) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range Find(body) {
		if m.Embed || seen[m.Target] || s.Resolve(m.Target) != "" {
			continue
		}
		seen[m.Target] = true
		out = append(out, m.Target)
	}
	return out
}

// StripUnresolved replaces every non-embed link that resolves to no page with
// its display text, and returns the new body and the distinct dropped targets.
// Embeds are left alone: they name attachments, not pages.
func StripUnresolved(body string, s *Set) (string, []string) {
	var b strings.Builder
	var dropped []string
	seen := map[string]bool{}
	last := 0
	for _, m := range Find(body) {
		if m.Embed || s.Resolve(m.Target) != "" {
			continue
		}
		b.WriteString(body[last:m.Start])
		b.WriteString(m.Display)
		last = m.End
		if !seen[m.Target] {
			seen[m.Target] = true
			dropped = append(dropped, m.Target)
		}
	}
	if last == 0 {
		return body, nil
	}
	b.WriteString(body[last:])
	return b.String(), dropped
}

func codeSpans(body string) [][2]int {
	var spans [][2]int
	lines := strings.Split(body, "\n")
	offset := 0
	inFence := false
	fenceStart := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !inFence && (strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")) {
			inFence = true
			fenceStart = offset
		} else if inFence && (strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")) {
			inFence = false
			spans = append(spans, [2]int{fenceStart, offset + len(line)})
		}
		offset += len(line) + 1
	}
	if inFence {
		spans = append(spans, [2]int{fenceStart, len(body)})
	}

	for i := 0; i < len(body); i++ {
		if body[i] != '`' || inCodeSpan(i, spans) {
			continue
		}
		ticks := 1
		for i+ticks < len(body) && body[i+ticks] == '`' {
			ticks++
		}
		start := i
		end := strings.Index(body[i+ticks:], strings.Repeat("`", ticks))
		if end < 0 {
			break
		}
		end += i + ticks + ticks
		spans = append(spans, [2]int{start, end})
		i = end - 1
	}
	return spans
}

func inCodeSpan(pos int, spans [][2]int) bool {
	for _, s := range spans {
		if pos >= s[0] && pos < s[1] {
			return true
		}
	}
	return false
}
