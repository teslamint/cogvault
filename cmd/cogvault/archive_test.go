package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/teslamint/cogvault/internal/archive"
)

func TestArchiveCLICaptureReportAndStatus(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "archive")
	src := filepath.Join(base, "paper.md")
	if err := os.WriteFile(src, []byte("# Paper\n\nFull text.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := filepath.Join(base, "report.md")
	if err := os.WriteFile(rep, []byte("# Report\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A config path that does not exist proves the archive never loads config.
	run := func(args ...string) string {
		t.Helper()
		out, _, err := executeCommand(append([]string{"archive", "--config", filepath.Join(base, "missing.yaml"), "--root", root}, args...)...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return out
	}
	first := strings.Fields(run("capture", "--file", src, "--url", "https://journal.example/p", "--completeness", "verified", "--basis", "page count matches the journal's table of contents"))
	if len(first) != 3 || first[0] != "created" {
		t.Fatalf("capture output = %v", first)
	}
	capID := first[1]
	again := strings.Fields(run("capture", "--file", src, "--url", "https://journal.example/p", "--completeness", "verified", "--basis", "page count matches the journal's table of contents"))
	if again[0] != "existing" || again[1] != capID {
		t.Fatalf("repeat capture output = %v", again)
	}
	mirror := strings.Fields(run("capture", "--file", src, "--url", "https://mirror.example/p"))
	if mirror[0] != "created" || mirror[1] == capID || mirror[2] != first[2] {
		t.Fatalf("mirror capture output = %v", mirror)
	}
	report := strings.Fields(run("import-report", "--file", rep, "--supplier", "chatgpt-deep-research", "--cite", "https://snippet.example/s", "--cite", capID))
	if report[0] != "created" {
		t.Fatalf("report output = %v", report)
	}

	out := run("status", report[1])
	for _, want := range []string{"kind          report", "1  https://snippet.example/s  (not captured)", "2  " + capID + "  (captured source, completeness verified)", "not a fact check"} {
		if !strings.Contains(out, want) {
			t.Errorf("report status missing %q:\n%s", want, out)
		}
	}
	out = run("status", capID)
	for _, want := range []string{"same bytes    " + mirror[1], "cited by      " + report[1], "name          paper.md", "basis         page count"} {
		if !strings.Contains(out, want) {
			t.Errorf("source status missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, base) {
		t.Errorf("status leaked a host path:\n%s", out)
	}
	var list []archive.Capture
	if err := json.Unmarshal([]byte(run("status", "--json")), &list); err != nil || len(list) != 3 {
		t.Fatalf("status --json list = %+v err=%v", list, err)
	}
}

func TestArchiveCLIRequiresRootAndRejectsConflicts(t *testing.T) {
	if _, _, err := executeCommand("archive", "status"); err == nil || !strings.Contains(err.Error(), "root") {
		t.Fatalf("err = %v, want required --root", err)
	}
	base := t.TempDir()
	root := filepath.Join(base, "archive")
	f := filepath.Join(base, "a.txt")
	for i, body := range []string{"v1", "v2"} {
		if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		_, _, err := executeCommand("archive", "--root", root, "capture", "--file", f, "--key", "k1")
		if i == 0 && err != nil {
			t.Fatal(err)
		}
		if i == 1 && (err == nil || !strings.Contains(err.Error(), "different content")) {
			t.Fatalf("err = %v, want key conflict", err)
		}
	}
}
