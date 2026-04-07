package fs

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/sky10/sky10/pkg/adapter"
	"github.com/sky10/sky10/pkg/config"
)

// deriveNSID deterministically derives an opaque namespace ID from the
// namespace key and name using HMAC-SHA256. Any device with the same
// key and name produces the same ID — no coordination needed.
func deriveNSID(nsKey []byte, nsName string) string {
	mac := hmac.New(sha256.New, nsKey)
	mac.Write([]byte(nsName))
	return hex.EncodeToString(mac.Sum(nil)[:16]) // 16 bytes = 32 hex chars
}

// nsidMeta is the encrypted metadata for a namespace, stored at
// keys/namespaces/{name}.meta.enc. Used for namespace discovery by
// new devices — the nsID itself is derived, not stored here.
type nsidMeta struct {
	NSID string `json:"nsid"`
	Name string `json:"name"`
}

// resolveNSID returns the opaque namespace ID for a given namespace name.
// The ID is derived deterministically via HMAC(nsKey, name). The meta.enc
// file in S3 is written for discovery by new devices but is not the source
// of truth — any device with the key can re-derive the ID.
func resolveNSID(ctx context.Context, backend adapter.Backend, nsName string, nsKey []byte) (string, error) {
	nsID := deriveNSID(nsKey, nsName)

	// Write meta.enc if it doesn't exist (for new-device discovery)
	metaKey := "keys/namespaces/" + nsKeyName(nsName) + ".meta.enc"
	if _, err := backend.Head(ctx, metaKey); errors.Is(err, adapter.ErrNotFound) {
		meta := nsidMeta{NSID: nsID, Name: nsName}
		plain, _ := json.Marshal(meta)
		encrypted, err := Encrypt(plain, nsKey)
		if err != nil {
			return nsID, nil // non-fatal — ID is derived, not dependent on meta
		}
		r := bytes.NewReader(encrypted)
		backend.Put(ctx, metaKey, r, int64(len(encrypted)))
	}

	return nsID, nil
}

// cacheNSID saves the nsID to a local file for fast startup.
func cacheNSID(nsName, nsID string) {
	dir, err := config.FSNSIDsDir()
	if err != nil {
		return
	}
	os.MkdirAll(dir, 0700)
	os.WriteFile(filepath.Join(dir, nsKeyName(nsName)), []byte(nsID), 0600)
}

// loadCachedNSID reads a locally cached nsID.
func loadCachedNSID(nsName string) (string, error) {
	dir, err := config.FSNSIDsDir()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(dir, nsKeyName(nsName)))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// discoverNamespaces lists all namespace metadata files in S3 and tries
// to decrypt each with the provided key. Returns name → nsID for accessible
// namespaces. Used by new devices to discover available drives.
func discoverNamespaces(ctx context.Context, backend adapter.Backend, nsKey []byte) (map[string]string, error) {
	keys, err := backend.List(ctx, "keys/namespaces/")
	if err != nil {
		return nil, err
	}

	result := make(map[string]string)
	for _, key := range keys {
		if len(key) < 10 || key[len(key)-9:] != ".meta.enc" {
			continue
		}
		rc, err := backend.Get(ctx, key)
		if err != nil {
			continue
		}
		encrypted, _ := io.ReadAll(rc)
		rc.Close()

		plain, err := Decrypt(encrypted, nsKey)
		if err != nil {
			continue // wrong key — not our namespace
		}
		var meta nsidMeta
		if json.Unmarshal(plain, &meta) == nil {
			result[meta.Name] = meta.NSID
		}
	}
	return result, nil
}
