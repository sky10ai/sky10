package fs

import (
	"os"
	"path/filepath"

	"github.com/sky10/sky10/pkg/config"
)

// CacheKeyLocally stores an FS namespace key on disk for the given shared
// identity address. Used by join flows to pre-populate drive namespace keys
// before the daemon starts.
func CacheKeyLocally(namespace, identityAddress string, key []byte) {
	dir, err := config.FSKeysDir(shortPubkeyID(identityAddress))
	if err != nil {
		return
	}
	_ = os.MkdirAll(dir, 0700)
	_ = os.WriteFile(filepath.Join(dir, nsKeyName(namespace)+".key"), key, 0600)
}
