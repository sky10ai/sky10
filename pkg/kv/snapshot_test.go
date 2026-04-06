package kv

import (
	"testing"
	"time"
)

func TestBuildSnapshotBasic(t *testing.T) {
	t.Parallel()
	entries := []Entry{
		{Type: Set, Key: "foo", Value: []byte("bar"), Device: "d1", Timestamp: 100, Seq: 1},
		{Type: Set, Key: "baz", Value: []byte("qux"), Device: "d1", Timestamp: 101, Seq: 2},
	}
	snap := buildSnapshot(nil, entries)

	if snap.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", snap.Len())
	}
	vi, ok := snap.Lookup("foo")
	if !ok {
		t.Fatal("foo not found")
	}
	if string(vi.Value) != "bar" {
		t.Errorf("foo = %q, want %q", vi.Value, "bar")
	}
}

func TestBuildSnapshotLWW(t *testing.T) {
	t.Parallel()
	entries := []Entry{
		{Type: Set, Key: "k", Value: []byte("old"), Device: "d1", Timestamp: 100, Seq: 1},
		{Type: Set, Key: "k", Value: []byte("new"), Device: "d2", Timestamp: 200, Seq: 1},
	}
	snap := buildSnapshot(nil, entries)

	vi, ok := snap.Lookup("k")
	if !ok {
		t.Fatal("k not found")
	}
	if string(vi.Value) != "new" {
		t.Errorf("k = %q, want %q (LWW should pick higher timestamp)", vi.Value, "new")
	}
}

func TestBuildSnapshotLWWReverse(t *testing.T) {
	t.Parallel()
	// Entries arrive in reverse order — LWW must still pick higher timestamp
	entries := []Entry{
		{Type: Set, Key: "k", Value: []byte("new"), Device: "d2", Timestamp: 200, Seq: 1},
		{Type: Set, Key: "k", Value: []byte("old"), Device: "d1", Timestamp: 100, Seq: 1},
	}
	snap := buildSnapshot(nil, entries)

	vi, _ := snap.Lookup("k")
	if string(vi.Value) != "new" {
		t.Errorf("k = %q, want %q", vi.Value, "new")
	}
}

func TestBuildSnapshotDelete(t *testing.T) {
	t.Parallel()
	entries := []Entry{
		{Type: Set, Key: "k", Value: []byte("v"), Device: "d1", Timestamp: 100, Seq: 1},
		{Type: Delete, Key: "k", Device: "d1", Timestamp: 200, Seq: 2},
	}
	snap := buildSnapshot(nil, entries)

	if snap.Len() != 0 {
		t.Fatalf("Len() = %d, want 0 after delete", snap.Len())
	}
	if _, ok := snap.Lookup("k"); ok {
		t.Error("k should not be found after delete")
	}
	if !snap.DeletedKeys()["k"] {
		t.Error("k should be in DeletedKeys()")
	}
}

func TestBuildSnapshotDeleteThenSet(t *testing.T) {
	t.Parallel()
	entries := []Entry{
		{Type: Set, Key: "k", Value: []byte("v1"), Device: "d1", Timestamp: 100, Seq: 1},
		{Type: Delete, Key: "k", Device: "d1", Timestamp: 200, Seq: 2},
		{Type: Set, Key: "k", Value: []byte("v2"), Device: "d1", Timestamp: 300, Seq: 3},
	}
	snap := buildSnapshot(nil, entries)

	vi, ok := snap.Lookup("k")
	if !ok {
		t.Fatal("k not found after re-set")
	}
	if string(vi.Value) != "v2" {
		t.Errorf("k = %q, want %q", vi.Value, "v2")
	}
	if snap.DeletedKeys()["k"] {
		t.Error("k should not be in DeletedKeys() after re-set")
	}
}

func TestBuildSnapshotWithBase(t *testing.T) {
	t.Parallel()
	base := buildSnapshot(nil, []Entry{
		{Type: Set, Key: "existing", Value: []byte("val"), Device: "d1", Timestamp: 100, Seq: 1},
	})
	snap := buildSnapshot(base, []Entry{
		{Type: Set, Key: "new", Value: []byte("val2"), Device: "d1", Timestamp: 200, Seq: 2},
	})

	if snap.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", snap.Len())
	}
	if _, ok := snap.Lookup("existing"); !ok {
		t.Error("existing key should carry over from base")
	}
	if _, ok := snap.Lookup("new"); !ok {
		t.Error("new key should be present")
	}
}

func TestSnapshotKeysWithPrefix(t *testing.T) {
	t.Parallel()
	entries := []Entry{
		{Type: Set, Key: "app/config", Value: []byte("v"), Device: "d1", Timestamp: 100, Seq: 1},
		{Type: Set, Key: "app/name", Value: []byte("v"), Device: "d1", Timestamp: 101, Seq: 2},
		{Type: Set, Key: "other/key", Value: []byte("v"), Device: "d1", Timestamp: 102, Seq: 3},
	}
	snap := buildSnapshot(nil, entries)

	keys := snap.KeysWithPrefix("app/")
	if len(keys) != 2 {
		t.Fatalf("KeysWithPrefix(app/) = %d keys, want 2", len(keys))
	}
	if keys[0] != "app/config" || keys[1] != "app/name" {
		t.Errorf("keys = %v, want [app/config app/name]", keys)
	}
}

