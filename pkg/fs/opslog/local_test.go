package opslog

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalAppendAndSnapshot(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "ops.jsonl")
	log := NewLocalOpsLog(path, "dev-a")

	entries := []Entry{
		{Type: Put, Path: "a.md", Chunks: []string{"c1"}, Size: 10, Checksum: "h1", Device: "dev-a", Timestamp: 100, Seq: 1},
		{Type: Put, Path: "b.md", Chunks: []string{"c2"}, Size: 20, Checksum: "h2", Device: "dev-a", Timestamp: 101, Seq: 2},
		{Type: Delete, Path: "a.md", Device: "dev-a", Timestamp: 102, Seq: 3},
	}
	for _, e := range entries {
		if err := log.Append(e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	snap, err := log.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", snap.Len())
	}
	fi, ok := snap.Lookup("b.md")
	if !ok {
		t.Fatal("b.md not in snapshot")
	}
	if fi.Checksum != "h2" {
		t.Errorf("checksum = %q, want h2", fi.Checksum)
	}
	if _, ok := snap.Lookup("a.md"); ok {
		t.Error("a.md should be deleted")
	}
}

func TestLocalLookup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	log := NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), "dev-a")

	if err := log.Append(Entry{
		Type: Put, Path: "x.md", Checksum: "h1",
		Device: "dev-a", Timestamp: 100, Seq: 1,
	}); err != nil {
		t.Fatal(err)
	}

	fi, ok := log.Lookup("x.md")
	if !ok {
		t.Fatal("Lookup(x.md) returned false")
	}
	if fi.Checksum != "h1" {
		t.Errorf("checksum = %q, want h1", fi.Checksum)
	}

	if _, ok := log.Lookup("missing.md"); ok {
		t.Error("Lookup(missing.md) should return false")
	}
}

func TestLocalLastRemoteOp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	log := NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), "dev-a")

	// Local entry — should not affect LastRemoteOp.
	if err := log.Append(Entry{
		Type: Put, Path: "a.md", Device: "dev-a", Timestamp: 100, Seq: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if got := log.LastRemoteOp(); got != 0 {
		t.Errorf("LastRemoteOp() = %d after local entry, want 0", got)
	}

	// Remote entry — should update.
	if err := log.Append(Entry{
		Type: Put, Path: "b.md", Device: "dev-b", Timestamp: 200, Seq: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if got := log.LastRemoteOp(); got != 200 {
		t.Errorf("LastRemoteOp() = %d, want 200", got)
	}

	// Older remote entry — should not decrease.
	if err := log.Append(Entry{
		Type: Put, Path: "c.md", Device: "dev-c", Timestamp: 150, Seq: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if got := log.LastRemoteOp(); got != 200 {
		t.Errorf("LastRemoteOp() = %d, want 200 (should not decrease)", got)
	}
}

func TestLocalSetLastRemoteOp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	log := NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), "dev-a")

	log.SetLastRemoteOp(100)
	if got := log.LastRemoteOp(); got != 100 {
		t.Errorf("got %d, want 100", got)
	}

	log.SetLastRemoteOp(200)
	if got := log.LastRemoteOp(); got != 200 {
		t.Errorf("got %d, want 200", got)
	}

	// Decrease — should be ignored.
	log.SetLastRemoteOp(150)
	if got := log.LastRemoteOp(); got != 200 {
		t.Errorf("got %d, want 200 (should not decrease)", got)
	}
}

func TestLocalCrashRecovery(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "ops.jsonl")

	// First session: write entries.
	log1 := NewLocalOpsLog(path, "dev-a")
	if err := log1.Append(Entry{
		Type: Put, Path: "a.md", Checksum: "h1",
		Device: "dev-a", Timestamp: 100, Seq: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := log1.Append(Entry{
		Type: Put, Path: "b.md", Checksum: "h2",
		Device: "dev-b", Timestamp: 200, Seq: 1,
	}); err != nil {
		t.Fatal(err)
	}

	// "Crash" — create new instance from same file.
	log2 := NewLocalOpsLog(path, "dev-a")
	snap, err := log2.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot after recovery: %v", err)
	}
	if snap.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", snap.Len())
	}
	if fi, ok := snap.Lookup("a.md"); !ok || fi.Checksum != "h1" {
		t.Error("a.md not recovered correctly")
	}
	if fi, ok := snap.Lookup("b.md"); !ok || fi.Checksum != "h2" {
		t.Error("b.md not recovered correctly")
	}

	// LastRemoteOp should be recovered from entries.
	if got := log2.LastRemoteOp(); got != 200 {
		t.Errorf("LastRemoteOp() = %d after recovery, want 200", got)
	}
}

func TestLocalIncrementalCache(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	log := NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), "dev-a")

	// Build initial snapshot (warms cache).
	if err := log.Append(Entry{
		Type: Put, Path: "a.md", Checksum: "h1",
		Device: "dev-a", Timestamp: 100, Seq: 1,
	}); err != nil {
		t.Fatal(err)
	}
	snap1, err := log.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	// Append with warm cache — should incrementally update.
	if err := log.Append(Entry{
		Type: Put, Path: "b.md", Checksum: "h2",
		Device: "dev-a", Timestamp: 101, Seq: 2,
	}); err != nil {
		t.Fatal(err)
	}
	snap2, err := log.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	if snap1 == snap2 {
		t.Error("expected new snapshot after append")
	}
	if snap2.Len() != 2 {
		t.Errorf("Len() = %d, want 2", snap2.Len())
	}
}

