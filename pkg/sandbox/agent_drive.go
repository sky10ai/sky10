package sandbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sky10/sky10/pkg/config"
	skyfs "github.com/sky10/sky10/pkg/fs"
)

type agentDriveInfo struct {
	ID        string
	Name      string
	LocalPath string
}

type agentDriveBackend interface {
	List(context.Context) ([]agentDriveInfo, error)
	Remove(context.Context, string) error
	Create(context.Context, string, string, string) error
}

type hostAgentDriveBackend struct {
	rpc func(context.Context, string, interface{}, interface{}) error
}

type localAgentDriveBackend struct {
	manager *skyfs.DriveManager
}

func defaultSharedDir(slug string) (string, error) {
	root, err := defaultAgentDriveRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, slug), nil
}

func defaultAgentDriveRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding home directory: %w", err)
	}
	return filepath.Join(home, "Sky10", "Drives", agentDriveRootName), nil
}

func legacyAgentDriveName(slug string) string {
	return agentDriveNamePrefix + strings.TrimSpace(slug)
}

func EnsureAgentHomeLayout(sharedDir string) error {
	if err := os.MkdirAll(sharedDir, 0o755); err != nil {
		return fmt.Errorf("creating agent home directory: %w", err)
	}
	for _, rel := range []string{agentWorkspaceDirName} {
		if err := os.MkdirAll(filepath.Join(sharedDir, rel), 0o755); err != nil {
			return fmt.Errorf("creating agent home directory %q: %w", rel, err)
		}
	}
	return nil
}

func (m *Manager) ensureAgentHome(ctx context.Context, slug, sharedDir string) error {
	cleanPath := filepath.Clean(sharedDir)
	if err := EnsureAgentHomeLayout(cleanPath); err != nil {
		return err
	}
	if m.hostRPC == nil {
		return ensureLocalAgentDriveConfig(slug, cleanPath)
	}

	if err := reconcileAgentDriveRoot(ctx, hostAgentDriveBackend{rpc: m.hostRPC}, slug, cleanPath); err != nil {
		if shouldFallbackToLocalDriveConfig(err) {
			return ensureLocalAgentDriveConfig(slug, cleanPath)
		}
		return err
	}
	return nil
}

func shouldFallbackToLocalDriveConfig(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(text, "daemon not running") ||
		strings.Contains(text, "connection refused") ||
		strings.Contains(text, "no such file or directory")
}

func ensureLocalAgentDriveConfig(slug, sharedDir string) error {
	cfgDir, err := config.Dir()
	if err != nil {
		return fmt.Errorf("resolving drive config directory: %w", err)
	}
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		return fmt.Errorf("creating drive config directory: %w", err)
	}

	backend := localAgentDriveBackend{
		manager: skyfs.NewDriveManager(nil, filepath.Join(cfgDir, "drives.json")),
	}
	return reconcileAgentDriveRoot(context.Background(), backend, slug, filepath.Clean(sharedDir))
}

func reconcileAgentDriveRoot(ctx context.Context, backend agentDriveBackend, slug, sharedDir string) error {
	driveRoot := filepath.Clean(filepath.Dir(sharedDir))

	drives, err := backend.List(ctx)
	if err != nil {
		return fmt.Errorf("listing drives for agent home %q: %w", slug, err)
	}

	legacyIDs := make([]string, 0)
	rootReady := false
	for _, drive := range drives {
		driveName := strings.TrimSpace(drive.Name)
		drivePath := filepath.Clean(strings.TrimSpace(drive.LocalPath))
		switch {
		case drivePath == driveRoot:
			if driveName != agentDriveRootName {
				return fmt.Errorf("drive %q already exists with path %q; expected drive %q", driveName, drive.LocalPath, agentDriveRootName)
			}
			rootReady = true
		case driveName == agentDriveRootName:
			return fmt.Errorf("drive %q already exists with path %q", agentDriveRootName, drive.LocalPath)
		case strings.HasPrefix(driveName, agentDriveNamePrefix) && filepath.Clean(filepath.Dir(drivePath)) == driveRoot:
			if strings.TrimSpace(drive.ID) != "" {
				legacyIDs = append(legacyIDs, strings.TrimSpace(drive.ID))
			}
		}
	}
	for _, id := range legacyIDs {
		if err := backend.Remove(ctx, id); err != nil {
			return fmt.Errorf("removing legacy agent drive %q: %w", id, err)
		}
	}
	if rootReady {
		return nil
	}

	if err := backend.Create(ctx, agentDriveRootName, driveRoot, agentDriveRootName); err != nil {
		return fmt.Errorf("creating drive %q for agent home %q: %w", agentDriveRootName, slug, err)
	}
	return nil
}

func (b hostAgentDriveBackend) List(ctx context.Context) ([]agentDriveInfo, error) {
	var listed struct {
		Drives []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			LocalPath string `json:"local_path"`
		} `json:"drives"`
	}
	if err := b.rpc(ctx, "skyfs.driveList", nil, &listed); err != nil {
		return nil, err
	}

	drives := make([]agentDriveInfo, 0, len(listed.Drives))
	for _, drive := range listed.Drives {
		drives = append(drives, agentDriveInfo{
			ID:        drive.ID,
			Name:      drive.Name,
			LocalPath: drive.LocalPath,
		})
	}
	return drives, nil
}

func (b hostAgentDriveBackend) Remove(ctx context.Context, id string) error {
	return b.rpc(ctx, "skyfs.driveRemove", map[string]string{"id": id}, nil)
}

func (b hostAgentDriveBackend) Create(ctx context.Context, name, path, namespace string) error {
	return b.rpc(ctx, "skyfs.driveCreate", map[string]string{
		"name":      name,
		"path":      path,
		"namespace": namespace,
	}, nil)
}

func (b localAgentDriveBackend) List(_ context.Context) ([]agentDriveInfo, error) {
	drives := b.manager.ListDrives()
	items := make([]agentDriveInfo, 0, len(drives))
	for _, drive := range drives {
		items = append(items, agentDriveInfo{
			ID:        drive.ID,
			Name:      drive.Name,
			LocalPath: drive.LocalPath,
		})
	}
	return items, nil
}

func (b localAgentDriveBackend) Remove(_ context.Context, id string) error {
	return b.manager.RemoveDrive(id)
}

func (b localAgentDriveBackend) Create(_ context.Context, name, path, namespace string) error {
	_, err := b.manager.CreateDrive(name, path, namespace)
	return err
}
