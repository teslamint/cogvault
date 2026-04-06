package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const configFileName = ".cogvault.yaml"

type Config struct {
	WikiDir             string   `yaml:"wiki_dir"`
	DBPath              string   `yaml:"db_path"`
	Exclude             []string `yaml:"exclude"`
	ExcludeRead         []string `yaml:"exclude_read"`
	Adapter             string   `yaml:"adapter"`
	ConsistencyInterval int      `yaml:"consistency_interval"`
}

func Load(vaultRoot string) (*Config, error) {
	configPath := filepath.Join(vaultRoot, configFileName)
	f, err := os.Open(configPath)
	if err != nil {
		return nil, fmt.Errorf("load config %s: %w", configPath, err)
	}
	defer f.Close()

	var cfg Config
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && err != io.EOF {
		return nil, fmt.Errorf("parse config %s: %w", configPath, err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err == nil {
		return nil, fmt.Errorf("parse config %s: unexpected multiple YAML documents", configPath)
	} else if err != io.EOF {
		return nil, fmt.Errorf("parse config %s: %w", configPath, err)
	}

	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.WikiDir == "" {
		c.WikiDir = "_wiki"
	}
	if c.DBPath == "" {
		c.DBPath = ".cogvault.db"
	}
	if c.Exclude == nil {
		c.Exclude = []string{".obsidian", ".trash"}
	}
	if c.ExcludeRead == nil {
		c.ExcludeRead = []string{}
	}
	if c.Adapter == "" {
		c.Adapter = "obsidian"
	}
	if c.ConsistencyInterval <= 0 {
		c.ConsistencyInterval = 5
	}
}

// AllExcluded returns exclude followed by exclude_read.
// No deduplication or normalization.
func (c *Config) AllExcluded() []string {
	result := make([]string, 0, len(c.Exclude)+len(c.ExcludeRead))
	result = append(result, c.Exclude...)
	result = append(result, c.ExcludeRead...)
	return result
}

func (c *Config) SchemaPath() string {
	return filepath.Join(c.WikiDir, "_schema.md")
}

func (c *Config) validate() error {
	if err := validatePath("wiki_dir", c.WikiDir, false); err != nil {
		return err
	}
	if err := validatePath("db_path", c.DBPath, false); err != nil {
		return err
	}
	cleanedDB := filepath.Clean(c.DBPath)
	if strings.HasSuffix(c.DBPath, "/") || strings.HasSuffix(c.DBPath, string(os.PathSeparator)) ||
		cleanedDB != c.DBPath && strings.HasSuffix(c.DBPath, ".") {
		return fmt.Errorf("db_path: must be a file path, not a directory")
	}
	for i, e := range c.Exclude {
		if err := validatePath(fmt.Sprintf("exclude[%d]", i), e, false); err != nil {
			return err
		}
	}
	for i, e := range c.ExcludeRead {
		if err := validatePath(fmt.Sprintf("exclude_read[%d]", i), e, false); err != nil {
			return err
		}
	}
	if c.Adapter != "obsidian" && c.Adapter != "markdown" {
		return fmt.Errorf("adapter: %q not supported; use \"obsidian\" or \"markdown\"", c.Adapter)
	}
	return nil
}

func validatePath(field, path string, allowDot bool) error {
	if path == "" {
		return fmt.Errorf("%s: must not be empty", field)
	}
	if filepath.IsAbs(path) {
		return fmt.Errorf("%s: absolute path not allowed; use a relative path", field)
	}
	if containsDotDot(path) {
		return fmt.Errorf("%s: path traversal (..) not allowed", field)
	}
	cleaned := filepath.Clean(path)
	if !allowDot && cleaned == "." {
		return fmt.Errorf("%s: %q not allowed; use a subdirectory", field, path)
	}
	return nil
}

func containsDotDot(path string) bool {
	for _, sep := range []string{"/", string(os.PathSeparator)} {
		for _, component := range strings.Split(path, sep) {
			if component == ".." {
				return true
			}
		}
	}
	return false
}
