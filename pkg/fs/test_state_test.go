package fs

import (
	"context"
	"os"
	"testing"

	"github.com/sky10/sky10/pkg/config"
)

func useIsolatedSky10Home(t *testing.T) {
	t.Helper()
	t.Setenv(config.EnvHome, t.TempDir())
}

func removeLocalBlobsForTest(t *testing.T, store *Store, namespace string, chunks []string) {
	t.Helper()

	ctx := context.Background()
	nsID, _, err := store.resolveNamespaceState(ctx, namespace)
	if err != nil {
		t.Fatalf("resolveNamespaceState(%s): %v", namespace, err)
	}
	for _, chunkHash := range chunks {
		chunkPath, err := localBlobPath(nsID, chunkHash)
		if err != nil {
			t.Fatalf("localBlobPath(%s): %v", chunkHash, err)
		}
		if err := os.Remove(chunkPath); err != nil && !os.IsNotExist(err) {
			t.Fatalf("remove local blob %s: %v", chunkHash, err)
		}
	}
}
