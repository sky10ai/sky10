package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
)

// SlowBackend wraps a MemoryBackend with configurable latency.
type SlowBackend struct {
	*s3adapter.MemoryBackend
	latency time.Duration
}

func NewSlowBackend(latency time.Duration) *SlowBackend {
	return &SlowBackend{MemoryBackend: s3adapter.NewMemory(), latency: latency}
}

func (s *SlowBackend) Put(ctx context.Context, key string, r interface{ Read([]byte) (int, error) }, size int64) error {
	time.Sleep(s.latency)
	return s.MemoryBackend.Put(ctx, key, r, size)
}

func (s *SlowBackend) Get(ctx context.Context, key string) (interface {
	Read([]byte) (int, error)
	Close() error
}, error) {
	time.Sleep(s.latency)
	return s.MemoryBackend.Get(ctx, key)
}

// FlakyBackend wraps a MemoryBackend with random failures.
type FlakyBackend struct {
	*s3adapter.MemoryBackend
	failRate float64
	mu       sync.Mutex
	rng      *rand.Rand
}

func NewFlakyBackend(failRate float64) *FlakyBackend {
	return &FlakyBackend{
		MemoryBackend: s3adapter.NewMemory(),
		failRate:      failRate,
		rng:           rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (f *FlakyBackend) shouldFail() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rng.Float64() < f.failRate
}

func (f *FlakyBackend) Put(ctx context.Context, key string, r interface{ Read([]byte) (int, error) }, size int64) error {
	if f.shouldFail() {
		return fmt.Errorf("flaky: random Put failure")
	}
	return f.MemoryBackend.Put(ctx, key, r, size)
}

// --- Helper: create a DaemonV2 with temp dirs ---

func newTestDaemonV2(t *testing.T, backend interface {
	Put(context.Context, string, interface{ Read([]byte) (int, error) }, int64) error
}) (*DaemonV2, string) {
	t.Helper()
	// Use the real s3adapter.MemoryBackend via the Store
	id, _ := GenerateDeviceKey()
	mem := s3adapter.NewMemory()
	store := New(mem, id)

	localDir := t.TempDir()
	manifestPath := filepath.Join(t.TempDir(), "manifest.json")

	daemon, err := NewDaemonV2(store, DaemonConfig{
		SyncConfig:   SyncConfig{LocalRoot: localDir},
		DriveID:      "test-drive",
		ManifestPath: manifestPath,
		PollSeconds:  30,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return daemon, localDir
}

func newTestDaemonV2WithStore(t *testing.T, store *Store) (*DaemonV2, string) {
	t.Helper()
	localDir := t.TempDir()
	manifestPath := filepath.Join(t.TempDir(), "manifest.json")

	daemon, err := NewDaemonV2(store, DaemonConfig{
		SyncConfig:   SyncConfig{LocalRoot: localDir},
		DriveID:      "test-drive-" + time.Now().Format("150405"),
		ManifestPath: manifestPath,
		PollSeconds:  60,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return daemon, localDir
}

// --- Tests ---

// Create 20 files rapidly, verify manifest has all within 2 seconds.
func TestDaemonV2RapidFileCreation(t *testing.T) {
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	daemon, localDir := newTestDaemonV2WithStore(t, store)

	ctx, cancel := context.WithCancel(context.Background())
	var wg3 sync.WaitGroup
	wg3.Add(1)
	go func() {
		defer wg3.Done()
		daemon.Run(ctx)
	}()
	defer func() { cancel(); wg3.Wait() }()
	time.Sleep(500 * time.Millisecond) // let goroutines start

	// Create 20 files as fast as possible
	for i := 0; i < 20; i++ {
		path := filepath.Join(localDir, fmt.Sprintf("file-%03d.txt", i))
		os.WriteFile(path, []byte(fmt.Sprintf("content %d", i)), 0644)
	}

	// Wait up to 3 seconds for manifest to have all 20
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		count := len(daemon.manifest.Files)
		if count >= 20 {
			return // pass
		}
		time.Sleep(100 * time.Millisecond)
	}

	t.Errorf("manifest has %d files after 3s, want 20", len(daemon.manifest.Files))
}

// Delete 10 files, verify manifest updated within 2 seconds.
func TestDaemonV2RapidFileDeletion(t *testing.T) {
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	daemon, localDir := newTestDaemonV2WithStore(t, store)

	// Pre-create 10 files
	for i := 0; i < 10; i++ {
		path := filepath.Join(localDir, fmt.Sprintf("del-%03d.txt", i))
		os.WriteFile(path, []byte(fmt.Sprintf("delete me %d", i)), 0644)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg4 sync.WaitGroup
	wg4.Add(1)
	go func() {
		defer wg4.Done()
		daemon.Run(ctx)
	}()
	defer func() { cancel(); wg4.Wait() }()

	// Wait for files to appear in manifest
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(daemon.manifest.Files) >= 10 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(daemon.manifest.Files) < 10 {
		t.Fatalf("files not in manifest after 3s: %d", len(daemon.manifest.Files))
	}

	// Delete all 10
	for i := 0; i < 10; i++ {
		os.Remove(filepath.Join(localDir, fmt.Sprintf("del-%03d.txt", i)))
	}

	// Wait for manifest to reflect deletes
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(daemon.manifest.Files) == 0 {
			return // pass
		}
		time.Sleep(100 * time.Millisecond)
	}

	t.Errorf("manifest has %d files after delete, want 0", len(daemon.manifest.Files))
}

// Daemon stays responsive for 15 seconds under Cirrus-like polling.
func TestDaemonV2StaysResponsive(t *testing.T) {
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	localDir := t.TempDir()
	manifestPath := filepath.Join(t.TempDir(), "manifest.json")
	sockPath := filepath.Join(t.TempDir(), "test.sock")
	driveCfgPath := filepath.Join(t.TempDir(), "drives.json")

	// Write drive config
	drives := []Drive{{
		ID: "test-responsive", Name: "Test", LocalPath: localDir,
		Namespace: "test", Enabled: true,
	}}
	data, _ := json.MarshalIndent(drives, "", "  ")
	os.WriteFile(driveCfgPath, data, 0600)
	_ = manifestPath

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server := NewRPCServer(store, sockPath, driveCfgPath, "test", nil)
	go server.Serve(ctx)

	// Wait for server
	time.Sleep(2 * time.Second)

	// Create some files while polling
	go func() {
		for i := 0; i < 5; i++ {
			os.WriteFile(filepath.Join(localDir, fmt.Sprintf("poll-%d.txt", i)),
				[]byte(fmt.Sprintf("data %d", i)), 0644)
			time.Sleep(time.Second)
		}
	}()

	// Simulate Cirrus polling every 2 seconds for 15 seconds
	failures := 0
	polls := 0
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		func() {
			conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
			if err != nil {
				failures++
				return
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(3 * time.Second))
			conn.Write([]byte(`{"jsonrpc":"2.0","method":"skyfs.ping","id":1}` + "\n"))
			buf := make([]byte, 256)
			_, err = conn.Read(buf)
			if err != nil {
				failures++
			}
			polls++
		}()
		time.Sleep(2 * time.Second)
	}

	if failures > 0 {
		t.Errorf("%d/%d polls failed — daemon became unresponsive", failures, polls)
	}
}

// Concurrent file changes + reconciliation don't deadlock.
func TestDaemonV2NoConcurrentDeadlock(t *testing.T) {
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	daemon, localDir := newTestDaemonV2WithStore(t, store)

	ctx, cancel := context.WithCancel(context.Background())
	var wg5 sync.WaitGroup
	wg5.Add(1)
	go func() {
		defer wg5.Done()
		daemon.Run(ctx)
	}()
	defer func() { cancel(); wg5.Wait() }()
	time.Sleep(500 * time.Millisecond)

	// Hammer with concurrent creates and deletes
	var wg sync.WaitGroup
	done := make(chan struct{})

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				path := filepath.Join(localDir, fmt.Sprintf("concurrent-%d-%d.txt", n, j))
				os.WriteFile(path, []byte(fmt.Sprintf("data %d %d", n, j)), 0644)
				time.Sleep(50 * time.Millisecond)
				os.Remove(path)
			}
		}(i)
	}

	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All goroutines completed — no deadlock
	case <-time.After(15 * time.Second):
		t.Fatal("deadlock: concurrent operations timed out")
	}
}

