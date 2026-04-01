package id

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sky10/sky10/pkg/config"
	skykey "github.com/sky10/sky10/pkg/key"
)

const (
	identityFile = "identity.json"
	deviceFile   = "device.json"
	manifestFile = "manifest.json"
	legacyFile   = "key.json"
)

// Store handles reading and writing identity bundles to disk.
type Store struct {
	dir string
}

// NewStore creates a Store using the default keys directory (~/.sky10/keys/).
func NewStore() (*Store, error) {
	dir, err := config.KeysDir()
	if err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

// NewStoreAt creates a Store at the given directory (for testing).
func NewStoreAt(dir string) *Store {
	return &Store{dir: dir}
}

// Load reads the identity bundle from disk. If new-format files don't
// exist but a legacy key.json does, it auto-migrates.
func (s *Store) Load() (*Bundle, error) {
	idPath := filepath.Join(s.dir, identityFile)
	devPath := filepath.Join(s.dir, deviceFile)
	manPath := filepath.Join(s.dir, manifestFile)

	// Check if new-format files exist.
	if _, err := os.Stat(idPath); os.IsNotExist(err) {
		if s.NeedsMigration() {
			return s.Migrate()
		}
		return nil, fmt.Errorf("identity not initialized: %s not found", idPath)
	}

	identity, err := skykey.Load(idPath)
	if err != nil {
		return nil, fmt.Errorf("loading identity key: %w", err)
	}
	device, err := skykey.Load(devPath)
	if err != nil {
		return nil, fmt.Errorf("loading device key: %w", err)
	}

	manData, err := os.ReadFile(manPath)
	if err != nil {
		return nil, fmt.Errorf("reading manifest: %w", err)
	}
	var manifest DeviceManifest
	if err := json.Unmarshal(manData, &manifest); err != nil {
		return nil, fmt.Errorf("parsing manifest: %w", err)
	}

	return New(identity, device, &manifest)
}

// Save writes the identity bundle to disk.
func (s *Store) Save(b *Bundle) error {
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return fmt.Errorf("creating keys directory: %w", err)
	}

	if err := skykey.SaveWithDescription(b.Identity, filepath.Join(s.dir, identityFile), "sky10 identity key"); err != nil {
		return fmt.Errorf("saving identity key: %w", err)
	}
	if err := skykey.SaveWithDescription(b.Device, filepath.Join(s.dir, deviceFile), "sky10 device key"); err != nil {
		return fmt.Errorf("saving device key: %w", err)
	}

	manData, err := json.MarshalIndent(b.Manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, manifestFile), manData, 0600); err != nil {
		return fmt.Errorf("writing manifest: %w", err)
	}

	return nil
}

// Generate creates a fresh identity bundle with a new identity key,
// device key, and signed manifest.
func (s *Store) Generate(deviceName string) (*Bundle, error) {
	identity, err := skykey.Generate()
	if err != nil {
		return nil, fmt.Errorf("generating identity key: %w", err)
	}
	device, err := skykey.Generate()
	if err != nil {
		return nil, fmt.Errorf("generating device key: %w", err)
	}

	manifest := NewManifest(identity)
	manifest.AddDevice(device.PublicKey, deviceName)
	if err := manifest.Sign(identity.PrivateKey); err != nil {
		return nil, fmt.Errorf("signing manifest: %w", err)
	}

	b, err := New(identity, device, manifest)
	if err != nil {
		return nil, err
	}

	if err := s.Save(b); err != nil {
		return nil, err
	}
	return b, nil
}

// Migrate converts a legacy single-key setup to the new identity + device
// bundle. The existing key.json becomes the identity key (preserving the
// user's sky10q... address), and a new device key is generated.
func (s *Store) Migrate() (*Bundle, error) {
	legacyPath := filepath.Join(s.dir, legacyFile)
	identity, err := skykey.Load(legacyPath)
	if err != nil {
		return nil, fmt.Errorf("loading legacy key: %w", err)
	}

	device, err := skykey.Generate()
	if err != nil {
		return nil, fmt.Errorf("generating device key: %w", err)
	}

	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "unknown"
	}

	manifest := NewManifest(identity)
	manifest.AddDevice(device.PublicKey, hostname)
	if err := manifest.Sign(identity.PrivateKey); err != nil {
		return nil, fmt.Errorf("signing manifest: %w", err)
	}

	b, err := New(identity, device, manifest)
	if err != nil {
		return nil, err
	}

	if err := s.Save(b); err != nil {
		return nil, err
	}
	return b, nil
}

// NeedsMigration returns true if a legacy key.json exists but the
// new identity.json does not.
func (s *Store) NeedsMigration() bool {
	_, legacyErr := os.Stat(filepath.Join(s.dir, legacyFile))
	_, newErr := os.Stat(filepath.Join(s.dir, identityFile))
	return legacyErr == nil && os.IsNotExist(newErr)
}
