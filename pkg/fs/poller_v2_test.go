package fs

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
	"github.com/sky10/sky10/pkg/fs/opslog"
)

func TestPollerV2FetchesRemoteOps(t *testing.T) {
	backend := s3adapter.NewMemory()
	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	storeA := NewWithDevice(backend, idA, "device-a")
	storeB := NewWithDevice(backend, idB, "device-b")

	// A uploads a file
	ctx := context.Background()
	storeA.SetNamespace("Test")
	storeA.Put(ctx, "from-a.txt", strings.NewReader("hello from A"))

	simulateApprove(t, ctx, backend, idA, idB)

	// B's poller should pick up A's op
	tmpDir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), storeB.deviceID)

	poller := NewPollerV2(storeB, localLog, time.Hour, "Test", nil)
	poller.pollOnce(ctx)

	// Local log should have the op
	fi, ok := localLog.Lookup("from-a.txt")
	if !ok {
		t.Fatal("from-a.txt not in local log after poll")
	}
	if fi.Device != "device-a" {
		t.Errorf("Device = %q, want device-a", fi.Device)
	}

	// Cursor should be updated
	if localLog.LastRemoteOp() == 0 {
		t.Error("cursor not updated")
	}
}

func TestPollerV2SkipsOwnOps(t *testing.T) {
	backend := s3adapter.NewMemory()
	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	storeA := NewWithDevice(backend, idA, "device-a")
	storeB := NewWithDevice(backend, idB, "device-b")

	ctx := context.Background()
	storeA.SetNamespace("Test")
	storeA.Put(ctx, "from-a.txt", strings.NewReader("a data"))

	simulateApprove(t, ctx, backend, idA, idB)

	// B uploads its own file
	storeB.SetNamespace("Test")
	storeB.Put(ctx, "from-b.txt", strings.NewReader("b data"))

	tmpDir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), storeB.deviceID)

	// First poll (cursor=0) — imports everything including own ops
	poller := NewPollerV2(storeB, localLog, time.Hour, "Test", nil)
	poller.pollOnce(ctx)

	if _, ok := localLog.Lookup("from-a.txt"); !ok {
		t.Error("from-a.txt should be in local log")
	}

	// Upload another file from B
	time.Sleep(time.Second)
	storeB.Put(ctx, "from-b-2.txt", strings.NewReader("b data 2"))

	// Second poll (cursor > 0) — should skip B's own new op
	poller.pollOnce(ctx)
	if _, ok := localLog.Lookup("from-b-2.txt"); ok {
		t.Error("from-b-2.txt should NOT be in local log (own op, cursor > 0)")
	}

	// Cursor should still advance past own ops
	if localLog.LastRemoteOp() == 0 {
		t.Error("cursor should advance past own ops")
	}
}

func TestPollerV2SkipsAlreadyHave(t *testing.T) {
	backend := s3adapter.NewMemory()
	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	storeA := NewWithDevice(backend, idA, "device-a")
	storeB := NewWithDevice(backend, idB, "device-b")

	ctx := context.Background()
	storeA.SetNamespace("Test")
	storeA.Put(ctx, "existing.txt", strings.NewReader("data"))

	simulateApprove(t, ctx, backend, idA, idB)

	tmpDir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), storeB.deviceID)

	poller := NewPollerV2(storeB, localLog, time.Hour, "Test", nil)

	// First poll — should append to local log
	poller.pollOnce(ctx)
	fi, ok := localLog.Lookup("existing.txt")
	if !ok {
		t.Fatal("existing.txt not in local log after first poll")
	}
	if fi.Checksum == "" {
		t.Fatal("checksum should not be empty")
	}

	// Pre-populate a fresh local log with the same file (simulates "already have"
	// with complete chunks — not chunkless).
	localLog2 := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops2.jsonl"), storeB.deviceID)
	localLog2.Append(opslog.Entry{
		Type: opslog.Put, Path: "existing.txt", Checksum: fi.Checksum,
		Chunks:    fi.Chunks,
		Namespace: "Test", Device: "device-a", Timestamp: 1, Seq: 1,
	})

	poked := false
	poller2 := NewPollerV2(storeB, localLog2, time.Hour, "Test", nil)
	poller2.pokeReconciler = func() { poked = true }

	// Second poll — should skip (already have same checksum)
	poller2.pollOnce(ctx)
	if poked {
		t.Error("reconciler should not be poked when all ops already have")
	}
}

