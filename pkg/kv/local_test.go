package kv

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestLocalLogAppendAndLookup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	log := NewLocalLog(filepath.Join(dir, "kv-ops.jsonl"), "dev1")

	if err := log.AppendLocal(Entry{Type: Set, Key: "foo", Value: []byte("bar")}); err != nil {
		t.Fatal(err)
	}

	vi, ok := log.Lookup("foo")
	if !ok {
		t.Fatal("foo not found")
	}
	if string(vi.Value) != "bar" {
		t.Errorf("foo = %q, want %q", vi.Value, "bar")
	}
	if vi.Device != "dev1" {
		t.Errorf("device = %q, want %q", vi.Device, "dev1")
	}
}

func TestLocalLogAppendSetsSeq(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	log := NewLocalLog(filepath.Join(dir, "kv-ops.jsonl"), "dev1")

	log.AppendLocal(Entry{Type: Set, Key: "a", Value: []byte("1")})
	log.AppendLocal(Entry{Type: Set, Key: "b", Value: []byte("2")})

	snap, err := log.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	vi, _ := snap.Lookup("b")
	if vi.Seq != 2 {
		t.Errorf("seq = %d, want 2", vi.Seq)
	}
}

func TestLocalLogDelete(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	log := NewLocalLog(filepath.Join(dir, "kv-ops.jsonl"), "dev1")

	log.AppendLocal(Entry{Type: Set, Key: "k", Value: []byte("v")})
	log.AppendLocal(Entry{Type: Delete, Key: "k"})

	vi, ok := log.Lookup("k")
	if ok {
		t.Errorf("k should not be found after delete, got %+v", vi)
	}
}

func TestLocalLogAppendRemote(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	log := NewLocalLog(filepath.Join(dir, "kv-ops.jsonl"), "dev1")

	// Append a fully-formed remote entry
	err := log.Append(Entry{
		Type:      Set,
		Key:       "remote-key",
		Value:     []byte("remote-val"),
		Device:    "dev2",
		Timestamp: 5000,
		Seq:       1,
	})
	if err != nil {
		t.Fatal(err)
	}

	vi, ok := log.Lookup("remote-key")
	if !ok {
		t.Fatal("remote-key not found")
	}
	if vi.Device != "dev2" {
		t.Errorf("device = %q, want %q", vi.Device, "dev2")
	}
}

func TestLocalLogSnapshotRebuild(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "kv-ops.jsonl")
	log := NewLocalLog(path, "dev1")

	log.AppendLocal(Entry{Type: Set, Key: "k", Value: []byte("v")})

	// Invalidate cache and rebuild from file
	log.InvalidateCache()

	vi, ok := log.Lookup("k")
	if !ok {
		t.Fatal("k not found after cache invalidation")
	}
	if string(vi.Value) != "v" {
		t.Errorf("k = %q, want %q", vi.Value, "v")
	}
}

func TestLocalLogCompact(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "kv-ops.jsonl")
	log := NewLocalLog(path, "dev1")

	// Write multiple ops for the same key
	log.AppendLocal(Entry{Type: Set, Key: "k", Value: []byte("v1")})
	log.AppendLocal(Entry{Type: Set, Key: "k", Value: []byte("v2")})
	log.AppendLocal(Entry{Type: Set, Key: "k", Value: []byte("v3")})
	log.AppendLocal(Entry{Type: Set, Key: "other", Value: []byte("val")})

	if err := log.Compact(); err != nil {
		t.Fatal(err)
	}

	// After compaction, state should be the same
	vi, ok := log.Lookup("k")
	if !ok {
		t.Fatal("k not found after compact")
	}
	if string(vi.Value) != "v3" {
		t.Errorf("k = %q, want %q", vi.Value, "v3")
	}

	// File should be smaller (2 entries instead of 4)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := 0
	for _, b := range data {
		if b == '\n' {
			lines++
		}
	}
	if lines != 2 {
		t.Errorf("compacted file has %d lines, want 2", lines)
	}
}

func TestLocalLogConcurrentAccess(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	log := NewLocalLog(filepath.Join(dir, "kv-ops.jsonl"), "dev1")

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := "k"
			log.AppendLocal(Entry{Type: Set, Key: key, Value: []byte("v")})
			log.Lookup(key)
		}(i)
	}
	wg.Wait()

	snap, err := log.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	// All writes to same key — should have exactly 1 live entry
	if snap.Len() != 1 {
		t.Errorf("Len() = %d, want 1", snap.Len())
	}
}

func TestLocalLogEmptyFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	log := NewLocalLog(filepath.Join(dir, "kv-ops.jsonl"), "dev1")

	snap, err := log.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snap.Len() != 0 {
		t.Errorf("empty log Len() = %d, want 0", snap.Len())
	}
}

func TestLocalLogDeviceID(t *testing.T) {
	t.Parallel()
	log := NewLocalLog("/tmp/unused", "mydev")
	if log.DeviceID() != "mydev" {
		t.Errorf("DeviceID() = %q, want %q", log.DeviceID(), "mydev")
	}
}

func TestLocalLogPresetTimestamp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	log := NewLocalLog(filepath.Join(dir, "kv-ops.jsonl"), "dev1")

	// AppendLocal should respect pre-set timestamp if > 0
	err := log.AppendLocal(Entry{
		Type:      Set,
		Key:       "k",
		Value:     []byte("v"),
		Timestamp: 9999,
	})
	if err != nil {
		t.Fatal(err)
	}

	vi, ok := log.Lookup("k")
	if !ok {
		t.Fatal("k not found")
	}
	if vi.Modified.Unix() != 9999 {
		t.Errorf("modified = %d, want 9999", vi.Modified.Unix())
	}
}

func TestLocalLogAppendLocalSetsActorCounterAndContext(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	log := NewLocalLogWithActor(filepath.Join(dir, "kv-ops.jsonl"), "dev1", "actor-1")

	if err := log.AppendLocal(Entry{Type: Set, Key: "a", Value: []byte("1")}); err != nil {
		t.Fatal(err)
	}
	if err := log.AppendLocal(Entry{Type: Set, Key: "b", Value: []byte("2")}); err != nil {
		t.Fatal(err)
	}

	snap, err := log.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	vi, ok := snap.Lookup("b")
	if !ok {
		t.Fatal("b not found")
	}
	if vi.Actor != "actor-1" {
		t.Fatalf("actor = %q, want actor-1", vi.Actor)
	}
	if vi.Counter != 2 {
		t.Fatalf("counter = %d, want 2", vi.Counter)
	}
	if vi.Context["actor-1"] != 1 {
		t.Fatalf("context[actor-1] = %d, want 1", vi.Context["actor-1"])
	}
}
