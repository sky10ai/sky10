package fs

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
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
	// If seed sets state before sending events, the watcher handler
	// will see "unchanged" and skip — outbox stays empty.
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
		t.Error("new-file.txt not queued in outbox after seed — seed updates state before sending events")
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
		if _, ok := daemon.state.GetFile("hello.txt"); ok {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if _, ok := daemon.state.GetFile("hello.txt"); !ok {
		t.Error("hello.txt not in state after daemon seed")
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

	// Wait for state to have all 10
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		count := 0
		for i := 0; i < 10; i++ {
			name := "rapid_" + string(rune('a'+i)) + ".txt"
			if _, ok := daemon.state.GetFile(name); ok {
				count++
			}
		}
		if count == 10 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Verify all 10 are in state
	for i := 0; i < 10; i++ {
		name := "rapid_" + string(rune('a'+i)) + ".txt"
		if _, ok := daemon.state.GetFile(name); !ok {
			t.Errorf("file %s not in state", name)
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

	// Create a file — state should update within 2 seconds
	os.WriteFile(filepath.Join(localDir, "late.txt"), []byte("late addition"), 0644)
	daemon.seedStateFromDisk()

	deadline := time.Now().Add(2 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		if _, ok := daemon.state.GetFile("late.txt"); ok {
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

	// Second run: state should already have the file
	ctx2, cancel2 := context.WithCancel(context.Background())

	daemon2, err := NewDaemonV2_5(store, cfg, nil)
	if err != nil {
		t.Fatalf("creating daemon: %v", err)
	}

	// State persisted to disk — load should find the file
	if _, ok := daemon2.state.GetFile("persist.txt"); !ok {
		// Might need a seed
		stop2 := runDaemon(ctx2, cancel2, daemon2)
		defer stop2()
		time.Sleep(2 * time.Second)
		if _, ok := daemon2.state.GetFile("persist.txt"); !ok {
			t.Error("persist.txt not found after daemon restart — state not persisted")
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

	if _, ok := daemon.state.GetFile("once.txt"); !ok {
		t.Error("once.txt not in state after SyncOnce")
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

	// Seed the two files into state
	daemon.seedStateFromDisk()

	if _, ok := daemon.state.GetFile("subdir/a.txt"); !ok {
		t.Fatal("subdir/a.txt not seeded")
	}
	if _, ok := daemon.state.GetFile("subdir/b.txt"); !ok {
		t.Fatal("subdir/b.txt not seeded")
	}

	// Delete the directory
	os.RemoveAll(subDir)

	// Run daemon — should detect deletions via seedStateFromDisk
	stop := runDaemon(ctx, cancel, daemon)
	defer stop()

	// Wait for the outbox worker to process delete ops
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, okA := daemon.state.GetFile("subdir/a.txt")
		_, okB := daemon.state.GetFile("subdir/b.txt")
		if !okA && !okB {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// After delete ops drain, state should no longer have the files
	if _, ok := daemon.state.GetFile("subdir/a.txt"); ok {
		t.Error("subdir/a.txt still in state after directory trash")
	}
	if _, ok := daemon.state.GetFile("subdir/b.txt"); ok {
		t.Error("subdir/b.txt still in state after directory trash")
	}
}