func TestPollerV2RemoteDelete(t *testing.T) {
	backend := s3adapter.NewMemory()
	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	storeA := NewWithDevice(backend, idA, "device-a")
	storeB := NewWithDevice(backend, idB, "device-b")

	ctx := context.Background()
	storeA.SetNamespace("Test")
	storeA.Put(ctx, "del.txt", strings.NewReader("delete me"))

	simulateApprove(t, ctx, backend, idA, idB)

	tmpDir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), storeB.deviceID)

	poller := NewPollerV2(storeB, localLog, time.Hour, "Test", nil)

	// First poll — gets the put
	poller.pollOnce(ctx)
	if _, ok := localLog.Lookup("del.txt"); !ok {
		t.Fatal("del.txt not in local log after first poll")
	}

	// A deletes the file
	time.Sleep(time.Second)
	storeA.Remove(ctx, "del.txt")

	// B polls — delete should be appended
	poller.pollOnce(ctx)

	// Local log should no longer have the file
	if _, ok := localLog.Lookup("del.txt"); ok {
		t.Error("del.txt should be removed from local log after delete")
	}
}

func TestPollerV2NamespaceFilter(t *testing.T) {
	backend := s3adapter.NewMemory()
	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	storeA := NewWithDevice(backend, idA, "device-a")
	storeB := NewWithDevice(backend, idB, "device-b")

	ctx := context.Background()
	storeA.Put(ctx, "journal/note.txt", strings.NewReader("journal"))
	storeA.Put(ctx, "photos/cat.jpg", strings.NewReader("cat"))

	simulateApprove(t, ctx, backend, idA, idB)

	tmpDir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), storeB.deviceID)

	// B only syncs "journal" namespace
	poller := NewPollerV2(storeB, localLog, time.Hour, "journal", nil)
	poller.pollOnce(ctx)

	// Only journal file should be in local log
	if _, ok := localLog.Lookup("journal/note.txt"); !ok {
		t.Error("journal/note.txt should be in local log")
	}
	if _, ok := localLog.Lookup("photos/cat.jpg"); ok {
		t.Error("photos/cat.jpg should NOT be in local log (wrong namespace)")
	}
}

// Regression: when a device deletes ops.jsonl and restarts (fresh local
// log, cursor=0), the poller must import its OWN ops from S3. Otherwise
// delete ops from this device are lost and deleted files reappear.
func TestPollerV2FreshLogImportsOwnOps(t *testing.T) {
	backend := s3adapter.NewMemory()
	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	storeA := NewWithDevice(backend, idA, "device-a")
	storeB := NewWithDevice(backend, idB, "device-b")

	ctx := context.Background()
	storeA.SetNamespace("Test")
	storeB.SetNamespace("Test")

	// A uploads a file
	storeA.Put(ctx, "doomed.txt", strings.NewReader("will be deleted"))

	simulateApprove(t, ctx, backend, idA, idB)

	// B polls — picks up the put
	tmpDir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), storeB.deviceID)
	poller := NewPollerV2(storeB, localLog, time.Hour, "Test", nil)
	poller.pollOnce(ctx)

	if _, ok := localLog.Lookup("doomed.txt"); !ok {
		t.Fatal("doomed.txt not in local log after first poll")
	}

	// B deletes the file via S3 (simulates outbox worker uploading a delete)
	time.Sleep(time.Second)
	storeB.Remove(ctx, "doomed.txt")

	// Also record the delete in B's local log (as WatcherHandler would)
	localLog.AppendLocal(opslog.Entry{
		Type: opslog.Delete, Path: "doomed.txt", Namespace: "Test",
	})

	// Verify local log shows delete
	if _, ok := localLog.Lookup("doomed.txt"); ok {
		t.Fatal("doomed.txt should be gone after B's delete")
	}

	// Simulate ops.jsonl deletion: create a fresh local log (cursor=0)
	freshLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "fresh-ops.jsonl"), storeB.deviceID)
	poller2 := NewPollerV2(storeB, freshLog, time.Hour, "Test", nil)
	poller2.pollOnce(ctx)

	// The delete op from device-b must be imported even though it's
	// "our own" op. Without the fix, the poller skips it, and doomed.txt
	// reappears in the snapshot.
	if _, ok := freshLog.Lookup("doomed.txt"); ok {
		t.Error("doomed.txt should NOT be in fresh log — device's own delete op was lost")
	}
}

