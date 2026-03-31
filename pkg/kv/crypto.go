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
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/sky10/sky10/pkg/adapter"
	skykey "github.com/sky10/sky10/pkg/key"
)

// Encrypt/Decrypt delegate to skykey.
func encrypt(plaintext, encKey []byte) ([]byte, error)  { return skykey.Encrypt(plaintext, encKey) }
func decrypt(ciphertext, encKey []byte) ([]byte, error) { return skykey.Decrypt(ciphertext, encKey) }

// wrapKey wraps a data key for a recipient's public key.
func wrapKey(dataKey []byte, pub ed25519.PublicKey) ([]byte, error) {
	return skykey.WrapKey(dataKey, pub)
}

// unwrapKey unwraps a data key with a recipient's private key.
func unwrapKey(wrapped []byte, priv ed25519.PrivateKey) ([]byte, error) {
	return skykey.UnwrapKey(wrapped, priv)
}

// deriveNSID deterministically derives an opaque namespace ID from
// the namespace key and name using HMAC-SHA256.
func deriveNSID(nsKey []byte, nsName string) string {
	mac := hmac.New(sha256.New, nsKey)
	mac.Write([]byte(nsName))
	return hex.EncodeToString(mac.Sum(nil)[:16])
}

type nsidMeta struct {
	NSID string `json:"nsid"`
	Name string `json:"name"`
}

// resolveNSID returns the opaque namespace ID, writing meta.enc if needed.
func resolveNSID(ctx context.Context, backend adapter.Backend, nsName string, nsKey []byte) (string, error) {
	nsID := deriveNSID(nsKey, nsName)

	metaKey := "keys/namespaces/" + nsName + ".meta.enc"
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

// cacheNSID saves the nsID to a local file for fast startup.
func cacheNSID(nsName, nsID string) {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".sky10", "kv", "nsids")
	os.MkdirAll(dir, 0700)
	os.WriteFile(filepath.Join(dir, nsName), []byte(nsID), 0600)
}

// loadCachedNSID reads a locally cached nsID.
func loadCachedNSID(nsName string) (string, error) {
	home, _ := os.UserHomeDir()
	data, err := os.ReadFile(filepath.Join(home, ".sky10", "kv", "nsids", nsName))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// getOrCreateNamespaceKey resolves the namespace encryption key.
// Checks S3 for device-specific wrapped key, shared key, and local cache.
// Creates a new key if none exists.
func getOrCreateNamespaceKey(
	ctx context.Context,
	backend adapter.Backend,
	nsName string,
	identity *skykey.Key,
	deviceID string,
) ([]byte, error) {
	// Try device-specific key
	devKeyPath := "keys/namespaces/" + nsName + "." + deviceID + ".ns.enc"
	if rc, err := backend.Get(ctx, devKeyPath); err == nil {
		wrapped, _ := io.ReadAll(rc)
		rc.Close()
		if key, err := unwrapKey(wrapped, identity.PrivateKey); err == nil {
			cacheKeyLocally(nsName, deviceID, key)
			return key, nil
		}
	}

	// Try shared key
	sharedKeyPath := "keys/namespaces/" + nsName + ".ns.enc"
	if rc, err := backend.Get(ctx, sharedKeyPath); err == nil {
		wrapped, _ := io.ReadAll(rc)
		rc.Close()
		if key, err := unwrapKey(wrapped, identity.PrivateKey); err == nil {
			cacheKeyLocally(nsName, deviceID, key)
			return key, nil
		}
	}

	// Check local cache
	if key, err := loadCachedKey(nsName, deviceID); err == nil {
		// Re-upload to S3 so other operations can find it
		if wrapped, err := wrapKey(key, identity.PublicKey); err == nil {
			backend.Put(ctx, devKeyPath, bytes.NewReader(wrapped), int64(len(wrapped)))
		}
		return key, nil
	}

	// Generate new namespace key
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

	// Wrap for all other registered devices
	wrapForAllDevices(ctx, backend, nsName, key, deviceID)

	cacheKeyLocally(nsName, deviceID, key)
	return key, nil
}

func cacheKeyLocally(nsName, deviceID string, key []byte) {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".sky10", "kv", "keys", deviceID)
	os.MkdirAll(dir, 0700)
	os.WriteFile(filepath.Join(dir, nsName+".key"), key, 0600)
}

func loadCachedKey(nsName, deviceID string) ([]byte, error) {
	home, _ := os.UserHomeDir()
	return os.ReadFile(filepath.Join(home, ".sky10", "kv", "keys", deviceID, nsName+".key"))
}

// wrapForAllDevices wraps the namespace key for all registered devices.
func wrapForAllDevices(ctx context.Context, backend adapter.Backend, nsName string, nsKey []byte, skipDevice string) {
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
		path := "keys/namespaces/" + nsName + "." + devID + ".ns.enc"
		backend.Put(ctx, path, bytes.NewReader(wrapped), int64(len(wrapped)))
	}
}
