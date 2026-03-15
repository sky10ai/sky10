package fs

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	s3adapter "github.com/sky10/sky10/pkg/adapter/s3"
)

func TestSyncUploadNewFiles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	id, _ := GenerateIdentity()
	store := New(backend, id)

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "hello.md"), []byte("hello world"), 0644)
	os.MkdirAll(filepath.Join(dir, "docs"), 0755)
	os.WriteFile(filepath.Join(dir, "docs", "readme.md"), []byte("readme"), 0644)

	engine := NewSyncEngine(store, SyncConfig{LocalRoot: dir})
	result, err := engine.SyncOnce(ctx)
	if err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}

	if result.Uploaded != 2 {
		t.Errorf("Uploaded = %d, want 2", result.Uploaded)
	}

	// Verify files are in remote
	entries, _ := store.List(ctx, "")
	if len(entries) != 2 {
		t.Errorf("remote has %d files, want 2", len(entries))
	}
}

func TestSyncDownloadNewFiles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	id, _ := GenerateIdentity()
	store := New(backend, id)

	// Put files via store (simulating another device)
	store.Put(ctx, "remote.md", strings.NewReader("from remote"))
	store.Put(ctx, "docs/spec.md", strings.NewReader("spec content"))

	dir := t.TempDir()
	engine := NewSyncEngine(store, SyncConfig{LocalRoot: dir})
	result, err := engine.SyncOnce(ctx)
	if err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}

	if result.Downloaded != 2 {
		t.Errorf("Downloaded = %d, want 2", result.Downloaded)
	}

	// Verify local files
	data, err := os.ReadFile(filepath.Join(dir, "remote.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "from remote" {
		t.Errorf("got %q, want %q", data, "from remote")
	}

	data, _ = os.ReadFile(filepath.Join(dir, "docs", "spec.md"))
	if string(data) != "spec content" {
		t.Errorf("got %q, want %q", data, "spec content")
	}
}

func TestSyncSkipsUnchanged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	id, _ := GenerateIdentity()
	store := New(backend, id)

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "file.md"), []byte("content"), 0644)

	engine := NewSyncEngine(store, SyncConfig{LocalRoot: dir})

	// First sync uploads
	r1, _ := engine.SyncOnce(ctx)
	if r1.Uploaded != 1 {
		t.Fatalf("first sync: Uploaded = %d, want 1", r1.Uploaded)
	}

	// Second sync should skip (checksums match)
	r2, _ := engine.SyncOnce(ctx)
	if r2.Uploaded != 0 {
		t.Errorf("second sync: Uploaded = %d, want 0 (unchanged)", r2.Uploaded)
	}
	if r2.Downloaded != 0 {
		t.Errorf("second sync: Downloaded = %d, want 0", r2.Downloaded)
	}
}

func TestSyncBidirectional(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	id, _ := GenerateIdentity()
	store := New(backend, id)

	// Remote has file A
	store.Put(ctx, "remote-only.md", strings.NewReader("from remote"))

	// Local has file B
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "local-only.md"), []byte("from local"), 0644)

	engine := NewSyncEngine(store, SyncConfig{LocalRoot: dir})
	result, err := engine.SyncOnce(ctx)
	if err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}

	if result.Uploaded != 1 {
		t.Errorf("Uploaded = %d, want 1", result.Uploaded)
	}
	if result.Downloaded != 1 {
		t.Errorf("Downloaded = %d, want 1", result.Downloaded)
	}

	// Local should have both files
	data, _ := os.ReadFile(filepath.Join(dir, "remote-only.md"))
	if string(data) != "from remote" {
		t.Errorf("remote file: got %q", data)
	}

	// Remote should have both files
	var buf bytes.Buffer
	store.Get(ctx, "local-only.md", &buf)
	if buf.String() != "from local" {
		t.Errorf("local file in remote: got %q", buf.String())
	}
}

func TestSyncModifiedLocalFile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	id, _ := GenerateIdentity()
	store := New(backend, id)

	dir := t.TempDir()
	filePath := filepath.Join(dir, "doc.md")
	os.WriteFile(filePath, []byte("version 1"), 0644)

	engine := NewSyncEngine(store, SyncConfig{LocalRoot: dir})
	engine.SyncOnce(ctx) // initial sync

	// Modify local file
	os.WriteFile(filePath, []byte("version 2"), 0644)

	result, _ := engine.SyncOnce(ctx)
	if result.Uploaded != 1 {
		t.Errorf("Uploaded = %d, want 1 (modified file)", result.Uploaded)
	}

	// Verify remote has v2
	var buf bytes.Buffer
	store.Get(ctx, "doc.md", &buf)
	if buf.String() != "version 2" {
		t.Errorf("remote: got %q, want %q", buf.String(), "version 2")
	}
}