func TestSnapshotEntries(t *testing.T) {
	t.Parallel()
	snap := buildSnapshot(nil, []Entry{
		{Type: Set, Key: "a", Value: []byte("1"), Device: "d1", Timestamp: 100, Seq: 1},
	})
	cp := snap.Entries()
	// Mutating copy should not affect original
	cp["a"] = ValueInfo{Value: []byte("mutated")}
	vi, _ := snap.Lookup("a")
	if string(vi.Value) != "1" {
		t.Error("Entries() copy mutation affected original")
	}
}

func TestMarshalUnmarshalSnapshot(t *testing.T) {
	t.Parallel()
	snap := buildSnapshot(nil, []Entry{
		{Type: Set, Key: "k1", Value: []byte("v1"), Device: "d1", Timestamp: 1000, Seq: 1, Actor: "actor-a", Counter: 1},
		{Type: Set, Key: "k2", Value: []byte("v2"), Device: "d2", Timestamp: 2000, Seq: 1, Actor: "actor-b", Counter: 1},
		{Type: Delete, Key: "gone", Device: "d2", Timestamp: 3000, Seq: 2, Actor: "actor-b", Counter: 2, Context: VersionVector{"actor-b": 1}},
	})

	data, err := MarshalSnapshot(snap)
	if err != nil {
		t.Fatal(err)
	}

	got, err := UnmarshalSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}

	if got.Len() != 2 {
		t.Fatalf("round-trip Len() = %d, want 2", got.Len())
	}
	vi, ok := got.Lookup("k1")
	if !ok {
		t.Fatal("k1 not found after round-trip")
	}
	if string(vi.Value) != "v1" {
		t.Errorf("k1 = %q, want %q", vi.Value, "v1")
	}
	if vi.Device != "d1" {
		t.Errorf("k1 device = %q, want %q", vi.Device, "d1")
	}
	if vi.Modified != time.Unix(1000, 0).UTC() {
		t.Errorf("k1 modified = %v, want %v", vi.Modified, time.Unix(1000, 0).UTC())
	}
	if vi.Actor != "actor-a" || vi.Counter != 1 {
		t.Errorf("k1 causal metadata = %q/%d, want actor-a/1", vi.Actor, vi.Counter)
	}
	if !got.DeletedKeys()["gone"] {
		t.Fatal("gone tombstone missing after round-trip")
	}
	if got.Vector()["actor-b"] != 2 {
		t.Fatalf("vector[actor-b] = %d, want 2", got.Vector()["actor-b"])
	}
}

func TestMarshalNilSnapshot(t *testing.T) {
	t.Parallel()
	data, err := MarshalSnapshot(nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnmarshalSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.Len() != 0 {
		t.Errorf("nil snapshot round-trip Len() = %d, want 0", got.Len())
	}
}

func TestBuildSnapshotOldDeleteIgnored(t *testing.T) {
	t.Parallel()
	// Delete with lower timestamp should not remove a newer set
	entries := []Entry{
		{Type: Set, Key: "k", Value: []byte("v"), Device: "d1", Timestamp: 200, Seq: 1},
		{Type: Delete, Key: "k", Device: "d2", Timestamp: 100, Seq: 1},
	}
	snap := buildSnapshot(nil, entries)
	if snap.Len() != 1 {
		t.Fatalf("old delete should not remove newer set, Len() = %d", snap.Len())
	}
}

func TestBuildSnapshotCausalOrderBeatsWallClock(t *testing.T) {
	t.Parallel()

	entries := []Entry{
		{
			Type:      Set,
			Key:       "k",
			Value:     []byte("from-a"),
			Device:    "dev-a",
			Timestamp: 200,
			Seq:       1,
			Actor:     "actor-a",
			Counter:   1,
		},
		{
			Type:      Set,
			Key:       "k",
			Value:     []byte("from-b"),
			Device:    "dev-b",
			Timestamp: 100,
			Seq:       1,
			Actor:     "actor-b",
			Counter:   1,
			Context:   VersionVector{"actor-a": 1},
		},
	}

	snap := buildSnapshot(nil, entries)
	vi, ok := snap.Lookup("k")
	if !ok {
		t.Fatal("k not found")
	}
	if string(vi.Value) != "from-b" {
		t.Fatalf("k = %q, want from-b", vi.Value)
	}
}

func TestSnapshotDeltaSince(t *testing.T) {
	t.Parallel()

	snap := buildSnapshot(nil, []Entry{
		{
			Type:      Set,
			Key:       "keep",
			Value:     []byte("v1"),
			Device:    "dev-a",
			Timestamp: 100,
			Seq:       1,
			Actor:     "actor-a",
			Counter:   1,
		},
		{
			Type:      Delete,
			Key:       "gone",
			Device:    "dev-b",
			Timestamp: 200,
			Seq:       1,
			Actor:     "actor-b",
			Counter:   1,
		},
	})

	delta := snap.DeltaSince(VersionVector{"actor-a": 1})
	if delta.Len() != 0 {
		t.Fatalf("delta live keys = %d, want 0", delta.Len())
	}
	if !delta.DeletedKeys()["gone"] {
		t.Fatal("delta should retain missing tombstone")
	}

	full := snap.DeltaSince(nil)
	if full.Len() != 1 || !full.DeletedKeys()["gone"] {
		t.Fatalf("full delta should include both live and deleted state")
	}
}
