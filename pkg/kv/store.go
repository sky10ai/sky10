package kv

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/sky10/sky10/pkg/adapter"
	skykey "github.com/sky10/sky10/pkg/key"
)

// MaxValueSize is the maximum inline value size for v1.
const MaxValueSize = 4096

// ErrValueTooLarge is returned when a value exceeds MaxValueSize.
var ErrValueTooLarge = errors.New("value exceeds 4KB limit")

// Config holds KV store configuration.
type Config struct {
	Namespace    string        // KV namespace name
	DataDir      string        // local data directory (ops log, baselines)
	PollInterval time.Duration // remote snapshot poll interval
}

// Store is the main KV store. It provides Get/Set/Delete/List and manages
// snapshot exchange with remote devices via S3.
type Store struct {
	backend  adapter.Backend
	identity *skykey.Key
	deviceID string
	config   Config
	logger   *slog.Logger

	localLog  *LocalLog
	uploader  *Uploader
	poller    *Poller
	baselines *BaselineStore

	mu       sync.Mutex
	nsKey    []byte // namespace encryption key (resolved lazily)
	nsID     string // opaque namespace ID
	notifier func(namespace string)
	p2pSync  *P2PSync // optional: P2P snapshot sync
}

// New creates a new KV store.
func New(
	backend adapter.Backend,
	identity *skykey.Key,
	config Config,
	logger *slog.Logger,
) *Store {
	if logger == nil {
		logger = slog.Default()
	}
	if config.PollInterval == 0 {
		config.PollInterval = 30 * time.Second
	}

	deviceID := ShortDeviceID(identity)

	dataDir := config.DataDir
	if dataDir == "" {
		home, _ := os.UserHomeDir()
		dataDir = filepath.Join(home, ".sky10", "kv", "stores", config.Namespace)
	}
	os.MkdirAll(dataDir, 0700)

	localLog := NewLocalLog(filepath.Join(dataDir, "kv-ops.jsonl"), deviceID)
	baselines := NewBaselineStore(filepath.Join(dataDir, "baselines"))

	return &Store{
		backend:   backend,
		identity:  identity,
		deviceID:  deviceID,
		config:    config,
		logger:    logger,
		localLog:  localLog,
		baselines: baselines,
	}
}

// Set stores a key-value pair. Appends to local log and triggers sync.
func (s *Store) Set(ctx context.Context, key string, value []byte) error {
	if len(value) > MaxValueSize {
		return ErrValueTooLarge
	}
	if err := s.localLog.AppendLocal(Entry{
		Type:      Set,
		Key:       key,
		Value:     value,
		Namespace: s.nsID,
	}); err != nil {
		return fmt.Errorf("kv set: %w", err)
	}
	s.pokeSync(ctx)
	return nil
}

// Get returns the value for a key, or nil/false if not found.
func (s *Store) Get(key string) ([]byte, bool) {
	vi, ok := s.localLog.Lookup(key)
	if !ok {
		return nil, false
	}
	return vi.Value, true
}

// Delete removes a key. Appends delete to local log and triggers sync.
func (s *Store) Delete(ctx context.Context, key string) error {
	if err := s.localLog.AppendLocal(Entry{
		Type:      Delete,
		Key:       key,
		Namespace: s.nsID,
	}); err != nil {
		return fmt.Errorf("kv delete: %w", err)
	}
	s.pokeSync(ctx)
	return nil
}

// pokeSync triggers the appropriate sync mechanism (S3 upload and/or P2P push).
func (s *Store) pokeSync(ctx context.Context) {
	if s.uploader != nil {
		s.uploader.Poke()
	}
	s.mu.Lock()
	p2p := s.p2pSync
	s.mu.Unlock()
	if p2p != nil {
		go p2p.PushToAll(ctx)
	}
}

// List returns all keys with the given prefix, sorted.
func (s *Store) List(prefix string) []string {
	snap, err := s.localLog.Snapshot()
	if err != nil {
		return nil
	}
	if prefix == "" {
		return snap.Keys()
	}
	return snap.KeysWithPrefix(prefix)
}

// GetAll returns all key-value pairs with the given prefix.
func (s *Store) GetAll(prefix string) map[string][]byte {
	snap, err := s.localLog.Snapshot()
	if err != nil {
		return nil
	}
	result := make(map[string][]byte)
	for key, vi := range snap.Entries() {
		if prefix == "" || len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			result[key] = vi.Value
		}
	}
	return result
}

