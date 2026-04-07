package kv

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/sky10/sky10/pkg/adapter"
	"github.com/sky10/sky10/pkg/config"
	skykey "github.com/sky10/sky10/pkg/key"
)

// Encrypt/Decrypt delegate to skykey.
func encrypt(plaintext, encKey []byte) ([]byte, error)  { return skykey.Encrypt(plaintext, encKey) }
func decrypt(ciphertext, encKey []byte) ([]byte, error) { return skykey.Decrypt(ciphertext, encKey) }

func wrapKey(dataKey []byte, pub ed25519.PublicKey) ([]byte, error) {
	return skykey.WrapKey(dataKey, pub)
}

func unwrapKey(wrapped []byte, priv ed25519.PrivateKey) ([]byte, error) {
	return skykey.UnwrapKey(wrapped, priv)
}

// nsKeyName returns the S3 key name prefix for a KV namespace.
// Prefixed with "kv:" to avoid collision with fs namespace names.
func nsKeyName(namespace string) string {
	return "kv:" + namespace
}

func deriveNSID(nsKey []byte, nsName string) string {
	mac := hmac.New(sha256.New, nsKey)
	mac.Write([]byte(nsName))
	return hex.EncodeToString(mac.Sum(nil)[:16])
}

type nsidMeta struct {
	NSID string `json:"nsid"`
	Name string `json:"name"`
}

func resolveNSID(ctx context.Context, backend adapter.Backend, nsName string, nsKey []byte) (string, error) {
	nsID := deriveNSID(nsKey, nsName)

	metaKey := "keys/namespaces/" + nsKeyName(nsName) + ".meta.enc"
	if _, err := backend.Head(ctx, metaKey); errors.Is(err, adapter.ErrNotFound) {
		meta := nsidMeta{NSID: nsID, Name: nsName}
		plain, _ := json.Marshal(meta)
		encrypted, err := encrypt(plain, nsKey)
		if err != nil {
			return nsID, nil
		}
		backend.Put(ctx, metaKey, bytes.NewReader(encrypted), int64(len(encrypted)))
	}

	return nsID, nil
}

func cacheNSID(nsName, nsID string) {
	dir, err := config.KVNSIDsDir()
	if err != nil {
		return
	}
	os.MkdirAll(dir, 0700)
	os.WriteFile(filepath.Join(dir, nsName), []byte(nsID), 0600)
}

// getOrCreateNamespaceKey resolves the KV namespace encryption key.
//
// With S3 backend, resolution order:
//  1. Device-specific wrapped key in S3
//  2. Shared (base) wrapped key in S3
//  3. Scan ALL wrapped keys for this namespace
//  4. Local disk cache
//  5. Generate new key and wrap for all registered devices
//
// Without S3 (backend == nil):
//  1. Local disk cache
//  2. Generate new key (cached locally)
func getOrCreateNamespaceKey(
	ctx context.Context,
	backend adapter.Backend,
	nsName string,
	identity *skykey.Key,
	deviceID string,
	requireExisting bool,
) ([]byte, error) {
	if backend == nil {
		return getOrCreateNamespaceKeyLocal(nsName, deviceID, requireExisting)
	}

	keyName := nsKeyName(nsName)

	// 1. Try device-specific wrapped key
	devKeyPath := "keys/namespaces/" + keyName + "." + deviceID + ".ns.enc"
	if rc, err := backend.Get(ctx, devKeyPath); err == nil {
		wrapped, _ := io.ReadAll(rc)
		rc.Close()
		if key, err := unwrapKey(wrapped, identity.PrivateKey); err == nil {
			CacheKeyLocally(nsName, deviceID, key)
			return key, nil
		}
	}

	// 2. Try shared (base) key
	sharedKeyPath := "keys/namespaces/" + keyName + ".ns.enc"
	if rc, err := backend.Get(ctx, sharedKeyPath); err == nil {
		wrapped, _ := io.ReadAll(rc)
		rc.Close()
		if key, err := unwrapKey(wrapped, identity.PrivateKey); err == nil {
			CacheKeyLocally(nsName, deviceID, key)
			return key, nil
		}
	}

	// 3. Scan all wrapped keys for this namespace. Another device may have
	// created the key and wrapped it for us before our device-specific path
	// was written (race between simultaneous first starts).
	prefix := "keys/namespaces/" + keyName + "."
	if allKeys, err := backend.List(ctx, prefix); err == nil {
		for _, k := range allKeys {
			if !strings.HasSuffix(k, ".ns.enc") {
				continue
			}
			rc, err := backend.Get(ctx, k)
			if err != nil {
				continue
			}
			wrapped, _ := io.ReadAll(rc)
			rc.Close()
			if key, err := unwrapKey(wrapped, identity.PrivateKey); err == nil {
				// Found a key we can unwrap — write our device-specific copy
				if w, err := wrapKey(key, identity.PublicKey); err == nil {
					backend.Put(ctx, devKeyPath, bytes.NewReader(w), int64(len(w)))
				}
				CacheKeyLocally(nsName, deviceID, key)
				return key, nil
			}
		}
	}

	// 4. Check local disk cache
	if key, err := loadCachedKey(nsName, deviceID); err == nil {
		if wrapped, err := wrapKey(key, identity.PublicKey); err == nil {
			backend.Put(ctx, devKeyPath, bytes.NewReader(wrapped), int64(len(wrapped)))
		}
		return key, nil
	}

	// 5. No key exists anywhere — create new and wrap for all devices
	key, err := skykey.GenerateSymmetricKey()
	if err != nil {
		return nil, err
	}
	wrapped, err := wrapKey(key, identity.PublicKey)
	if err != nil {
		return nil, err
	}
	if err := backend.Put(ctx, devKeyPath, bytes.NewReader(wrapped), int64(len(wrapped))); err != nil {
		return nil, err
	}

	wrapForAllDevices(ctx, backend, keyName, key, deviceID)
	CacheKeyLocally(nsName, deviceID, key)
	return key, nil
}

