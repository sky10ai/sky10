package opslog

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
	skykey "github.com/sky10/sky10/pkg/key"
)

// testClock returns a clock that advances by 1 second on every call,
// ensuring each entry gets a distinct timestamp in tests.
func testClock() func() time.Time {
	var tick int64
	return func() time.Time {
		n := atomic.AddInt64(&tick, 1)
		return time.Unix(1700000000+n, 0)
	}
}

func newTestLog(t *testing.T) (*OpsLog, *s3adapter.MemoryBackend) {
	t.Helper()
	backend := s3adapter.NewMemory()
	encKey, err := skykey.GenerateSymmetricKey()
	if err != nil {
		t.Fatalf("GenerateSymmetricKey: %v", err)
	}
	log := New(backend, encKey, "dev-a", "test/1.0")
	log.now = testClock()
	return log, backend
}

func TestAppendAndReadSince(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log, _ := newTestLog(t)

	entries := []Entry{
		{Type: Put, Path: "a.md", Chunks: []string{"c1"}, Size: 10, Checksum: "h1"},
		{Type: Put, Path: "b.md", Chunks: []string{"c2"}, Size: 20, Checksum: "h2"},
		{Type: Delete, Path: "a.md"},
	}

	for i := range entries {
		if err := log.Append(ctx, &entries[i]); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}

	// Read all
	all, err := log.ReadSince(ctx, 0)
	if err != nil {
		t.Fatalf("ReadSince(0): %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("got %d entries, want 3", len(all))
	}

	// Verify device and seq are set
	for i, e := range all {
		if e.Device != "dev-a" {
			t.Errorf("entry %d: device = %q, want %q", i, e.Device, "dev-a")
		}
		if e.Seq == 0 {
			t.Errorf("entry %d: seq = 0", i)
		}
		if e.Timestamp == 0 {
			t.Errorf("entry %d: timestamp = 0", i)
		}
	}

	// Read since first entry's timestamp (should exclude it)
	filtered, err := log.ReadSince(ctx, all[0].Timestamp)
	if err != nil {
		t.Fatalf("ReadSince: %v", err)
	}
	// Entries 2 and 3 have the same or later timestamp
	if len(filtered) < 1 {
		t.Errorf("expected at least 1 filtered entry, got %d", len(filtered))
	}
}

func TestAppendSetsFields(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log, _ := newTestLog(t)

	e := Entry{Type: Put, Path: "x.md", Checksum: "h1"}
	if err := log.Append(ctx, &e); err != nil {
		t.Fatal(err)
	}

	if e.Device != "dev-a" {
		t.Errorf("device = %q, want %q", e.Device, "dev-a")
	}
	if e.Client != "test/1.0" {
		t.Errorf("client = %q, want %q", e.Client, "test/1.0")
	}
	if e.Seq != 1 {
		t.Errorf("seq = %d, want 1", e.Seq)
	}
	if e.Timestamp == 0 {
		t.Error("timestamp not set")
	}
}

func TestSnapshot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log, _ := newTestLog(t)

	// Append some entries
	for _, e := range []Entry{
		{Type: Put, Path: "a.md", Chunks: []string{"c1"}, Size: 10, Checksum: "h1", Namespace: "ns"},
		{Type: Put, Path: "b.md", Chunks: []string{"c2"}, Size: 20, Checksum: "h2", Namespace: "ns"},
		{Type: Delete, Path: "a.md"},
	} {
		e := e
		if err := log.Append(ctx, &e); err != nil {
			t.Fatal(err)
		}
	}

	snap, err := log.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	// a.md deleted, only b.md should remain
	if snap.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", snap.Len())
	}

	fi, ok := snap.Lookup("b.md")
	if !ok {
		t.Fatal("b.md not in snapshot")
	}
	if fi.Checksum != "h2" {
		t.Errorf("b.md checksum = %q, want %q", fi.Checksum, "h2")
	}
	if fi.Size != 20 {
		t.Errorf("b.md size = %d, want 20", fi.Size)
	}

	_, ok = snap.Lookup("a.md")
	if ok {
		t.Error("a.md should not be in snapshot (was deleted)")
	}

	paths := snap.Paths()
	if len(paths) != 1 || paths[0] != "b.md" {
		t.Errorf("Paths() = %v, want [b.md]", paths)
	}
}

