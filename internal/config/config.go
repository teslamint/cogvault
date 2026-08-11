package config

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

type SourceDir struct {
	Path  string   `yaml:"path"`
	Types []string `yaml:"types"`
}

type LLMConfig struct {
	Backend          string `yaml:"backend"`
	Model            string `yaml:"model"`
	BaseURL          string `yaml:"base_url"`
	EmbeddingModel   string `yaml:"embedding_model"`
	EmbeddingBaseURL string `yaml:"embedding_base_url"`
}

type OAuthConfig struct {
	Issuer         string   `yaml:"issuer"`
	Audience       string   `yaml:"audience"`
	RequiredScopes []string `yaml:"required_scopes"`
	JWKSTTLSeconds int      `yaml:"jwks_ttl_seconds"`
}

type AuthConfig struct {
	Mode             string      `yaml:"mode"`
	MaxBodyMB        int         `yaml:"max_body_mb"`
	MaxStreamSeconds int         `yaml:"max_stream_seconds"`
	OAuth            OAuthConfig `yaml:"oauth"`
}

type Config struct {
	WikiDir             string      `yaml:"wiki_dir"`
	DBPath              string      `yaml:"db_path"`
	Sources             []SourceDir `yaml:"sources"`
	Exclude             []string    `yaml:"exclude"`
	ExcludeRead         []string    `yaml:"exclude_read"`
	Adapter             string      `yaml:"adapter"`
	ConsistencyInterval int         `yaml:"consistency_interval"`
	MaxFileSizeMB       int         `yaml:"max_file_size_mb"`
	LLM                 LLMConfig   `yaml:"llm"`
	Auth                AuthConfig  `yaml:"auth"`
}

const archiveExcludePath = "sources/_archived"

// maxStreamSecondsCeiling bounds auth.max_stream_seconds from above. Callers
// derive nanosecond durations from it — the streamable HTTP session sweeper's
// idle TTL is 2x this value — and an unbounded input overflows int64
// nanoseconds into a negative duration, which mcp-go reads as "sweeper
// disabled". That would silently restore the session leak the sweeper exists
// to prevent, so the ceiling is a guard against failing open, not a policy
// judgment about how long a stream may live. One day is far beyond any
// legitimate stream.
const maxStreamSecondsCeiling = 86400

// ValidAuthModes returns the auth.mode values this package accepts.
//
// internal/httpauth deliberately keeps its own independent switch rather than
// importing this package: it is the access boundary and stays standalone.
// This accessor exists so a test at a layer that already sees both packages
// can iterate the real list and catch the dangerous drift direction — a mode
// config accepts that httpauth would panic on at startup.
func ValidAuthModes() []string {
	return []string{"none", "bearer", "oauth"}
}

func Load(configPath string) (*Config, error) {
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

func DefaultConfig() *Config {
	var cfg Config
	cfg.applyDefaults()
	return &cfg
}

// DefaultConfigPath returns ~/.config/cogvault/config.yaml.
func DefaultConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".config", "cogvault", "config.yaml"), nil
}

func Save(configPath string) error {
	if _, err := os.Stat(configPath); err == nil {
		return nil
	}

	cfg := DefaultConfig()
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}