// getOrCreateNamespaceKeyLocal resolves namespace key from local cache only.
// Generates a new key if none is cached. Used in S3-free mode.
func getOrCreateNamespaceKeyLocal(nsName, deviceID string, requireExisting bool) ([]byte, error) {
	if key, err := loadCachedKey(nsName, deviceID); err == nil {
		return key, nil
	}
	if requireExisting {
		return nil, fmt.Errorf("missing cached namespace key for %q on device %s", nsName, deviceID)
	}
	key, err := skykey.GenerateSymmetricKey()
	if err != nil {
		return nil, err
	}
	CacheKeyLocally(nsName, deviceID, key)
	return key, nil
}

// CacheKeyLocally stores a namespace key on disk for the given device.
// Used by the join flow to pre-populate keys received from the inviter.
func CacheKeyLocally(nsName, deviceID string, key []byte) {
	dir, err := config.KVKeysDir(deviceID)
	if err != nil {
		return
	}
	os.MkdirAll(dir, 0700)
	os.WriteFile(filepath.Join(dir, nsName+".key"), key, 0600)
}

func loadCachedKey(nsName, deviceID string) ([]byte, error) {
	dir, err := config.KVKeysDir(deviceID)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(filepath.Join(dir, nsName+".key"))
}

// wrapForAllDevices wraps the namespace key for all registered devices.
func wrapForAllDevices(ctx context.Context, backend adapter.Backend, keyName string, nsKey []byte, skipDevice string) {
	keys, err := backend.List(ctx, "devices/")
	if err != nil {
		return
	}
	for _, k := range keys {
		name := strings.TrimPrefix(k, "devices/")
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		devID := strings.TrimSuffix(name, ".json")
		if devID == skipDevice {
			continue
		}

		rc, err := backend.Get(ctx, k)
		if err != nil {
			continue
		}
		data, _ := io.ReadAll(rc)
		rc.Close()

		var info struct {
			PubKey string `json:"pubkey"`
		}
		if json.Unmarshal(data, &info) != nil || info.PubKey == "" {
			continue
		}
		k, err := skykey.ParseAddress(info.PubKey)
		if err != nil {
			continue
		}
		wrapped, err := wrapKey(nsKey, k.PublicKey)
		if err != nil {
			continue
		}
		path := "keys/namespaces/" + keyName + "." + devID + ".ns.enc"
		backend.Put(ctx, path, bytes.NewReader(wrapped), int64(len(wrapped)))
	}
}
