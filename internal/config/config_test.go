package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".cogvault.yaml"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadAllFields(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `
wiki_dir: "docs"
db_path: "my.db"
exclude: ["vendor", "node_modules"]
exclude_read: ["private"]
adapter: "markdown"
consistency_interval: 10
`)
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WikiDir != "docs" {
		t.Errorf("WikiDir = %q, want %q", cfg.WikiDir, "docs")
	}
	if cfg.DBPath != "my.db" {
		t.Errorf("DBPath = %q, want %q", cfg.DBPath, "my.db")
	}
	if len(cfg.Exclude) != 2 || cfg.Exclude[0] != "vendor" || cfg.Exclude[1] != "node_modules" {
		t.Errorf("Exclude = %v, want [vendor node_modules]", cfg.Exclude)
	}
	if len(cfg.ExcludeRead) != 1 || cfg.ExcludeRead[0] != "private" {
		t.Errorf("ExcludeRead = %v, want [private]", cfg.ExcludeRead)
	}
	if cfg.Adapter != "markdown" {
		t.Errorf("Adapter = %q, want %q", cfg.Adapter, "markdown")
	}
	if cfg.ConsistencyInterval != 10 {
		t.Errorf("ConsistencyInterval = %d, want 10", cfg.ConsistencyInterval)
	}
}

func TestLoadDefaults(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "")

	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WikiDir != "_wiki" {
		t.Errorf("WikiDir = %q, want %q", cfg.WikiDir, "_wiki")
	}
	if cfg.DBPath != ".cogvault.db" {
		t.Errorf("DBPath = %q, want %q", cfg.DBPath, ".cogvault.db")
	}
	if len(cfg.Exclude) != 2 || cfg.Exclude[0] != ".obsidian" || cfg.Exclude[1] != ".trash" {
		t.Errorf("Exclude = %v, want [.obsidian .trash]", cfg.Exclude)
	}
	if cfg.ExcludeRead == nil || len(cfg.ExcludeRead) != 0 {
		t.Errorf("ExcludeRead = %v, want []", cfg.ExcludeRead)
	}
	if cfg.Adapter != "obsidian" {
		t.Errorf("Adapter = %q, want %q", cfg.Adapter, "obsidian")
	}
	if cfg.ConsistencyInterval != 5 {
		t.Errorf("ConsistencyInterval = %d, want 5", cfg.ConsistencyInterval)
	}
}

func TestLoadExplicitEmptyExclude(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "exclude: []\n")

	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Exclude == nil || len(cfg.Exclude) != 0 {
		t.Errorf("explicit empty exclude should be empty slice, got %v", cfg.Exclude)
	}
}

func TestLoadExplicitEmptyExcludeRead(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "exclude_read: []\n")

	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ExcludeRead == nil || len(cfg.ExcludeRead) != 0 {
		t.Errorf("explicit empty exclude_read should be empty slice, got %v", cfg.ExcludeRead)
	}
}

func TestLoadPartialConfigs(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		check   func(*testing.T, *Config)
	}{
		{
			name: "only wiki_dir",
			yaml: "wiki_dir: wiki\n",
			check: func(t *testing.T, cfg *Config) {
				if cfg.WikiDir != "wiki" {
					t.Errorf("WikiDir = %q, want %q", cfg.WikiDir, "wiki")
				}
				if cfg.DBPath != ".cogvault.db" {
					t.Errorf("DBPath should default, got %q", cfg.DBPath)
				}
				if cfg.Adapter != "obsidian" {
					t.Errorf("Adapter should default, got %q", cfg.Adapter)
				}
			},
		},
		{
			name: "only adapter",
			yaml: "adapter: markdown\n",
			check: func(t *testing.T, cfg *Config) {
				if cfg.Adapter != "markdown" {
					t.Errorf("Adapter = %q, want %q", cfg.Adapter, "markdown")
				}
				if cfg.WikiDir != "_wiki" {
					t.Errorf("WikiDir should default, got %q", cfg.WikiDir)
				}
			},
		},
		{
			name: "db_path and exclude",
			yaml: "db_path: data.db\nexclude: [\"build\"]\n",
			check: func(t *testing.T, cfg *Config) {
				if cfg.DBPath != "data.db" {
					t.Errorf("DBPath = %q, want %q", cfg.DBPath, "data.db")
				}
				if len(cfg.Exclude) != 1 || cfg.Exclude[0] != "build" {
					t.Errorf("Exclude = %v, want [build]", cfg.Exclude)
				}
				if cfg.WikiDir != "_wiki" {
					t.Errorf("WikiDir should default, got %q", cfg.WikiDir)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeConfig(t, dir, tt.yaml)
			cfg, err := Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			tt.check(t, cfg)
		})
	}
}

