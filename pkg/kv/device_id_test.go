package kv

import (
	"strings"
	"testing"

	skykey "github.com/sky10/sky10/pkg/key"
)

// Regression: KV and fs must produce the same device ID from the same address.
func TestDeviceID_MatchesFS(t *testing.T) {
	t.Parallel()
	id, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}

	kvID := ShortDeviceID(id)
	fsID := fsStyleDeviceID(id.Address())

	if kvID != fsID {
		t.Errorf("device ID mismatch: kv=%q fs=%q (address=%s)", kvID, fsID, id.Address())
	}
}

// Verify the format: "D-" + 8 chars.
func TestDeviceID_Format(t *testing.T) {
	t.Parallel()
	id, _ := skykey.Generate()
	devID := ShortDeviceID(id)

	if !strings.HasPrefix(devID, "D-") {
		t.Errorf("device ID should start with D-, got %q", devID)
	}
	if len(devID) != 10 { // "D-" + 8 chars
		t.Errorf("device ID length = %d, want 10", len(devID))
	}

	addr := id.Address()
	expected := "D-" + addr[5:13]
	if devID != expected {
		t.Errorf("device ID = %q, want %q", devID, expected)
	}
}

// Verify consistency across multiple keys.
func TestDeviceID_DifferentKeysProduceDifferentIDs(t *testing.T) {
	t.Parallel()
	idA, _ := skykey.Generate()
	idB, _ := skykey.Generate()

	a := ShortDeviceID(idA)
	b := ShortDeviceID(idB)

	if a == b {
		t.Errorf("different keys produced same device ID: %s", a)
	}
}

// fsStyleDeviceID replicates the fs package's ShortPubkeyID for testing.
func fsStyleDeviceID(pubkey string) string {
	return "D-" + skykey.ShortIDFromAddress(pubkey)
}