func TestLocalCRDTProperties(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	entries := []Entry{
		{Type: Put, Path: "x.md", Checksum: "v1", Device: "dev-a", Timestamp: 100, Seq: 1},
		{Type: Put, Path: "x.md", Checksum: "v2", Device: "dev-b", Timestamp: 200, Seq: 1},
		{Type: Put, Path: "y.md", Checksum: "v3", Device: "dev-a", Timestamp: 150, Seq: 2},
		{Type: Delete, Path: "y.md", Device: "dev-c", Timestamp: 300, Seq: 1},
	}

	// All 24 permutations must produce the same snapshot.
	for i, perm := range permutations(entries) {
		path := filepath.Join(dir, fmt.Sprintf("ops-%d.jsonl", i))
		log := NewLocalOpsLog(path, "dev-a")
		for _, e := range perm {
			if err := log.Append(e); err != nil {
				t.Fatalf("perm %d: Append: %v", i, err)
			}
		}

		snap, err := log.Snapshot()
		if err != nil {
			t.Fatalf("perm %d: Snapshot: %v", i, err)
		}
		if snap.Len() != 1 {
			t.Fatalf("perm %d: Len() = %d, want 1", i, snap.Len())
		}
		fi, ok := snap.Lookup("x.md")
		if !ok {
			t.Fatalf("perm %d: x.md missing", i)
		}
		if fi.Checksum != "v2" {
			t.Errorf("perm %d: x.md checksum = %q, want v2", i, fi.Checksum)
		}
		if _, ok := snap.Lookup("y.md"); ok {
			t.Errorf("perm %d: y.md should be deleted", i)
		}
	}
}

func TestLocalEmptyLog(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	log := NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), "dev-a")

	snap, err := log.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snap.Len() != 0 {
		t.Errorf("Len() = %d, want 0", snap.Len())
	}
	if log.LastRemoteOp() != 0 {
		t.Errorf("LastRemoteOp() = %d, want 0", log.LastRemoteOp())
	}
}

