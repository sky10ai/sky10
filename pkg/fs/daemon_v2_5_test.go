package fs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestDaemonV25SetsPerDriveReconcilerStagingDir(t *testing.T) {
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	cfg := DaemonConfig{
		SyncConfig:  SyncConfig{LocalRoot: localDir},
		DriveID:     "test_reconcile_staging",
		PollSeconds: 300,
	}
	daemon, err := NewDaemonV2_5(store, cfg, nil)
	if err != nil {
		t.Fatalf("creating daemon: %v", err)
	}

	want := transferStagingDir(driveDataDir(cfg.DriveID))
	if daemon.reconciler.stagingDir != want {
		t.Fatalf("reconciler staging dir = %q, want %q", daemon.reconciler.stagingDir, want)
	}
}

func TestDaemonV25CreatesTransferWorkspaceDirs(t *testing.T) {
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	cfg := DaemonConfig{
		SyncConfig:  SyncConfig{LocalRoot: localDir},
		DriveID:     "test_transfer_workspace",
		PollSeconds: 300,
	}
	daemon, err := NewDaemonV2_5(store, cfg, nil)
	if err != nil {
		t.Fatalf("creating daemon: %v", err)
	}

	for _, dir := range []string{
		transferStagingDir(daemon.driveDir),
		transferObjectsDir(daemon.driveDir),
		transferSessionsDir(daemon.driveDir),
	} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat %s: %v", dir, err)
		}
		if !info.IsDir() {
			t.Fatalf("%s should be a directory", dir)
		}
	}
}

func TestDaemonV25SyncOnceCleansStaleStagingFiles(t *testing.T) {
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	cfg := DaemonConfig{
		SyncConfig:  SyncConfig{LocalRoot: localDir},
		DriveID:     "test_transfer_cleanup",
		PollSeconds: 300,
	}
	daemon, err := NewDaemonV2_5(store, cfg, nil)
	if err != nil {
		t.Fatalf("creating daemon: %v", err)
	}

	stagingDir := transferStagingDir(daemon.driveDir)
	stalePath := filepath.Join(stagingDir, "stale-upload.tmp")
	if err := os.WriteFile(stalePath, []byte("stale"), 0600); err != nil {
		t.Fatalf("write stale file: %v", err)
	}

	daemon.SyncOnce(context.Background())

	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Fatalf("stale staging file should be removed on startup, stat err=%v", err)
	}
}

func TestDaemonV25SyncOnceRepublishesStagedTransferFile(t *testing.T) {
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	cfg := DaemonConfig{
		SyncConfig:  SyncConfig{LocalRoot: localDir},
		DriveID:     "test_transfer_republish",
		PollSeconds: 300,
	}
	daemon, err := NewDaemonV2_5(store, cfg, nil)
	if err != nil {
		t.Fatalf("creating daemon: %v", err)
	}

	tmpFile, tmpPath, err := createStagingTempFile(transferStagingDir(daemon.driveDir), "recover-*")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if _, err := tmpFile.WriteString("publish me"); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	if err := tmpFile.Close(); err != nil {
		t.Fatalf("close temp file: %v", err)
	}

	targetPath := filepath.Join(localDir, "republished.txt")
	session, err := newTransferSession(transferSessionsDir(daemon.driveDir), "download", tmpPath, targetPath)
	if err != nil {
		t.Fatalf("new transfer session: %v", err)
	}
	if err := session.markStaged(); err != nil {
		t.Fatalf("mark staged: %v", err)
	}

	daemon.SyncOnce(context.Background())

	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(data) != "publish me" {
		t.Fatalf("content = %q", string(data))
	}
	if _, err := os.Stat(session.path); !os.IsNotExist(err) {
		t.Fatalf("session path should be removed after republish, stat err=%v", err)
	}
}

