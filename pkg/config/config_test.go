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

	// No config.json → empty config (S3-free mode), not an error.
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load should succeed without config.json: %v", err)
	}
	if cfg.HasStorage() {
		t.Error("empty config should not have storage")
	}
	if cfg.IdentityFile == "" {
		t.Error("IdentityFile should have a default path")
	}
}

func TestHasStorage(t *testing.T) {
	tests := []struct {
		name string
		cfg  *Config
		want bool
	}{
		{"nil config", nil, false},
		{"empty bucket", &Config{}, false},
		{"with bucket", &Config{Bucket: "my-bucket"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.HasStorage(); got != tt.want {
				t.Errorf("HasStorage() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRelays(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		cfg := &Config{}
		if got := cfg.Relays(); len(got) == 0 {
			t.Error("Relays() should return defaults when none configured")
		}
	})
	t.Run("custom", func(t *testing.T) {
		cfg := &Config{NostrRelays: []string{"wss://custom.relay"}}
		got := cfg.Relays()
		if len(got) != 1 || got[0] != "wss://custom.relay" {
			t.Errorf("Relays() = %v, want [wss://custom.relay]", got)
		}
	})
}

func TestMigrateFromFlatLayout(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// Create old flat .sky10/ layout (config.json at top level)
	oldDir := filepath.Join(tmp, ".sky10")
	os.MkdirAll(oldDir, 0700)
	os.WriteFile(filepath.Join(oldDir, "config.json"),
		[]byte(`{"bucket":"migrated"}`), 0600)
	os.WriteFile(filepath.Join(oldDir, "key.json"),
		[]byte(`{"seed":"test"}`), 0600)

	// Dir() should auto-migrate to .sky10/fs/
	dir, err := Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}

	if filepath.Base(dir) != "fs" {
		t.Errorf("dir = %q, want to end with /fs", dir)
	}

	// Config should be in fs/ now
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load after migration: %v", err)
	}
	if cfg.Bucket != "migrated" {
		t.Errorf("bucket = %q, want migrated", cfg.Bucket)
	}

	// key.json should be migrated to ~/.sky10/keys/
	expectedKeyPath := filepath.Join(tmp, ".sky10", "keys", "key.json")
	if _, err := os.Stat(expectedKeyPath); err != nil {
		t.Errorf("key.json should be at %s, err: %v", expectedKeyPath, err)
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

func TestDirsUseSky10Home(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "instance-a")
	t.Setenv(EnvHome, root)

	dir, err := Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if dir != filepath.Join(root, "fs") {
		t.Fatalf("Dir = %q, want %q", dir, filepath.Join(root, "fs"))
	}

	keysDir, err := KeysDir()
	if err != nil {
		t.Fatalf("KeysDir: %v", err)
	}
	if keysDir != filepath.Join(root, "keys") {
		t.Fatalf("KeysDir = %q, want %q", keysDir, filepath.Join(root, "keys"))
	}

	kvDir, err := KVDir()
	if err != nil {
		t.Fatalf("KVDir: %v", err)
	}
	if kvDir != filepath.Join(root, "kv") {
		t.Fatalf("KVDir = %q, want %q", kvDir, filepath.Join(root, "kv"))
	}
}

func TestRuntimeDirUsesSky10Home(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "instance-b")
	t.Setenv(EnvHome, root)

	got := RuntimeDir()
	want := filepath.Join(root, "run")
	if got != want {
		t.Fatalf("RuntimeDir = %q, want %q", got, want)
	}
}

func TestRuntimeDirUsesExplicitOverride(t *testing.T) {
	tmp := t.TempDir()
	override := filepath.Join(tmp, "runtime")
	t.Setenv(EnvRuntimeDir, override)

	got := RuntimeDir()
	if got != override {
		t.Fatalf("RuntimeDir = %q, want %q", got, override)
	}
}
