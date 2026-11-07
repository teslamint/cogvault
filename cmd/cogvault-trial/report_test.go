package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOperatorReportImportNoNetworkAndPerCaptureCompleteness(t *testing.T) {
	env, _ := trialEnv(t)
	dir := t.TempDir()
	manifest := `{"version":1,"report_id":"report-fixture","selected_sha256":"sha256:` + strings.Repeat("a", 64) + `","sources":[{"id":"S0001","citation_url":"https://www.rfc-editor.org/rfc/rfc9110.pdf","relation":"citation","citation_count":1,"mode":"manual"}]}`
	path := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, _, err := do(t, env, "import-manifest", "--file", path); err != nil || !strings.Contains(out, "capture_id") {
		t.Fatalf("import: %s %v", out, err)
	}
	for _, tc := range []struct{ body, media, completeness string }{{"%PDF-1.4\nfixture", "application/pdf", "unknown"}, {"<!doctype html><html><body>Abstract only</body></html>", "text/html", "incomplete"}} {
		f := filepath.Join(dir, "source")
		if err := os.WriteFile(f, []byte(tc.body), 0o600); err != nil {
			t.Fatal(err)
		}
		hash := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(tc.body)))
		args := []string{"import-original", "--report", "report-fixture", "--source", "S0001", "--file", f, "--sha256", hash, "--bytes", fmt.Sprint(len(tc.body)), "--media", tc.media, "--completeness", tc.completeness, "--method", "existing_file"}
		if out, _, err := do(t, env, args...); err != nil || !strings.Contains(out, "capture_id") {
			t.Fatalf("file import: %s %v", out, err)
		}
		if out, _, err := do(t, env, args...); err != nil || !strings.Contains(out, `"existing": true`) {
			t.Fatalf("duplicate: %s %v", out, err)
		}
	}
	out, _, err := do(t, env, "report-status", "report-fixture")
	if err != nil || !strings.Contains(out, "incomplete") || !strings.Contains(out, "unknown") || strings.Contains(out, dir) {
		t.Fatalf("status: %s %v", out, err)
	}
	if out, _, err := do(t, env, "status", "--json"); err != nil || !strings.Contains(out, `"submissions_last_day": 0`) {
		t.Fatalf("import queued network: %s %v", out, err)
	}
}

func TestOperatorInputRefusesSymlinkDirectoryAndMissingFlags(t *testing.T) {
	env, _ := trialEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "input")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{dir, link} {
		if _, _, err := do(t, env, "import-manifest", "--file", p); err == nil {
			t.Fatalf("accepted %s", p)
		}
	}
	if _, _, err := do(t, env, "import-original", "--file", path); err == nil {
		t.Fatal("missing provenance/hash accepted")
	}
}
