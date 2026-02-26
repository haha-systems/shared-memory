package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config contains runtime configuration for memory-mcp.
type Config struct {
	ServerName              string `yaml:"server_name"`
	DBPath                  string `yaml:"db_path"`
	LogLevel                string `yaml:"log_level"`
	NamespacePattern        string `yaml:"namespace_pattern"`
	DefaultShortTTLHours    int    `yaml:"default_short_ttl_hours"`
	TTLCheckIntervalSeconds int    `yaml:"ttl_check_interval_seconds"`
	MaxContextPackItems     int    `yaml:"max_context_pack_items"`
	DefaultSearchK          int    `yaml:"default_search_k"`
}

// Default returns a Config populated with safe defaults.
func Default() Config {
	return Config{
		ServerName:              "memory-mcp",
		DBPath:                  filepath.Join(userHomeDir(), ".memory-mcp", "memories.db"),
		LogLevel:                "info",
		NamespacePattern:        `^[a-zA-Z0-9_.-]+(/[a-zA-Z0-9_.-]+){1,7}$`,
		DefaultShortTTLHours:    48,
		TTLCheckIntervalSeconds: 60,
		MaxContextPackItems:     8,
		DefaultSearchK:          10,
	}
}

// Load loads config from disk; if path does not exist, default config is returned.
func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}

	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("read config: %w", err)
	}

	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config yaml: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return cfg, err
	}

	return cfg, nil
}

// Validate checks configuration sanity.
func (c *Config) Validate() error {
	if c.ServerName == "" {
		return errors.New("server_name must not be empty")
	}
	if c.DBPath == "" {
		return errors.New("db_path must not be empty")
	}
	if c.DefaultShortTTLHours <= 0 {
		return errors.New("default_short_ttl_hours must be > 0")
	}
	if c.TTLCheckIntervalSeconds <= 0 {
		return errors.New("ttl_check_interval_seconds must be > 0")
	}
	if c.MaxContextPackItems <= 0 {
		return errors.New("max_context_pack_items must be > 0")
	}
	if c.DefaultSearchK <= 0 {
		return errors.New("default_search_k must be > 0")
	}
	if _, err := regexp.Compile(c.NamespacePattern); err != nil {
		return fmt.Errorf("invalid namespace_pattern: %w", err)
	}
	return nil
}

// EnsurePaths creates parent directories for config-managed paths.
func (c *Config) EnsurePaths() error {
	c.DBPath = ExpandPath(c.DBPath)
	parent := filepath.Dir(c.DBPath)
	if parent == "." {
		return nil
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create db parent dir: %w", err)
	}
	return nil
}

// ExpandPath expands "~/" to the current user's home directory.
func ExpandPath(p string) string {
	if p == "" {
		return p
	}
	if p == "~" {
		return userHomeDir()
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(userHomeDir(), p[2:])
	}
	return p
}

func userHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return home
}