func TestLoadBothAdapters(t *testing.T) {
	for _, adapter := range []string{"obsidian", "markdown"} {
		t.Run(adapter, func(t *testing.T) {
			dir := t.TempDir()
			writeConfig(t, dir, "adapter: "+adapter+"\n")
			cfg, err := Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Adapter != adapter {
				t.Errorf("Adapter = %q, want %q", cfg.Adapter, adapter)
			}
		})
	}
}

func TestValidationErrors(t *testing.T) {
	tests := []struct {
		name         string
		yaml         string
		wantField    string
		wantKeywords []string
	}{
		{"wiki_dir traversal", "wiki_dir: ../outside\n", "wiki_dir", []string{".."}},
		{"wiki_dir absolute", "wiki_dir: /tmp/wiki\n", "wiki_dir", []string{"absolute"}},
		{"wiki_dir dot", "wiki_dir: \".\"\n", "wiki_dir", []string{"not allowed"}},
		{"wiki_dir embedded dotdot", "wiki_dir: foo/../bar\n", "wiki_dir", []string{".."}},
		{"wiki_dir dot-slash", "wiki_dir: \"./\"\n", "wiki_dir", []string{"not allowed"}},
		{"db_path traversal", "db_path: ../db\n", "db_path", []string{".."}},
		{"db_path absolute", "db_path: /tmp/db\n", "db_path", []string{"absolute"}},
		{"db_path dot", "db_path: \".\"\n", "db_path", []string{"not allowed"}},
		{"db_path trailing slash", "db_path: data/\n", "db_path", []string{"file path"}},
		{"db_path embedded dotdot", "db_path: foo/../bar.db\n", "db_path", []string{".."}},
		{"db_path dot-slash", "db_path: \"./\"\n", "db_path", []string{"not allowed"}},
		{"db_path trailing dot", "db_path: \"data/.\"\n", "db_path", []string{"file path"}},
		{"exclude traversal", "exclude: [\"../secret\"]\n", "exclude[0]", []string{".."}},
		{"exclude empty", "exclude: [\"\"]\n", "exclude[0]", []string{"empty"}},
		{"exclude dot", "exclude: [\".\"]\n", "exclude[0]", []string{"not allowed"}},
		{"exclude absolute", "exclude: [\"/tmp\"]\n", "exclude[0]", []string{"absolute"}},
		{"exclude dot-slash", "exclude: [\"./\"]\n", "exclude[0]", []string{"not allowed"}},
		{"exclude_read traversal", "exclude_read: [\"foo/../bar\"]\n", "exclude_read[0]", []string{".."}},
		{"exclude_read empty", "exclude_read: [\"\"]\n", "exclude_read[0]", []string{"empty"}},
		{"exclude_read absolute", "exclude_read: [\"/absolute\"]\n", "exclude_read[0]", []string{"absolute"}},
		{"adapter invalid", "adapter: notion\n", "adapter", []string{"not supported"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeConfig(t, dir, tt.yaml)
			_, err := Load(dir)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			msg := err.Error()
			if !strings.Contains(msg, tt.wantField) {
				t.Errorf("error %q should contain field %q", msg, tt.wantField)
			}
			for _, kw := range tt.wantKeywords {
				if !strings.Contains(msg, kw) {
					t.Errorf("error %q should contain keyword %q", msg, kw)
				}
			}
		})
	}
}

