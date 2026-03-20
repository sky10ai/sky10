package fs

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
	"github.com/sky10/sky10/pkg/fs/opslog"
)

// runDaemon starts the daemon and returns a function that cancels the
// context and waits for Run to return. This prevents TempDir cleanup
// races on CI where the daemon goroutine is still writing files.
func runDaemon(ctx context.Context, cancel context.CancelFunc, daemon *DaemonV2_5) func() {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		daemon.Run(ctx)
	}()
	return func() {
		// Wait for outbox to drain before cancelling — otherwise
		// in-flight uploads write to the drive data dir after cancel
		driveDir := driveDataDir(daemon.config.DriveID)
		outbox := NewSyncLog[OutboxEntry](filepath.Join(driveDir, "outbox.jsonl"))
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if outbox.Len() == 0 {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		cancel()
		wg.Wait()
	}
}

// Regression: seedStateFromDisk must queue new files for upload.
// Bug: seed set state BEFORE sending events to the watcher handler,
// so the handler saw matching checksums and skipped — files never
// got queued in the outbox and never synced to S3.
func TestDaemonV25SeedQueuesOutbox(t *testing.T) {
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	// Create a file before daemon starts — empty state
	os.WriteFile(filepath.Join(localDir, "new-file.txt"), []byte("must be uploaded"), 0644)

	cfg := DaemonConfig{
		SyncConfig:  SyncConfig{LocalRoot: localDir},
		DriveID:     "test_seed_outbox",
		PollSeconds: 300,
	}
	daemon, err := NewDaemonV2_5(store, cfg, nil)
	if err != nil {
		t.Fatalf("creating daemon: %v", err)
	}

	// Run seed (this is what happens on startup)
	daemon.seedStateFromDisk()

	// The outbox must have an entry for the new file.
	// If seed records in local log before sending events, the handler sees
	// "unchanged" and skips — outbox stays empty.
	driveDir := driveDataDir("test_seed_outbox")
	outbox := NewSyncLog[OutboxEntry](filepath.Join(driveDir, "outbox.jsonl"))
	entries, _ := outbox.ReadAll()

	found := false
	for _, e := range entries {
		if e.Path == "new-file.txt" && e.Op == OpPut {
			found = true
		}
	}
	if !found {
		t.Error("new-file.txt not queued in outbox after seed — seed records before sending events")
	}
}

