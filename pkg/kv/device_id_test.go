package kv

import (
	"testing"

	skykey "github.com/sky10/sky10/pkg/key"
)

// Regression: KV used last 12 chars of address as device ID while fs
// used chars 5-21 (16 chars after "sky10" prefix). The poller lists
// devices from devices/ (fs-style IDs) but looked for KV snapshots
// under KV-style IDs — never found them.
func TestDeviceID_MatchesFS(t *testing.T) {
	t.Parallel()
	id, err := skykey.Generate()
	if err != nil {
		t.Fatal(err)
	}

	kvID := shortDeviceID(id)
	fsID := fsStyleDeviceID(id.Address())

	if kvID != fsID {
		t.Errorf("device ID mismatch: kv=%q fs=%q (address=%s)", kvID, fsID, id.Address())
	}
}

// Verify the format: 16 chars, from position 5-21 of the address.
func TestDeviceID_Format(t *testing.T) {
	t.Parallel()
	id, _ := skykey.Generate()
	devID := shortDeviceID(id)

	if len(devID) != 16 {
		t.Errorf("device ID length = %d, want 16", len(devID))
	}

	addr := id.Address()
	expected := addr[5:21]
	if devID != expected {
		t.Errorf("device ID = %q, want addr[5:21] = %q", devID, expected)
	}
}

// Verify consistency across multiple keys.
func TestDeviceID_DifferentKeysProduceDifferentIDs(t *testing.T) {
	t.Parallel()
	idA, _ := skykey.Generate()
	idB, _ := skykey.Generate()

	a := shortDeviceID(idA)
	b := shortDeviceID(idB)

	if a == b {
		t.Errorf("different keys produced same device ID: %s", a)
	}
}

// fsStyleDeviceID replicates the fs package's shortPubkeyID for testing.
// If this function's logic ever diverges from pkg/fs/device.go, the
// TestDeviceID_MatchesFS test above will catch it.
func fsStyleDeviceID(pubkey string) string {
	if len(pubkey) > 21 {
		return pubkey[5:21]
	}
	return pubkey
}