func TestSyncSelectiveNamespace(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	id, _ := GenerateIdentity()
	store := New(backend, id)

	// Remote has files in two namespaces
	store.Put(ctx, "journal/a.md", strings.NewReader("journal"))
	store.Put(ctx, "financial/b.md", strings.NewReader("financial"))

	dir := t.TempDir()
	engine := NewSyncEngine(store, SyncConfig{
		LocalRoot:  dir,
		Namespaces: []string{"journal"},
	})
	result, _ := engine.SyncOnce(ctx)

	if result.Downloaded != 1 {
		t.Errorf("Downloaded = %d, want 1 (only journal)", result.Downloaded)
	}

	// Only journal file should exist locally
	if _, err := os.Stat(filepath.Join(dir, "journal", "a.md")); os.IsNotExist(err) {
		t.Error("journal/a.md should exist")
	}
	if _, err := os.Stat(filepath.Join(dir, "financial", "b.md")); !os.IsNotExist(err) {
		t.Error("financial/b.md should NOT exist (filtered out)")
	}
}

func TestSyncSelectivePrefix(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	id, _ := GenerateIdentity()
	store := New(backend, id)

	store.Put(ctx, "docs/readme.md", strings.NewReader("readme"))
	store.Put(ctx, "photos/cat.jpg", strings.NewReader("cat"))

	dir := t.TempDir()
	engine := NewSyncEngine(store, SyncConfig{
		LocalRoot: dir,
		Prefixes:  []string{"docs/"},
	})
	result, _ := engine.SyncOnce(ctx)

	if result.Downloaded != 1 {
		t.Errorf("Downloaded = %d, want 1 (only docs/)", result.Downloaded)
	}
}

func TestSyncIgnoreDotfiles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	id, _ := GenerateIdentity()
	store := New(backend, id)

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "visible.md"), []byte("visible"), 0644)
	os.WriteFile(filepath.Join(dir, ".hidden"), []byte("hidden"), 0644)
	os.MkdirAll(filepath.Join(dir, ".git"), 0755)
	os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("git"), 0644)

	engine := NewSyncEngine(store, SyncConfig{LocalRoot: dir})
	result, _ := engine.SyncOnce(ctx)

	if result.Uploaded != 1 {
		t.Errorf("Uploaded = %d, want 1 (dotfiles should be ignored)", result.Uploaded)
	}
}

func TestScanDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.md"), []byte("aaa"), 0644)
	os.MkdirAll(filepath.Join(dir, "sub"), 0755)
	os.WriteFile(filepath.Join(dir, "sub", "b.md"), []byte("bbb"), 0644)
	os.WriteFile(filepath.Join(dir, ".hidden"), []byte("hidden"), 0644)

	files, err := ScanDirectory(dir, nil)
	if err != nil {
		t.Fatalf("ScanDirectory: %v", err)
	}

	if len(files) != 2 {
		t.Errorf("got %d files, want 2 (dotfile excluded)", len(files))
	}

	if _, ok := files["a.md"]; !ok {
		t.Error("missing a.md")
	}
	if _, ok := files["sub/b.md"]; !ok {
		t.Error("missing sub/b.md")
	}
	if _, ok := files[".hidden"]; ok {
		t.Error(".hidden should be excluded")
	}
}

func TestDiffLocalRemote(t *testing.T) {
	t.Parallel()

	local := map[string]string{
		"new.md":       "hash-new",
		"modified.md":  "hash-local",
		"unchanged.md": "hash-same",
	}
	remote := map[string]FileEntry{
		"modified.md":  {Checksum: "hash-remote", Namespace: "default"},
		"unchanged.md": {Checksum: "hash-same", Namespace: "default"},
		"remote.md":    {Checksum: "hash-r", Namespace: "default"},
	}

	diffs := DiffLocalRemote(local, remote)

	types := make(map[string]DiffType)
	for _, d := range diffs {
		types[d.Path] = d.Type
	}

	if types["new.md"] != DiffUpload {
		t.Error("new.md should be upload")
	}
	if types["modified.md"] != DiffUpload {
		t.Error("modified.md should be upload (local wins)")
	}
	if _, ok := types["unchanged.md"]; ok {
		t.Error("unchanged.md should not appear in diff")
	}
	if types["remote.md"] != DiffDownload {
		t.Error("remote.md should be download")
	}
}

func TestDownloadAll(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := s3adapter.NewMemory()
	id, _ := GenerateIdentity()
	store := New(backend, id)

	store.Put(ctx, "a.md", strings.NewReader("aaa"))
	store.Put(ctx, "sub/b.md", strings.NewReader("bbb"))

	dir := t.TempDir()
	engine := NewSyncEngine(store, SyncConfig{LocalRoot: dir})
	result, err := engine.DownloadAll(ctx)
	if err != nil {
		t.Fatalf("DownloadAll: %v", err)
	}
	if result.Downloaded != 2 {
		t.Errorf("Downloaded = %d, want 2", result.Downloaded)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "a.md"))
	if string(data) != "aaa" {
		t.Errorf("a.md = %q, want %q", data, "aaa")
	}
}
