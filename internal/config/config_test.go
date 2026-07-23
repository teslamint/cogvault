package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfigFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadAllFields(t *testing.T) {
	p := writeConfigFile(t, `
wiki_dir: /data/wiki
db_path: /state/cogvault.db
sources:
  - path: /downloads/articles
    types: [pdf, epub]
exclude: ["vendor", "node_modules"]
exclude_read: ["private"]
adapter: "markdown"
consistency_interval: 10
llm:
  backend: claudecode
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WikiDir != "/data/wiki" {
		t.Errorf("WikiDir = %q, want %q", cfg.WikiDir, "/data/wiki")
	}
	if cfg.DBPath != "/state/cogvault.db" {
		t.Errorf("DBPath = %q, want %q", cfg.DBPath, "/state/cogvault.db")
	}
	if len(cfg.Sources) != 1 {
		t.Fatalf("Sources = %v, want one entry", cfg.Sources)
	}
	if cfg.Sources[0].Path != "/downloads/articles" {
		t.Errorf("Sources[0].Path = %q, want %q", cfg.Sources[0].Path, "/downloads/articles")
	}
	if len(cfg.Sources[0].Types) != 2 || cfg.Sources[0].Types[0] != "pdf" || cfg.Sources[0].Types[1] != "epub" {
		t.Errorf("Sources[0].Types = %v, want [pdf epub]", cfg.Sources[0].Types)
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
	if cfg.LLM.Backend != "claudecode" {
		t.Errorf("LLM.Backend = %q, want %q", cfg.LLM.Backend, "claudecode")
	}
}

func TestLoadLLMModel(t *testing.T) {
	p := writeConfigFile(t, "wiki_dir: /data/wiki\ndb_path: /state/db.db\nllm:\n  model: opus\n")
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.Model != "opus" {
		t.Errorf("LLM.Model = %q, want %q", cfg.LLM.Model, "opus")
	}
}

func TestLoadLLMModelDefaultEmpty(t *testing.T) {
	p := writeConfigFile(t, "wiki_dir: /data/wiki\ndb_path: /state/db.db\n")
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.Model != "" {
		t.Errorf("LLM.Model = %q, want empty default", cfg.LLM.Model)
	}
}

func TestLoadDefaults(t *testing.T) {
	p := writeConfigFile(t, "wiki_dir: /data/wiki\ndb_path: /state/db.db\n")
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
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
	if cfg.LLM.Backend != "claudecode" {
		t.Errorf("LLM.Backend = %q, want %q (default)", cfg.LLM.Backend, "claudecode")
	}
	if len(cfg.Sources) != 0 {
		t.Errorf("Sources = %v, want empty", cfg.Sources)
	}
}

func TestLoadEmptySourcesValid(t *testing.T) {
	p := writeConfigFile(t, "wiki_dir: /data/wiki\ndb_path: /state/db.db\nsources: []\n")
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Sources) != 0 {
		t.Errorf("Sources = %v, want empty", cfg.Sources)
	}
}

func TestLoadTildeExpansion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	p := writeConfigFile(t, `
wiki_dir: ~/wiki
db_path: ~/state/db.db
sources:
  - path: ~/articles
    types: [pdf]
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "wiki"); cfg.WikiDir != want {
		t.Errorf("WikiDir = %q, want %q", cfg.WikiDir, want)
	}
	if want := filepath.Join(home, "state/db.db"); cfg.DBPath != want {
		t.Errorf("DBPath = %q, want %q", cfg.DBPath, want)
	}
	if want := filepath.Join(home, "articles"); cfg.Sources[0].Path != want {
		t.Errorf("Sources[0].Path = %q, want %q", cfg.Sources[0].Path, want)
	}
}

func TestLoadTildeBareExpands(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	p := writeConfigFile(t, "wiki_dir: \"~\"\ndb_path: /state/db.db\n")
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WikiDir != home {
		t.Errorf("WikiDir = %q, want %q", cfg.WikiDir, home)
	}
}

