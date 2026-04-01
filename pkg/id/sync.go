package id

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/sky10/sky10/pkg/adapter"
	skykey "github.com/sky10/sky10/pkg/key"
)

const (
	s3IdentityEnc = "identity/key.enc" // encrypted identity private key
	s3IdentityPub = "identity/pub"     // plaintext identity address
	s3ManifestKey = "identity/manifest.json"
)

// SyncIdentity ensures this device has the correct shared identity for
// the S3 bucket. It handles three cases:
//
//  1. No local identity, no S3 identity → generate both, publish to S3
//  2. No local identity, S3 identity exists → adopt S3 identity
//  3. Local identity exists, S3 identity exists → adopt S3 if different
//
// The identity private key is stored in S3 encrypted with a key derived
// from the identity address. Any device with bucket access can read the
// address and decrypt the key. The bucket IS the trust boundary.
func SyncIdentity(ctx context.Context, store *Store, backend adapter.Backend, deviceName string) (*Bundle, error) {
	remoteIdentity, err := downloadIdentity(ctx, backend)
	if err != nil && !errors.Is(err, adapter.ErrNotFound) {
		return nil, fmt.Errorf("fetching remote identity: %w", err)
	}

	localBundle, localErr := store.Load()

	switch {
	case remoteIdentity != nil && localErr == nil:
		// Both exist.
		if localBundle.Address() == remoteIdentity.Address() {
			return syncManifest(ctx, store, backend, localBundle)
		}
		return adoptIdentity(ctx, store, backend, remoteIdentity, deviceName)

	case remoteIdentity != nil:
		// Remote exists, no local → adopt.
		return adoptIdentity(ctx, store, backend, remoteIdentity, deviceName)

	case localErr == nil:
		// Local exists, no remote → publish.
		if err := uploadIdentity(ctx, backend, localBundle); err != nil {
			return nil, err
		}
		return localBundle, nil

	default:
		// Neither exists → generate + publish.
		bundle, err := store.Generate(deviceName)
		if err != nil {
			return nil, err
		}
		if err := uploadIdentity(ctx, backend, bundle); err != nil {
			return nil, err
		}
		return bundle, nil
	}
}

// adoptIdentity replaces the local identity with the remote one,
// generating a new device key and updating the manifest.
func adoptIdentity(ctx context.Context, store *Store, backend adapter.Backend, identity *skykey.Key, name string) (*Bundle, error) {
	device, err := skykey.Generate()
	if err != nil {
		return nil, fmt.Errorf("generating device key: %w", err)
	}

	manifest, _ := downloadManifest(ctx, backend, identity)
	if manifest == nil {
		manifest = NewManifest(identity)
	}

	manifest.AddDevice(device.PublicKey, name)
	if err := manifest.Sign(identity.PrivateKey); err != nil {
		return nil, fmt.Errorf("signing manifest: %w", err)
	}

	bundle, err := New(identity, device, manifest)
	if err != nil {
		return nil, err
	}
	if err := store.Save(bundle); err != nil {
		return nil, err
	}
	if err := uploadManifest(ctx, backend, manifest); err != nil {
		return nil, err
	}
	return bundle, nil
}

// syncManifest ensures this device is in the S3 manifest.
func syncManifest(ctx context.Context, store *Store, backend adapter.Backend, bundle *Bundle) (*Bundle, error) {
	remote, _ := downloadManifest(ctx, backend, bundle.Identity)
	if remote != nil && remote.HasDevice(bundle.Device.PublicKey) {
		return bundle, nil
	}

	manifest := remote
	if manifest == nil {
		manifest = NewManifest(bundle.Identity)
	}
	if !manifest.HasDevice(bundle.Device.PublicKey) {
		hostname, _ := os.Hostname()
		if hostname == "" {
			hostname = "unknown"
		}
		manifest.AddDevice(bundle.Device.PublicKey, hostname)
	}
	if err := manifest.Sign(bundle.Identity.PrivateKey); err != nil {
		return nil, fmt.Errorf("signing manifest: %w", err)
	}

	bundle.Manifest = manifest
	if err := store.Save(bundle); err != nil {
		return nil, err
	}
	return bundle, uploadManifest(ctx, backend, manifest)
}

