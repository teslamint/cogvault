package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/teslamint/cogvault/internal/adapter/markdown"
	"github.com/teslamint/cogvault/internal/terms"
)

// Fictional conditions for regression validation only, not rights evidence.
const capturedPermission = "---\ntitle: Fictional capture\nusage_terms:\n  status: recorded\n  reviewed_by: Untrusted remote reviewer\n  reviewed_at: \"2026-10-20\"\n  conditions: Retain the synthetic acknowledgement.\n  evidence:\n    reference: fictional-capture-policy\n    quote: Synthetic acknowledgement is mandatory.\n---\nCaptured body.\n"

func TestFetchPreservesSuppliedPermissionMetadata(t *testing.T) {
	config, _, _ := testVault(t)
	sources := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = w.Write([]byte(capturedPermission))
	}))
	defer server.Close()
	_, _, err := executeCommand("fetch", server.URL, "--config", config, "--source-dir", sources, "--name", "capture")
	if err != nil {
		t.Fatal(err)
	}
	src, err := markdown.New().Parse(sources, "capture.md", true)
	if err != nil {
		t.Fatal(err)
	}
	if src.Frontmatter["usage_terms"] == nil {
		t.Fatalf("fetch buried explicit terms outside usable metadata: %s", src.Content)
	}
	terms := src.Frontmatter["usage_terms"].(map[interface{}]interface{})
	if terms["conditions"] != "Retain the synthetic acknowledgement." || !strings.Contains(src.Content, "Captured body.") {
		t.Fatalf("fetch lost permission conditions or article body: %+v", src)
	}
	if terms["status"] != "unverified" || terms["reviewed_by"] != nil || terms["reviewed_at"] != nil {
		t.Fatalf("fetch trusted a remote review claim: %#v", terms)
	}
	if src.Frontmatter["source_url"] != server.URL {
		t.Fatalf("fetch lost the evidence source URL: %#v", src.Frontmatter)
	}
}

func TestFetchRejectsOverwriteThatDiscardsPermission(t *testing.T) {
	config, _, _ := testVault(t)
	sources := t.TempDir()
	page := filepath.Join(sources, "capture.md")
	if err := os.WriteFile(page, []byte(capturedPermission), 0o644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = w.Write([]byte("# Replacement without any permission evidence\n"))
	}))
	defer server.Close()
	_, _, err := executeCommand("fetch", server.URL, "--config", config, "--source-dir", sources, "--name", "capture")
	if err == nil {
		t.Fatal("fetch overwrote supplied permission evidence")
	}
	data, readErr := os.ReadFile(page)
	if readErr != nil || string(data) != capturedPermission {
		t.Fatalf("rejected capture changed original: %q, %v", data, readErr)
	}
}

func TestFetchKeepsRawResponseAndExactSourceURL(t *testing.T) {
	const sourceURL = "https://example.invalid/article #fragment"
	for _, input := range []string{
		"---\nnot: [valid YAML\n---\nRaw text.\n",
		"---\ntitle: Original metadata\n---\nOriginal body.\n",
		capturedPermission,
	} {
		captured, err := formatFetchedContent(sourceURL, []byte(input))
		if err != nil {
			t.Errorf("capture rejected textual response: %v", err)
			continue
		}
		metadata, body, err := terms.Parse([]byte(captured))
		if err != nil || metadata["source_url"] != sourceURL || string(body) != input {
			t.Errorf("capture changed raw response or URL: metadata=%#v body=%q error=%v", metadata, body, err)
		}
	}
}
