// Package config handles sky10 configuration loading and storage.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	configDir    = ".sky10"
	oldConfigDir = ".skyfs" // auto-migrate from old location
	configFile   = "config.json"
	keyFile      = "key.json"
)

// Config holds storage configuration.
type Config struct {
	Bucket         string `json:"bucket"`
	Region         string `json:"region,omitempty"`
	Endpoint       string `json:"endpoint,omitempty"`
	ForcePathStyle bool   `json:"force_path_style,omitempty"`
	IdentityFile   string `json:"identity_file,omitempty"`
}

// Dir returns the sky10 configuration directory path (~/.sky10/).
// Auto-migrates from ~/.skyfs/ if it exists and ~/.sky10/ doesn't.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding home directory: %w", err)
	}

	newDir := filepath.Join(home, configDir)
	oldDir := filepath.Join(home, oldConfigDir)

	// Auto-migrate if old dir exists and new doesn't
	if _, err := os.Stat(newDir); os.IsNotExist(err) {
		if _, err := os.Stat(oldDir); err == nil {
			os.Rename(oldDir, newDir)
		}
	}

	return newDir, nil
}

// DefaultIdentityPath returns the default path for the key file.
func DefaultIdentityPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, keyFile), nil
}

// Load reads the config from the default location (~/.sky10/config.json).
func Load() (*Config, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}

	path := filepath.Join(dir, configFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no config found — run 'sky10 fs init' first")
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if cfg.IdentityFile == "" {
		cfg.IdentityFile = filepath.Join(dir, keyFile)
	}

	return &cfg, nil
}

// Save writes the config to the default location.
func Save(cfg *Config) error {
	dir, err := Dir()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	path := filepath.Join(dir, configFile)
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	return nil
}
