package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (m *Manager) mutateStoredRecord(name string, mutate func(*Record) (bool, error)) (*Record, error) {
	m.mu.Lock()
	rec, ok := m.records[name]
	if !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("sandbox %q not found", name)
	}
	changed, err := mutate(&rec)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if !changed {
		m.mu.Unlock()
		return nil, nil
	}
	rec.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	m.records[name] = rec
	if err := m.saveLocked(); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	m.mu.Unlock()
	return &rec, nil
}

func (m *Manager) resetProgress(name string) error {
	rec, err := m.mutateStoredRecord(name, func(rec *Record) (bool, error) {
		progress, tracker := newInitialProgress(rec.Provider, rec.Template)
		rec.Progress = progress
		if tracker != nil {
			m.progress[name] = tracker
		} else {
			delete(m.progress, name)
		}
		return true, nil
	})
	if err != nil {
		return err
	}
	if rec != nil {
		m.emitState(*rec)
	}
	return nil
}

func (m *Manager) updateProgress(name string, event progressEvent) error {
	event.Event = strings.ToLower(strings.TrimSpace(event.Event))
	event.ID = strings.TrimSpace(event.ID)
	event.Summary = strings.TrimSpace(event.Summary)
	event.Detail = strings.TrimSpace(event.Detail)
	if event.ID == "" {
		return nil
	}

	rec, err := m.mutateStoredRecord(name, func(rec *Record) (bool, error) {
		tracker := m.progress[name]
		if tracker == nil {
			tracker = newProgressTracker(rec.Provider, rec.Template)
			if tracker == nil {
				return false, nil
			}
			m.progress[name] = tracker
		}
		rec.Progress = tracker.apply(event)
		return true, nil
	})
	if err != nil {
		return err
	}
	if rec != nil {
		m.emitState(*rec)
	}
	return nil
}

func (m *Manager) failCurrentProgress(name, detail string) error {
	detail = strings.TrimSpace(detail)

	rec, err := m.mutateStoredRecord(name, func(rec *Record) (bool, error) {
		tracker := m.progress[name]
		currentID := ""
		currentSummary := ""
		if tracker != nil {
			currentID, currentSummary = tracker.current()
		}
		if currentID == "" && rec.Progress != nil {
			currentID = strings.TrimSpace(rec.Progress.StepID)
			currentSummary = strings.TrimSpace(rec.Progress.Summary)
		}
		if currentSummary == "" {
			currentSummary = "Provisioning failed."
		}
		if tracker != nil && currentID != "" {
			rec.Progress = tracker.apply(progressEvent{
				Event:   "fail",
				ID:      currentID,
				Summary: currentSummary,
				Detail:  detail,
			})
		} else if rec.Progress != nil {
			rec.Progress.Summary = currentSummary
		}
		return true, nil
	})
	if err != nil {
		return err
	}
	if rec != nil {
		m.emitState(*rec)
	}
	return nil
}

func (m *Manager) appendLog(slug, stream, line string) {
	line = strings.TrimRight(line, "\r\n")
	if strings.TrimSpace(line) == "" {
		return
	}
	if event, ok := parseProgressMarker(line); ok {
		_ = m.updateProgress(slug, event)
		return
	}
	entry := LogEntry{
		Time:   time.Now().UTC().Format(time.RFC3339),
		Stream: stream,
		Line:   line,
	}
	data, _ := json.Marshal(entry)
	logPath := m.logPath(slug)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err == nil {
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = f.Write(append(data, '\n'))
			_ = f.Close()
		}
	}
	rec, err := m.mutateStoredRecord(slug, func(rec *Record) (bool, error) {
		rec.LastLogAt = entry.Time
		return true, nil
	})
	if err != nil {
		rec = nil
	}
	if m.emit != nil {
		displayName := slug
		if rec != nil && strings.TrimSpace(rec.Name) != "" {
			displayName = rec.Name
		}
		m.emit("sandbox:log", map[string]any{
			"name":   displayName,
			"slug":   slug,
			"stream": stream,
			"time":   entry.Time,
			"line":   line,
		})
	}
}

func (m *Manager) updateStatus(name, status, lastErr string) error {
	rec, err := m.mutateStoredRecord(name, func(rec *Record) (bool, error) {
		rec.Status = status
		rec.LastError = lastErr
		if status == "ready" || status == "stopped" {
			rec.Progress = nil
			delete(m.progress, name)
		}
		return true, nil
	})
	if err != nil {
		return err
	}
	if rec != nil {
		m.emitState(*rec)
	}
	return nil
}

func (m *Manager) updateVMStatus(name, vmStatus string) error {
	rec, err := m.mutateStoredRecord(name, func(rec *Record) (bool, error) {
		rec.VMStatus = vmStatus
		return true, nil
	})
	if err != nil {
		return err
	}
	if rec != nil {
		m.emitState(*rec)
	}
	return nil
}

func (m *Manager) updateIPAddress(name, ip string) error {
	rec, err := m.mutateStoredRecord(name, func(rec *Record) (bool, error) {
		rec.IPAddress = ip
		return true, nil
	})
	if err != nil {
		return err
	}
	if rec != nil {
		m.emitState(*rec)
	}
	return nil
}

func (m *Manager) recordGuestIdentity(name string, guest guestIdentity) error {
	return m.recordGuestDevice(name, guest.DeviceID, guest.DevicePubKey)
}

