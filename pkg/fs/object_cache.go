package fs

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/sky10/sky10/pkg/config"
)

func localBlobExists(nsID, hash string) bool {
	path, err := localBlobPath(nsID, hash)
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

func readLocalBlob(nsID, hash string) ([]byte, error) {
	path, err := localBlobPath(nsID, hash)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func writeLocalBlob(nsID, hash string, blob []byte) error {
	path, err := localBlobPath(nsID, hash)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("creating blob cache dir: %w", err)
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, blob, 0600); err != nil {
		return fmt.Errorf("writing blob cache temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("publishing blob cache file: %w", err)
	}
	return nil
}

func localBlobPath(nsID, hash string) (string, error) {
	objectsDir, err := config.FSObjectsDir()
	if err != nil {
		return "", err
	}
	prefix := "xx"
	if len(hash) >= 2 {
		prefix = hash[:2]
	}
	return filepath.Join(objectsDir, nsID, "blobs", prefix, hash+".blob"), nil
}
