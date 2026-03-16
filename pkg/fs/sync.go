package fs

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// SyncConfig configures the sync engine.
type SyncConfig struct {
	LocalRoot        string
	PollInterval     int      // seconds, 0 = no polling (manual sync)
	Namespaces       []string // empty = sync all
	Prefixes         []string // empty = sync all
	ExcludePrefixes  []string
	ConflictStrategy string // "lww" (default), "keep-both"
	IgnoreFunc       func(string) bool
}

// SyncResult contains stats from a sync operation.
type SyncResult struct {
	Uploaded   int
	Downloaded int
	Deleted    int
	Conflicts  int
	Skipped    int
	Errors     []error
}

// SyncEngine performs bidirectional sync between a local directory and
// encrypted remote storage.
type SyncEngine struct {
	store  *Store
	config SyncConfig
	state  *SyncState
}

// NewSyncEngine creates a sync engine with persistent sync state.
func NewSyncEngine(store *Store, config SyncConfig) *SyncEngine {
	return &SyncEngine{
		store:  store,
		config: config,
		state:  LoadSyncState(),
	}
}

// SyncOnce performs a single bidirectional sync pass.
func (se *SyncEngine) SyncOnce(ctx context.Context) (*SyncResult, error) {
	result := &SyncResult{}

	// 1. Scan local directory
	localFiles, err := ScanDirectory(se.config.LocalRoot, se.config.IgnoreFunc)
	if err != nil {
		return nil, fmt.Errorf("scanning local: %w", err)
	}

	// 2. Load remote state
	state, err := se.store.loadCurrentState(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading remote state: %w", err)
	}

	// 3. Filter remote state by sync config
	remoteFiles := se.filterRemote(state.Tree)

	// 4. Filter out files already synced with same checksum
	filteredLocal := make(map[string]string)
	for path, checksum := range localFiles {
		if prev, ok := se.state.LocalChecksums[path]; ok && prev == checksum {
			// File hasn't changed since last sync — check if remote has it
			if _, inRemote := remoteFiles[path]; inRemote {
				continue // skip, already synced
			}
		}
		filteredLocal[path] = checksum
	}

	// Also filter remote files that we know are synced locally
	filteredRemote := make(map[string]FileEntry)
	for path, entry := range remoteFiles {
		if _, ok := se.state.LocalChecksums[path]; ok {
			// We have this file synced — only include if local has changed
			if _, inLocal := filteredLocal[path]; !inLocal {
				continue // skip, already synced and local unchanged
			}
		}
		filteredRemote[path] = entry
	}

	// 5. Compute diff
	diffs := DiffLocalRemote(filteredLocal, filteredRemote)

	// 6. Execute diffs
	for _, d := range diffs {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		switch d.Type {
		case DiffUpload:
			if err := se.upload(ctx, d); err != nil {
				result.Errors = append(result.Errors, fmt.Errorf("upload %s: %w", d.Path, err))
				continue
			}
			se.state.LocalChecksums[d.Path] = d.LocalChecksum
			result.Uploaded++

		case DiffDownload:
			if err := se.download(ctx, d); err != nil {
				result.Errors = append(result.Errors, fmt.Errorf("download %s: %w", d.Path, err))
				continue
			}
			// After download, compute local checksum and mark as synced
			localPath := filepath.Join(se.config.LocalRoot, filepath.FromSlash(d.Path))
			if cksum, err := fileChecksum(localPath); err == nil {
				se.state.LocalChecksums[d.Path] = cksum
			}
			result.Downloaded++

		case DiffConflict:
			result.Conflicts++

		case DiffDeleteRemote:
			if err := se.store.Remove(ctx, d.Path); err != nil {
				result.Errors = append(result.Errors, fmt.Errorf("delete remote %s: %w", d.Path, err))
				continue
			}
			result.Deleted++
		}
	}

	return result, nil
}

func (se *SyncEngine) upload(ctx context.Context, d DiffEntry) error {
	localPath := filepath.Join(se.config.LocalRoot, filepath.FromSlash(d.Path))
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()
	return se.store.Put(ctx, d.Path, f)
}

func (se *SyncEngine) download(ctx context.Context, d DiffEntry) error {
	localPath := filepath.Join(se.config.LocalRoot, filepath.FromSlash(d.Path))

	// Create parent directories
	dir := filepath.Dir(localPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating directory %s: %w", dir, err)
	}

	f, err := os.Create(localPath)
	if err != nil {
		return err
	}

	if err := se.store.Get(ctx, d.Path, f); err != nil {
		f.Close()
		os.Remove(localPath)
		return err
	}

	return f.Close()
}

// DownloadAll downloads all remote files to the local directory.
// Used for initial sync on a new device.
func (se *SyncEngine) DownloadAll(ctx context.Context) (*SyncResult, error) {
	result := &SyncResult{}

	state, err := se.store.loadCurrentState(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading remote state: %w", err)
	}

	remoteFiles := se.filterRemote(state.Tree)

	for path := range remoteFiles {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		d := DiffEntry{Path: path, Type: DiffDownload}
		if err := se.download(ctx, d); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("download %s: %w", path, err))
			continue
		}
		result.Downloaded++
	}

	return result, nil
}

// UploadAll uploads all local files to remote storage.
func (se *SyncEngine) UploadAll(ctx context.Context) (*SyncResult, error) {
	result := &SyncResult{}

	localFiles, err := ScanDirectory(se.config.LocalRoot, se.config.IgnoreFunc)
	if err != nil {
		return nil, fmt.Errorf("scanning local: %w", err)
	}

	for path := range localFiles {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		d := DiffEntry{Path: path, Type: DiffUpload}
		if err := se.upload(ctx, d); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("upload %s: %w", path, err))
			continue
		}
		result.Uploaded++
	}

	return result, nil
}

// filterRemote filters the remote file tree by sync config.
func (se *SyncEngine) filterRemote(tree map[string]FileEntry) map[string]FileEntry {
	if len(se.config.Namespaces) == 0 && len(se.config.Prefixes) == 0 && len(se.config.ExcludePrefixes) == 0 {
		return tree
	}

	filtered := make(map[string]FileEntry)
	for path, entry := range tree {
		if se.matchesFilter(path, entry) {
			filtered[path] = entry
		}
	}
	return filtered
}

func (se *SyncEngine) matchesFilter(path string, entry FileEntry) bool {
	// Check excludes first
	for _, exc := range se.config.ExcludePrefixes {
		if len(path) >= len(exc) && path[:len(exc)] == exc {
			return false
		}
	}

	// If namespace filter set, check it
	if len(se.config.Namespaces) > 0 {
		matched := false
		for _, ns := range se.config.Namespaces {
			if entry.Namespace == ns {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	// If prefix filter set, check it
	if len(se.config.Prefixes) > 0 {
		matched := false
		for _, p := range se.config.Prefixes {
			if len(path) >= len(p) && path[:len(p)] == p {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	return true
}

// WriteFile is a helper that writes data to a local file path, creating
// parent directories as needed.
func WriteFile(path string, r io.Reader) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