func TestSnapshotCaching(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log, _ := newTestLog(t)

	e := Entry{Type: Put, Path: "x.md", Checksum: "h1"}
	if err := log.Append(ctx, &e); err != nil {
		t.Fatal(err)
	}

	snap1, err := log.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	snap2, err := log.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Same pointer — cache hit
	if snap1 != snap2 {
		t.Error("expected cached snapshot, got different pointer")
	}

	// Append invalidates cache
	e2 := Entry{Type: Put, Path: "y.md", Checksum: "h2"}
	if err := log.Append(ctx, &e2); err != nil {
		t.Fatal(err)
	}

	snap3, err := log.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snap3 == snap1 {
		t.Error("expected new snapshot after Append, got cached")
	}
	if snap3.Len() != 2 {
		t.Errorf("Len() = %d, want 2", snap3.Len())
	}
}

func TestSnapshotImmutability(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log, _ := newTestLog(t)

	e := Entry{Type: Put, Path: "x.md", Checksum: "h1"}
	if err := log.Append(ctx, &e); err != nil {
		t.Fatal(err)
	}

	snap, err := log.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Mutating Files() copy should not affect Lookup
	files := snap.Files()
	delete(files, "x.md")

	_, ok := snap.Lookup("x.md")
	if !ok {
		t.Error("Lookup(x.md) returned false after mutating Files() copy")
	}
}

func TestCompact(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log, backend := newTestLog(t)

	// Write several entries
	for _, path := range []string{"a.md", "b.md", "c.md"} {
		e := Entry{Type: Put, Path: path, Chunks: []string{"ch"}, Size: 10, Checksum: "h-" + path}
		if err := log.Append(ctx, &e); err != nil {
			t.Fatal(err)
		}
	}

	// Verify ops exist
	opKeys, _ := backend.List(ctx, "ops/")
	if len(opKeys) != 3 {
		t.Fatalf("expected 3 ops before compact, got %d", len(opKeys))
	}

	result, err := log.Compact(ctx, 2)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	if result.OpsCompacted != 3 {
		t.Errorf("OpsCompacted = %d, want 3", result.OpsCompacted)
	}
	if result.OpsDeleted != 3 {
		t.Errorf("OpsDeleted = %d, want 3", result.OpsDeleted)
	}
	if result.SnapshotsKept != 1 {
		t.Errorf("SnapshotsKept = %d, want 1", result.SnapshotsKept)
	}

	// Ops should be gone
	opKeys, _ = backend.List(ctx, "ops/")
	if len(opKeys) != 0 {
		t.Errorf("expected 0 ops after compact, got %d", len(opKeys))
	}

	// Snapshot should still work (from stored snapshot, no ops)
	snap, err := log.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot after compact: %v", err)
	}
	if snap.Len() != 3 {
		t.Errorf("Len() = %d after compact, want 3", snap.Len())
	}
}

func TestCompactThenAppend(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log, _ := newTestLog(t)

	// Initial entries
	e1 := Entry{Type: Put, Path: "a.md", Checksum: "h1", Chunks: []string{"c1"}}
	if err := log.Append(ctx, &e1); err != nil {
		t.Fatal(err)
	}

	if _, err := log.Compact(ctx, 2); err != nil {
		t.Fatal(err)
	}

	// New entry after compact
	e2 := Entry{Type: Put, Path: "b.md", Checksum: "h2", Chunks: []string{"c2"}}
	if err := log.Append(ctx, &e2); err != nil {
		t.Fatal(err)
	}

	snap, err := log.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Both files should be present: a.md from snapshot, b.md from new op
	if snap.Len() != 2 {
		t.Errorf("Len() = %d, want 2", snap.Len())
	}
	if _, ok := snap.Lookup("a.md"); !ok {
		t.Error("a.md missing (should be in snapshot)")
	}
	if _, ok := snap.Lookup("b.md"); !ok {
		t.Error("b.md missing (should be from new op)")
	}
}