func TestDaemonV25PeriodicScanFindsMissedLocalFile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	cfg := DaemonConfig{
		SyncConfig:       SyncConfig{LocalRoot: localDir},
		DriveID:          "test_periodic_scan",
		PollSeconds:      300,
		ScanSeconds:      1,
		ReconcileSeconds: 300,
	}
	daemon, err := NewDaemonV2_5(store, cfg, nil)
	if err != nil {
		t.Fatalf("creating daemon: %v", err)
	}

	stop := runDaemon(ctx, cancel, daemon)
	defer stop()

	if err := daemon.watcher.Close(); err != nil {
		t.Fatalf("close watcher: %v", err)
	}

	target := filepath.Join(localDir, "scan-only.txt")
	if err := os.WriteFile(target, []byte("found by scan"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := daemon.localLog.Lookup("scan-only.txt"); ok {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("periodic scan did not queue missed local file")
}

func TestDaemonV25PeriodicReconcileDownloadsRemoteWithoutPoke(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	cfg := DaemonConfig{
		SyncConfig:       SyncConfig{LocalRoot: localDir},
		DriveID:          "test_periodic_reconcile",
		PollSeconds:      300,
		ScanSeconds:      300,
		ReconcileSeconds: 1,
	}
	daemon, err := NewDaemonV2_5(store, cfg, nil)
	if err != nil {
		t.Fatalf("creating daemon: %v", err)
	}

	stop := runDaemon(ctx, cancel, daemon)
	defer stop()

	nsDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(nsDeadline) {
		if daemon.store.nsID != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if daemon.store.nsID == "" {
		t.Fatal("daemon namespace ID not resolved in time")
	}

	if err := daemon.store.Put(ctx, "remote-only.txt", strings.NewReader("from periodic reconcile")); err != nil {
		t.Fatalf("store.Put: %v", err)
	}
	res := daemon.store.LastPutResult()
	if res == nil {
		t.Fatal("LastPutResult nil")
	}
	if err := daemon.localLog.Append(opslog.Entry{
		Type:      opslog.Put,
		Path:      "remote-only.txt",
		Chunks:    res.Chunks,
		Checksum:  res.Checksum,
		Size:      res.Size,
		Namespace: NamespaceFromPath("remote-only.txt"),
		Device:    "remote-device",
		Timestamp: time.Now().Unix(),
		Seq:       1,
	}); err != nil {
		t.Fatalf("append remote op: %v", err)
	}

	target := filepath.Join(localDir, "remote-only.txt")
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(target)
		if err == nil && string(data) == "from periodic reconcile" {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("periodic reconcile did not materialize remote file")
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

	// Seed queues the two files to outbox. Drain the outbox so they
	// appear in the local log (upload-then-record).
	daemon.seedStateFromDisk()
	daemon.outboxWorker.drain(ctx)

	if _, ok := daemon.localLog.Lookup("subdir/a.txt"); !ok {
		t.Fatal("subdir/a.txt not in local log after outbox drain")
	}
	if _, ok := daemon.localLog.Lookup("subdir/b.txt"); !ok {
		t.Fatal("subdir/b.txt not in local log after outbox drain")
	}

	// Delete the directory
	os.RemoveAll(subDir)

	// Re-run seed — should detect files in CRDT but not on disk as local deletes.
	daemon.seedStateFromDisk()

	// After seed, local log should no longer have the files
	if _, ok := daemon.localLog.Lookup("subdir/a.txt"); ok {
		t.Error("subdir/a.txt still in local log after seed with deleted dir")
	}
	if _, ok := daemon.localLog.Lookup("subdir/b.txt"); ok {
		t.Error("subdir/b.txt still in local log after seed with deleted dir")
	}
	cancel()
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

	// Upload-then-record: new files go to outbox only, not local log.
	if _, ok := daemon.localLog.Lookup("a.txt"); ok {
		t.Error("a.txt should NOT be in local log before outbox drain")
	}

	// Both should be in the outbox
	entries, _ := daemon.outbox.ReadAll()
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

// Seed treats ALL files in CRDT but not on disk as local deletes.
// In the snapshot-exchange architecture, the local CRDT is the merge base:
// if a file was known and is now gone from disk, the user deleted it.
// Remote state is merged AFTER seed, so remote adds will be re-introduced
// by the snapshot poller.
func TestSeedDeletesMissingFiles(t *testing.T) {
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	localDir := filepath.Join(tmpDir, "sync")
	os.MkdirAll(localDir, 0755)

	cfg := DaemonConfig{
		SyncConfig:  SyncConfig{LocalRoot: localDir},
		DriveID:     "test_seed_deletes",
		PollSeconds: 300,
	}
	daemon, err := NewDaemonV2_5(store, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a file in the local log (regardless of device origin)
	daemon.localLog.Append(opslog.Entry{
		Type: opslog.Put, Path: "was-here.txt", Checksum: "h1",
		Device: "other-device", Timestamp: 100, Seq: 1,
	})

	// File is in CRDT but NOT on disk
	if _, ok := daemon.localLog.Lookup("was-here.txt"); !ok {
		t.Fatal("was-here.txt should be in snapshot")
	}

	daemon.seedStateFromDisk()

	// Seed should have written a delete — file was in CRDT but gone from disk
	if _, ok := daemon.localLog.Lookup("was-here.txt"); ok {
		t.Error("was-here.txt should be deleted from CRDT after seed")
	}
}

// Migration: state.json → ops.jsonl on first V3 startup.
func TestMigrateStateToOpsLog(t *testing.T) {
	tmpDir := t.TempDir()
	driveDir := filepath.Join(tmpDir, "drive")
	os.MkdirAll(driveDir, 0700)

	// Write a V2.5-style state.json
	state := map[string]interface{}{
		"last_remote_op": 500,
		"files": map[string]interface{}{
			"a.txt": map[string]string{"checksum": "h1", "namespace": "Test"},
			"b.txt": map[string]string{"checksum": "h2", "namespace": "Test"},
		},
	}
	data, _ := json.Marshal(state)
	os.WriteFile(filepath.Join(driveDir, "state.json"), data, 0600)

	// Run migration
	migrateStateToOpsLog(driveDir, "dev-a", nil)

	// ops.jsonl should exist and have the files
	localLog := opslog.NewLocalOpsLog(filepath.Join(driveDir, "ops.jsonl"), "dev-a")
	snap, err := localLog.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snap.Len() != 2 {
		t.Fatalf("snapshot has %d files, want 2", snap.Len())
	}
	if fi, ok := snap.Lookup("a.txt"); !ok || fi.Checksum != "h1" {
		t.Error("a.txt not migrated correctly")
	}
	if fi, ok := snap.Lookup("b.txt"); !ok || fi.Checksum != "h2" {
		t.Error("b.txt not migrated correctly")
	}

	// LastRemoteOp cursor is not migrated (poller re-reads from S3 once).
	// This is by design — the cursor is in-memory only.
}

// Migration is a no-op when ops.jsonl already exists.
func TestMigrateSkipsIfOpsLogExists(t *testing.T) {
	tmpDir := t.TempDir()
	driveDir := filepath.Join(tmpDir, "drive")
	os.MkdirAll(driveDir, 0700)

	// Write both state.json and ops.jsonl
	state := map[string]interface{}{
		"files": map[string]interface{}{
			"old.txt": map[string]string{"checksum": "old"},
		},
	}
	data, _ := json.Marshal(state)
	os.WriteFile(filepath.Join(driveDir, "state.json"), data, 0600)

	// Pre-existing ops.jsonl with different content
	localLog := opslog.NewLocalOpsLog(filepath.Join(driveDir, "ops.jsonl"), "dev-a")
	localLog.AppendLocal(opslog.Entry{
		Type: opslog.Put, Path: "new.txt", Checksum: "new",
	})

	// Migration should be a no-op
	migrateStateToOpsLog(driveDir, "dev-a", nil)

	// Should still have new.txt, not old.txt
	snap, _ := localLog.Snapshot()
	if _, ok := snap.Lookup("old.txt"); ok {
		t.Error("migration should not overwrite existing ops.jsonl")
	}
	if _, ok := snap.Lookup("new.txt"); !ok {
		t.Error("new.txt should still be in ops.jsonl")
	}
}
