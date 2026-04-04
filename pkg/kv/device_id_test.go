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
	fsID := "D-" + skykey.ShortIDFromAddress(id.Address())

	if kvID != fsID {
		t.Errorf("device ID mismatch: kv=%q fs=%q", kvID, fsID)
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
	if len(devID) != 10 {
		t.Errorf("device ID length = %d, want 10", len(devID))
	}
}

// Verify different keys produce different IDs.
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
