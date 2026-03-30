package fs

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/sky10/sky10/pkg/adapter"
)

// nsidMeta is the encrypted metadata for a namespace, stored at
// keys/namespaces/{name}.meta.enc. Encrypted with the namespace key.
type nsidMeta struct {
	NSID string `json:"nsid"`
	Name string `json:"name"`
}

// resolveNSID returns the opaque namespace ID for a given namespace name.
// If no ID exists yet, generates one and uploads the encrypted metadata.
func resolveNSID(ctx context.Context, backend adapter.Backend, nsName string, nsKey []byte) (string, error) {
	metaKey := "keys/namespaces/" + nsName + ".meta.enc"

	// Try to load existing
	rc, err := backend.Get(ctx, metaKey)
	if err == nil {
		defer rc.Close()
		encrypted, err := io.ReadAll(rc)
		if err != nil {
			return "", fmt.Errorf("reading nsid meta: %w", err)
		}
		plain, err := Decrypt(encrypted, nsKey)
		if err != nil {
			return "", fmt.Errorf("decrypting nsid meta: %w", err)
		}
		var meta nsidMeta
		if err := json.Unmarshal(plain, &meta); err != nil {
			return "", fmt.Errorf("parsing nsid meta: %w", err)
		}
		return meta.NSID, nil
	}
	if !errors.Is(err, adapter.ErrNotFound) {
		return "", fmt.Errorf("checking nsid meta: %w", err)
	}

	// Generate new opaque ID (16 random bytes = 32 hex chars)
	idBytes := make([]byte, 16)
	rand.Read(idBytes)
	nsID := hex.EncodeToString(idBytes)

	meta := nsidMeta{NSID: nsID, Name: nsName}
	plain, _ := json.Marshal(meta)
	encrypted, err := Encrypt(plain, nsKey)
	if err != nil {
		return "", fmt.Errorf("encrypting nsid meta: %w", err)
	}
	r := bytes.NewReader(encrypted)
	if err := backend.Put(ctx, metaKey, r, int64(len(encrypted))); err != nil {
		return "", fmt.Errorf("storing nsid meta: %w", err)
	}

	return nsID, nil
}

// cacheNSID saves the nsID to a local file for fast startup.
func cacheNSID(nsName, nsID string) {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".sky10", "fs", "nsids")
	os.MkdirAll(dir, 0700)
	os.WriteFile(filepath.Join(dir, nsName), []byte(nsID), 0600)
}

// loadCachedNSID reads a locally cached nsID.
func loadCachedNSID(nsName string) (string, error) {
	home, _ := os.UserHomeDir()
	data, err := os.ReadFile(filepath.Join(home, ".sky10", "fs", "nsids", nsName))
	if err != nil {
		return "", err
	}
	return string(data), nil
}
