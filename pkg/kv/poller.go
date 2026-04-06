package kv

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/sky10/sky10/pkg/adapter"
)

// Poller periodically downloads remote device snapshots, diffs against
// stored baselines, and merges changes into the local KV CRDT.
type Poller struct {
	backend   adapter.Backend
	localLog  *LocalLog
	deviceID  string
	nsID      string
	encKey    []byte
	interval  time.Duration
	baselines *BaselineStore
	logger    *slog.Logger
	heartbeat func()
	onEvent   func(string, map[string]any)
	onChange  func() // called when remote changes are merged
	pokeCh    chan struct{}
}

// NewPoller creates a KV snapshot poller.
func NewPoller(
	backend adapter.Backend,
	localLog *LocalLog,
	deviceID, nsID string,
	encKey []byte,
	interval time.Duration,
	baselines *BaselineStore,
	logger *slog.Logger,
) *Poller {
	if logger == nil {
		logger = slog.Default()
	}
	return &Poller{
		backend:   backend,
		localLog:  localLog,
		deviceID:  deviceID,
		nsID:      nsID,
		encKey:    encKey,
		interval:  interval,
		baselines: baselines,
		logger:    logger,
		heartbeat: func() {},
		onEvent:   func(string, map[string]any) {},
		onChange:  func() {},
		pokeCh:    make(chan struct{}, 1),
	}
}

// Run polls on an interval until ctx is cancelled.
func (p *Poller) Run(ctx context.Context) {
	p.logger.Info("kv poller started", "interval", p.interval)

	p.pollOnce(ctx)

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		p.heartbeat()
		select {
		case <-ctx.Done():
			p.logger.Info("kv poller stopped")
			return
		case <-ticker.C:
			p.pollOnce(ctx)
		case <-p.pokeCh:
			p.pollOnce(ctx)
		}
	}
}

// Poke triggers an immediate poll cycle.
func (p *Poller) Poke() {
	select {
	case p.pokeCh <- struct{}{}:
	default:
	}
}

// pollOnce downloads remote snapshots and merges changes.
func (p *Poller) pollOnce(ctx context.Context) {
	deviceKeys, err := p.backend.List(ctx, "devices/")
	if err != nil {
		p.logger.Warn("kv poll: list devices failed", "error", err)
		return
	}
	remoteDevices := parseDeviceList(deviceKeys)

	merged := 0
	for _, remoteID := range remoteDevices {
		if remoteID == p.deviceID {
			continue
		}
		n, err := p.syncDevice(ctx, remoteID)
		if err != nil {
			p.logger.Warn("kv poll: sync device failed", "device", remoteID, "error", err)
			continue
		}
		merged += n
	}

	if merged > 0 {
		p.logger.Info("kv poll: merged remote changes", "entries", merged)
		p.onEvent("kv.poll.complete", map[string]any{"entries": merged})
		p.onChange()
	}
}

// syncDevice downloads and merges one remote device's snapshot.
func (p *Poller) syncDevice(ctx context.Context, remoteID string) (int, error) {
	latestKey := snapshotLatestKey(p.nsID, remoteID)
	rc, err := p.backend.Get(ctx, latestKey)
	if err != nil {
		if isNotFoundError(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("downloading kv snapshot: %w", err)
	}
	encrypted, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		return 0, fmt.Errorf("reading kv snapshot: %w", err)
	}

	plain, err := decrypt(encrypted, p.encKey)
	if err != nil {
		return 0, fmt.Errorf("decrypting kv snapshot: %w", err)
	}

	remote, err := UnmarshalSnapshot(plain)
	if err != nil {
		return 0, fmt.Errorf("parsing kv snapshot: %w", err)
	}

	baseline, err := p.baselines.Load(remoteID)
	if err != nil {
		p.logger.Warn("kv poll: loading baseline failed", "device", remoteID, "error", err)
		baseline = nil
	}

	merged := p.diffAndMergeLocal(remote, baseline)

	if err := p.baselines.Save(remoteID, remote); err != nil {
		p.logger.Warn("kv poll: saving baseline failed", "device", remoteID, "error", err)
	}

	return merged, nil
}

