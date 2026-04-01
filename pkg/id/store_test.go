package id

import (
	"os"
	"path/filepath"
	"testing"

	skykey "github.com/sky10/sky10/pkg/key"
)

func TestGenerate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := NewStoreAt(dir)

	b, err := s.Generate("test-device")
	if err != nil {
		t.Fatal(err)
	}

	if b.Address() == "" {
		t.Error("expected non-empty identity address")
	}
	if b.Address() == b.DeviceID() {
		t.Error("identity and device addresses should differ")
	}
	if !b.Manifest.Verify(b.Identity.PublicKey) {
		t.Error("generated manifest should be valid")
	}
	if !b.Manifest.HasDevice(b.Device.PublicKey) {
		t.Error("manifest should contain the device")
	}
	if len(b.Manifest.Devices) != 1 {
		t.Errorf("expected 1 device, got %d", len(b.Manifest.Devices))
	}
	if b.Manifest.Devices[0].Name != "test-device" {
		t.Errorf("device name = %q, want %q", b.Manifest.Devices[0].Name, "test-device")
	}

	// Files should exist on disk.
	for _, f := range []string{identityFile, deviceFile, manifestFile} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("expected %s to exist: %v", f, err)
		}
	}
}

func TestSaveLoad(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := NewStoreAt(dir)

	original, err := s.Generate("laptop")
	if err != nil {
		t.Fatal(err)
	}

	loaded, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}

	if loaded.Address() != original.Address() {
		t.Errorf("loaded address = %s, want %s", loaded.Address(), original.Address())
	}
	if loaded.DeviceID() != original.DeviceID() {
		t.Errorf("loaded device address = %s, want %s", loaded.DeviceID(), original.DeviceID())
	}
	if !loaded.Manifest.Verify(loaded.Identity.PublicKey) {
		t.Error("loaded manifest should verify")
	}
}

func TestMigrate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := NewStoreAt(dir)

	// Create a legacy key.json.
	legacy, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := skykey.SaveWithDescription(legacy, filepath.Join(dir, legacyFile), "skyfs device key"); err != nil {
		t.Fatal(err)
	}

	if !s.NeedsMigration() {
		t.Fatal("should need migration")
	}

	b, err := s.Migrate()
	if err != nil {
		t.Fatal(err)
	}

	// Identity address should match the legacy key.
	if b.Address() != legacy.Address() {
		t.Errorf("migrated address = %s, want legacy %s", b.Address(), legacy.Address())
	}

	// Device key should be different from identity.
	if b.DeviceID() == b.Address() {
		t.Error("device address should differ from identity after migration")
	}

	// Manifest should be valid.
	if !b.Manifest.Verify(b.Identity.PublicKey) {
		t.Error("migrated manifest should verify")
	}
	if !b.Manifest.HasDevice(b.Device.PublicKey) {
		t.Error("manifest should contain the new device")
	}

	// Should no longer need migration.
	if s.NeedsMigration() {
		t.Error("should not need migration after migrating")
	}

	// Legacy key.json should still exist (not deleted).
	if _, err := os.Stat(filepath.Join(dir, legacyFile)); err != nil {
		t.Error("legacy key.json should be preserved")
	}
}

func TestNeedsMigration(t *testing.T) {
	t.Parallel()

	t.Run("empty dir", func(t *testing.T) {
		s := NewStoreAt(t.TempDir())
		if s.NeedsMigration() {
			t.Error("empty dir should not need migration")
		}
	})

	t.Run("legacy exists", func(t *testing.T) {
		dir := t.TempDir()
		k, _ := skykey.Generate()
		skykey.Save(k, filepath.Join(dir, legacyFile))

		s := NewStoreAt(dir)
		if !s.NeedsMigration() {
			t.Error("should need migration when key.json exists")
		}
	})

	t.Run("already migrated", func(t *testing.T) {
		dir := t.TempDir()
		s := NewStoreAt(dir)
		s.Generate("test")

		if s.NeedsMigration() {
			t.Error("should not need migration after Generate")
		}
	})
}

func TestLoadAfterMigrate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := NewStoreAt(dir)

	// Create legacy key and migrate.
	legacy, _ := skykey.Generate()
	skykey.Save(legacy, filepath.Join(dir, legacyFile))
	migrated, err := s.Migrate()
	if err != nil {
		t.Fatal(err)
	}

	// Load should return the same bundle.
	loaded, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}

	if loaded.Address() != migrated.Address() {
		t.Errorf("loaded = %s, migrated = %s", loaded.Address(), migrated.Address())
	}
	if loaded.DeviceID() != migrated.DeviceID() {
		t.Errorf("loaded device = %s, migrated device = %s", loaded.DeviceID(), migrated.DeviceID())
	}
}

func TestLoadAutoMigrates(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := NewStoreAt(dir)

	// Only legacy key.json exists.
	legacy, _ := skykey.Generate()
	skykey.Save(legacy, filepath.Join(dir, legacyFile))

	// Load should auto-migrate.
	b, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if b.Address() != legacy.Address() {
		t.Errorf("auto-migrated address = %s, want %s", b.Address(), legacy.Address())
	}
}
