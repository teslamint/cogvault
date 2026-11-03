package config

import (
	"os"
	"strings"
	"testing"
)

const archiveBase = "wiki_dir: /tmp/cv/wiki\ndb_path: /tmp/cv/state/index.db\nsources:\n  - path: /tmp/cv/src\n    types: [md]\n"

func TestArchiveConfigDefaultsOffAndValidatesPaths(t *testing.T) {
	cfg, err := Load(writeConfigFile(t, archiveBase))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Archive.Root != "" || cfg.Archive.CaptureEnabled {
		t.Fatalf("archive defaults = %+v", cfg.Archive)
	}
	ok := archiveBase + "archive:\n  root: /tmp/cv/archive\n  capture_enabled: true\n"
	if _, err := Load(writeConfigFile(t, ok)); err != nil {
		t.Fatalf("valid archive config rejected: %v", err)
	}
	for name, extra := range map[string]string{
		"enabled without root": "archive:\n  capture_enabled: true\n",
		"relative root":        "archive:\n  root: archive\n",
		"inside wiki":          "archive:\n  root: /tmp/cv/wiki/archive\n",
		"contains wiki":        "archive:\n  root: /tmp/cv\n",
		"overlaps source":      "archive:\n  root: /tmp/cv/src/a\n",
		"contains db":          "archive:\n  root: /tmp/cv/state\n",
		"relative report":      "archive:\n  root: /tmp/cv/archive\n  deployment:\n    restore_test_report: r.json\n",
		"unknown key":          "archive:\n  root: /tmp/cv/archive\n  mode: test\n",
		"replica inside root":  "archive:\n  root: /tmp/cv/archive\n  deployment:\n    deletion_log_replica: /tmp/cv/archive/copy.jsonl\n",
		"relative replica":     "archive:\n  root: /tmp/cv/archive\n  deployment:\n    deletion_log_replica: copy.jsonl\n",
		// No config key can relax the production activation check.
		"prototype bypass": "archive:\n  root: /tmp/cv/archive\n  prototype: true\n",
	} {
		if _, err := Load(writeConfigFile(t, archiveBase+extra)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestMissingDeploymentSettingsNamesKeysOnly(t *testing.T) {
	d := ArchiveDeployment{BackupTarget: "secret-target", PermissionsOwner: "  "}
	got := strings.Join(d.MissingDeploymentSettings(), ",")
	want := "archive.deployment.backup_schedule,archive.deployment.restore_test_report,archive.deployment.permissions_owner,archive.deployment.handoff_mount,archive.deployment.deletion_log_replica"
	if got != want {
		t.Fatalf("missing = %s, want %s", got, want)
	}
}

func TestSavedDefaultConfigHasNoArchiveSection(t *testing.T) {
	path := t.TempDir() + "/config.yaml"
	if err := Save(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains("\n"+string(data), "\narchive:") {
		t.Fatalf("init config mentions archive:\n%s", data)
	}
}