func (m *Manager) recordGuestDevice(name, deviceID, devicePubKey string) error {
	deviceID = strings.TrimSpace(deviceID)
	devicePubKey = strings.ToLower(strings.TrimSpace(devicePubKey))
	if deviceID == "" && devicePubKey == "" {
		return nil
	}

	rec, err := m.mutateStoredRecord(name, func(rec *Record) (bool, error) {
		changed := false
		if deviceID != "" && rec.GuestDeviceID != deviceID {
			rec.GuestDeviceID = deviceID
			changed = true
		}
		if devicePubKey != "" && rec.GuestDevicePubKey != devicePubKey {
			rec.GuestDevicePubKey = devicePubKey
			changed = true
		}
		return changed, nil
	})
	if err != nil {
		return err
	}
	if rec != nil {
		m.emitState(*rec)
	}
	return nil
}

func (m *Manager) forgetRecord(rec Record) error {
	m.mu.Lock()
	delete(m.records, rec.Slug)
	delete(m.running, rec.Slug)
	delete(m.progress, rec.Slug)
	err := m.saveLocked()
	m.mu.Unlock()
	if err != nil {
		return err
	}
	if m.emit != nil {
		m.emit("sandbox:state", map[string]any{
			"name":   rec.Name,
			"slug":   rec.Slug,
			"status": "deleted",
		})
	}
	return nil
}

// GuardDeletePath rejects destructive deletes that would remove a managed
// sandbox home or one of its ancestors. Durable agent profiles must be
// detached from their sandbox first to avoid immediate recreation.
func (m *Manager) GuardDeletePath(target string) error {
	target = filepath.Clean(strings.TrimSpace(target))
	if target == "" || target == "." {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	conflicts := make([]string, 0)
	for _, rec := range m.records {
		sharedDir := filepath.Clean(strings.TrimSpace(rec.SharedDir))
		if sharedDir == "" || !pathContainsTarget(target, sharedDir) {
			continue
		}
		name := strings.TrimSpace(rec.Name)
		if name == "" {
			name = rec.Slug
		}
		conflicts = append(conflicts, name)
	}
	if len(conflicts) == 0 {
		return nil
	}

	sort.Strings(conflicts)
	if len(conflicts) == 1 {
		return fmt.Errorf("path %q is the managed agent home for sandbox %q; delete the sandbox from Settings -> Sandboxes first", target, conflicts[0])
	}
	return fmt.Errorf("path %q contains managed agent homes for sandboxes %s; delete those sandboxes from Settings -> Sandboxes first", target, strings.Join(conflicts, ", "))
}

func pathContainsTarget(target, child string) bool {
	rel, err := filepath.Rel(target, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

func (m *Manager) shouldAutoRemoveMissingRecord(rec Record) bool {
	if m.running[rec.Slug] || rec.Status == "creating" || rec.Status == "starting" {
		return false
	}
	if rec.Provider != providerLima {
		return false
	}
	return strings.TrimSpace(rec.GuestDevicePubKey) != ""
}

func (m *Manager) cleanupMissingSandbox(ctx context.Context, rec Record) error {
	if err := cleanupLimaInstanceDir(rec.Slug); err != nil {
		return err
	}
	if err := m.removeSandboxDevice(ctx, rec); err != nil {
		return err
	}
	return m.forgetRecord(rec)
}

func (m *Manager) emitState(rec Record) {
	if m.emit == nil {
		return
	}
	m.emit("sandbox:state", withCurrentShellCommand(rec))
}

func (m *Manager) statePath() string {
	return filepath.Join(m.rootDir, "state.json")
}

func (m *Manager) sandboxDir(name string) string {
	return filepath.Join(m.rootDir, name)
}

func (m *Manager) sandboxStateDir(name string) string {
	return filepath.Join(m.sandboxDir(name), sandboxStateDirName)
}

func (m *Manager) sandboxLogsDir(name string) string {
	return filepath.Join(m.sandboxDir(name), sandboxLogsDirName)
}

func (m *Manager) logPath(name string) string {
	return filepath.Join(m.sandboxLogsDir(name), logFileName)
}

func (m *Manager) load() error {
	path := m.statePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading sandbox state: %w", err)
	}
	var state stateFile
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("parsing sandbox state: %w", err)
	}
	changed := false
	for _, rec := range state.Sandboxes {
		rec.Name = normalizeDisplayName(rec.Name)
		if rec.Name == "" {
			rec.Name = rec.Slug
			changed = true
		}
		if rec.Slug == "" {
			rec.Slug = slugifySandboxName(rec.Name)
			changed = true
		}
		if rec.Slug == "" {
			continue
		}
		if rec.SharedDir == "" {
			if dir, err := defaultSharedDir(rec.Slug); err == nil {
				rec.SharedDir = dir
				changed = true
			}
		}
		if shell := defaultShellCommand(rec.Slug, rec.Template); rec.Shell != shell {
			rec.Shell = shell
			changed = true
		}
		if endpointChanged, err := m.assignForwardedEndpointLocked(&rec); err != nil {
			return err
		} else if endpointChanged {
			changed = true
		}
		m.records[rec.Slug] = rec
	}
	if changed {
		return m.saveLocked()
	}
	return nil
}

func (m *Manager) saveLocked() error {
	items := make([]Record, 0, len(m.records))
	for _, rec := range m.records {
		items = append(items, rec)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	data, err := json.MarshalIndent(stateFile{Sandboxes: items}, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding sandbox state: %w", err)
	}
	if err := os.WriteFile(m.statePath(), append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("writing sandbox state: %w", err)
	}
	return nil
}