func TestMultiDeviceReplay(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	encKey, _ := skykey.GenerateSymmetricKey()

	clock := testClock()
	logA := New(backend, encKey, "dev-a", "test/1.0")
	logA.now = clock
	logB := New(backend, encKey, "dev-b", "test/1.0")
	logB.now = clock

	// Device A writes
	e1 := Entry{Type: Put, Path: "a.md", Checksum: "h1"}
	if err := logA.Append(ctx, &e1); err != nil {
		t.Fatal(err)
	}

	// Device B writes
	e2 := Entry{Type: Put, Path: "b.md", Checksum: "h2"}
	if err := logB.Append(ctx, &e2); err != nil {
		t.Fatal(err)
	}

	// Both should see the same snapshot
	snapA, err := logA.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	snapB, err := logB.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if snapA.Len() != 2 {
		t.Errorf("device A: Len() = %d, want 2", snapA.Len())
	}
	if snapB.Len() != 2 {
		t.Errorf("device B: Len() = %d, want 2", snapB.Len())
	}
}

func TestEntrySorting(t *testing.T) {
	t.Parallel()

	entries := []Entry{
		{Timestamp: 3, Device: "a", Seq: 1},
		{Timestamp: 1, Device: "b", Seq: 1},
		{Timestamp: 2, Device: "a", Seq: 1},
		{Timestamp: 2, Device: "a", Seq: 2},
		{Timestamp: 2, Device: "b", Seq: 1},
	}
	sortEntries(entries)

	want := []struct {
		ts  int64
		dev string
		seq int
	}{
		{1, "b", 1},
		{2, "a", 1},
		{2, "a", 2},
		{2, "b", 1},
		{3, "a", 1},
	}

	for i, w := range want {
		if entries[i].Timestamp != w.ts || entries[i].Device != w.dev || entries[i].Seq != w.seq {
			t.Errorf("entry %d: got (%d, %s, %d), want (%d, %s, %d)",
				i, entries[i].Timestamp, entries[i].Device, entries[i].Seq,
				w.ts, w.dev, w.seq)
		}
	}
}

func TestEntryKeyFormat(t *testing.T) {
	t.Parallel()

	e := Entry{Timestamp: 1707900000, Device: "abc123", Seq: 1}
	got := e.entryKey()
	want := "ops/1707900000-abc123-0001.enc"
	if got != want {
		t.Errorf("entryKey() = %q, want %q", got, want)
	}
}

func TestParseTimestamps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		fn   func(string) int64
		key  string
		want int64
	}{
		{"entry", parseEntryTimestamp, "ops/1707900000-abc123-0001.enc", 1707900000},
		{"entry no prefix", parseEntryTimestamp, "1707900000-abc123-0001.enc", 1707900000},
		{"snapshot", parseSnapshotTimestamp, "manifests/snapshot-1707900000.enc", 1707900000},
		{"empty", parseEntryTimestamp, "", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.fn(tt.key)
			if got != tt.want {
				t.Errorf("%s(%q) = %d, want %d", tt.name, tt.key, got, tt.want)
			}
		})
	}
}

func TestEmptySnapshot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log, _ := newTestLog(t)

	snap, err := log.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Len() != 0 {
		t.Errorf("Len() = %d, want 0", snap.Len())
	}
	if len(snap.Paths()) != 0 {
		t.Errorf("Paths() = %v, want empty", snap.Paths())
	}
}

func TestInvalidateCache(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log, _ := newTestLog(t)

	e := Entry{Type: Put, Path: "x.md", Checksum: "h1"}
	if err := log.Append(ctx, &e); err != nil {
		t.Fatal(err)
	}

	snap1, _ := log.Snapshot(ctx)
	log.InvalidateCache()
	snap2, _ := log.Snapshot(ctx)

	if snap1 == snap2 {
		t.Error("expected different snapshots after InvalidateCache")
	}
}