func TestValidationFailFast(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "wiki_dir: \".\"\nadapter: notion\n")
	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	msg := err.Error()
	hasWikiDir := strings.Contains(msg, "wiki_dir")
	hasAdapter := strings.Contains(msg, "adapter")
	if hasWikiDir && hasAdapter {
		t.Error("fail-fast should return only one error, got both wiki_dir and adapter")
	}
	if !hasWikiDir && !hasAdapter {
		t.Errorf("error %q should contain either wiki_dir or adapter", msg)
	}
}

func TestLoadFileNotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("error should wrap fs.ErrNotExist, got: %v", err)
	}
	if !strings.Contains(err.Error(), ".cogvault.yaml") {
		t.Errorf("error %q should contain config file path", err.Error())
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, ": bad yaml [\n")
	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestLoadUnknownField(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "wiki_dr: _wiki\n")
	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
}

func TestLoadMultipleYAMLDocuments(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "wiki_dir: wiki\n---\nadapter: markdown\n")
	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error for multiple YAML documents, got nil")
	}
	if !strings.Contains(err.Error(), "multiple YAML documents") {
		t.Errorf("error %q should mention multiple YAML documents", err.Error())
	}
}

func TestLoadMalformedTrailingContent(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "wiki_dir: wiki\n---\n: broken [\n")
	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error for malformed trailing content, got nil")
	}
}

func TestConsistencyIntervalEdgeCases(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want int
	}{
		{"zero", "consistency_interval: 0\n", 5},
		{"negative", "consistency_interval: -1\n", 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeConfig(t, dir, tt.yaml)
			cfg, err := Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.ConsistencyInterval != tt.want {
				t.Errorf("ConsistencyInterval = %d, want %d", cfg.ConsistencyInterval, tt.want)
			}
		})
	}
}

func TestWikiDirNestedAllowed(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "wiki_dir: a/b/c\n")
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WikiDir != "a/b/c" {
		t.Errorf("WikiDir = %q, want %q", cfg.WikiDir, "a/b/c")
	}
}

func TestAllExcluded(t *testing.T) {
	cfg := &Config{
		Exclude:     []string{"a", "b"},
		ExcludeRead: []string{"c"},
	}
	got := cfg.AllExcluded()
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("AllExcluded() = %v, want %v", got, want)
	}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("AllExcluded()[%d] = %q, want %q", i, got[i], v)
		}
	}
}

func TestAllExcludedWithDuplicates(t *testing.T) {
	cfg := &Config{
		Exclude:     []string{"a", "b"},
		ExcludeRead: []string{"a"},
	}
	got := cfg.AllExcluded()
	want := []string{"a", "b", "a"}
	if len(got) != len(want) {
		t.Fatalf("AllExcluded() = %v, want %v", got, want)
	}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("AllExcluded()[%d] = %q, want %q", i, got[i], v)
		}
	}
}

func TestAllExcludedBothEmpty(t *testing.T) {
	cfg := &Config{
		Exclude:     []string{},
		ExcludeRead: []string{},
	}
	got := cfg.AllExcluded()
	if len(got) != 0 {
		t.Errorf("AllExcluded() = %v, want empty", got)
	}
}

func TestSchemaPath(t *testing.T) {
	tests := []struct {
		wikiDir string
		want    string
	}{
		{"_wiki", "_wiki/_schema.md"},
		{"docs/wiki", "docs/wiki/_schema.md"},
	}
	for _, tt := range tests {
		cfg := &Config{WikiDir: tt.wikiDir}
		got := cfg.SchemaPath()
		if got != tt.want {
			t.Errorf("SchemaPath() with WikiDir=%q = %q, want %q", tt.wikiDir, got, tt.want)
		}
	}
}

func TestContainsDotDot(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"a/../b", true},
		{"..", true},
		{"a/b", false},
		{"a/b/c", false},
		{"...", false},
		{"a..b", false},
		{"a/..hidden", false},
	}
	for _, tt := range tests {
		got := containsDotDot(tt.path)
		if got != tt.want {
			t.Errorf("containsDotDot(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}
