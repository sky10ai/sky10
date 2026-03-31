package kv

import (
	"context"
	"testing"
	"time"
)

func TestIntegration_TwoDeviceSetGet(t *testing.T) {
	env := setupTwoDevices(t)
	ctx := context.Background()

	// A sets a key
	env.localLogA.AppendLocal(Entry{
		Type: Set, Key: "greeting", Value: []byte("hello from A"),
	})
	env.uploaderA.Upload(ctx)

	// B polls — should see the key
	env.pollerB.pollOnce(ctx)

	vi, ok := env.localLogB.Lookup("greeting")
	if !ok {
		t.Fatal("B: greeting not found after poll")
	}
	if string(vi.Value) != "hello from A" {
		t.Errorf("B: got %q, want %q", vi.Value, "hello from A")
	}
	if vi.Device != env.devIDA {
		t.Errorf("B: device = %q, want %q", vi.Device, env.devIDA)
	}
}

func TestIntegration_DeletePropagation(t *testing.T) {
	env := setupTwoDevices(t)
	ctx := context.Background()

	// A sets a key
	env.localLogA.AppendLocal(Entry{
		Type: Set, Key: "temp", Value: []byte("temporary"),
	})
	env.uploaderA.Upload(ctx)

	// B polls — gets it
	env.pollerB.pollOnce(ctx)
	if _, ok := env.localLogB.Lookup("temp"); !ok {
		t.Fatal("B: temp not found after first poll")
	}

	// A deletes
	env.localLogA.AppendLocal(Entry{Type: Delete, Key: "temp"})
	env.uploaderA.Upload(ctx)

	// B polls again — baseline diff detects delete
	env.pollerB.pollOnce(ctx)

	if _, ok := env.localLogB.Lookup("temp"); ok {
		t.Error("B: temp should be gone after delete propagation")
	}

	snapB, _ := env.localLogB.Snapshot()
	if !snapB.DeletedKeys()["temp"] {
		t.Error("B: temp should be in DeletedKeys()")
	}
}

func TestIntegration_BidirectionalSync(t *testing.T) {
	env := setupTwoDevices(t)
	ctx := context.Background()

	// A sets key-a
	env.localLogA.AppendLocal(Entry{
		Type: Set, Key: "key-a", Value: []byte("from A"),
	})
	env.uploaderA.Upload(ctx)

	// B sets key-b
	env.localLogB.AppendLocal(Entry{
		Type: Set, Key: "key-b", Value: []byte("from B"),
	})
	env.uploaderB.Upload(ctx)

	// Both poll
	env.pollerA.pollOnce(ctx)
	env.pollerB.pollOnce(ctx)

	// A should have both
	snapA, _ := env.localLogA.Snapshot()
	if snapA.Len() != 2 {
		t.Errorf("A: Len() = %d, want 2", snapA.Len())
	}
	viA, ok := snapA.Lookup("key-b")
	if !ok {
		t.Error("A: missing key-b")
	} else if string(viA.Value) != "from B" {
		t.Errorf("A: key-b = %q, want %q", viA.Value, "from B")
	}

	// B should have both
	snapB, _ := env.localLogB.Snapshot()
	if snapB.Len() != 2 {
		t.Errorf("B: Len() = %d, want 2", snapB.Len())
	}
	viB, ok := snapB.Lookup("key-a")
	if !ok {
		t.Error("B: missing key-a")
	} else if string(viB.Value) != "from A" {
		t.Errorf("B: key-a = %q, want %q", viB.Value, "from A")
	}
}

func TestIntegration_OfflineDeviceCatchesUp(t *testing.T) {
	env := setupTwoDevices(t)
	ctx := context.Background()

	// A sets 10 keys while B is offline
	for i := 0; i < 10; i++ {
		env.localLogA.AppendLocal(Entry{
			Type:  Set,
			Key:   "batch/" + string(rune('a'+i)),
			Value: []byte("value"),
		})
	}
	env.uploaderA.Upload(ctx)

	// B comes online and polls
	env.pollerB.pollOnce(ctx)

	snapB, _ := env.localLogB.Snapshot()
	if snapB.Len() != 10 {
		t.Errorf("B: Len() = %d, want 10", snapB.Len())
	}

	keys := snapB.KeysWithPrefix("batch/")
	if len(keys) != 10 {
		t.Errorf("B: batch/ keys = %d, want 10", len(keys))
	}
}

