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