// deriveEncKey derives the symmetric encryption key for the identity
// private key from the identity address. Deterministic: same address
// always produces the same key.
func deriveEncKey(address string) ([]byte, error) {
	return skykey.DeriveKey([]byte(address), []byte("sky10-identity"), "identity-encryption")
}

// uploadIdentity stores the identity key and manifest in S3.
func uploadIdentity(ctx context.Context, backend adapter.Backend, bundle *Bundle) error {
	addr := bundle.Address()
	encKey, err := deriveEncKey(addr)
	if err != nil {
		return fmt.Errorf("deriving encryption key: %w", err)
	}

	encrypted, err := skykey.Encrypt(bundle.Identity.PrivateKey, encKey)
	if err != nil {
		return fmt.Errorf("encrypting identity key: %w", err)
	}

	// Upload encrypted private key.
	if err := backend.Put(ctx, s3IdentityEnc,
		bytes.NewReader(encrypted), int64(len(encrypted))); err != nil {
		return fmt.Errorf("uploading identity key: %w", err)
	}

	// Upload plaintext address (needed to derive the decryption key).
	addrBytes := []byte(addr)
	if err := backend.Put(ctx, s3IdentityPub,
		bytes.NewReader(addrBytes), int64(len(addrBytes))); err != nil {
		return fmt.Errorf("uploading identity address: %w", err)
	}

	return uploadManifest(ctx, backend, bundle.Manifest)
}

// downloadIdentity fetches the identity key from S3.
func downloadIdentity(ctx context.Context, backend adapter.Backend) (*skykey.Key, error) {
	// Read the address first.
	pubRC, err := backend.Get(ctx, s3IdentityPub)
	if err != nil {
		return nil, err
	}
	defer pubRC.Close()

	addrBytes, err := io.ReadAll(pubRC)
	if err != nil {
		return nil, fmt.Errorf("reading identity address: %w", err)
	}
	addr := string(addrBytes)

	parsed, err := skykey.ParseAddress(addr)
	if err != nil {
		return nil, fmt.Errorf("parsing identity address: %w", err)
	}

	// Read and decrypt the private key.
	encRC, err := backend.Get(ctx, s3IdentityEnc)
	if err != nil {
		return nil, fmt.Errorf("fetching encrypted identity: %w", err)
	}
	defer encRC.Close()

	encrypted, err := io.ReadAll(encRC)
	if err != nil {
		return nil, fmt.Errorf("reading encrypted identity: %w", err)
	}

	encKey, err := deriveEncKey(addr)
	if err != nil {
		return nil, fmt.Errorf("deriving encryption key: %w", err)
	}

	privBytes, err := skykey.Decrypt(encrypted, encKey)
	if err != nil {
		return nil, fmt.Errorf("decrypting identity key: %w", err)
	}

	return &skykey.Key{
		PublicKey:  parsed.PublicKey,
		PrivateKey: privBytes,
	}, nil
}

// uploadManifest stores the manifest in S3.
func uploadManifest(ctx context.Context, backend adapter.Backend, manifest *DeviceManifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling manifest: %w", err)
	}
	return backend.Put(ctx, s3ManifestKey, bytes.NewReader(data), int64(len(data)))
}

// downloadManifest fetches and verifies the manifest from S3.
func downloadManifest(ctx context.Context, backend adapter.Backend, identity *skykey.Key) (*DeviceManifest, error) {
	rc, err := backend.Get(ctx, s3ManifestKey)
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	var manifest DeviceManifest
	if err := json.NewDecoder(rc).Decode(&manifest); err != nil {
		return nil, err
	}
	if !manifest.Verify(identity.PublicKey) {
		return nil, fmt.Errorf("manifest signature invalid")
	}
	return &manifest, nil
}