func TestLocalCorruptLine(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "ops.jsonl")

	// Write valid entry, corrupt line, valid entry.
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"op":"put","path":"a.md","checksum":"h1","device":"dev-a","timestamp":100,"seq":1}` + "\n")
	f.WriteString(`{corrupt json` + "\n")
	f.WriteString(`{"op":"put","path":"b.md","checksum":"h2","device":"dev-a","timestamp":101,"seq":2}` + "\n")
	f.Close()

	log := NewLocalOpsLog(path, "dev-a")
	snap, err := log.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snap.Len() != 2 {
		t.Fatalf("Len() = %d, want 2 (corrupt line should be skipped)", snap.Len())
	}
}

func TestLocalInvalidateCache(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	log := NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), "dev-a")

	if err := log.Append(Entry{
		Type: Put, Path: "x.md", Checksum: "h1",
		Device: "dev-a", Timestamp: 100, Seq: 1,
	}); err != nil {
		t.Fatal(err)
	}

	snap1, _ := log.Snapshot()
	log.InvalidateCache()
	snap2, _ := log.Snapshot()

	if snap1 == snap2 {
		t.Error("expected different snapshots after InvalidateCache")
	}
	if snap2.Len() != 1 {
		t.Errorf("Len() = %d, want 1", snap2.Len())
	}
}

func TestLocalAppendLocal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	log := NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), "dev-a")

	if err := log.AppendLocal(Entry{
		Type: Put, Path: "a.md", Checksum: "h1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := log.AppendLocal(Entry{
		Type: Put, Path: "b.md", Checksum: "h2",
	}); err != nil {
		t.Fatal(err)
	}

	snap, err := log.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snap.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", snap.Len())
	}

	fi, ok := snap.Lookup("a.md")
	if !ok {
		t.Fatal("a.md not in snapshot")
	}
	if fi.Device != "dev-a" {
		t.Errorf("Device = %q, want dev-a", fi.Device)
	}
	if fi.Checksum != "h1" {
		t.Errorf("Checksum = %q, want h1", fi.Checksum)
	}
	if fi.Seq < 1 {
		t.Errorf("Seq = %d, want >= 1", fi.Seq)
	}

	// Seq should increase monotonically
	fi2, _ := snap.Lookup("b.md")
	if fi2.Seq <= fi.Seq {
		t.Errorf("b.md Seq (%d) should be > a.md Seq (%d)", fi2.Seq, fi.Seq)
	}
}

func TestLocalAppendLocalSeqRecovery(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "ops.jsonl")

	// First session: append entries
	log1 := NewLocalOpsLog(path, "dev-a")
	log1.AppendLocal(Entry{Type: Put, Path: "a.md", Checksum: "h1"})
	log1.AppendLocal(Entry{Type: Put, Path: "b.md", Checksum: "h2"})

	// "Crash" — new instance from same file
	log2 := NewLocalOpsLog(path, "dev-a")

	// Force rebuild to recover seq
	log2.InvalidateCache()
	if _, err := log2.Snapshot(); err != nil {
		t.Fatal(err)
	}

	// New appends should have higher seq than recovered entries
	log2.AppendLocal(Entry{Type: Put, Path: "c.md", Checksum: "h3"})
	snap, _ := log2.Snapshot()
	fiB, _ := snap.Lookup("b.md")
	fiC, _ := snap.Lookup("c.md")
	if fiC.Seq <= fiB.Seq {
		t.Errorf("c.md Seq (%d) should be > b.md Seq (%d) after recovery", fiC.Seq, fiB.Seq)
	}
}

func TestLocalAppendLocalDoesNotAffectLastRemoteOp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	log := NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), "dev-a")

	log.AppendLocal(Entry{Type: Put, Path: "a.md", Checksum: "h1"})
	if got := log.LastRemoteOp(); got != 0 {
		t.Errorf("LastRemoteOp() = %d, want 0 (local entries should not affect)", got)
	}
}

func TestLocalSetLastRemoteOpPreservedAcrossRebuild(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	log := NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), "dev-a")

	// SetLastRemoteOp to a value higher than any entry.
	log.SetLastRemoteOp(500)

	// Append a remote entry with lower timestamp.
	if err := log.Append(Entry{
		Type: Put, Path: "a.md", Device: "dev-b", Timestamp: 200, Seq: 1,
	}); err != nil {
		t.Fatal(err)
	}

	// Invalidate and rebuild — SetLastRemoteOp value should be preserved.
	log.InvalidateCache()
	if _, err := log.Snapshot(); err != nil {
		t.Fatal(err)
	}
	if got := log.LastRemoteOp(); got != 500 {
		t.Errorf("LastRemoteOp() = %d, want 500 (should preserve SetLastRemoteOp)", got)
	}
}

func TestLocalCompact(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "ops.jsonl")
	log := NewLocalOpsLog(path, "dev-a")

	// Write many entries, some superseded
	log.AppendLocal(Entry{Type: Put, Path: "a.md", Checksum: "v1", Chunks: []string{"c1"}})
	log.AppendLocal(Entry{Type: Put, Path: "a.md", Checksum: "v2", Chunks: []string{"c2"}}) // supersedes v1
	log.AppendLocal(Entry{Type: Put, Path: "b.md", Checksum: "h1", Chunks: []string{"c3"}})
	log.AppendLocal(Entry{Type: Delete, Path: "b.md"}) // deletes b.md
	log.AppendLocal(Entry{Type: Put, Path: "c.md", Checksum: "h2", Chunks: []string{"c4"}})
	log.Append(Entry{Type: Put, Path: "d.md", Checksum: "h3", Chunks: []string{"c5"}, Device: "dev-b", Timestamp: 200, Seq: 1})

	// 6 entries on disk, snapshot has 3 files (a=v2, c, d)
	snap1, _ := log.Snapshot()
	if snap1.Len() != 3 {
		t.Fatalf("pre-compact Len() = %d, want 3", snap1.Len())
	}

	// Get file size before compaction
	info1, _ := os.Stat(path)
	sizeBefore := info1.Size()

	// Compact
	if err := log.Compact(); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	// File should be smaller (3 entries instead of 6)
	info2, _ := os.Stat(path)
	sizeAfter := info2.Size()
	if sizeAfter >= sizeBefore {
		t.Errorf("file did not shrink: before=%d after=%d", sizeBefore, sizeAfter)
	}

	// Snapshot should be identical
	snap2, _ := log.Snapshot()
	if snap2.Len() != 3 {
		t.Fatalf("post-compact Len() = %d, want 3", snap2.Len())
	}
	if fi, ok := snap2.Lookup("a.md"); !ok || fi.Checksum != "v2" {
		t.Error("a.md not correct after compact")
	}
	if _, ok := snap2.Lookup("b.md"); ok {
		t.Error("b.md should still be deleted after compact")
	}
	if fi, ok := snap2.Lookup("c.md"); !ok || fi.Checksum != "h2" {
		t.Error("c.md not correct after compact")
	}
	if fi, ok := snap2.Lookup("d.md"); !ok || fi.Checksum != "h3" {
		t.Error("d.md not correct after compact")
	}
}

func TestLocalCompactSurvivesReload(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "ops.jsonl")

	log1 := NewLocalOpsLog(path, "dev-a")
	log1.AppendLocal(Entry{Type: Put, Path: "x.md", Checksum: "h1", Chunks: []string{"c1"}})
	log1.AppendLocal(Entry{Type: Put, Path: "x.md", Checksum: "h2", Chunks: []string{"c2"}})
	log1.AppendLocal(Entry{Type: Put, Path: "y.md", Checksum: "h3", Chunks: []string{"c3"}})
	log1.Append(Entry{Type: Put, Path: "z.md", Checksum: "h4", Chunks: []string{"c4"}, Device: "dev-b", Timestamp: 300, Seq: 1})
	log1.Compact()

	// "Crash" — new instance from compacted file
	log2 := NewLocalOpsLog(path, "dev-a")
	snap, err := log2.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot after reload: %v", err)
	}
	if snap.Len() != 3 {
		t.Fatalf("Len() = %d, want 3", snap.Len())
	}
	if fi, ok := snap.Lookup("x.md"); !ok || fi.Checksum != "h2" {
		t.Error("x.md not correct after reload")
	}

	// LastRemoteOp should be recovered from the remote entry
	if log2.LastRemoteOp() != 300 {
		t.Errorf("LastRemoteOp() = %d, want 300", log2.LastRemoteOp())
	}

	// New appends should work on the compacted file
	log2.AppendLocal(Entry{Type: Put, Path: "new.md", Checksum: "h5", Chunks: []string{"c5"}})
	snap2, _ := log2.Snapshot()
	if snap2.Len() != 4 {
		t.Errorf("Len() = %d after append, want 4", snap2.Len())
	}
}

func TestLocalCompactEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "ops.jsonl")
	log := NewLocalOpsLog(path, "dev-a")

	// Compact an empty log
	if err := log.Compact(); err != nil {
		t.Fatalf("Compact empty: %v", err)
	}

	// Should still work
	snap, _ := log.Snapshot()
	if snap.Len() != 0 {
		t.Errorf("Len() = %d, want 0", snap.Len())
	}
}

// Regression: local compaction must strip chunkless Put entries from the
// compacted file. Chunkless Puts are tracking state ("upload pending") that
// should not persist through compaction — they cause the integrity sweep
// to re-queue files every cycle and the reconciler to spin on retries.
func TestLocalCompactStripsChunklessPuts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "ops.jsonl")
	log := NewLocalOpsLog(path, "dev-a")

	// Chunkless put (watcher handler — upload pending)
	log.AppendLocal(Entry{Type: Put, Path: "pending.md", Checksum: "h1", Size: 50})

	// Put with chunks (upload confirmed)
	log.AppendLocal(Entry{Type: Put, Path: "done.md", Checksum: "h2", Size: 100, Chunks: []string{"c1"}})

	// Symlink (always chunkless — should survive)
	log.AppendLocal(Entry{Type: Symlink, Path: "link.md", LinkTarget: "done.md"})

	// Remote entry with chunks
	log.Append(Entry{
		Type: Put, Path: "remote.md", Checksum: "h3", Size: 200,
		Chunks: []string{"c2"}, Device: "dev-b", Timestamp: 300, Seq: 1,
	})

	// Pre-compact: 4 files
	snap, _ := log.Snapshot()
	if snap.Len() != 4 {
		t.Fatalf("pre-compact Len() = %d, want 4", snap.Len())
	}

	if err := log.Compact(); err != nil {
		t.Fatal(err)
	}

	// Reload from compacted file
	log2 := NewLocalOpsLog(path, "dev-a")
	snap, _ = log2.Snapshot()

	// pending.md should be stripped (chunkless put)
	if _, ok := snap.Lookup("pending.md"); ok {
		t.Error("pending.md should be stripped by compaction (chunkless put)")
	}

	// done.md should survive (has chunks)
	if _, ok := snap.Lookup("done.md"); !ok {
		t.Error("done.md should survive compaction (has chunks)")
	}

	// link.md should survive (symlink)
	if _, ok := snap.Lookup("link.md"); !ok {
		t.Error("link.md should survive compaction (symlink)")
	}

	// remote.md should survive (has chunks)
	if _, ok := snap.Lookup("remote.md"); !ok {
		t.Error("remote.md should survive compaction (has chunks)")
	}

	// Total: 3 files after compaction
	if snap.Len() != 3 {
		t.Errorf("post-compact Len() = %d, want 3", snap.Len())
	}
}

func TestLocalCompactAllDeleted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "ops.jsonl")
	log := NewLocalOpsLog(path, "dev-a")

	// Put then delete everything
	log.AppendLocal(Entry{Type: Put, Path: "gone.md", Checksum: "h1"})
	log.AppendLocal(Entry{Type: Delete, Path: "gone.md"})

	if err := log.Compact(); err != nil {
		t.Fatal(err)
	}

	// File should be empty (or near-empty)
	info, _ := os.Stat(path)
	if info.Size() > 10 {
		t.Errorf("compacted file should be tiny, got %d bytes", info.Size())
	}

	// Reload should work
	log2 := NewLocalOpsLog(path, "dev-a")
	snap, _ := log2.Snapshot()
	if snap.Len() != 0 {
		t.Errorf("Len() = %d, want 0", snap.Len())
	}
}
