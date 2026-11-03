package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/teslamint/cogvault/internal/archive"
)

func TestArchiveOperatorCLIQuarantineBackupRestoreTest(t *testing.T) {
	root := filepath.Join(t.TempDir(), "archive")
	src := filepath.Join(t.TempDir(), "a.md")
	os.WriteFile(src, []byte("operator fixture\n"), 0o644)
	out, _, err := executeCommand("archive", "--root", root, "capture", "--file", src)
	if err != nil {
		t.Fatal(err)
	}
	capID, objID := strings.Fields(out)[1], strings.Fields(out)[2]

	out, _, err = executeCommand("archive", "--root", root, "impact", capID)
	if err != nil {
		t.Fatal(err)
	}
	var im archive.Impact
	if err := json.Unmarshal([]byte(out), &im); err != nil || im.ObjectID != objID || im.Digest == "" {
		t.Fatalf("impact = %s, %v", out, err)
	}
	if _, _, err := executeCommand("archive", "--root", root, "quarantine", capID, "--impact", "stale"); err == nil {
		t.Fatal("quarantine with a wrong impact digest succeeded")
	}
	if _, _, err := executeCommand("archive", "--root", root, "quarantine", capID, "--impact", im.Digest); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCommand("archive", "--root", root, "status", capID); err == nil {
		t.Fatal("status of a quarantined capture succeeded")
	}
	if _, _, err := executeCommand("archive", "--root", root, "purge", objID, "--confirm", objID); err == nil {
		t.Fatal("purge before the quarantine minimum succeeded")
	}

	backups := t.TempDir()
	bdir := filepath.Join(backups, "b1")
	if _, _, err := executeCommand("archive", "--root", root, "backup", "--to", bdir); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCommand("archive", "--root", root, "unquarantine", capID); err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(t.TempDir(), "report.json")
	out, _, err = executeCommand("archive", "--root", root, "restore-test", "--backup", bdir, "--report", report, "--scratch", t.TempDir())
	if err != nil || !strings.Contains(out, "passed") {
		t.Fatalf("restore-test: %q %v", out, err)
	}
	rep, err := archive.ReadRestoreReport(report)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	// The live log's restore record brings the single object back active.
	if rep.ObjectsVerified != 1 {
		t.Fatalf("objects verified = %d, want 1 active object after unquarantine", rep.ObjectsVerified)
	}
	if _, _, err := executeCommand("archive", "--root", root, "restore-test", "--backup", bdir, "--report", report); err == nil {
		t.Fatal("restore-test overwrote an existing report")
	}
}
