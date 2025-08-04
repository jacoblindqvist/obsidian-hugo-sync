package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

// Config holds all configuration values for the daemon
type Config struct {
	// Required paths
	Vault      string `toml:"vault"`
	Repo       string `toml:"repo"`
	ContentDir string `toml:"content_dir"`

	// Behavior settings
	AutoWeight      bool   `toml:"auto_weight"`
	LinkFormat      string `toml:"link_format"`
	UnpublishedLink string `toml:"unpublished_link"`

	// Timing and performance
	Interval time.Duration `toml:"-"` // Parsed from string
	interval string        `toml:"interval"`

	// Logging and debugging
	LogLevel string `toml:"log_level"`
	DryRun   bool   `toml:"dry_run"`

	// Internal paths (computed)
	CacheDir   string `toml:"-"`
	ConfigFile string `toml:"-"`
}

// Options represents command-line and environment variable inputs
type Options struct {
	Vault           string
	Repo            string
	ContentDir      string
	AutoWeight      bool
	LinkFormat      string
	UnpublishedLink string
	Interval        string
	LogLevel        string
	DryRun          bool
	ConfigFile      string
}

// Load creates a Config by merging CLI flags, config file, and environment variables
func Load(opts *Options) (*Config, error) {
	cfg := &Config{
		// Set defaults
		ContentDir:      "content/docs",
		AutoWeight:      true,
		LinkFormat:      "relref",
		UnpublishedLink: "text",
		interval:        "30s",
		LogLevel:        "info",
		DryRun:          false,
	}

	// Load config file if specified or exists in default location
	configPath := opts.ConfigFile
	if configPath == "" {
		configPath = getDefaultConfigPath()
	}

	if _, err := os.Stat(configPath); err == nil {
		if err := loadConfigFile(cfg, configPath); err != nil {
			return nil, fmt.Errorf("loading config file %s: %w", configPath, err)
		}
	}

	// Override with CLI flags and environment variables
	if err := applyOverrides(cfg, opts); err != nil {
		return nil, fmt.Errorf("applying configuration overrides: %w", err)
	}

	// Parse interval string to duration
	interval, err := time.ParseDuration(cfg.interval)
	if err != nil {
		return nil, fmt.Errorf("invalid interval %q: %w", cfg.interval, err)
	}
	cfg.Interval = interval

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("configuration validation failed: %w", err)
	}

	// Set computed paths
	if err := cfg.setComputedPaths(); err != nil {
		return nil, fmt.Errorf("setting computed paths: %w", err)
	}

	cfg.ConfigFile = configPath
	return cfg, nil
}

// Validate checks that all required configuration is present and valid
func (c *Config) Validate() error {
	if c.Vault == "" {
		return fmt.Errorf("vault path is required")
	}
	if c.Repo == "" {
		return fmt.Errorf("hugo directory path is required")
	}

	// Check that vault exists
	if stat, err := os.Stat(c.Vault); err != nil {
		return fmt.Errorf("vault path %q: %w", c.Vault, err)
	} else if !stat.IsDir() {
		return fmt.Errorf("vault path %q is not a directory", c.Vault)
	}

	// Check that hugo directory exists
	if stat, err := os.Stat(c.Repo); err != nil {
		return fmt.Errorf("hugo directory path %q: %w", c.Repo, err)
	} else if !stat.IsDir() {
		return fmt.Errorf("hugo directory path %q is not a directory", c.Repo)
	}

	// Validate link format
	if c.LinkFormat != "relref" && c.LinkFormat != "md" {
		return fmt.Errorf("link-format must be 'relref' or 'md', got %q", c.LinkFormat)
	}

	// Validate unpublished link handling
	if c.UnpublishedLink != "text" && c.UnpublishedLink != "hash" {
		return fmt.Errorf("unpublished-link must be 'text' or 'hash', got %q", c.UnpublishedLink)
	}

	// Validate log level
	validLevels := []string{"debug", "info", "warn", "error"}
	validLevel := false
	for _, level := range validLevels {
		if c.LogLevel == level {
			validLevel = true
			break
		}
	}
	if !validLevel {
		return fmt.Errorf("log-level must be one of %v, got %q", validLevels, c.LogLevel)
	}

	// Validate interval
	if c.Interval < time.Second {
		return fmt.Errorf("interval must be at least 1 second, got %v", c.Interval)
	}

	return nil
}

// setComputedPaths calculates derived paths like cache directory
func (c *Config) setComputedPaths() error {
	// Create cache directory based on vault path hash
	vaultAbs, err := filepath.Abs(c.Vault)
	if err != nil {
		return fmt.Errorf("getting absolute vault path: %w", err)
	}

	vaultHash := hashString(vaultAbs)
	c.CacheDir = getCacheDir(vaultHash)

	// Ensure cache directory exists
	if err := os.MkdirAll(c.CacheDir, 0755); err != nil {
		return fmt.Errorf("creating cache directory %s: %w", c.CacheDir, err)
	}

	return nil
}

// loadConfigFile reads and parses a TOML configuration file
func loadConfigFile(cfg *Config, path string) error {
	_, err := toml.DecodeFile(path, cfg)
	return err
}

// applyOverrides applies CLI flags and environment variables over config file values
func applyOverrides(cfg *Config, opts *Options) error {
	// Apply CLI flags (they override everything)
	if opts.Vault != "" {
		cfg.Vault = opts.Vault
	}
	if opts.Repo != "" {
		cfg.Repo = opts.Repo
	}
	if opts.ContentDir != "" {
		cfg.ContentDir = opts.ContentDir
	}
	if opts.LinkFormat != "" {
		cfg.LinkFormat = opts.LinkFormat
	}
	if opts.UnpublishedLink != "" {
		cfg.UnpublishedLink = opts.UnpublishedLink
	}
	if opts.Interval != "" {
		cfg.interval = opts.Interval
	}
	if opts.LogLevel != "" {
		cfg.LogLevel = opts.LogLevel
	}
	if opts.DryRun {
		cfg.DryRun = opts.DryRun
	}

	// Check for environment variable overrides
	if vault := os.Getenv("OBSIDIAN_VAULT"); vault != "" && opts.Vault == "" {
		cfg.Vault = vault
	}
	if repo := os.Getenv("HUGO_REPO"); repo != "" && opts.Repo == "" {
		cfg.Repo = repo
	}

	return nil
}

// getDefaultConfigPath returns the platform-specific default config file location
func getDefaultConfigPath() string {
	configDir := getConfigDir()
	return filepath.Join(configDir, "config.toml")
}

// getConfigDir returns the platform-specific configuration directory
func getConfigDir() string {
	if xdgConfig := os.Getenv("XDG_CONFIG_HOME"); xdgConfig != "" {
		return filepath.Join(xdgConfig, "obsidian-hugo-sync")
	}
	
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "obsidian-hugo-sync")
}

// getCacheDir returns the platform-specific cache directory for a vault
func getCacheDir(vaultHash string) string {
	if xdgCache := os.Getenv("XDG_CACHE_HOME"); xdgCache != "" {
		return filepath.Join(xdgCache, "obsidian-hugo-sync", vaultHash)
	}
	
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "obsidian-hugo-sync", vaultHash)
}

// hashString creates a simple hash of a string for directory names
func hashString(s string) string {
	// Simple hash for directory naming - not cryptographic
	h := uint32(2166136261)
	for _, b := range []byte(s) {
		h ^= uint32(b)
		h *= 16777619
	}
	return fmt.Sprintf("%08x", h)
} 