func TestDeleteNonexistent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log, _ := newTestLog(t)

	// Delete a file that was never added
	e := Entry{Type: Delete, Path: "ghost.md"}
	if err := log.Append(ctx, &e); err != nil {
		t.Fatal(err)
	}

	snap, err := log.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Len() != 0 {
		t.Errorf("Len() = %d, want 0", snap.Len())
	}
}

// --- CRDT tests ---
// These test the LWW-Register-Map properties directly via buildSnapshot,
// proving that conflict resolution is order-independent.

func TestCRDTOrderIndependence(t *testing.T) {
	t.Parallel()

	// a.md: two puts, dev-b (t=200) should win over dev-a (t=100)
	// b.md: put then delete, delete (t=300) should win
	entries := []Entry{
		{Type: Put, Path: "a.md", Checksum: "v1", Timestamp: 100, Device: "dev-a", Seq: 1},
		{Type: Put, Path: "a.md", Checksum: "v2", Timestamp: 200, Device: "dev-b", Seq: 1},
		{Type: Put, Path: "b.md", Checksum: "v3", Timestamp: 150, Device: "dev-a", Seq: 2},
		{Type: Delete, Path: "b.md", Timestamp: 300, Device: "dev-c", Seq: 1},
	}

	// All 24 permutations must produce the same snapshot.
	for i, perm := range permutations(entries) {
		snap := buildSnapshot(nil, perm)
		if snap.Len() != 1 {
			t.Fatalf("perm %d: Len() = %d, want 1", i, snap.Len())
		}
		fi, ok := snap.Lookup("a.md")
		if !ok {
			t.Fatalf("perm %d: a.md missing", i)
		}
		if fi.Checksum != "v2" {
			t.Errorf("perm %d: a.md checksum = %q, want v2", i, fi.Checksum)
		}
		if _, ok := snap.Lookup("b.md"); ok {
			t.Errorf("perm %d: b.md should be deleted", i)
		}
	}
}

func TestCRDTDeleteBeatsOlderPut(t *testing.T) {
	t.Parallel()

	del := Entry{Type: Delete, Path: "x.md", Timestamp: 200, Device: "dev-a", Seq: 1}
	put := Entry{Type: Put, Path: "x.md", Checksum: "v1", Timestamp: 100, Device: "dev-b", Seq: 1}

	// Delete processed first, then older put — put should be rejected.
	snap := buildSnapshot(nil, []Entry{del, put})
	if _, ok := snap.Lookup("x.md"); ok {
		t.Error("x.md should be deleted (delete t=200 beats put t=100)")
	}

	// Reverse order — same result.
	snap2 := buildSnapshot(nil, []Entry{put, del})
	if _, ok := snap2.Lookup("x.md"); ok {
		t.Error("x.md should be deleted regardless of order")
	}
}

func TestCRDTPutBeatsOlderDelete(t *testing.T) {
	t.Parallel()

	put := Entry{Type: Put, Path: "x.md", Checksum: "v1", Timestamp: 200, Device: "dev-a", Seq: 1}
	del := Entry{Type: Delete, Path: "x.md", Timestamp: 100, Device: "dev-b", Seq: 1}

	// Put first, then older delete — delete should be rejected.
	snap := buildSnapshot(nil, []Entry{put, del})
	fi, ok := snap.Lookup("x.md")
	if !ok {
		t.Fatal("x.md should exist (put t=200 beats delete t=100)")
	}
	if fi.Checksum != "v1" {
		t.Errorf("checksum = %q, want v1", fi.Checksum)
	}

	// Reverse order — same result.
	snap2 := buildSnapshot(nil, []Entry{del, put})
	fi2, ok := snap2.Lookup("x.md")
	if !ok {
		t.Fatal("x.md should exist regardless of order")
	}
	if fi2.Checksum != "v1" {
		t.Errorf("checksum = %q, want v1", fi2.Checksum)
	}
}

