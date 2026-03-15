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
		t.Error("IdentityFile should have a default path")
	}
}

func TestLoadNoConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	_, err := Load()
	if err == nil {
		t.Error("expected error when config doesn't exist")
	}
}

func TestMigrateFromOldDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// Create old .skyfs directory with a config
	oldDir := filepath.Join(tmp, ".skyfs")
	os.MkdirAll(oldDir, 0700)
	os.WriteFile(filepath.Join(oldDir, "config.json"),
		[]byte(`{"bucket":"migrated"}`), 0600)

	// Dir() should auto-migrate to .sky10
	dir, err := Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}

	if filepath.Base(dir) != ".sky10" {
		t.Errorf("dir = %q, want .sky10", dir)
	}

	// Old dir should be gone
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Error(".skyfs should be renamed to .sky10")
	}

	// Config should be loadable from new location
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load after migration: %v", err)
	}
	if cfg.Bucket != "migrated" {
		t.Errorf("bucket = %q, want migrated", cfg.Bucket)
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