func TestIntegration_LWWConflictResolution(t *testing.T) {
	env := setupTwoDevices(t)
	ctx := context.Background()

	// Both devices set the same key — A at ts=100, B at ts=200
	env.localLogA.Append(Entry{
		Type: Set, Key: "config", Value: []byte("A-value"),
		Device: env.devIDA, Timestamp: 100, Seq: 1,
	})
	env.uploaderA.Upload(ctx)

	env.localLogB.Append(Entry{
		Type: Set, Key: "config", Value: []byte("B-value"),
		Device: env.devIDB, Timestamp: 200, Seq: 1,
	})
	env.uploaderB.Upload(ctx)

	// Both poll
	env.pollerA.pollOnce(ctx)
	env.pollerB.pollOnce(ctx)

	// Both should converge to B's value (higher timestamp wins)
	snapA, _ := env.localLogA.Snapshot()
	snapB, _ := env.localLogB.Snapshot()

	viA, _ := snapA.Lookup("config")
	viB, _ := snapB.Lookup("config")

	if string(viA.Value) != "B-value" {
		t.Errorf("A: config = %q, want %q (LWW: B has higher ts)", viA.Value, "B-value")
	}
	if string(viB.Value) != "B-value" {
		t.Errorf("B: config = %q, want %q", viB.Value, "B-value")
	}
}

func TestIntegration_MultipleRounds(t *testing.T) {
	env := setupTwoDevices(t)
	ctx := context.Background()

	// Use explicit timestamps to guarantee LWW ordering across rounds.
	// AppendLocal uses time.Now().Unix() (seconds) — rounds within the
	// same second would tiebreak on device ID, not temporal order.
	base := time.Now().Unix()

	// Round 1: A sets at ts=base
	env.localLogA.Append(Entry{
		Type: Set, Key: "counter", Value: []byte("1"),
		Device: env.devIDA, Timestamp: base, Seq: 1,
	})
	env.uploaderA.Upload(ctx)
	env.pollerB.pollOnce(ctx)

	vi, _ := env.localLogB.Lookup("counter")
	if string(vi.Value) != "1" {
		t.Fatalf("round 1: B got %q, want %q", vi.Value, "1")
	}

	// Round 2: B updates at ts=base+10
	env.localLogB.Append(Entry{
		Type: Set, Key: "counter", Value: []byte("2"),
		Device: env.devIDB, Timestamp: base + 10, Seq: 1,
	})
	env.uploaderB.Upload(ctx)
	env.pollerA.pollOnce(ctx)

	vi, _ = env.localLogA.Lookup("counter")
	if string(vi.Value) != "2" {
		t.Fatalf("round 2: A got %q, want %q", vi.Value, "2")
	}

	// Round 3: A updates at ts=base+20
	env.localLogA.Append(Entry{
		Type: Set, Key: "counter", Value: []byte("3"),
		Device: env.devIDA, Timestamp: base + 20, Seq: 2,
	})
	env.uploaderA.Upload(ctx)
	env.pollerB.pollOnce(ctx)

	vi, _ = env.localLogB.Lookup("counter")
	if string(vi.Value) != "3" {
		t.Fatalf("round 3: B got %q, want %q", vi.Value, "3")
	}
}

func TestIntegration_EmptyValue(t *testing.T) {
	env := setupTwoDevices(t)
	ctx := context.Background()

	// A sets a key with empty value
	env.localLogA.AppendLocal(Entry{
		Type: Set, Key: "empty", Value: []byte{},
	})
	env.uploaderA.Upload(ctx)

	env.pollerB.pollOnce(ctx)

	vi, ok := env.localLogB.Lookup("empty")
	if !ok {
		t.Fatal("B: empty key not found")
	}
	if len(vi.Value) != 0 {
		t.Errorf("B: value len = %d, want 0", len(vi.Value))
	}
}

func TestIntegration_SnapshotEncryption(t *testing.T) {
	env := setupTwoDevices(t)
	ctx := context.Background()

	env.localLogA.AppendLocal(Entry{
		Type: Set, Key: "secret", Value: []byte("sensitive-data"),
	})
	env.uploaderA.Upload(ctx)

	// Read raw S3 data — should be encrypted (not readable as JSON)
	latestKey := snapshotLatestKey(env.nsID, env.devIDA)
	rc, err := env.backend.Get(ctx, latestKey)
	if err != nil {
		t.Fatalf("reading snapshot from S3: %v", err)
	}
	defer rc.Close()

	// The raw data should NOT contain the plaintext value
	buf := make([]byte, 4096)
	n, _ := rc.Read(buf)
	raw := string(buf[:n])
	if len(raw) == 0 {
		t.Fatal("snapshot is empty in S3")
	}
	if contains(raw, "sensitive-data") {
		t.Error("plaintext value found in encrypted snapshot — encryption broken")
	}

	// But after decryption via poller, B should see it
	env.pollerB.pollOnce(ctx)
	vi, ok := env.localLogB.Lookup("secret")
	if !ok {
		t.Fatal("B: secret not found")
	}
	if string(vi.Value) != "sensitive-data" {
		t.Errorf("B: got %q, want %q", vi.Value, "sensitive-data")
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