func TestCRDTCausalSuccessorBeatsHigherTimestamp(t *testing.T) {
	t.Parallel()

	ancestor := Entry{Type: Put, Path: "x.md", Checksum: "base", Timestamp: 200, Device: "dev-b", Seq: 1}
	successor := Entry{Type: Put, Path: "x.md", Checksum: "next", PrevChecksum: "base", Timestamp: 100, Device: "dev-a", Seq: 1}

	for i, order := range [][]Entry{{ancestor, successor}, {successor, ancestor}} {
		snap := buildSnapshot(nil, order)
		fi, ok := snap.Lookup("x.md")
		if !ok {
			t.Fatalf("order %d: x.md missing", i)
		}
		if fi.Checksum != "next" {
			t.Errorf("order %d: checksum = %q, want next", i, fi.Checksum)
		}
		if fi.PrevChecksum != "base" {
			t.Errorf("order %d: prev_checksum = %q, want base", i, fi.PrevChecksum)
		}
	}
}

func TestCRDTCausalDeleteBeatsHigherTimestamp(t *testing.T) {
	t.Parallel()

	put := Entry{Type: Put, Path: "x.md", Checksum: "base", Timestamp: 200, Device: "dev-b", Seq: 1}
	del := Entry{Type: Delete, Path: "x.md", PrevChecksum: "base", Timestamp: 100, Device: "dev-a", Seq: 1}

	for i, order := range [][]Entry{{put, del}, {del, put}} {
		snap := buildSnapshot(nil, order)
		if _, ok := snap.Lookup("x.md"); ok {
			t.Fatalf("order %d: x.md should be deleted", i)
		}
		tomb, ok := snap.Tombstones()["x.md"]
		if !ok {
			t.Fatalf("order %d: x.md tombstone missing", i)
		}
		if tomb.PrevChecksum != "base" {
			t.Errorf("order %d: tomb prev_checksum = %q, want base", i, tomb.PrevChecksum)
		}
	}
}

func TestCRDTSameTimestampTiebreak(t *testing.T) {
	t.Parallel()

	// Same timestamp — higher device ID wins (lexicographic tiebreak).
	a := Entry{Type: Put, Path: "x.md", Checksum: "from-a", Timestamp: 100, Device: "dev-a", Seq: 1}
	b := Entry{Type: Put, Path: "x.md", Checksum: "from-b", Timestamp: 100, Device: "dev-b", Seq: 1}

	for i, order := range [][]Entry{{a, b}, {b, a}} {
		snap := buildSnapshot(nil, order)
		fi, ok := snap.Lookup("x.md")
		if !ok {
			t.Fatalf("order %d: x.md missing", i)
		}
		if fi.Checksum != "from-b" {
			t.Errorf("order %d: checksum = %q, want from-b (dev-b > dev-a)", i, fi.Checksum)
		}
	}
}

func TestCRDTClockSurvivesCompaction(t *testing.T) {
	t.Parallel()

	// Base snapshot has x.md from dev-b at t=200.
	base := buildSnapshot(nil, []Entry{
		{Type: Put, Path: "x.md", Checksum: "base", Timestamp: 200, Device: "dev-b", Seq: 1},
	})

	// Same timestamp from dev-a should NOT win (dev-a < dev-b).
	snap := buildSnapshot(base, []Entry{
		{Type: Put, Path: "x.md", Checksum: "stale", Timestamp: 200, Device: "dev-a", Seq: 1},
	})
	fi, _ := snap.Lookup("x.md")
	if fi.Checksum != "base" {
		t.Errorf("checksum = %q, want 'base' (base clock should win)", fi.Checksum)
	}

	// Higher timestamp should win.
	snap2 := buildSnapshot(base, []Entry{
		{Type: Put, Path: "x.md", Checksum: "newer", Timestamp: 300, Device: "dev-a", Seq: 1},
	})
	fi2, _ := snap2.Lookup("x.md")
	if fi2.Checksum != "newer" {
		t.Errorf("checksum = %q, want 'newer' (higher timestamp wins)", fi2.Checksum)
	}
}

