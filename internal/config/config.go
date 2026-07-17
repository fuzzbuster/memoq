// Package config handles memoq's on-disk layout and the single YAML-free,
// JSON config file. Everything lives under ~/.memoq/ by default and can be
// overridden with the MEMOQ_HOME environment variable.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Config is the full persisted configuration. It is deliberately tiny: only
// what is needed to reach the Memos server and control lazy sync. No LLM,
// no embedding, no logging knobs.
type Config struct {
	// ServerURL is the base URL of the Memos instance, e.g. https://memos.example.com
	ServerURL string `json:"server_url"`
	// Token is a Memos personal access token (Bearer).
	Token string `json:"token"`
	// AutoSyncTTLSeconds controls read-time lazy sync: if the local cache is
	// older than this many seconds, read commands trigger an incremental sync
	// first. 0 disables auto-sync (fully offline until an explicit `sync`).
	AutoSyncTTLSeconds int `json:"auto_sync_ttl_seconds"`
}

// Paths resolves all on-disk locations derived from the memoq home directory.
type Paths struct {
	Home           string // ~/.memoq
	ConfigFile     string // ~/.memoq/config.json
	DBFile         string // ~/.memoq/memoq.db
	AttachmentsDir string // ~/.memoq/attachments
}

// ResolvePaths computes the memoq home directory and derived paths.
// MEMOQ_HOME overrides the default of ~/.memoq.
func ResolvePaths() (*Paths, error) {
	home := os.Getenv("MEMOQ_HOME")
	if home == "" {
		u, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("cannot resolve home dir: %w", err)
		}
		home = filepath.Join(u, ".memoq")
	}
	return &Paths{
		Home:           home,
		ConfigFile:     filepath.Join(home, "config.json"),
		DBFile:         filepath.Join(home, "memoq.db"),
		AttachmentsDir: filepath.Join(home, "attachments"),
	}, nil
}

// EnsureHome creates the memoq home directory if it does not exist.
func (p *Paths) EnsureHome() error {
	return os.MkdirAll(p.Home, 0o755)
}

// EnsureAttachmentsDir creates the attachments directory if it does not exist.
func (p *Paths) EnsureAttachmentsDir() error {
	return os.MkdirAll(p.AttachmentsDir, 0o755)
}

// Load reads config.json. A missing file yields a zero-value config with a
// sensible default TTL rather than an error, so first-run `config set` works.
func Load(p *Paths) (*Config, error) {
	data, err := os.ReadFile(p.ConfigFile)
	if os.IsNotExist(err) {
		return &Config{AutoSyncTTLSeconds: 30}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &c, nil
}

// Save writes config.json (pretty-printed, 0600 since it holds a token).
func Save(p *Paths, c *Config) error {
	if err := p.EnsureHome(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p.ConfigFile, data, 0o600)
}

// Validate reports whether the config is usable for network operations.
func (c *Config) Validate() error {
	if c.ServerURL == "" {
		return fmt.Errorf("server_url is not set (run: memoq config set server_url <url>)")
	}
	if c.Token == "" {
		return fmt.Errorf("token is not set (run: memoq config set token <token>)")
	}
	return nil
}
