package fs

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/fs/opslog"
)

func TestFSP2PSyncPushToAllCoalescesConcurrentTriggers(t *testing.T) {
	t.Parallel()

	syncer := NewP2PSync(nil, nil)
	release := make(chan struct{})
	started := make(chan int32, 4)
	var rounds atomic.Int32
	syncer.pushRoundHook = func(context.Context) {
		round := rounds.Add(1)
		started <- round
		<-release
	}

	for i := 0; i < 20; i++ {
		syncer.PushToAll(context.Background())
	}

	select {
	case got := <-started:
		if got != 1 {
			t.Fatalf("first round = %d, want 1", got)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for first round")
	}
	select {
	case got := <-started:
		t.Fatalf("unexpected overlapping round %d", got)
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	waitForFSUnit(t, 3*time.Second, func() bool {
		syncer.pushMu.Lock()
		idle := !syncer.pushRunning
		syncer.pushMu.Unlock()
		return rounds.Load() == 2 && idle
	})
	if got := rounds.Load(); got != 2 {
		t.Fatalf("rounds = %d, want exactly 2", got)
	}
}

func waitForFSUnit(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !fn() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for condition")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSummarizeFSSnapshotStableForUnchangedState(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	log := opslog.NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), "dev-a")
	if err := log.Append(opslog.Entry{
		Type:      opslog.Put,
		Path:      "doc.txt",
		Checksum:  "h1",
		Chunks:    []string{"c1"},
		Device:    "dev-a",
		Timestamp: 100,
		Seq:       1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := log.Append(opslog.Entry{
		Type:      opslog.Delete,
		Path:      "gone.txt",
		Device:    "dev-b",
		Timestamp: 200,
		Seq:       1,
	}); err != nil {
		t.Fatal(err)
	}

	snap, err := log.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	summary1, err := summarizeFSSnapshot(snap)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	summary2, err := summarizeFSSnapshot(snap)
	if err != nil {
		t.Fatal(err)
	}

	if summary1.Digest != summary2.Digest {
		t.Fatalf("digest changed for unchanged snapshot: %s vs %s", summary1.Digest, summary2.Digest)
	}
}

func TestMergePeerSnapshotSkipsDuplicateState(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "ops.jsonl")
	localLog := opslog.NewLocalOpsLog(path, "dev-a")
	remoteLog := opslog.NewLocalOpsLog(filepath.Join(dir, "remote.jsonl"), "dev-b")

	if err := remoteLog.Append(opslog.Entry{
		Type:      opslog.Put,
		Path:      "a.txt",
		Checksum:  "h1",
		Chunks:    []string{"c1"},
		Device:    "dev-b",
		Timestamp: 100,
		Seq:       1,
	}); err != nil {
		t.Fatal(err)
	}

	remoteSnap, err := remoteLog.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	merged, err := mergePeerSnapshot(localLog, remoteSnap)
	if err != nil {
		t.Fatal(err)
	}
	if merged != 1 {
		t.Fatalf("first merge = %d, want 1", merged)
	}

	merged, err = mergePeerSnapshot(localLog, remoteSnap)
	if err != nil {
		t.Fatal(err)
	}
	if merged != 0 {
		t.Fatalf("second merge = %d, want 0", merged)
	}
}

func TestMergePeerSnapshotPreservesDeleteDirAuthority(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), "dev-a")
	remoteLog := opslog.NewLocalOpsLog(filepath.Join(dir, "remote.jsonl"), "dev-b")

	if err := localLog.Append(opslog.Entry{
		Type:      opslog.Put,
		Path:      "dir/stale.txt",
		Checksum:  "old",
		Chunks:    []string{"c1"},
		Device:    "dev-a",
		Timestamp: 100,
		Seq:       1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := remoteLog.Append(opslog.Entry{
		Type:      opslog.DeleteDir,
		Path:      "dir",
		Device:    "dev-b",
		Timestamp: 200,
		Seq:       1,
	}); err != nil {
		t.Fatal(err)
	}

	remoteSnap, err := remoteLog.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	merged, err := mergePeerSnapshot(localLog, remoteSnap)
	if err != nil {
		t.Fatal(err)
	}
	if merged != 1 {
		t.Fatalf("merge = %d, want 1", merged)
	}

	localSnap, err := localLog.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := localSnap.Lookup("dir/stale.txt"); ok {
		t.Fatal("dir/stale.txt should be removed by remote delete_dir")
	}
	if !localSnap.DeletedDirs()["dir"] {
		t.Fatal("dir tombstone should be present after merge")
	}
}

func TestMergePeerSnapshotPreservesDeleteRootAuthority(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), "dev-a")
	remoteLog := opslog.NewLocalOpsLog(filepath.Join(dir, "remote.jsonl"), "dev-b")

	if err := localLog.Append(opslog.Entry{
		Type:      opslog.Put,
		Path:      "dir/stale.txt",
		Checksum:  "old",
		Chunks:    []string{"c1"},
		Device:    "dev-a",
		Timestamp: 100,
		Seq:       1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := remoteLog.Append(opslog.Entry{
		Type:      opslog.DeleteRoot,
		Path:      "",
		Namespace: "Agents",
		Device:    "dev-b",
		Timestamp: 200,
		Seq:       1,
	}); err != nil {
		t.Fatal(err)
	}

	remoteSnap, err := remoteLog.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	merged, err := mergePeerSnapshot(localLog, remoteSnap)
	if err != nil {
		t.Fatal(err)
	}
	if merged != 1 {
		t.Fatalf("merge = %d, want 1", merged)
	}

	localSnap, err := localLog.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := localSnap.Lookup("dir/stale.txt"); ok {
		t.Fatal("dir/stale.txt should be removed by remote delete_root")
	}
	if !localSnap.RootDeleted() {
		t.Fatal("root tombstone should be present after merge")
	}
}

func TestMergePeerSnapshotAppliesCausalSuccessorDespiteOlderTimestamp(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), "dev-a")
	remoteLog := opslog.NewLocalOpsLog(filepath.Join(dir, "remote.jsonl"), "dev-b")

	if err := localLog.Append(opslog.Entry{
		Type:      opslog.Put,
		Path:      "doc.txt",
		Checksum:  "base",
		Chunks:    []string{"c1"},
		Device:    "dev-a",
		Timestamp: 200,
		Seq:       1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := remoteLog.Append(opslog.Entry{
		Type:         opslog.Put,
		Path:         "doc.txt",
		Checksum:     "next",
		PrevChecksum: "base",
		Chunks:       []string{"c2"},
		Device:       "dev-b",
		Timestamp:    100,
		Seq:          1,
	}); err != nil {
		t.Fatal(err)
	}

	remoteSnap, err := remoteLog.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	merged, err := mergePeerSnapshot(localLog, remoteSnap)
	if err != nil {
		t.Fatal(err)
	}
	if merged != 1 {
		t.Fatalf("merge = %d, want 1", merged)
	}

	localSnap, err := localLog.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	fi, ok := localSnap.Lookup("doc.txt")
	if !ok {
		t.Fatal("doc.txt missing after merge")
	}
	if fi.Checksum != "next" {
		t.Fatalf("checksum = %q, want next", fi.Checksum)
	}
	if fi.PrevChecksum != "base" {
		t.Fatalf("prev_checksum = %q, want base", fi.PrevChecksum)
	}
}

func TestMergePeerSnapshotAppliesCausalDeleteDespiteOlderTimestamp(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	localLog := opslog.NewLocalOpsLog(filepath.Join(dir, "ops.jsonl"), "dev-a")
	remoteLog := opslog.NewLocalOpsLog(filepath.Join(dir, "remote.jsonl"), "dev-b")

	if err := localLog.Append(opslog.Entry{
		Type:      opslog.Put,
		Path:      "doc.txt",
		Checksum:  "base",
		Chunks:    []string{"c1"},
		Device:    "dev-a",
		Timestamp: 200,
		Seq:       1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := remoteLog.Append(opslog.Entry{
		Type:         opslog.Delete,
		Path:         "doc.txt",
		PrevChecksum: "base",
		Device:       "dev-b",
		Timestamp:    100,
		Seq:          1,
	}); err != nil {
		t.Fatal(err)
	}

	remoteSnap, err := remoteLog.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	merged, err := mergePeerSnapshot(localLog, remoteSnap)
	if err != nil {
		t.Fatal(err)
	}
	if merged != 1 {
		t.Fatalf("merge = %d, want 1", merged)
	}

	localSnap, err := localLog.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := localSnap.Lookup("doc.txt"); ok {
		t.Fatal("doc.txt should be deleted after merge")
	}
	tomb, ok := localSnap.Tombstones()["doc.txt"]
	if !ok {
		t.Fatal("doc.txt tombstone missing after merge")
	}
	if tomb.PrevChecksum != "base" {
		t.Fatalf("tomb prev_checksum = %q, want base", tomb.PrevChecksum)
	}
}
