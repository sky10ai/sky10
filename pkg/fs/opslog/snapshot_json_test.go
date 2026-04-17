package opslog

import (
	"path/filepath"
	"testing"
)

func TestMarshalUnmarshalSnapshot(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	log := NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), "dev-a")

	log.AppendLocal(Entry{
		Type: Put, Path: "a.txt", Checksum: "h1",
		Chunks: []string{"c1"}, Size: 100, Namespace: "ns",
	})
	log.AppendLocal(Entry{
		Type: Symlink, Path: "link.txt", Checksum: "h2",
		LinkTarget: "/target", Namespace: "ns",
	})
	log.AppendLocal(Entry{
		Type: CreateDir, Path: "subdir", Namespace: "ns",
	})

	snap, _ := log.Snapshot()
	data, err := MarshalSnapshot(snap)
	if err != nil {
		t.Fatalf("MarshalSnapshot: %v", err)
	}

	snap2, err := UnmarshalSnapshot(data)
	if err != nil {
		t.Fatalf("UnmarshalSnapshot: %v", err)
	}

	if snap2.Len() != 2 {
		t.Errorf("files = %d, want 2", snap2.Len())
	}
	fi, ok := snap2.Lookup("a.txt")
	if !ok || fi.Checksum != "h1" {
		t.Errorf("a.txt: %+v", fi)
	}
	fi2, ok := snap2.Lookup("link.txt")
	if !ok || fi2.LinkTarget != "/target" {
		t.Errorf("link.txt: %+v", fi2)
	}
	dirs := snap2.Dirs()
	if _, ok := dirs["subdir"]; !ok {
		t.Error("subdir not in dirs")
	}
}

func TestMarshalSnapshot_SkipsChunkless(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	log := NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), "dev-a")

	// Entry with chunks = uploaded
	log.AppendLocal(Entry{
		Type: Put, Path: "uploaded.txt", Checksum: "h1",
		Chunks: []string{"c1"}, Size: 100, Namespace: "ns",
	})
	// Entry without chunks = upload pending (should be excluded)
	log.AppendLocal(Entry{
		Type: Put, Path: "pending.txt", Checksum: "h2",
		Size: 50, Namespace: "ns",
	})

	snap, _ := log.Snapshot()
	data, _ := MarshalSnapshot(snap)
	snap2, _ := UnmarshalSnapshot(data)

	if snap2.Len() != 1 {
		t.Errorf("expected 1 file (uploaded only), got %d", snap2.Len())
	}
	if _, ok := snap2.Lookup("pending.txt"); ok {
		t.Error("pending.txt should not be in marshaled snapshot")
	}
}

func TestMarshalPeerSnapshotPreservesTombstones(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	log := NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), "dev-a")

	if err := log.Append(Entry{
		Type: Put, Path: "gone.txt", Checksum: "h1", Chunks: []string{"c1"},
		Device: "dev-a", Timestamp: 100, Seq: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := log.Append(Entry{
		Type: Delete, Path: "gone.txt",
		Device: "dev-b", Timestamp: 200, Seq: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := log.Append(Entry{
		Type: Put, Path: "dir/a.txt", Checksum: "h2", Chunks: []string{"c2"},
		Device: "dev-a", Timestamp: 110, Seq: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := log.Append(Entry{
		Type: DeleteDir, Path: "dir",
		Device: "dev-c", Timestamp: 300, Seq: 1,
	}); err != nil {
		t.Fatal(err)
	}

	snap, _ := log.Snapshot()
	data, err := MarshalPeerSnapshot(snap)
	if err != nil {
		t.Fatalf("MarshalPeerSnapshot: %v", err)
	}

	snap2, err := UnmarshalPeerSnapshot(data)
	if err != nil {
		t.Fatalf("UnmarshalPeerSnapshot: %v", err)
	}

	if !snap2.DeletedFiles()["gone.txt"] {
		t.Fatal("gone.txt tombstone missing from peer snapshot")
	}
	if tomb := snap2.Tombstones()["gone.txt"]; tomb.Device != "dev-b" || tomb.Modified.Unix() != 200 {
		t.Fatalf("gone.txt tombstone = %+v, want device dev-b @ 200", tomb)
	}
	if !snap2.DeletedDirs()["dir"] {
		t.Fatal("dir tombstone missing from peer snapshot")
	}
	if tomb := snap2.DirTombstones()["dir"]; tomb.Device != "dev-c" || tomb.Modified.Unix() != 300 {
		t.Fatalf("dir tombstone = %+v, want device dev-c @ 300", tomb)
	}
}

func TestMarshalSnapshotPreservesDeleteRoot(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	log := NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), "dev-a")

	if err := log.Append(Entry{
		Type:      Put,
		Path:      "agents/lisa/memory.md",
		Checksum:  "h1",
		Chunks:    []string{"c1"},
		Device:    "dev-a",
		Timestamp: 100,
		Seq:       1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := log.Append(Entry{
		Type:      DeleteRoot,
		Path:      "",
		Namespace: "Agents",
		Device:    "dev-b",
		Timestamp: 200,
		Seq:       1,
	}); err != nil {
		t.Fatal(err)
	}

	snap, _ := log.Snapshot()
	data, err := MarshalSnapshot(snap)
	if err != nil {
		t.Fatalf("MarshalSnapshot: %v", err)
	}

	snap2, err := UnmarshalSnapshot(data)
	if err != nil {
		t.Fatalf("UnmarshalSnapshot: %v", err)
	}
	if !snap2.RootDeleted() {
		t.Fatal("delete_root should survive standard snapshot marshaling")
	}
}

func TestMarshalSnapshotOmitsTombstones(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	log := NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), "dev-a")

	if err := log.Append(Entry{
		Type: Put, Path: "gone.txt", Checksum: "h1", Chunks: []string{"c1"},
		Device: "dev-a", Timestamp: 100, Seq: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := log.Append(Entry{
		Type: Delete, Path: "gone.txt",
		Device: "dev-b", Timestamp: 200, Seq: 1,
	}); err != nil {
		t.Fatal(err)
	}

	snap, _ := log.Snapshot()
	data, err := MarshalSnapshot(snap)
	if err != nil {
		t.Fatalf("MarshalSnapshot: %v", err)
	}

	snap2, err := UnmarshalSnapshot(data)
	if err != nil {
		t.Fatalf("UnmarshalSnapshot: %v", err)
	}
	if len(snap2.Tombstones()) != 0 {
		t.Fatalf("expected S3 snapshot format to omit tombstones, got %+v", snap2.Tombstones())
	}
	if len(snap2.DirTombstones()) != 0 {
		t.Fatalf("expected S3 snapshot format to omit dir tombstones, got %+v", snap2.DirTombstones())
	}
	if snap2.RootDeleted() {
		t.Fatal("delete_root should not appear when it was never recorded")
	}
}