// Run resolves the namespace key, then starts sync goroutines.
// With an S3 backend: polls remote snapshots and uploads local changes.
// Without S3 (P2P-only): waits for P2P sync pushes from connected peers.
// Blocks until ctx is cancelled.
func (s *Store) Run(ctx context.Context) error {
	if err := s.resolveKeys(ctx); err != nil {
		return fmt.Errorf("resolving kv namespace key: %w", err)
	}

	// S3-free mode: keys resolved locally, sync via P2P only.
	if s.backend == nil {
		s.logger.Info("kv store running in P2P-only mode")
		<-ctx.Done()
		return nil
	}

	s.uploader = NewUploader(s.backend, s.localLog, s.deviceID, s.nsID, s.nsKey, s.logger)
	s.uploader.onUpload = func() {
		s.mu.Lock()
		notify := s.notifier
		p2p := s.p2pSync
		s.mu.Unlock()
		if notify != nil {
			notify(s.config.Namespace)
		}
		// Also push to connected peers for faster convergence.
		if p2p != nil {
			go p2p.PushToAll(context.Background())
		}
	}
	s.poller = NewPoller(s.backend, s.localLog, s.deviceID, s.nsID, s.nsKey, s.config.PollInterval, s.baselines, s.logger)
	s.poller.onChange = s.uploader.Poke

	// Initial sync: poll remote → upload local
	s.poller.pollOnce(ctx)
	if err := s.uploader.Upload(ctx); err != nil {
		s.logger.Warn("kv initial upload failed", "error", err)
	}

	// Start goroutines
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); s.uploader.Run(ctx) }()
	go func() { defer wg.Done(); s.poller.Run(ctx) }()
	wg.Wait()

	return nil
}

// SyncOnce performs a single poll + upload cycle.
func (s *Store) SyncOnce(ctx context.Context) error {
	if s.nsKey == nil {
		if err := s.resolveKeys(ctx); err != nil {
			return err
		}
	}

	if s.poller == nil {
		s.poller = NewPoller(s.backend, s.localLog, s.deviceID, s.nsID, s.nsKey, s.config.PollInterval, s.baselines, s.logger)
	}
	if s.uploader == nil {
		s.uploader = NewUploader(s.backend, s.localLog, s.deviceID, s.nsID, s.nsKey, s.logger)
	}

	s.poller.pollOnce(ctx)
	return s.uploader.Upload(ctx)
}

// Close is a no-op for now — Run exits when its context is cancelled.
func (s *Store) Close() {}

// SetNotifier registers a callback invoked after each successful S3 upload.
// Used by skylink to push sync notifications to connected peers.
// The notification fires AFTER data is on S3, so remote peers can poll
// immediately and find the new snapshot.
func (s *Store) SetNotifier(fn func(namespace string)) {
	s.mu.Lock()
	s.notifier = fn
	s.mu.Unlock()
}

// SetP2PSync attaches a P2P sync handler for direct peer-to-peer KV exchange.
func (s *Store) SetP2PSync(sync *P2PSync) {
	s.mu.Lock()
	s.p2pSync = sync
	s.mu.Unlock()
}

// Poke triggers an immediate poll of remote snapshots.
func (s *Store) Poke() {
	if s.poller != nil {
		s.poller.Poke()
	}
}

// Snapshot returns the current KV snapshot (for RPC/status).
func (s *Store) Snapshot() (*Snapshot, error) {
	return s.localLog.Snapshot()
}

// NamespaceKey returns the resolved namespace name and symmetric key,
// or empty values if not yet resolved. Used by the join handler to share
// keys with joining devices.
func (s *Store) NamespaceKey() (string, []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.config.Namespace, s.nsKey
}

// resolveKeys resolves the namespace encryption key and opaque namespace ID.
// With nil backend, resolves from local cache or generates new keys.
func (s *Store) resolveKeys(ctx context.Context) error {
	nsKey, err := getOrCreateNamespaceKey(ctx, s.backend, s.config.Namespace, s.identity, s.deviceID)
	if err != nil {
		return err
	}

	nsID := deriveNSID(nsKey, s.config.Namespace)
	if s.backend != nil {
		nsID, err = resolveNSID(ctx, s.backend, s.config.Namespace, nsKey)
		if err != nil {
			return err
		}
	}

	cacheNSID(s.config.Namespace, nsID)

	s.mu.Lock()
	s.nsKey = nsKey
	s.nsID = nsID
	s.mu.Unlock()

	return nil
}

// ShortDeviceID derives a short device ID from the identity key.
// Must match ShortPubkeyID in pkg/fs/device.go — the device registry
// and poller use this ID to match snapshot paths to registered devices.
// Format: "D-" + 8 chars.
func ShortDeviceID(identity *skykey.Key) string {
	addr := identity.Address()
	if len(addr) > 13 {
		return "D-" + addr[5:13]
	}
	return addr
}