// Regression: if two devices write ops at the same Unix second, and the
// poller processes one device's ops (advancing the cursor to that second),
// the other device's ops at the same second must not be skipped.
// Previously ReadSince used ts <= since, permanently losing same-second ops.
func TestPollerV2SameTimestampNotSkipped(t *testing.T) {
	backend := s3adapter.NewMemory()
	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	storeA := NewWithDevice(backend, idA, "device-a")
	storeB := NewWithDevice(backend, idB, "device-b")

	ctx := context.Background()

	// A creates namespace, approve B, then both can write ops
	storeA.SetNamespace("Test")
	storeA.Put(ctx, "seed.txt", strings.NewReader("seed"))
	simulateApprove(t, ctx, backend, idA, idB)

	// Both upload files — no sleep, same Unix second
	storeA.Put(ctx, "from-a.txt", strings.NewReader("aaa"))
	storeB.SetNamespace("Test")
	storeB.Put(ctx, "from-b.txt", strings.NewReader("bbb"))

	// B's poller: first poll picks up A's ops
	tmpDir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), storeB.deviceID)
	poller := NewPollerV2(storeB, localLog, time.Hour, "Test", nil)
	poller.pollOnce(ctx)

	if _, ok := localLog.Lookup("from-a.txt"); !ok {
		t.Fatal("from-a.txt not in local log after first poll")
	}

	cursor := localLog.LastRemoteOp()

	// A uploads another file — same second as cursor (test runs fast)
	storeA.Put(ctx, "from-a-2.txt", strings.NewReader("aaa2"))

	// Verify the new op has the same timestamp as cursor
	opsLog, _ := storeB.getOpsLog(ctx)
	allEntries, _ := opsLog.ReadSince(ctx, 0)
	var newOpTs int64
	for _, e := range allEntries {
		if e.Path == "from-a-2.txt" {
			newOpTs = e.Timestamp
		}
	}

	if newOpTs != cursor {
		t.Skipf("timestamps differ (cursor=%d, new=%d) — race window missed", cursor, newOpTs)
	}

	// Second poll — from-a-2.txt has the same timestamp as cursor.
	// With the bug: ReadSince(cursor) uses ts <= cursor, skips it forever.
	poller.pollOnce(ctx)

	if _, ok := localLog.Lookup("from-a-2.txt"); !ok {
		t.Error("from-a-2.txt not in local log — poller skipped op at same timestamp as cursor")
	}
}

// Regression: delete_dir ops must be deduped like create_dir and delete.
// Without dedup, a delete_dir at the cursor timestamp is re-appended to the
// local log every poll cycle.
func TestPollerV2DedupDeleteDir(t *testing.T) {
	backend := s3adapter.NewMemory()
	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	storeA := NewWithDevice(backend, idA, "device-a")
	storeB := NewWithDevice(backend, idB, "device-b")

	ctx := context.Background()
	storeA.SetNamespace("Test")

	// A creates a directory and a file under it
	storeA.writeOp(ctx, &Op{Type: OpCreateDir, Path: "photos", Namespace: "Test"})
	storeA.Put(ctx, "photos/a.jpg", strings.NewReader("image"))

	// A deletes the directory
	time.Sleep(time.Second)
	storeA.Remove(ctx, "photos/a.jpg")
	storeA.writeOp(ctx, &Op{Type: OpDeleteDir, Path: "photos", Namespace: "Test"})

	simulateApprove(t, ctx, backend, idA, idB)

	tmpDir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), storeB.deviceID)

	pokeCount := 0
	poller := NewPollerV2(storeB, localLog, time.Hour, "Test", nil)
	poller.pokeReconciler = func() { pokeCount++ }

	// First poll — imports all ops
	poller.pollOnce(ctx)
	if pokeCount == 0 {
		t.Fatal("reconciler should be poked on first poll")
	}

	// Verify the delete_dir was applied: dir not in snapshot
	snap, _ := localLog.Snapshot()
	if snap != nil {
		if _, ok := snap.Dirs()["photos"]; ok {
			t.Error("photos dir should not be in snapshot after delete_dir")
		}
	}

	// Second poll — delete_dir should be deduped, no new appends
	pokeCount = 0
	poller.pollOnce(ctx)
	if pokeCount != 0 {
		t.Error("reconciler should not be poked — delete_dir should be deduped")
	}
}