func TestLoadTildeMidPathLiteral(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	p := writeConfigFile(t, `
wiki_dir: /data/wiki
db_path: /state/db.db
sources:
  - path: ~/Mobile/com~apple~CloudDocs/articles
    types: [pdf]
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "Mobile/com~apple~CloudDocs/articles")
	if cfg.Sources[0].Path != want {
		t.Errorf("Sources[0].Path = %q, want %q (mid-path ~ literal)", cfg.Sources[0].Path, want)
	}
}

func TestLoadTypesNormalized(t *testing.T) {
	p := writeConfigFile(t, `
wiki_dir: /data/wiki
db_path: /state/db.db
sources:
  - path: /downloads/articles
    types: [".PDF", "Md", ".epub"]
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.Sources[0].Types
	want := []string{"pdf", "md", "epub"}
	if len(got) != len(want) {
		t.Fatalf("Types = %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("Types[%d] = %q, want %q", i, got[i], w)
		}
	}
}

func TestValidationErrors(t *testing.T) {
	tests := []struct {
		name         string
		yaml         string
		wantField    string
		wantKeywords []string
	}{
		{"wiki_dir relative", "wiki_dir: wiki\ndb_path: /state/db.db\n", "wiki_dir", []string{"absolute"}},
		{"wiki_dir empty", "db_path: /state/db.db\n", "wiki_dir", []string{"empty"}},
		{"db_path relative", "wiki_dir: /data/wiki\ndb_path: db.db\n", "db_path", []string{"absolute"}},
		{"db_path empty", "wiki_dir: /data/wiki\n", "db_path", []string{"empty"}},
		{"db_path directory", "wiki_dir: /data/wiki\ndb_path: /state/db/\n", "db_path", []string{"file path"}},
		{"source relative", "wiki_dir: /data/wiki\ndb_path: /state/db.db\nsources:\n  - path: rel/articles\n    types: [pdf]\n", "sources", []string{"absolute"}},
		{"source equals wiki_dir", "wiki_dir: /data/wiki\ndb_path: /state/db.db\nsources:\n  - path: /data/wiki\n    types: [pdf]\n", "sources", []string{"wiki_dir"}},
		{"source contains wiki_dir", "wiki_dir: /data/wiki/sub\ndb_path: /state/db.db\nsources:\n  - path: /data/wiki\n    types: [pdf]\n", "sources", []string{"wiki_dir"}},
		{"source under wiki_dir", "wiki_dir: /data/wiki\ndb_path: /state/db.db\nsources:\n  - path: /data/wiki/src\n    types: [pdf]\n", "sources", []string{"wiki_dir"}},
		{"db_path inside wiki_dir", "wiki_dir: /data/wiki\ndb_path: /data/wiki/x.db\n", "db_path", []string{"wiki_dir"}},
		{"exclude traversal", "wiki_dir: /data/wiki\ndb_path: /state/db.db\nexclude: [\"../secret\"]\n", "exclude[0]", []string{".."}},
		{"exclude absolute", "wiki_dir: /data/wiki\ndb_path: /state/db.db\nexclude: [\"/tmp\"]\n", "exclude[0]", []string{"absolute"}},
		{"exclude_read empty", "wiki_dir: /data/wiki\ndb_path: /state/db.db\nexclude_read: [\"\"]\n", "exclude_read[0]", []string{"empty"}},
		{"adapter invalid", "wiki_dir: /data/wiki\ndb_path: /state/db.db\nadapter: notion\n", "adapter", []string{"not supported"}},
		{"llm backend invalid", "wiki_dir: /data/wiki\ndb_path: /state/db.db\nllm:\n  backend: local\n", "llm.backend", []string{"not supported"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := writeConfigFile(t, tt.yaml)
			_, err := Load(p)
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

func TestSiblingPathsAllowed(t *testing.T) {
	p := writeConfigFile(t, `
wiki_dir: /data/wiki
db_path: /state/db.db
sources:
  - path: /data/wiki-other
    types: [pdf]
`)
	if _, err := Load(p); err != nil {
		t.Errorf("sibling paths should be allowed, got %v", err)
	}
}

func TestLoadFileNotFound(t *testing.T) {
	p := filepath.Join(t.TempDir(), "missing.yaml")
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("error should wrap fs.ErrNotExist, got: %v", err)
	}
	if !strings.Contains(err.Error(), "missing.yaml") {
		t.Errorf("error %q should contain config file path", err.Error())
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	p := writeConfigFile(t, ": bad yaml [\n")
	if _, err := Load(p); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestLoadUnknownField(t *testing.T) {
	p := writeConfigFile(t, "wiki_dr: /data/wiki\n")
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
}

func TestLoadMultipleYAMLDocuments(t *testing.T) {
	p := writeConfigFile(t, "wiki_dir: /data/wiki\ndb_path: /state/db.db\n---\nadapter: markdown\n")
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for multiple YAML documents, got nil")
	}
	if !strings.Contains(err.Error(), "multiple YAML documents") {
		t.Errorf("error %q should mention multiple YAML documents", err.Error())
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
			p := writeConfigFile(t, "wiki_dir: /data/wiki\ndb_path: /state/db.db\n"+tt.yaml)
			cfg, err := Load(p)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.ConsistencyInterval != tt.want {
				t.Errorf("ConsistencyInterval = %d, want %d", cfg.ConsistencyInterval, tt.want)
			}
		})
	}
}

func TestDefaultConfigPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err := DefaultConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".config", "cogvault", "config.yaml")
	if got != want {
		t.Errorf("DefaultConfigPath() = %q, want %q", got, want)
	}
}

func TestSaveWritesTemplateThenNoOp(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := Save(p); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	// Save writes DefaultConfig() with empty wiki_dir/db_path, which is not a
	// usable config: Load must reject it until the user fills in absolute paths.
	if _, err := Load(p); err == nil {
		t.Error("Load of freshly-saved template should fail validation (empty wiki_dir)")
	}
	if err := Save(p); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Error("Save should be a no-op when the file already exists")
	}
}

func TestSchemaPath(t *testing.T) {
	cfg := &Config{WikiDir: "/data/wiki"}
	if got := cfg.SchemaPath(); got != "_schema.md" {
		t.Errorf("SchemaPath() = %q, want %q", got, "_schema.md")
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

func TestAllExcludedBothEmpty(t *testing.T) {
	cfg := &Config{
		Exclude:     []string{},
		ExcludeRead: []string{},
	}
	if got := cfg.AllExcluded(); len(got) != 0 {
		t.Errorf("AllExcluded() = %v, want empty", got)
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
		{"...", false},
		{"a..b", false},
		{"a/..hidden", false},
	}
	for _, tt := range tests {
		if got := containsDotDot(tt.path); got != tt.want {
			t.Errorf("containsDotDot(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}
