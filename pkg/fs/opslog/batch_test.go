package opslog

import (
	"context"
	"testing"
)

func TestReadBatchedAllInOneBatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log, _ := newTestLog(t)

	for i, path := range []string{"a.md", "b.md", "c.md"} {
		e := Entry{Type: Put, Path: path, Checksum: "h" + path}
		_ = i
		if err := log.Append(ctx, &e); err != nil {
			t.Fatal(err)
		}
	}

	var batches [][]Entry
	total, err := log.ReadBatched(ctx, 0, 100, func(entries []Entry) {
		cp := make([]Entry, len(entries))
		copy(cp, entries)
		batches = append(batches, cp)
	})
	if err != nil {
		t.Fatalf("ReadBatched: %v", err)
	}

	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if len(batches) != 1 {
		t.Errorf("batches = %d, want 1 (all fit in one batch)", len(batches))
	}
	if len(batches[0]) != 3 {
		t.Errorf("batch[0] size = %d, want 3", len(batches[0]))
	}
}

func TestReadBatchedMultipleBatches(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log, _ := newTestLog(t)

	// Write 7 entries, batch size 3 → 3 batches (3, 3, 1)
	for i := 0; i < 7; i++ {
		e := Entry{Type: Put, Path: "file" + string(rune('a'+i)) + ".md", Checksum: "h"}
		if err := log.Append(ctx, &e); err != nil {
			t.Fatal(err)
		}
	}

	var batchSizes []int
	total, err := log.ReadBatched(ctx, 0, 3, func(entries []Entry) {
		batchSizes = append(batchSizes, len(entries))
	})
	if err != nil {
		t.Fatalf("ReadBatched: %v", err)
	}

	if total != 7 {
		t.Errorf("total = %d, want 7", total)
	}
	if len(batchSizes) != 3 {
		t.Errorf("batch count = %d, want 3", len(batchSizes))
	}
	if batchSizes[0] != 3 || batchSizes[1] != 3 || batchSizes[2] != 1 {
		t.Errorf("batch sizes = %v, want [3 3 1]", batchSizes)
	}
}

func TestReadBatchedSinceFilter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log, _ := newTestLog(t)

	// Write 5 entries with increasing timestamps
	for i := 0; i < 5; i++ {
		e := Entry{Type: Put, Path: "file" + string(rune('a'+i)) + ".md", Checksum: "h"}
		if err := log.Append(ctx, &e); err != nil {
			t.Fatal(err)
		}
	}

	// Read all to get timestamps
	all, _ := log.ReadSince(ctx, 0)
	if len(all) != 5 {
		t.Fatalf("got %d entries, want 5", len(all))
	}

	// Read since third entry's timestamp — should get entries at or after that ts
	since := all[2].Timestamp
	var total int
	total, err := log.ReadBatched(ctx, since, 100, func(entries []Entry) {
		for _, e := range entries {
			if e.Timestamp < since {
				t.Errorf("entry %q has ts %d < since %d", e.Path, e.Timestamp, since)
			}
		}
	})
	if err != nil {
		t.Fatalf("ReadBatched: %v", err)
	}
	if total < 1 {
		t.Errorf("total = %d, want at least 1", total)
	}
}

func TestReadBatchedEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log, _ := newTestLog(t)

	called := false
	total, err := log.ReadBatched(ctx, 0, 10, func(entries []Entry) {
		called = true
	})
	if err != nil {
		t.Fatalf("ReadBatched: %v", err)
	}
	if total != 0 {
		t.Errorf("total = %d, want 0", total)
	}
	if called {
		t.Error("callback should not be called for empty log")
	}
}

