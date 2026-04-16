package fs

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/sky10/sky10/pkg/adapter"
	"github.com/sky10/sky10/pkg/fs/opslog"
)

// SnapshotPoller periodically downloads remote device snapshots, diffs
// them against stored baselines, and merges changes into the local CRDT.
// Replaces the old PollerV2 which read individual S3 ops.
type SnapshotPoller struct {
	backend        adapter.Backend
	localLog       *opslog.LocalOpsLog
	deviceID       string
	nsID           string
	encKey         []byte // namespace encryption key
	interval       time.Duration
	baselines      *BaselineStore
	logger         *slog.Logger
	pokeReconciler func()
	pokeUploader   func()
	heartbeat      func()
	onEvent        func(string, map[string]any)
	driveName      string
}

// NewSnapshotPoller creates a snapshot poller.
func NewSnapshotPoller(
	backend adapter.Backend,
	localLog *opslog.LocalOpsLog,
	deviceID, nsID string,
	encKey []byte,
	interval time.Duration,
	baselines *BaselineStore,
	logger *slog.Logger,
) *SnapshotPoller {
	logger = defaultLogger(logger)
	return &SnapshotPoller{
		backend:        backend,
		localLog:       localLog,
		deviceID:       deviceID,
		nsID:           nsID,
		encKey:         encKey,
		interval:       interval,
		baselines:      baselines,
		logger:         logger,
		pokeReconciler: func() {},
		pokeUploader:   func() {},
		heartbeat:      func() {},
		onEvent:        func(string, map[string]any) {},
	}
}

// Run polls on an interval until ctx is cancelled.
func (p *SnapshotPoller) Run(ctx context.Context) {
	p.logger.Info("snapshot poller started", "interval", p.interval)
	if p.backend == nil {
		<-ctx.Done()
		p.logger.Info("snapshot poller stopped")
		return
	}

	// Immediate first poll
	p.pollOnce(ctx)

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		p.heartbeat()
		select {
		case <-ctx.Done():
			p.logger.Info("snapshot poller stopped")
			return
		case <-ticker.C:
			p.pollOnce(ctx)
		}
	}
}

// Poke triggers an immediate poll (non-blocking).
func (p *SnapshotPoller) Poke() {
	// The poller runs on a timer. Poke is a no-op — the next tick
	// will pick up changes. For immediate sync, call pollOnce directly.
}

// pollOnce downloads remote device snapshots, diffs against baselines,
// and merges changes into the local CRDT.
func (p *SnapshotPoller) pollOnce(ctx context.Context) {
	if p.backend == nil {
		return
	}
	// List registered devices
	deviceKeys, err := p.backend.List(ctx, "devices/")
	if err != nil {
		p.logger.Warn("poll: list devices failed", "error", err)
		return
	}
	remoteDevices := parseDeviceList(deviceKeys)

	merged := 0
	for _, remoteID := range remoteDevices {
		if remoteID == p.deviceID {
			continue // skip self
		}

		n, err := p.syncDevice(ctx, remoteID)
		if err != nil {
			p.logger.Warn("poll: sync device failed", "device", remoteID, "error", err)
			continue
		}
		merged += n
	}

	if merged > 0 {
		p.logger.Info("poll: merged remote changes", "entries", merged)
		p.onEvent("poll.complete", map[string]any{
			"drive":   p.driveName,
			"entries": merged,
		})
		p.pokeReconciler()
		p.pokeUploader()
	}
}

// syncDevice downloads one remote device's snapshot, diffs against
// the stored baseline, and merges changes into the local CRDT.
// Returns the number of entries merged.
func (p *SnapshotPoller) syncDevice(ctx context.Context, remoteID string) (int, error) {
	// Download remote snapshot
	latestKey := snapshotLatestKey(p.nsID, remoteID)
	rc, err := p.backend.Get(ctx, latestKey)
	if err != nil {
		if isNotFoundError(err) {
			return 0, nil // device hasn't uploaded a snapshot yet
		}
		return 0, fmt.Errorf("downloading snapshot: %w", err)
	}
	encrypted, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		return 0, fmt.Errorf("reading snapshot: %w", err)
	}

	plain, err := Decrypt(encrypted, p.encKey)
	if err != nil {
		return 0, fmt.Errorf("decrypting snapshot: %w", err)
	}

	remote, err := opslog.UnmarshalSnapshot(plain)
	if err != nil {
		return 0, fmt.Errorf("parsing snapshot: %w", err)
	}

	// Load stored baseline (nil if first sync with this device)
	baseline, err := p.baselines.Load(remoteID)
	if err != nil {
		p.logger.Warn("poll: loading baseline failed, treating as new device", "device", remoteID, "error", err)
		baseline = nil
	}

	// Diff and merge
	merged := p.diffAndMerge(remote, baseline)

	// Save new baseline
	if err := p.baselines.Save(remoteID, remote); err != nil {
		p.logger.Warn("poll: saving baseline failed", "device", remoteID, "error", err)
	}

	return merged, nil
}

