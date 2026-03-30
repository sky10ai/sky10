//go:build integration

package fs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Regression: dotfiles must sync across devices like any other file.
// Previously ScanDirectory and the watcher skipped all dotfiles, so
// files like .env and .gitignore were silently ignored.
func TestIntegrationDotfileSyncsAcrossDevices(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
	h := StartMinIO(t)
	ctx := context.Background()

	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	backend := h.Backend(t, "dotfile-sync")

	// A uploads files including dotfiles
	storeA := NewWithDevice(backend, idA, "device-a")
	storeA.SetNamespace("shared")
	storeA.Put(ctx, "readme.md", strings.NewReader("hello"))
	storeA.Put(ctx, ".env", strings.NewReader("SECRET=hunter2"))
	storeA.Put(ctx, ".gitignore", strings.NewReader("*.log"))
	storeA.Put(ctx, "config/.hidden.yml", strings.NewReader("key: value"))

	simulateApprove(t, ctx, backend, idA, idB)

	// --- Device B: sync should download ALL files including dotfiles ---
	tmpB := t.TempDir()
	dirB := filepath.Join(tmpB, "sync")
	os.MkdirAll(dirB, 0755)

	storeB := NewWithDevice(backend, idB, "device-b")
	storeB.SetNamespace("shared")
	cfgB := DaemonConfig{
		SyncConfig:   SyncConfig{LocalRoot: dirB, Namespaces: []string{"shared"}},
		DriveID:      "test-dotfile-b",
		ManifestPath: filepath.Join(tmpB, "data", "manifest.json"),
		PollSeconds:  300,
	}
	daemonB, err := NewDaemonV2_5(storeB, cfgB, nil)
	if err != nil {
		t.Fatalf("creating daemon B: %v", err)
	}
	daemonB.SyncOnce(ctx)

	// ALL files should exist — dotfiles are not special
	checks := map[string]string{
		"readme.md":          "hello",
		".env":               "SECRET=hunter2",
		".gitignore":         "*.log",
		"config/.hidden.yml": "key: value",
	}
	for path, want := range checks {
		data, err := os.ReadFile(filepath.Join(dirB, filepath.FromSlash(path)))
		if err != nil {
			t.Errorf("%s not downloaded: %v", path, err)
			continue
		}
		if string(data) != want {
			t.Errorf("%s content = %q, want %q", path, string(data), want)
		}
	}
}

// Regression: dotfiles created locally must be detected by the watcher/seed
// and uploaded, then synced to the other device.
func TestIntegrationDotfileUploadFromDisk(t *testing.T) {
	t.Skip("snapshot-exchange: requires rewrite")
	h := StartMinIO(t)
	ctx := context.Background()

	idA, _ := GenerateDeviceKey()
	idB, _ := GenerateDeviceKey()
	backend := h.Backend(t, "dotfile-upload")

	// A needs a namespace key
	storeA := NewWithDevice(backend, idA, "device-a")
	storeA.SetNamespace("shared")
	storeA.Put(ctx, "anchor.txt", strings.NewReader("anchor"))

	simulateApprove(t, ctx, backend, idA, idB)

	// --- Device A: has dotfiles on disk, seed should pick them up ---
	tmpA := t.TempDir()
	dirA := filepath.Join(tmpA, "sync")
	os.MkdirAll(dirA, 0755)
	os.WriteFile(filepath.Join(dirA, "anchor.txt"), []byte("anchor"), 0644)
	os.WriteFile(filepath.Join(dirA, ".env"), []byte("DB_HOST=localhost"), 0644)
	os.WriteFile(filepath.Join(dirA, ".tool-versions"), []byte("go 1.22"), 0644)

	cfgA := DaemonConfig{
		SyncConfig:   SyncConfig{LocalRoot: dirA, Namespaces: []string{"shared"}},
		DriveID:      "test-dotfile-upload-a",
		ManifestPath: filepath.Join(tmpA, "data", "manifest.json"),
		PollSeconds:  300,
	}
	daemonA, err := NewDaemonV2_5(storeA, cfgA, nil)
	if err != nil {
		t.Fatalf("creating daemon A: %v", err)
	}
	daemonA.SyncOnce(ctx)
	daemonA.outboxWorker.drain(ctx)

	// --- Device B: sync should get the dotfiles ---
	tmpB := t.TempDir()
	dirB := filepath.Join(tmpB, "sync")
	os.MkdirAll(dirB, 0755)

	storeB := NewWithDevice(backend, idB, "device-b")
	storeB.SetNamespace("shared")
	cfgB := DaemonConfig{
		SyncConfig:   SyncConfig{LocalRoot: dirB, Namespaces: []string{"shared"}},
		DriveID:      "test-dotfile-upload-b",
		ManifestPath: filepath.Join(tmpB, "data", "manifest.json"),
		PollSeconds:  300,
	}
	daemonB, err := NewDaemonV2_5(storeB, cfgB, nil)
	if err != nil {
		t.Fatalf("creating daemon B: %v", err)
	}
	daemonB.SyncOnce(ctx)

	for _, path := range []string{".env", ".tool-versions"} {
		if _, err := os.Stat(filepath.Join(dirB, path)); err != nil {
			t.Errorf("%s should exist on device B — dotfiles must sync", path)
		}
	}
}
