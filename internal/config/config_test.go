package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveAndLoad(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	cfg := &Config{
		Bucket:         "test-bucket",
		Region:         "us-west-2",
		Endpoint:       "https://s3.example.com",
		ForcePathStyle: true,
	}

	if err := Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.Bucket != cfg.Bucket {
		t.Errorf("Bucket = %q, want %q", loaded.Bucket, cfg.Bucket)
	}
	if loaded.Region != cfg.Region {
		t.Errorf("Region = %q, want %q", loaded.Region, cfg.Region)
	}
	if loaded.Endpoint != cfg.Endpoint {
		t.Errorf("Endpoint = %q, want %q", loaded.Endpoint, cfg.Endpoint)
	}
	if loaded.ForcePathStyle != cfg.ForcePathStyle {
		t.Errorf("ForcePathStyle = %v, want %v", loaded.ForcePathStyle, cfg.ForcePathStyle)
	}

	// Default identity path should be set
	if loaded.IdentityFile == "" {
		t.Error("IdentityFile should default to ~/.skyfs/identity.key")
	}
}

func TestLoadNoConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	_, err := Load()
	if err == nil {
		t.Error("expected error when config doesn't exist")
	}
}

func TestSavePermissions(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	cfg := &Config{Bucket: "test"}
	if err := Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	path := filepath.Join(tmp, configDir, configFile)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("config file permissions = %o, want 0600", perm)
	}

	// Config directory should be 0700
	dirInfo, err := os.Stat(filepath.Join(tmp, configDir))
	if err != nil {
		t.Fatalf("Stat dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0700 {
		t.Errorf("config dir permissions = %o, want 0700", perm)
	}
}