func (c *Config) applyDefaults() {
	c.WikiDir = expandTilde(c.WikiDir)
	c.DBPath = expandTilde(c.DBPath)
	for i := range c.Sources {
		c.Sources[i].Path = expandTilde(c.Sources[i].Path)
		for j := range c.Sources[i].Types {
			c.Sources[i].Types[j] = strings.ToLower(strings.TrimLeft(c.Sources[i].Types[j], "."))
		}
	}
	if c.Exclude == nil {
		c.Exclude = []string{".obsidian", ".trash", archiveExcludePath}
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
	if c.MaxFileSizeMB == 0 {
		c.MaxFileSizeMB = 32
	}
	if c.LLM.Backend == "" {
		c.LLM.Backend = "claudecode"
	}
	if c.Auth.Mode == "" {
		c.Auth.Mode = "none"
	}
	if c.Auth.MaxBodyMB == 0 {
		c.Auth.MaxBodyMB = 4
	}
	if c.Auth.MaxStreamSeconds == 0 {
		c.Auth.MaxStreamSeconds = 3600
	}
	if c.Auth.OAuth.JWKSTTLSeconds == 0 {
		c.Auth.OAuth.JWKSTTLSeconds = 900
	}
}

// AllExcluded returns the effective scan/index exclusion list without mutating
// the configured exclude slices. The returned slice always includes the
// internal archive path exactly once.
func (c *Config) AllExcluded() []string {
	result := make([]string, 0, len(c.Exclude)+len(c.ExcludeRead)+1)
	result = append(result, c.Exclude...)
	if !containsString(result, archiveExcludePath) {
		result = append(result, archiveExcludePath)
	}
	for _, path := range c.ExcludeRead {
		if path == archiveExcludePath && containsString(result, archiveExcludePath) {
			continue
		}
		result = append(result, path)
	}
	return result
}

func (c *Config) SchemaPath() string {
	return "_schema.md"
}

func (c *Config) validate() error {
	if hasUnexpandedTilde(c.WikiDir) {
		return fmt.Errorf("wiki_dir: cannot expand \"~\": home directory not available")
	}
	if hasUnexpandedTilde(c.DBPath) {
		return fmt.Errorf("db_path: cannot expand \"~\": home directory not available")
	}
	for i, s := range c.Sources {
		if hasUnexpandedTilde(s.Path) {
			return fmt.Errorf("sources[%d].path: cannot expand \"~\": home directory not available", i)
		}
	}
	if err := validateAbsPath("wiki_dir", c.WikiDir); err != nil {
		return err
	}
	if err := validateAbsPath("db_path", c.DBPath); err != nil {
		return err
	}
	cleanedDB := filepath.Clean(c.DBPath)
	isTrailingSlash := strings.HasSuffix(c.DBPath, "/") || strings.HasSuffix(c.DBPath, string(os.PathSeparator))
	isTrailingDot := cleanedDB != c.DBPath && strings.HasSuffix(c.DBPath, ".")
	if isTrailingSlash || isTrailingDot {
		return fmt.Errorf("db_path: must be a file path, not a directory")
	}
	cleanWiki := filepath.Clean(c.WikiDir)
	if hasPathPrefix(cleanedDB, cleanWiki) {
		return fmt.Errorf("db_path: must not be inside wiki_dir; expected a path outside the wiki root")
	}
	for i, s := range c.Sources {
		field := fmt.Sprintf("sources[%d].path", i)
		if err := validateAbsPath(field, s.Path); err != nil {
			return err
		}
		cleanSrc := filepath.Clean(s.Path)
		if hasPathPrefix(cleanSrc, cleanWiki) || hasPathPrefix(cleanWiki, cleanSrc) {
			return fmt.Errorf("%s: must not overlap wiki_dir; expected a path that is neither equal to, inside, nor containing the wiki root", field)
		}
	}
	for i, e := range c.Exclude {
		if err := validatePath(fmt.Sprintf("exclude[%d]", i), e); err != nil {
			return err
		}
	}
	for i, e := range c.ExcludeRead {
		if err := validatePath(fmt.Sprintf("exclude_read[%d]", i), e); err != nil {
			return err
		}
	}
	if c.MaxFileSizeMB < 0 {
		return fmt.Errorf("max_file_size_mb: must be positive; expected a value in megabytes")
	}
	if c.Adapter != "obsidian" && c.Adapter != "markdown" {
		return fmt.Errorf("adapter: %q not supported; use \"obsidian\" or \"markdown\"", c.Adapter)
	}
	if c.LLM.Backend != "claudecode" && c.LLM.Backend != "ollama" {
		return fmt.Errorf("llm.backend: %q not supported; use \"claudecode\" or \"ollama\"", c.LLM.Backend)
	}
	if c.LLM.EmbeddingBaseURL != "" && c.LLM.EmbeddingModel == "" {
		return fmt.Errorf("llm.embedding_base_url: requires embedding_model to be set")
	}
	if !slices.Contains(ValidAuthModes(), c.Auth.Mode) {
		return fmt.Errorf("auth.mode: %q not supported; use \"none\", \"bearer\", or \"oauth\"", c.Auth.Mode)
	}
	if c.Auth.Mode == "oauth" {
		if c.Auth.OAuth.Issuer == "" {
			return fmt.Errorf("auth.oauth.issuer: must not be empty when auth.mode is \"oauth\"")
		}
		if err := validateIssuer(c.Auth.OAuth.Issuer); err != nil {
			return err
		}
	}
	if c.Auth.MaxBodyMB < 0 {
		return fmt.Errorf("auth.max_body_mb: must be positive; expected a value in megabytes")
	}
	if c.Auth.MaxStreamSeconds < 0 || c.Auth.MaxStreamSeconds > maxStreamSecondsCeiling {
		return fmt.Errorf("auth.max_stream_seconds: must be positive and at most %d; expected a value in seconds", maxStreamSecondsCeiling)
	}
	if c.Auth.OAuth.JWKSTTLSeconds < 0 {
		return fmt.Errorf("auth.oauth.jwks_ttl_seconds: must be positive; expected a value in seconds")
	}
	return nil
}

func validateIssuer(issuer string) error {
	u, err := url.Parse(issuer)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("auth.oauth.issuer: %q is not an absolute https:// URL; expected a scheme-qualified issuer URL", issuer)
	}
	return nil
}

func validateAbsPath(field, path string) error {
	if path == "" {
		return fmt.Errorf("%s: must not be empty", field)
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%s: %q is not absolute; expected an absolute path or a leading \"~/\"", field, path)
	}
	return nil
}

func validatePath(field, path string) error {
	if path == "" {
		return fmt.Errorf("%s: must not be empty", field)
	}
	if filepath.IsAbs(path) {
		return fmt.Errorf("%s: absolute path not allowed; use a relative path", field)
	}
	if containsDotDot(path) {
		return fmt.Errorf("%s: path traversal (..) not allowed", field)
	}
	if filepath.Clean(path) == "." {
		return fmt.Errorf("%s: %q not allowed; use a subdirectory", field, path)
	}
	return nil
}

// hasPathPrefix reports whether path equals prefix or lies under it.
// Both arguments must already be filepath.Clean-ed.
func hasPathPrefix(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+string(os.PathSeparator))
}

// expandTilde expands a leading "~/" (or an exact "~") to the user's home
// directory. A "~" anywhere else in the path is left literal.
func expandTilde(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}

func hasUnexpandedTilde(path string) bool {
	return path == "~" || strings.HasPrefix(path, "~/")
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

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
