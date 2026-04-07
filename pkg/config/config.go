// Package config handles sky10 configuration loading and storage.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	configDir    = baseDirName + "/" + fsDirName
	oldConfigDir = baseDirName // auto-migrate from flat layout
	configFile   = "config.json"
	keyFile      = "key.json"
)

// DefaultNostrRelays are used when no relays are configured.
var DefaultNostrRelays = []string{
	"wss://relay.damus.io",
	"wss://nos.lol",
	"wss://relay.nostr.band",
}

// Config holds storage configuration.
type Config struct {
	Bucket         string   `json:"bucket,omitempty"`
	Region         string   `json:"region,omitempty"`
	Endpoint       string   `json:"endpoint,omitempty"`
	ForcePathStyle bool     `json:"force_path_style,omitempty"`
	IdentityFile   string   `json:"identity_file,omitempty"`
	NostrRelays    []string `json:"nostr_relays,omitempty"`
}

// HasStorage reports whether an S3-compatible storage backend is configured.
func (c *Config) HasStorage() bool {
	return c != nil && c.Bucket != ""
}

// Relays returns the configured Nostr relays, falling back to defaults.
func (c *Config) Relays() []string {
	if c != nil && len(c.NostrRelays) > 0 {
		return c.NostrRelays
	}
	return DefaultNostrRelays
}

// Dir returns the skyfs configuration directory path (~/.sky10/fs/).
// Auto-migrates from ~/.sky10/ flat layout if config.json exists there.
func Dir() (string, error) {
	root, err := RootDir()
	if err != nil {
		return "", err
	}

	newDir := filepath.Join(root, fsDirName)

	// Auto-migrate: if ~/.sky10/fs/config.json doesn't exist but ~/.sky10/config.json does,
	// move fs-related files into the fs/ subdirectory and keys to ~/.sky10/keys/.
	newConfig := filepath.Join(newDir, configFile)
	if _, err := os.Stat(newConfig); os.IsNotExist(err) {
		oldDir := root
		oldConfig := filepath.Join(oldDir, configFile)
		if _, err := os.Stat(oldConfig); err == nil {
			os.MkdirAll(newDir, 0700)
			// Move fs-specific files (not key.json — that goes to keys/)
			for _, f := range []string{configFile, "drives.json"} {
				old := filepath.Join(oldDir, f)
				if _, err := os.Stat(old); err == nil {
					os.Rename(old, filepath.Join(newDir, f))
				}
			}
			// Move namespace key cache to fs/keys/ FIRST (before creating ~/.sky10/keys/)
			oldNsKeys := filepath.Join(oldDir, "keys")
			if _, err := os.Stat(oldNsKeys); err == nil {
				os.Rename(oldNsKeys, filepath.Join(newDir, "keys"))
			}
			// Move key.json to ~/.sky10/keys/
			keysDir := filepath.Join(root, keysDirName)
			os.MkdirAll(keysDir, 0700)
			oldKey := filepath.Join(oldDir, keyFile)
			if _, err := os.Stat(oldKey); err == nil {
				os.Rename(oldKey, filepath.Join(keysDir, keyFile))
			}
		}

		// Also check for ancient ~/.skyfs/ layout
		if !hasCustomRoot() {
			home, err := os.UserHomeDir()
			if err == nil {
				ancientDir := filepath.Join(home, ancientDirName)
				if _, err := os.Stat(newConfig); os.IsNotExist(err) {
					if _, err := os.Stat(ancientDir); err == nil {
						os.Rename(ancientDir, newDir)
					}
				}
			}
		}
	}

	return newDir, nil
}

// KeysDir returns the directory for keys (~/.sky10/keys/).
func KeysDir() (string, error) {
	root, err := RootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, keysDirName), nil
}

// DefaultIdentityPath returns the default path for the skyfs device key.
func DefaultIdentityPath() (string, error) {
	dir, err := KeysDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, keyFile), nil
}

// Load reads the config from the default location (~/.sky10/fs/config.json).
func Load() (*Config, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}

	path := filepath.Join(dir, configFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// No config file — return empty config (S3-free mode).
			cfg := &Config{}
			cfg.IdentityFile = resolveIdentityPath("")
			return cfg, nil
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	cfg.IdentityFile = resolveIdentityPath(cfg.IdentityFile)
	return &cfg, nil
}

// resolveIdentityPath returns the best identity file path, falling back to
// the default ~/.sky10/keys/key.json when current is empty or missing.
func resolveIdentityPath(current string) string {
	if current != "" && fileExists(current) {
		return current
	}
	keysDir, _ := KeysDir()
	newPath := filepath.Join(keysDir, keyFile)
	if current == "" || fileExists(newPath) {
		return newPath
	}
	return current
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

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