// Regression: create_dir ops must be deduped when the directory already
// exists in the local snapshot.
func TestPollerV2DedupCreateDir(t *testing.T) {
	backend := s3adapter.NewMemory()
	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	storeA := NewWithDevice(backend, idA, "device-a")
	storeB := NewWithDevice(backend, idB, "device-b")

	ctx := context.Background()
	storeA.SetNamespace("Test")

	// A creates a directory
	storeA.writeOp(ctx, &Op{Type: OpCreateDir, Path: "docs", Namespace: "Test"})

	simulateApprove(t, ctx, backend, idA, idB)

	tmpDir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), storeB.deviceID)

	pokeCount := 0
	poller := NewPollerV2(storeB, localLog, time.Hour, "Test", nil)
	poller.pokeReconciler = func() { pokeCount++ }

	// First poll — imports the create_dir
	poller.pollOnce(ctx)
	if pokeCount == 0 {
		t.Fatal("reconciler should be poked on first poll")
	}

	snap, _ := localLog.Snapshot()
	if snap == nil {
		t.Fatal("snapshot should not be nil")
	}
	if _, ok := snap.Dirs()["docs"]; !ok {
		t.Fatal("docs dir should be in snapshot after create_dir")
	}

	// Second poll — create_dir should be deduped
	pokeCount = 0
	poller.pollOnce(ctx)
	if pokeCount != 0 {
		t.Error("reconciler should not be poked — create_dir should be deduped")
	}
}

// Regression: the poller's dedup compares checksums but ignores chunk presence.
// When the local snapshot has a chunkless entry (upload pending) and a remote
// op arrives with the same checksum but actual chunks, the poller skips it
// because checksums match. The file stays chunkless forever — the integrity
// sweep re-queues it every cycle, but the poller never imports the chunked
// version from the remote.
func TestPollerV2ChunklessNotDeduped(t *testing.T) {
	backend := s3adapter.NewMemory()
	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	storeA := NewWithDevice(backend, idA, "device-a")
	storeB := NewWithDevice(backend, idB, "device-b")

	ctx := context.Background()
	storeA.SetNamespace("Test")
	storeA.Put(ctx, "file.txt", strings.NewReader("hello"))

	simulateApprove(t, ctx, backend, idA, idB)

	// Probe poll to discover A's checksum and chunks.
	tmpDir := t.TempDir()
	probeLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "probe.jsonl"), storeB.deviceID)
	NewPollerV2(storeB, probeLog, time.Hour, "Test", nil).pollOnce(ctx)

	probed, ok := probeLog.Lookup("file.txt")
	if !ok {
		t.Fatal("file.txt not found in probe poll")
	}
	if len(probed.Chunks) == 0 {
		t.Fatal("probe: A's entry should have chunks")
	}

	// B's local log has a chunkless entry with the same checksum.
	// This simulates the watcher creating a Put before the upload completes.
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), storeB.deviceID)
	localLog.Append(opslog.Entry{
		Type:      opslog.Put,
		Path:      "file.txt",
		Checksum:  probed.Checksum,
		Namespace: "Test",
		Device:    storeB.deviceID,
		Timestamp: 1, // Old timestamp — A's entry should win LWW
		Seq:       1,
		// No Chunks — chunkless entry
	})

	// Verify pre-condition: local entry exists and is chunkless.
	pre, _ := localLog.Lookup("file.txt")
	if len(pre.Chunks) > 0 {
		t.Fatal("pre-condition: local entry should be chunkless")
	}

	// Poll — the poller should NOT skip A's chunked op.
	poller := NewPollerV2(storeB, localLog, time.Hour, "Test", nil)
	poller.pollOnce(ctx)

	// After poll: A's chunked entry should win LWW in the snapshot.
	post, ok := localLog.Lookup("file.txt")
	if !ok {
		t.Fatal("file.txt not in local log after poll")
	}
	if len(post.Chunks) == 0 {
		t.Error("file.txt should have chunks after poll — poller incorrectly skipped the chunked remote op because checksum matched the chunkless local entry")
	}
}

func TestPollerV2PokesReconciler(t *testing.T) {
	backend := s3adapter.NewMemory()
	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	storeA := NewWithDevice(backend, idA, "device-a")
	storeB := NewWithDevice(backend, idB, "device-b")

	ctx := context.Background()
	storeA.SetNamespace("Test")
	storeA.Put(ctx, "a.txt", strings.NewReader("aaa"))

	simulateApprove(t, ctx, backend, idA, idB)

	tmpDir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(tmpDir, "ops.jsonl"), storeB.deviceID)

	poked := false
	poller := NewPollerV2(storeB, localLog, time.Hour, "Test", nil)
	poller.pokeReconciler = func() { poked = true }
	poller.pollOnce(ctx)

	if !poked {
		t.Error("reconciler should be poked after new ops")
	}

	// Second poll (no new ops) should NOT poke
	poked = false
	poller.pollOnce(ctx)
	if poked {
		t.Error("reconciler should not be poked when no new ops")
	}
}
