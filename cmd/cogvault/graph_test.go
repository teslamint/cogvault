package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGraphOutputsNodesAndEdges(t *testing.T) {
	configPath, wikiDir, _ := testVault(t)
	writeWikiPage(t, wikiDir, "sources/a.md", "A", "---\ntitle: A\ntype: source\n---\n\nA body\n\n[[b]]\n")
	writeWikiPage(t, wikiDir, "sources/b.md", "B", "---\ntitle: B\ntype: source\n---\n\nB body\n")

	if _, _, err := executeCommand("init", "--config", configPath); err != nil {
		t.Fatalf("init: %v", err)
	}

	stdout, _, err := executeCommand("graph", "--config", configPath)
	if err != nil {
		t.Fatalf("graph: %v", err)
	}

	var out struct {
		Nodes []struct {
			Path  string `json:"path"`
			Title string `json:"title"`
			Type  string `json:"type"`
		} `json:"nodes"`
		Edges []struct {
			Source string `json:"source"`
			Target string `json:"target"`
		} `json:"edges"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("unmarshal graph output %q: %v", stdout, err)
	}
	// nodes: sources/a.md, sources/b.md, plus the _schema.md scaffold page.
	if len(out.Nodes) != 3 {
		t.Fatalf("nodes = %d, want 3 (two sources + _schema.md)", len(out.Nodes))
	}
	if len(out.Edges) != 1 || out.Edges[0].Source != "sources/a.md" || out.Edges[0].Target != "sources/b.md" {
		t.Fatalf("edges = %+v, want a→b", out.Edges)
	}
}

func TestGraphUnresolvableLinkNoEdge(t *testing.T) {
	configPath, wikiDir, _ := testVault(t)
	writeWikiPage(t, wikiDir, "sources/a.md", "A", "---\ntitle: A\ntype: source\n---\n\n[[nowhere]]\n")

	if _, _, err := executeCommand("init", "--config", configPath); err != nil {
		t.Fatalf("init: %v", err)
	}
	stdout, _, err := executeCommand("graph", "--config", configPath)
	if err != nil {
		t.Fatalf("graph: %v", err)
	}
	if strings.Contains(stdout, "\"edges\": [") && !strings.Contains(stdout, "]") {
		t.Fatalf("unexpected edges format: %s", stdout)
	}
	var out struct {
		Edges []map[string]string `json:"edges"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Edges) != 0 {
		t.Fatalf("edges = %+v, want none for unresolvable link", out.Edges)
	}
}

func TestIndexCmdWritesIndexPage(t *testing.T) {
	configPath, wikiDir, _ := testVault(t)
	writeWikiPage(t, wikiDir, "sources/note.md", "Note", "# Note\nbody")
	writeWikiPage(t, wikiDir, "sources/zeta.md", "Zeta", "# Zeta\nbody")

	if _, _, err := executeCommand("init", "--config", configPath); err != nil {
		t.Fatalf("init: %v", err)
	}

	stdout, _, err := executeCommand("index", "--config", configPath)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	if !strings.Contains(stdout, "wrote _index.md (2 pages)") {
		t.Fatalf("stdout = %q", stdout)
	}

	data, err := os.ReadFile(filepath.Join(wikiDir, "_index.md"))
	if err != nil {
		t.Fatalf("_index.md missing: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "type: index") {
		t.Fatalf("missing index type:\n%s", content)
	}
	// Sorted by path: note before zeta.
	noteIdx := strings.Index(content, "[[sources/note|Note]]")
	zetaIdx := strings.Index(content, "[[sources/zeta|Zeta]]")
	if noteIdx < 0 || zetaIdx < 0 || noteIdx > zetaIdx {
		t.Fatalf("wikilinks missing or unsorted:\n%s", content)
	}
}

func TestIndexCmdExcludesSelfAndSchema(t *testing.T) {
	configPath, wikiDir, _ := testVault(t)
	writeWikiPage(t, wikiDir, "sources/note.md", "Note", "# Note\nbody")

	if _, _, err := executeCommand("init", "--config", configPath); err != nil {
		t.Fatalf("init: %v", err)
	}
	// Run index twice: the generated _index.md must not list itself.
	if _, _, err := executeCommand("index", "--config", configPath); err != nil {
		t.Fatalf("index 1: %v", err)
	}
	stdout, _, err := executeCommand("index", "--config", configPath)
	if err != nil {
		t.Fatalf("index 2: %v", err)
	}
	if !strings.Contains(stdout, "wrote _index.md (1 pages)") {
		t.Fatalf("stdout = %q, want only note counted", stdout)
	}
}