func TestReadBatchedEntriesSorted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log, _ := newTestLog(t)

	for i := 0; i < 6; i++ {
		e := Entry{Type: Put, Path: "f" + string(rune('a'+i)) + ".md", Checksum: "h"}
		if err := log.Append(ctx, &e); err != nil {
			t.Fatal(err)
		}
	}

	// Batch size 3 — each batch should be internally sorted
	_, err := log.ReadBatched(ctx, 0, 3, func(entries []Entry) {
		for i := 1; i < len(entries); i++ {
			prev := entries[i-1]
			curr := entries[i]
			if curr.Timestamp < prev.Timestamp {
				t.Errorf("unsorted: entry %d ts=%d < entry %d ts=%d", i, curr.Timestamp, i-1, prev.Timestamp)
			}
			if curr.Timestamp == prev.Timestamp && curr.Device < prev.Device {
				t.Errorf("unsorted: same ts, entry %d dev=%q < entry %d dev=%q", i, curr.Device, i-1, prev.Device)
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestReadBatchedMatchesReadSince(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log, _ := newTestLog(t)

	for i := 0; i < 10; i++ {
		e := Entry{Type: Put, Path: "f" + string(rune('a'+i)) + ".md", Checksum: "h" + string(rune('a'+i))}
		if err := log.Append(ctx, &e); err != nil {
			t.Fatal(err)
		}
	}

	// ReadSince gets everything
	all, err := log.ReadSince(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}

	// ReadBatched should produce the same entries
	var batched []Entry
	total, err := log.ReadBatched(ctx, 0, 4, func(entries []Entry) {
		batched = append(batched, entries...)
	})
	if err != nil {
		t.Fatal(err)
	}

	if total != len(all) {
		t.Errorf("total = %d, want %d", total, len(all))
	}
	if len(batched) != len(all) {
		t.Fatalf("batched entries = %d, want %d", len(batched), len(all))
	}

	// Same paths (order within a batch may differ from global sort,
	// but all entries should be present)
	allPaths := make(map[string]string)
	for _, e := range all {
		allPaths[e.Path] = e.Checksum
	}
	for _, e := range batched {
		if cksum, ok := allPaths[e.Path]; !ok {
			t.Errorf("batched has extra entry: %q", e.Path)
		} else if cksum != e.Checksum {
			t.Errorf("checksum mismatch for %q: %q vs %q", e.Path, e.Checksum, cksum)
		}
	}
}

func TestReadBatchedContextCancellation(t *testing.T) {
	t.Parallel()
	log, _ := newTestLog(t)
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		e := Entry{Type: Put, Path: "f" + string(rune('a'+i)) + ".md", Checksum: "h"}
		if err := log.Append(ctx, &e); err != nil {
			t.Fatal(err)
		}
	}

	// Cancel after first batch
	cancelCtx, cancel := context.WithCancel(ctx)
	batchCount := 0
	total, _ := log.ReadBatched(cancelCtx, 0, 3, func(entries []Entry) {
		batchCount++
		cancel() // cancel after first batch
	})

	// Should have processed at most the first batch
	if batchCount != 1 {
		t.Errorf("batch count = %d, want 1 (cancelled after first)", batchCount)
	}
	if total > 3 {
		t.Errorf("total = %d, want <= 3", total)
	}
}

func TestReadBatchedSymlinkEntries(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log, _ := newTestLog(t)

	// Mix of puts and symlinks
	entries := []Entry{
		{Type: Put, Path: "real.txt", Checksum: "h1"},
		{Type: Symlink, Path: "link.txt", Checksum: "h2", LinkTarget: "../real.txt"},
		{Type: Put, Path: "other.txt", Checksum: "h3"},
	}
	for i := range entries {
		if err := log.Append(ctx, &entries[i]); err != nil {
			t.Fatal(err)
		}
	}

	var got []Entry
	_, err := log.ReadBatched(ctx, 0, 10, func(batch []Entry) {
		got = append(got, batch...)
	})
	if err != nil {
		t.Fatal(err)
	}

	// Find the symlink entry and verify LinkTarget survived round-trip
	found := false
	for _, e := range got {
		if e.Path == "link.txt" {
			found = true
			if string(e.Type) != string(Symlink) {
				t.Errorf("type = %q, want symlink", e.Type)
			}
			if e.LinkTarget != "../real.txt" {
				t.Errorf("link_target = %q, want %q", e.LinkTarget, "../real.txt")
			}
		}
	}
	if !found {
		t.Error("symlink entry not found in batched results")
	}
}
