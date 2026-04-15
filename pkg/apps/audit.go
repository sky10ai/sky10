package apps

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/sky10/sky10/pkg/config"
)

const managedAppAuditLogName = "managed-app-audit.jsonl"

// UninstallAuditInfo carries durable caller metadata for managed-app removals.
type UninstallAuditInfo struct {
	Source    string `json:"source,omitempty"`
	Method    string `json:"method,omitempty"`
	Transport string `json:"transport,omitempty"`
	Remote    string `json:"remote,omitempty"`
}

type managedAppAuditEvent struct {
	Timestamp      string               `json:"timestamp"`
	Event          string               `json:"event"`
	App            ID                   `json:"app"`
	Source         string               `json:"source,omitempty"`
	Method         string               `json:"method,omitempty"`
	Transport      string               `json:"transport,omitempty"`
	Remote         string               `json:"remote,omitempty"`
	StablePath     string               `json:"stable_path,omitempty"`
	InstalledPath  string               `json:"installed_path,omitempty"`
	CurrentVersion string               `json:"current_version,omitempty"`
	Path           string               `json:"path,omitempty"`
	MissingPath    string               `json:"missing_path,omitempty"`
	Removed        *bool                `json:"removed,omitempty"`
	Error          string               `json:"error,omitempty"`
	Process        managedAppAuditActor `json:"process"`
}

type managedAppAuditActor struct {
	PID        int      `json:"pid"`
	PPID       int      `json:"ppid,omitempty"`
	UID        *int     `json:"uid,omitempty"`
	Executable string   `json:"executable,omitempty"`
	CWD        string   `json:"cwd,omitempty"`
	Argv       []string `json:"argv,omitempty"`
}

func managedAppAuditLogPath() (string, error) {
	root, err := config.RootDir()
	if err != nil {
		return "", fmt.Errorf("finding root directory: %w", err)
	}
	return filepath.Join(root, "logs", managedAppAuditLogName), nil
}

func currentManagedAppAuditActor() managedAppAuditActor {
	actor := managedAppAuditActor{
		PID:  os.Getpid(),
		PPID: os.Getppid(),
	}
	if uid, ok := currentProcessUID(); ok {
		actor.UID = &uid
	}
	if exe, err := os.Executable(); err == nil && exe != "" {
		actor.Executable = filepath.Clean(exe)
	}
	if cwd, err := os.Getwd(); err == nil && cwd != "" {
		actor.CWD = filepath.Clean(cwd)
	}
	if len(os.Args) > 0 {
		actor.Argv = append([]string(nil), os.Args...)
	}
	return actor
}

func appendManagedAppAudit(logger *slog.Logger, event managedAppAuditEvent) {
	if event.Timestamp == "" {
		event.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	event.Process = currentManagedAppAuditActor()

	path, err := managedAppAuditLogPath()
	if err != nil {
		logger.Warn("managed app audit path failed", "error", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		logger.Warn("managed app audit mkdir failed", "path", path, "error", err)
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		logger.Warn("managed app audit open failed", "path", path, "error", err)
		return
	}
	defer f.Close()

	if err := json.NewEncoder(f).Encode(event); err != nil {
		logger.Warn("managed app audit write failed", "path", path, "error", err)
	}
}
