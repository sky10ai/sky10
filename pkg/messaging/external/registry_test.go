package external

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/sky10/sky10/pkg/messaging"
)

func TestRegistryDiscoversFilesystemBundle(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeEntry(t, dir, "dist/adapter.js", "console.log('fixture');\n")
	writeManifest(t, dir, testManifest())

	registry, err := NewRegistry(ResolveOptions{BunPath: "/managed/bin/bun"}, dir)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	infos := registry.List()
	if len(infos) != 1 {
		t.Fatalf("List() len = %d, want 1", len(infos))
	}
	info := infos[0]
	if info.Adapter.ID != "slack" {
		t.Fatalf("adapter id = %q, want slack", info.Adapter.ID)
	}
	if len(info.Settings) != 1 || info.Settings[0].Key != "bot_token" {
		t.Fatalf("settings = %#v, want bot_token setting", info.Settings)
	}
	if len(info.Actions) != 1 || info.Actions[0].Kind != ActionKindConnect {
		t.Fatalf("actions = %#v, want connect action", info.Actions)
	}

	spec, err := registry.ProcessSpec("slack")
	if err != nil {
		t.Fatalf("ProcessSpec() error = %v", err)
	}
	if spec.Path != "/managed/bin/bun" {
		t.Fatalf("spec path = %q, want /managed/bin/bun", spec.Path)
	}
}

func TestBundledRegistryRequiresMaterialization(t *testing.T) {
	t.Parallel()

	registry, err := NewBundledRegistry(testBundleFS(t), ".", ResolveOptions{BunPath: "/managed/bin/bun"})
	if err != nil {
		t.Fatalf("NewBundledRegistry() error = %v", err)
	}
	infos := registry.List()
	if len(infos) != 1 {
		t.Fatalf("List() len = %d, want 1", len(infos))
	}
	if !infos[0].Bundled {
		t.Fatalf("Bundled = false, want true")
	}
	if _, err := registry.ProcessSpec("slack"); err == nil || !strings.Contains(err.Error(), "materialized") {
		t.Fatalf("ProcessSpec() error = %v, want materialized error", err)
	}
}

func TestMaterializedBundledRegistry(t *testing.T) {
	t.Parallel()

	installRoot := t.TempDir()
	registry, err := NewMaterializedBundledRegistry(testBundleFS(t), ".", installRoot, ResolveOptions{
		BunPath: "/managed/bin/bun",
	})
	if err != nil {
		t.Fatalf("NewMaterializedBundledRegistry() error = %v", err)
	}

	info, ok := registry.Info(messaging.AdapterID("slack"))
	if !ok {
		t.Fatal("Info(slack) ok = false, want true")
	}
	if info.Bundled {
		t.Fatalf("Bundled = true, want false after materialization")
	}
	wantBundleDir := filepath.Join(installRoot, "slack", "_bundle")
	if info.BundleDir != wantBundleDir {
		t.Fatalf("BundleDir = %q, want %q", info.BundleDir, wantBundleDir)
	}
	if _, err := os.Stat(filepath.Join(wantBundleDir, "dist", "adapter.js")); err != nil {
		t.Fatalf("materialized entry missing: %v", err)
	}

	spec, err := registry.ProcessSpec("slack")
	if err != nil {
		t.Fatalf("ProcessSpec() error = %v", err)
	}
	if spec.Dir != wantBundleDir {
		t.Fatalf("spec dir = %q, want %q", spec.Dir, wantBundleDir)
	}
	if spec.Args[0] != filepath.Join(wantBundleDir, "dist", "adapter.js") {
		t.Fatalf("spec entry = %q, want materialized adapter entry", spec.Args[0])
	}
}

func testBundleFS(t *testing.T) fstest.MapFS {
	t.Helper()

	raw, err := json.MarshalIndent(testManifest(), "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent() error = %v", err)
	}
	return fstest.MapFS{
		"slack/adapter.json":     &fstest.MapFile{Data: raw, Mode: 0o644},
		"slack/dist/adapter.js":  &fstest.MapFile{Data: []byte("console.log('fixture');\n"), Mode: 0o644},
		"slack/package.json":     &fstest.MapFile{Data: []byte(`{"type":"module"}` + "\n"), Mode: 0o644},
		"ignored/README.md":      &fstest.MapFile{Data: []byte("ignored\n"), Mode: 0o644},
		"not-an-adapter/file.js": &fstest.MapFile{Data: []byte("ignored\n"), Mode: 0o644},
	}
}
