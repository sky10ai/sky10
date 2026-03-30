package fs

import (
	"bytes"
	"context"
	"strings"
	"testing"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
)

func TestListVersions(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	// Write 3 versions of the same file
	store.Put(ctx, "doc.md", strings.NewReader("version 1"))
	store.Put(ctx, "doc.md", strings.NewReader("version 2"))
	store.Put(ctx, "doc.md", strings.NewReader("version 3"))

	versions, err := ListVersions(ctx, store, "doc.md")
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}

	if len(versions) != 3 {
		t.Fatalf("got %d versions, want 3", len(versions))
	}

	// Most recent first
	if versions[0].Timestamp.Before(versions[2].Timestamp) {
		t.Error("versions should be most recent first")
	}
}

func TestRestoreVersion(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	store.Put(ctx, "doc.md", strings.NewReader("version 1"))
	store.Put(ctx, "doc.md", strings.NewReader("version 2"))

	versions, _ := ListVersions(ctx, store, "doc.md")
	if len(versions) < 2 {
		t.Fatal("need at least 2 versions")
	}

	// Restore the older version
	oldest := versions[len(versions)-1]
	var buf bytes.Buffer
	if err := RestoreVersion(ctx, store, "doc.md", oldest.Timestamp, &buf); err != nil {
		t.Fatalf("RestoreVersion: %v", err)
	}

	if buf.String() != "version 1" {
		t.Errorf("restored = %q, want %q", buf.String(), "version 1")
	}
}

func TestListSnapshots(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	store.Put(ctx, "a.md", strings.NewReader("aaa"))
	store.Put(ctx, "b.md", strings.NewReader("bbb"))
	store.SaveSnapshot(ctx)

	snapshots, err := ListSnapshots(ctx, store)
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}

	if len(snapshots) != 1 {
		t.Fatalf("got %d snapshots, want 1", len(snapshots))
	}

	if snapshots[0].FileCount != 2 {
		t.Errorf("snapshot: %d files, want 2", snapshots[0].FileCount)
	}
}

func TestListVersionsNoVersions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	versions, err := ListVersions(ctx, store, "nonexistent.md")
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions) != 0 {
		t.Errorf("got %d versions, want 0", len(versions))
	}
}