func TestCRDTDeleteClockSurvivesCompactionBase(t *testing.T) {
	t.Parallel()

	base := buildSnapshot(nil, []Entry{
		{Type: Delete, Path: "x.md", Timestamp: 200, Device: "dev-b", Seq: 1},
	})

	snap := buildSnapshot(base, []Entry{
		{Type: Put, Path: "x.md", Checksum: "stale", Timestamp: 100, Device: "dev-a", Seq: 1},
	})
	if _, ok := snap.Lookup("x.md"); ok {
		t.Fatal("x.md should remain deleted when base tombstone beats incoming put")
	}
	if !snap.DeletedFiles()["x.md"] {
		t.Fatal("x.md tombstone missing after replay")
	}
}

func TestCRDTDeleteDirClockSurvivesCompactionBase(t *testing.T) {
	t.Parallel()

	base := buildSnapshot(nil, []Entry{
		{Type: DeleteDir, Path: "dir", Timestamp: 200, Device: "dev-b", Seq: 1},
	})

	snap := buildSnapshot(base, []Entry{
		{Type: Put, Path: "dir/stale.txt", Checksum: "stale", Timestamp: 100, Device: "dev-a", Seq: 1},
		{Type: CreateDir, Path: "dir", Timestamp: 150, Device: "dev-a", Seq: 2},
	})
	if _, ok := snap.Lookup("dir/stale.txt"); ok {
		t.Fatal("dir/stale.txt should remain deleted when base delete_dir beats incoming put")
	}
	if _, ok := snap.Dirs()["dir"]; ok {
		t.Fatal("dir should remain deleted when base delete_dir beats incoming create_dir")
	}
	if !snap.DeletedDirs()["dir"] {
		t.Fatal("dir tombstone missing after replay")
	}
}

// permutations returns all orderings of the given entries.
func permutations(entries []Entry) [][]Entry {
	if len(entries) <= 1 {
		return [][]Entry{append([]Entry{}, entries...)}
	}
	var result [][]Entry
	for i, e := range entries {
		rest := make([]Entry, 0, len(entries)-1)
		rest = append(rest, entries[:i]...)
		rest = append(rest, entries[i+1:]...)
		for _, p := range permutations(rest) {
			result = append(result, append([]Entry{e}, p...))
		}
	}
	return result
}

func TestCRDTDeleteDir(t *testing.T) {
	t.Parallel()

	entries := []Entry{
		{Type: Put, Path: "dir/a.txt", Checksum: "a1", Timestamp: 100, Device: "dev-a", Seq: 1},
		{Type: Put, Path: "dir/b.txt", Checksum: "b1", Timestamp: 100, Device: "dev-a", Seq: 2},
		{Type: Put, Path: "other.txt", Checksum: "o1", Timestamp: 100, Device: "dev-a", Seq: 3},
		{Type: DeleteDir, Path: "dir", Timestamp: 200, Device: "dev-b", Seq: 1},
	}

	// All files under dir/ should be gone, other.txt should remain
	snap := buildSnapshot(nil, entries)
	if snap.Len() != 1 {
		t.Fatalf("Len() = %d, want 1 (only other.txt)", snap.Len())
	}
	if _, ok := snap.Lookup("dir/a.txt"); ok {
		t.Error("dir/a.txt should be deleted by delete_dir")
	}
	if _, ok := snap.Lookup("dir/b.txt"); ok {
		t.Error("dir/b.txt should be deleted by delete_dir")
	}
	if _, ok := snap.Lookup("other.txt"); !ok {
		t.Error("other.txt should survive delete_dir")
	}
}

func TestCRDTDeleteDirLaterPutWins(t *testing.T) {
	t.Parallel()

	// DeleteDir at t=200, but a put at t=300 should survive
	entries := []Entry{
		{Type: Put, Path: "dir/old.txt", Checksum: "old", Timestamp: 100, Device: "dev-a", Seq: 1},
		{Type: DeleteDir, Path: "dir", Timestamp: 200, Device: "dev-b", Seq: 1},
		{Type: Put, Path: "dir/new.txt", Checksum: "new", Timestamp: 300, Device: "dev-c", Seq: 1},
	}

	snap := buildSnapshot(nil, entries)
	if _, ok := snap.Lookup("dir/old.txt"); ok {
		t.Error("dir/old.txt should be deleted (t=100 < t=200)")
	}
	if _, ok := snap.Lookup("dir/new.txt"); !ok {
		t.Error("dir/new.txt should survive (t=300 > t=200 delete_dir)")
	}
}