// diffAndMerge compares a remote device's latest snapshot against the
// stored baseline and merges changes into the local CRDT.
func (p *SnapshotPoller) diffAndMerge(remote, baseline *opslog.Snapshot) int {
	merged := 0
	remoteFiles := remote.Files()
	var baselineFiles map[string]opslog.FileInfo
	if baseline != nil {
		baselineFiles = baseline.Files()
	}

	// Get local snapshot for conflict detection
	localSnap, _ := p.localLog.Snapshot()

	// Additions and modifications: in remote but not in baseline (or changed)
	for path, remotefi := range remoteFiles {
		basefi, inBaseline := baselineFiles[path]
		if inBaseline && basefi.Checksum == remotefi.Checksum {
			continue // unchanged
		}

		// Conflict detection: if the local CRDT also changed this file
		// since the baseline, it's a concurrent edit.
		if localfi, localExists := localSnap.Lookup(path); localExists && inBaseline {
			localChanged := localfi.Checksum != basefi.Checksum
			remoteChanged := remotefi.Checksum != basefi.Checksum
			if localChanged && remoteChanged && localfi.Checksum != remotefi.Checksum {
				// Conflict — LWW will pick the winner. Save the loser
				// as a conflict copy so the user doesn't lose data.
				p.createConflictCopy(path, localfi, remotefi)
			}
		}

		// Merge into local CRDT via LWW (the CRDT resolves the winner)
		prevChecksum := remotefi.PrevChecksum
		if prevChecksum == "" && inBaseline {
			prevChecksum = basefi.Checksum
		}
		if err := p.localLog.Append(opslog.Entry{
			Type:         opslog.Put,
			Path:         path,
			Chunks:       remotefi.Chunks,
			Size:         remotefi.Size,
			Checksum:     remotefi.Checksum,
			PrevChecksum: prevChecksum,
			Namespace:    remotefi.Namespace,
			LinkTarget:   remotefi.LinkTarget,
			Device:       remotefi.Device,
			Timestamp:    remotefi.Modified.Unix(),
			Seq:          remotefi.Seq,
		}); err != nil {
			p.logger.Warn("poll: merge failed", "path", path, "error", err)
			continue
		}
		merged++
	}

	// Deletes: in baseline but not in remote
	now := time.Now().Unix()
	if baseline != nil {
		for path, basefi := range baselineFiles {
			if _, inRemote := remoteFiles[path]; !inRemote {
				// Remote deleted this file. Use a timestamp guaranteed
				// to beat the original entry's clock.
				deleteTS := now
				if basefi.Modified.Unix() >= deleteTS {
					deleteTS = basefi.Modified.Unix() + 1
				}
				if err := p.localLog.Append(opslog.Entry{
					Type:         opslog.Delete,
					Path:         path,
					Namespace:    basefi.Namespace,
					Device:       basefi.Device,
					Timestamp:    deleteTS,
					Seq:          basefi.Seq + 1,
					PrevChecksum: basefi.Checksum,
				}); err != nil {
					p.logger.Warn("poll: delete merge failed", "path", path, "error", err)
					continue
				}
				merged++
			}
		}
	}

	// Directory changes
	remoteDirs := remote.Dirs()
	var baselineDirs map[string]opslog.DirInfo
	if baseline != nil {
		baselineDirs = baseline.Dirs()
	}

	for path, di := range remoteDirs {
		if _, inBaseline := baselineDirs[path]; !inBaseline {
			p.localLog.Append(opslog.Entry{
				Type:      opslog.CreateDir,
				Path:      path,
				Namespace: di.Namespace,
				Device:    di.Device,
				Timestamp: di.Modified.Unix(),
				Seq:       di.Seq,
			})
			merged++
		}
	}

	if baseline != nil {
		for path, di := range baselineDirs {
			if _, inRemote := remoteDirs[path]; !inRemote {
				p.localLog.Append(opslog.Entry{
					Type:      opslog.DeleteDir,
					Path:      path,
					Namespace: di.Namespace,
					Device:    di.Device,
					Timestamp: time.Now().Unix(),
				})
				merged++
			}
		}
	}

	return merged
}

// createConflictCopy records the losing version of a concurrent edit as a
// conflict copy file. The loser's blob is already in S3 (upload-then-record),
// so we just write a CRDT entry pointing to it with a conflict path.
func (p *SnapshotPoller) createConflictCopy(path string, localfi, remotefi opslog.FileInfo) {
	// Determine which version loses LWW
	loser := localfi
	remoteClock := opslog.ClockTuple(remotefi)
	localClock := opslog.ClockTuple(localfi)
	if remoteClock.Beats(localClock) {
		loser = localfi // remote wins, local is the loser
	} else {
		loser = remotefi // local wins, remote is the loser
	}

	// Build conflict path: file.conflict-{device}-{timestamp}.ext
	conflictPath := conflictCopyPath(path, loser.Device, loser.Modified.Unix())
	p.logger.Info("conflict copy", "path", path, "conflict", conflictPath, "loser", loser.Device)

	p.localLog.AppendLocal(opslog.Entry{
		Type:      opslog.Put,
		Path:      conflictPath,
		Chunks:    loser.Chunks,
		Size:      loser.Size,
		Checksum:  loser.Checksum,
		Namespace: loser.Namespace,
	})
}

// isNotFoundError checks if an error is a not-found error.
func isNotFoundError(err error) bool {
	return err != nil && (err == adapter.ErrNotFound ||
		err.Error() == "not found" ||
		err.Error() == "The specified key does not exist.")
}