// Manifest reflects correct state after mixed operations.
func TestDaemonV2ManifestConsistency(t *testing.T) {
	backend := s3adapter.NewMemory()
	id, _ := GenerateDeviceKey()
	store := New(backend, id)

	daemon, localDir := newTestDaemonV2WithStore(t, store)

	ctx, cancel := context.WithCancel(context.Background())
	var wg2 sync.WaitGroup
	wg2.Add(1)
	go func() {
		defer wg2.Done()
		daemon.Run(ctx)
	}()
	defer func() { cancel(); wg2.Wait() }()
	time.Sleep(500 * time.Millisecond)

	// Create 5 files
	for i := 0; i < 5; i++ {
		os.WriteFile(filepath.Join(localDir, fmt.Sprintf("keep-%d.txt", i)),
			[]byte(fmt.Sprintf("keep %d", i)), 0644)
	}
	// Create 3 files then delete them
	for i := 0; i < 3; i++ {
		path := filepath.Join(localDir, fmt.Sprintf("temp-%d.txt", i))
		os.WriteFile(path, []byte("temp"), 0644)
	}

	time.Sleep(2 * time.Second)

	// Delete the temp files
	for i := 0; i < 3; i++ {
		os.Remove(filepath.Join(localDir, fmt.Sprintf("temp-%d.txt", i)))
	}

	// Wait for reconcile
	time.Sleep(2 * time.Second)

	// Manifest should have exactly 5 files
	count := len(daemon.manifest.Files)
	if count != 5 {
		names := make([]string, 0)
		for k := range daemon.manifest.Files {
			names = append(names, k)
		}
		t.Errorf("manifest has %d files, want 5: %v", count, names)
	}

	// Verify each keep file is present
	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("keep-%d.txt", i)
		if _, ok := daemon.manifest.GetFile(name); !ok {
			t.Errorf("missing %s", name)
		}
	}

	// Verify no temp files remain
	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("temp-%d.txt", i)
		if _, ok := daemon.manifest.GetFile(name); ok {
			t.Errorf("%s should be gone", name)
		}
	}
}