func TestCRDTDeleteDirOrderIndependent(t *testing.T) {
	t.Parallel()

	entries := []Entry{
		{Type: Put, Path: "d/x.txt", Checksum: "x", Timestamp: 100, Device: "dev-a", Seq: 1},
		{Type: Put, Path: "d/y.txt", Checksum: "y", Timestamp: 150, Device: "dev-a", Seq: 2},
		{Type: DeleteDir, Path: "d", Timestamp: 200, Device: "dev-b", Seq: 1},
	}

	// All 6 permutations should produce the same result (0 files)
	for i, perm := range permutations(entries) {
		snap := buildSnapshot(nil, perm)
		if snap.Len() != 0 {
			t.Errorf("perm %d: Len() = %d, want 0", i, snap.Len())
		}
	}
}

func TestCRDTCreateDir(t *testing.T) {
	t.Parallel()

	entries := []Entry{
		{Type: CreateDir, Path: "empty", Namespace: "ns", Timestamp: 100, Device: "dev-a", Seq: 1},
	}
	snap := buildSnapshot(nil, entries)
	if len(snap.Dirs()) != 1 {
		t.Fatalf("Dirs() len = %d, want 1", len(snap.Dirs()))
	}
	if _, ok := snap.Dirs()["empty"]; !ok {
		t.Error("empty dir should be in snapshot")
	}
}

func TestCRDTDeleteDirRemovesCreateDir(t *testing.T) {
	t.Parallel()

	entries := []Entry{
		{Type: CreateDir, Path: "foo", Timestamp: 100, Device: "dev-a", Seq: 1},
		{Type: CreateDir, Path: "foo/bar", Timestamp: 100, Device: "dev-a", Seq: 2},
		{Type: DeleteDir, Path: "foo", Timestamp: 200, Device: "dev-b", Seq: 1},
	}

	// All permutations should produce 0 dirs
	for i, perm := range permutations(entries) {
		snap := buildSnapshot(nil, perm)
		if len(snap.Dirs()) != 0 {
			t.Errorf("perm %d: Dirs() len = %d, want 0", i, len(snap.Dirs()))
		}
	}
}

func TestCRDTCreateDirAfterDeleteDir(t *testing.T) {
	t.Parallel()

	// DeleteDir at t=200, then CreateDir at t=300 — dir should exist
	entries := []Entry{
		{Type: DeleteDir, Path: "d", Timestamp: 200, Device: "dev-a", Seq: 1},
		{Type: CreateDir, Path: "d", Timestamp: 300, Device: "dev-b", Seq: 1},
	}

	for i, perm := range permutations(entries[:2]) {
		snap := buildSnapshot(nil, perm)
		if len(snap.Dirs()) != 1 {
			t.Errorf("perm %d: Dirs() len = %d, want 1", i, len(snap.Dirs()))
		}
	}
}

func TestLastWriterWins(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	encKey, _ := skykey.GenerateSymmetricKey()

	clock := testClock()
	logA := New(backend, encKey, "dev-a", "test/1.0")
	logA.now = clock
	logB := New(backend, encKey, "dev-b", "test/1.0")
	logB.now = clock

	// Both write the same path
	e1 := Entry{Type: Put, Path: "conflict.md", Checksum: "version-a", Size: 10}
	if err := logA.Append(ctx, &e1); err != nil {
		t.Fatal(err)
	}
	e2 := Entry{Type: Put, Path: "conflict.md", Checksum: "version-b", Size: 20}
	if err := logB.Append(ctx, &e2); err != nil {
		t.Fatal(err)
	}

	// Snapshot should have the later write (device B, higher timestamp or seq)
	snap, _ := logA.Snapshot(ctx)
	fi, ok := snap.Lookup("conflict.md")
	if !ok {
		t.Fatal("conflict.md not in snapshot")
	}
	// Last writer wins — B wrote second
	if fi.Checksum != "version-b" {
		t.Errorf("checksum = %q, want version-b (last writer wins)", fi.Checksum)
	}
}