func (p *Poller) diffAndMergeLocal(remote, baseline *Snapshot) int {
	return diffAndMerge(p.localLog, remote, baseline, p.logger)
}

// diffAndMerge compares a remote snapshot against the baseline and merges
// changes into the local CRDT. No conflict copies — pure LWW. Shared by
// both the S3 poller and P2P sync handler.
func diffAndMerge(localLog *LocalLog, remote, baseline *Snapshot, logger *slog.Logger) int {
	merged := 0
	remoteEntries := remote.Entries()
	var baselineEntries map[string]ValueInfo
	if baseline != nil {
		baselineEntries = baseline.Entries()
	}

	// Additions and modifications
	for key, remoteVI := range remoteEntries {
		baseVI, inBaseline := baselineEntries[key]
		if inBaseline && string(baseVI.Value) == string(remoteVI.Value) &&
			baseVI.Device == remoteVI.Device && baseVI.Seq == remoteVI.Seq &&
			baseVI.Actor == remoteVI.Actor && baseVI.Counter == remoteVI.Counter {
			continue // unchanged
		}

		if err := localLog.Append(Entry{
			Type:      Set,
			Key:       key,
			Value:     remoteVI.Value,
			Device:    remoteVI.Device,
			Timestamp: remoteVI.Modified.Unix(),
			Seq:       remoteVI.Seq,
			Actor:     remoteVI.Actor,
			Counter:   remoteVI.Counter,
			Context:   remoteVI.Context.Clone(),
		}); err != nil {
			logger.Warn("kv merge failed", "key", key, "error", err)
			continue
		}
		merged++
	}

	// Explicit tombstones: preferred over baseline-derived delete inference.
	for key, tomb := range remote.Tombstones() {
		if baseline != nil {
			if baseTomb, ok := baseline.Tombstones()[key]; ok &&
				baseTomb.Device == tomb.Device && baseTomb.Seq == tomb.Seq &&
				baseTomb.Actor == tomb.Actor && baseTomb.Counter == tomb.Counter {
				continue
			}
		}
		if err := localLog.Append(Entry{
			Type:      Delete,
			Key:       key,
			Device:    tomb.Device,
			Timestamp: tomb.Modified.Unix(),
			Seq:       tomb.Seq,
			Actor:     tomb.Actor,
			Counter:   tomb.Counter,
			Context:   tomb.Context.Clone(),
		}); err != nil {
			logger.Warn("kv tombstone merge failed", "key", key, "error", err)
			continue
		}
		merged++
	}

	// Legacy delete inference for snapshots that do not yet carry tombstones.
	if baseline != nil {
		now := time.Now().Unix()
		remoteTombstones := remote.DeletedKeys()
		for key, baseVI := range baselineEntries {
			if _, inRemote := remoteEntries[key]; !inRemote && !remoteTombstones[key] {
				deleteTS := now
				if baseVI.Modified.Unix() >= deleteTS {
					deleteTS = baseVI.Modified.Unix() + 1
				}
				if err := localLog.Append(Entry{
					Type:      Delete,
					Key:       key,
					Device:    baseVI.Device,
					Timestamp: deleteTS,
					Seq:       baseVI.Seq + 1,
					Actor:     effectiveActor(baseVI.Actor, baseVI.Device),
					Counter:   effectiveCounter(baseVI.Counter, baseVI.Seq) + 1,
					Context:   remote.Vector(),
				}); err != nil {
					logger.Warn("kv delete merge failed", "key", key, "error", err)
					continue
				}
				merged++
			}
		}
	}

	return merged
}

// parseDeviceList extracts device IDs from S3 devices/ keys.
func parseDeviceList(keys []string) []string {
	var ids []string
	for _, k := range keys {
		name := strings.TrimPrefix(k, "devices/")
		if strings.HasSuffix(name, ".json") {
			ids = append(ids, strings.TrimSuffix(name, ".json"))
		}
	}
	return ids
}

// isNotFoundError checks if an error is a not-found error.
func isNotFoundError(err error) bool {
	return err != nil && (err == adapter.ErrNotFound ||
		err.Error() == "not found" ||
		err.Error() == "The specified key does not exist.")
}