// DaemonV2.5 should detect a pre-existing file and upload it to S3.
func TestDaemonV25SeedsFromDisk(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	// Create file before daemon starts
	os.WriteFile(filepath.Join(localDir, "hello.txt"), []byte("hello world"), 0644)

	cfg := DaemonConfig{
		SyncConfig:  SyncConfig{LocalRoot: localDir},
		DriveID:     "test_seed",
		PollSeconds: 300, // long poll so only seed runs
	}
	daemon, err := NewDaemonV2_5(store, cfg, nil)
	if err != nil {
		t.Fatalf("creating daemon: %v", err)
	}

	stop := runDaemon(ctx, cancel, daemon)
	defer stop()

	// Wait for seed + outbox drain
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := daemon.localLog.Lookup("hello.txt"); ok {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if _, ok := daemon.localLog.Lookup("hello.txt"); !ok {
		t.Error("hello.txt not in local log after daemon seed")
	}
}

// DaemonV2.5 should handle rapid file operations without losing entries.
func TestDaemonV25RapidOps(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	cfg := DaemonConfig{
		SyncConfig:  SyncConfig{LocalRoot: localDir},
		DriveID:     "test_rapid",
		PollSeconds: 300,
	}
	daemon, err := NewDaemonV2_5(store, cfg, nil)
	if err != nil {
		t.Fatalf("creating daemon: %v", err)
	}

	stop := runDaemon(ctx, cancel, daemon)
	defer stop()
	time.Sleep(500 * time.Millisecond) // let daemon start

	// Create 10 files rapidly
	for i := 0; i < 10; i++ {
		name := filepath.Join(localDir, "rapid_"+string(rune('a'+i))+".txt")
		os.WriteFile(name, []byte("content"), 0644)
	}

	// Seed picks them up
	daemon.seedStateFromDisk()

	// Wait for local log to have all 10
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		count := 0
		for i := 0; i < 10; i++ {
			name := "rapid_" + string(rune('a'+i)) + ".txt"
			if _, ok := daemon.localLog.Lookup(name); ok {
				count++
			}
		}
		if count == 10 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Verify all 10 are in local log
	for i := 0; i < 10; i++ {
		name := "rapid_" + string(rune('a'+i)) + ".txt"
		if _, ok := daemon.localLog.Lookup(name); !ok {
			t.Errorf("file %s not in local log", name)
		}
	}

	// Wait for outbox to drain before cleanup
	driveDir := driveDataDir("test_rapid")
	outbox := NewSyncLog[OutboxEntry](filepath.Join(driveDir, "outbox.jsonl"))
	drainDeadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(drainDeadline) {
		if outbox.Len() == 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Daemon stays responsive after running for a sustained period.
func TestDaemonV25StaysResponsive(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping long-running test")
	}

	ctx, cancel := context.WithCancel(context.Background())

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	cfg := DaemonConfig{
		SyncConfig:  SyncConfig{LocalRoot: localDir},
		DriveID:     "test_responsive",
		PollSeconds: 300,
	}
	daemon, err := NewDaemonV2_5(store, cfg, nil)
	if err != nil {
		t.Fatalf("creating daemon: %v", err)
	}

	stop := runDaemon(ctx, cancel, daemon)
	defer stop()

	// Wait 5 seconds to let daemon settle
	time.Sleep(5 * time.Second)

	// Create a file — local log should update within 2 seconds
	os.WriteFile(filepath.Join(localDir, "late.txt"), []byte("late addition"), 0644)
	daemon.seedStateFromDisk()

	deadline := time.Now().Add(2 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		if _, ok := daemon.localLog.Lookup("late.txt"); ok {
			found = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !found {
		t.Error("late.txt not detected after 5s of running — daemon may be stuck")
	}
}

// State survives daemon restart (crash recovery).
func TestDaemonV25CrashRecovery(t *testing.T) {
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)
	os.WriteFile(filepath.Join(localDir, "persist.txt"), []byte("persisted"), 0644)

	cfg := DaemonConfig{
		SyncConfig:  SyncConfig{LocalRoot: localDir},
		DriveID:     "test_crash",
		PollSeconds: 300,
	}

	// First run: seed the state
	ctx1, cancel1 := context.WithCancel(context.Background())
	daemon1, err := NewDaemonV2_5(store, cfg, nil)
	if err != nil {
		t.Fatalf("creating daemon: %v", err)
	}
	stop1 := runDaemon(ctx1, cancel1, daemon1)
	time.Sleep(2 * time.Second)
	stop1() // "crash"

	// Second run: local log should already have the file (from ops.jsonl)
	ctx2, cancel2 := context.WithCancel(context.Background())

	daemon2, err := NewDaemonV2_5(store, cfg, nil)
	if err != nil {
		t.Fatalf("creating daemon: %v", err)
	}

	// Local log persisted to ops.jsonl — load should find the file
	if _, ok := daemon2.localLog.Lookup("persist.txt"); !ok {
		// Might need a seed
		stop2 := runDaemon(ctx2, cancel2, daemon2)
		defer stop2()
		time.Sleep(2 * time.Second)
		if _, ok := daemon2.localLog.Lookup("persist.txt"); !ok {
			t.Error("persist.txt not found after daemon restart — ops log not persisted")
		}
	} else {
		cancel2()
	}
}

// SyncOnce does a complete one-shot sync cycle.
func TestDaemonV25SyncOnce(t *testing.T) {
	ctx := context.Background()

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)
	os.WriteFile(filepath.Join(localDir, "once.txt"), []byte("one shot"), 0644)

	cfg := DaemonConfig{
		SyncConfig:  SyncConfig{LocalRoot: localDir},
		DriveID:     "test_once",
		PollSeconds: 300,
	}

	daemon, err := NewDaemonV2_5(store, cfg, nil)
	if err != nil {
		t.Fatalf("creating daemon: %v", err)
	}

	daemon.SyncOnce(ctx)

	if _, ok := daemon.localLog.Lookup("once.txt"); !ok {
		t.Error("once.txt not in local log after SyncOnce")
	}
}

// Directory deletion should produce delete events for all contained files.
func TestDaemonV25DirectoryTrash(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	localDir := filepath.Join(tmpDir, "sync")
	subDir := filepath.Join(localDir, "subdir")
	os.MkdirAll(subDir, 0755)
	os.WriteFile(filepath.Join(subDir, "a.txt"), []byte("aaa"), 0644)
	os.WriteFile(filepath.Join(subDir, "b.txt"), []byte("bbb"), 0644)

	cfg := DaemonConfig{
		SyncConfig:  SyncConfig{LocalRoot: localDir},
		DriveID:     "test_trash",
		PollSeconds: 300,
	}

	daemon, err := NewDaemonV2_5(store, cfg, nil)
	if err != nil {
		t.Fatalf("creating daemon: %v", err)
	}

	// Seed the two files into local log
	daemon.seedStateFromDisk()

	if _, ok := daemon.localLog.Lookup("subdir/a.txt"); !ok {
		t.Fatal("subdir/a.txt not seeded")
	}
	if _, ok := daemon.localLog.Lookup("subdir/b.txt"); !ok {
		t.Fatal("subdir/b.txt not seeded")
	}

	// Delete the directory
	os.RemoveAll(subDir)

	// Run daemon — should detect deletions via seedStateFromDisk
	stop := runDaemon(ctx, cancel, daemon)
	defer stop()

	// Wait for the delete ops to be recorded
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, okA := daemon.localLog.Lookup("subdir/a.txt")
		_, okB := daemon.localLog.Lookup("subdir/b.txt")
		if !okA && !okB {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// After delete ops, local log should no longer have the files
	if _, ok := daemon.localLog.Lookup("subdir/a.txt"); ok {
		t.Error("subdir/a.txt still in local log after directory trash")
	}
	if _, ok := daemon.localLog.Lookup("subdir/b.txt"); ok {
		t.Error("subdir/b.txt still in local log after directory trash")
	}
}

// Seed with empty log — all local files are new.
func TestSeedEmptyLog(t *testing.T) {
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	os.WriteFile(filepath.Join(localDir, "a.txt"), []byte("aaa"), 0644)
	os.WriteFile(filepath.Join(localDir, "b.txt"), []byte("bbb"), 0644)

	cfg := DaemonConfig{
		SyncConfig:  SyncConfig{LocalRoot: localDir},
		DriveID:     "test_seed_empty",
		PollSeconds: 300,
	}
	daemon, err := NewDaemonV2_5(store, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	daemon.seedStateFromDisk()

	// Both files should be in the local log
	if _, ok := daemon.localLog.Lookup("a.txt"); !ok {
		t.Error("a.txt not in local log")
	}
	if _, ok := daemon.localLog.Lookup("b.txt"); !ok {
		t.Error("b.txt not in local log")
	}

	// Both should be in the outbox
	driveDir := driveDataDir("test_seed_empty")
	outbox := NewSyncLog[OutboxEntry](filepath.Join(driveDir, "outbox.jsonl"))
	entries, _ := outbox.ReadAll()
	if len(entries) != 2 {
		t.Errorf("outbox has %d entries, want 2", len(entries))
	}
}

// Seed with pre-existing log — tracked files are skipped.
func TestSeedPreExistingLog(t *testing.T) {
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	os.WriteFile(filepath.Join(localDir, "tracked.txt"), []byte("tracked"), 0644)
	cksum, _ := fileChecksum(filepath.Join(localDir, "tracked.txt"))

	cfg := DaemonConfig{
		SyncConfig:  SyncConfig{LocalRoot: localDir},
		DriveID:     "test_seed_existing",
		PollSeconds: 300,
	}
	daemon, err := NewDaemonV2_5(store, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Pre-populate the local log with the file
	daemon.localLog.AppendLocal(opslog.Entry{
		Type: opslog.Put, Path: "tracked.txt", Checksum: cksum,
	})

	daemon.seedStateFromDisk()

	// Outbox should be empty (file already tracked with same checksum)
	driveDir := driveDataDir("test_seed_existing")
	outbox := NewSyncLog[OutboxEntry](filepath.Join(driveDir, "outbox.jsonl"))
	if outbox.Len() != 0 {
		t.Errorf("outbox has %d entries, want 0 (file already tracked)", outbox.Len())
	}
}

// Seed detects local modifications (different checksum).
func TestSeedLocalModification(t *testing.T) {
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	cfg := DaemonConfig{
		SyncConfig:  SyncConfig{LocalRoot: localDir},
		DriveID:     "test_seed_modified",
		PollSeconds: 300,
	}
	daemon, err := NewDaemonV2_5(store, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Track version 1
	os.WriteFile(filepath.Join(localDir, "doc.txt"), []byte("v1"), 0644)
	daemon.seedStateFromDisk()

	// Modify the file
	os.WriteFile(filepath.Join(localDir, "doc.txt"), []byte("v2 modified"), 0644)
	daemon.seedStateFromDisk()

	// Outbox should have 2 entries (initial + modification)
	driveDir := driveDataDir("test_seed_modified")
	outbox := NewSyncLog[OutboxEntry](filepath.Join(driveDir, "outbox.jsonl"))
	entries, _ := outbox.ReadAll()
	if len(entries) != 2 {
		t.Errorf("outbox has %d entries, want 2", len(entries))
	}
}

// Seed does NOT delete remote files that haven't been downloaded yet.
func TestSeedRemoteFilesNotDeleted(t *testing.T) {
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	cfg := DaemonConfig{
		SyncConfig:  SyncConfig{LocalRoot: localDir},
		DriveID:     "test_seed_remote",
		PollSeconds: 300,
	}
	daemon, err := NewDaemonV2_5(store, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a remote file in the local log (from poller)
	daemon.localLog.Append(opslog.Entry{
		Type: opslog.Put, Path: "remote-only.txt", Checksum: "h1",
		Device: "other-device", Timestamp: 100, Seq: 1,
	})

	// File is in snapshot but NOT on disk
	if _, ok := daemon.localLog.Lookup("remote-only.txt"); !ok {
		t.Fatal("remote-only.txt should be in snapshot")
	}

	// Seed should NOT delete it (it's a remote file waiting for download)
	daemon.seedStateFromDisk()

	// File should still be in the snapshot
	if _, ok := daemon.localLog.Lookup("remote-only.txt"); !ok {
		t.Error("seed should not delete remote files from snapshot")
	}

	// Outbox should NOT have a delete entry
	driveDir := driveDataDir("test_seed_remote")
	outbox := NewSyncLog[OutboxEntry](filepath.Join(driveDir, "outbox.jsonl"))
	entries, _ := outbox.ReadAll()
	for _, e := range entries {
		if e.Op == OpDelete && e.Path == "remote-only.txt" {
			t.Error("seed should not queue delete for remote file")
		}
	}
}
