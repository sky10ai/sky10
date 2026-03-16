package fs

import (
	"context"
	"fmt"
	"io"
	"sort"
	"time"
)

// Version represents a historical version of a file.
type Version struct {
	Path      string
	Timestamp time.Time
	Device    string
	Checksum  string
	Size      int64
	Chunks    []string
}

// Snapshot represents a compacted manifest snapshot.
type Snapshot struct {
	Key       string
	Timestamp time.Time
	FileCount int
	TotalSize int64
}

// ListVersions returns all historical versions of a file from the ops log.
func ListVersions(ctx context.Context, store *Store, path string) ([]Version, error) {
	encKey, err := store.opsKey(ctx)
	if err != nil {
		return nil, err
	}

	ops, err := ReadAllOps(ctx, store.backend, encKey)
	if err != nil {
		return nil, fmt.Errorf("reading ops: %w", err)
	}

	var versions []Version
	for _, op := range ops {
		if op.Path != path || op.Type != OpPut {
			continue
		}
		versions = append(versions, Version{
			Path:      op.Path,
			Timestamp: time.Unix(op.Timestamp, 0).UTC(),
			Device:    op.Device,
			Checksum:  op.Checksum,
			Size:      op.Size,
			Chunks:    op.Chunks,
		})
	}

	// Most recent first
	sort.Slice(versions, func(i, j int) bool {
		return versions[i].Timestamp.After(versions[j].Timestamp)
	})

	return versions, nil
}

// RestoreVersion downloads a specific historical version of a file.
func RestoreVersion(ctx context.Context, store *Store, path string, timestamp time.Time, w io.Writer) error {
	versions, err := ListVersions(ctx, store, path)
	if err != nil {
		return err
	}

	// Find the version closest to the requested timestamp
	var target *Version
	for i := range versions {
		if !versions[i].Timestamp.After(timestamp) {
			target = &versions[i]
			break
		}
	}
	if target == nil && len(versions) > 0 {
		target = &versions[len(versions)-1]
	}
	if target == nil {
		return fmt.Errorf("no version found for %s", path)
	}

	// Download using the version's chunks
	nsKey, err := store.getOrCreateNamespaceKey(ctx, NamespaceFromPath(path))
	if err != nil {
		return err
	}

	for i, chunkHash := range target.Chunks {
		blobKey := (&Chunk{Hash: chunkHash}).BlobKey()

		rc, err := store.backend.Get(ctx, blobKey)
		if err != nil {
			return fmt.Errorf("downloading chunk %d: %w", i, err)
		}

		raw, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return fmt.Errorf("reading chunk %d: %w", i, err)
		}

		encrypted, _, err := StripBlobHeader(raw)
		if err != nil {
			return fmt.Errorf("parsing chunk %d header: %w", i, err)
		}

		fileKey, err := DeriveFileKey(nsKey, []byte(chunkHash))
		if err != nil {
			return fmt.Errorf("deriving file key: %w", err)
		}

		compressed, err := Decrypt(encrypted, fileKey)
		if err != nil {
			return fmt.Errorf("decrypting chunk %d: %w", i, err)
		}

		plaintext, err := DecompressChunk(compressed)
		if err != nil {
			return fmt.Errorf("decompressing chunk %d: %w", i, err)
		}

		if _, err := w.Write(plaintext); err != nil {
			return fmt.Errorf("writing chunk %d: %w", i, err)
		}
	}

	return nil
}

// ListSnapshots returns all compacted manifest snapshots.
func ListSnapshots(ctx context.Context, store *Store) ([]Snapshot, error) {
	keys, err := store.backend.List(ctx, "manifests/snapshot-")
	if err != nil {
		return nil, fmt.Errorf("listing snapshots: %w", err)
	}

	var snapshots []Snapshot
	for _, key := range keys {
		ts := parseSnapshotTimestamp(key)

		m, err := store.loadManifestFromKey(ctx, key)
		if err != nil {
			continue
		}

		var totalSize int64
		for _, entry := range m.Tree {
			totalSize += entry.Size
		}

		snapshots = append(snapshots, Snapshot{
			Key:       key,
			Timestamp: time.Unix(ts, 0).UTC(),
			FileCount: len(m.Tree),
			TotalSize: totalSize,
		})
	}

	// Most recent first
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].Timestamp.After(snapshots[j].Timestamp)
	})

	return snapshots, nil
}
