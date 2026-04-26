package external

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sky10/sky10/pkg/messaging"
	messagingruntime "github.com/sky10/sky10/pkg/messaging/runtime"
)

const manifestFileName = "adapter.json"
const materializedBundleDirName = "_bundle"

// AdapterInfo is one external adapter discovered from a manifest bundle.
type AdapterInfo struct {
	Adapter      messaging.Adapter `json:"adapter"`
	Settings     []Setting         `json:"settings,omitempty"`
	Actions      []Action          `json:"actions,omitempty"`
	ManifestPath string            `json:"manifest_path,omitempty"`
	BundleDir    string            `json:"bundle_dir,omitempty"`
	Bundled      bool              `json:"bundled,omitempty"`
}

// Registry discovers and resolves external adapter bundles.
type Registry struct {
	options ResolveOptions
	items   map[messaging.AdapterID]registeredAdapter
}

type registeredAdapter struct {
	manifest     Manifest
	manifestPath string
	bundleDir    string
	bundled      bool
}

// NewRegistry creates a registry from explicit bundle directories.
func NewRegistry(options ResolveOptions, bundleDirs ...string) (*Registry, error) {
	registry := &Registry{
		options: options,
		items:   make(map[messaging.AdapterID]registeredAdapter),
	}
	for _, dir := range bundleDirs {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		if err := registry.AddBundleDir(dir); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

// NewBundledRegistry creates a registry from an embedded external/messengers
// tree. Bundled-only registry entries are discoverable but not launchable until
// the bundle is materialized onto the local filesystem.
func NewBundledRegistry(fsys fs.FS, root string, options ResolveOptions) (*Registry, error) {
	registry := &Registry{
		options: options,
		items:   make(map[messaging.AdapterID]registeredAdapter),
	}
	bundles, err := discoverBundledAdapters(fsys, root)
	if err != nil {
		return nil, err
	}
	for _, bundle := range bundles {
		raw, err := fs.ReadFile(fsys, bundle.manifestPath)
		if err != nil {
			continue
		}
		manifest, err := parseManifestBytes(raw)
		if err != nil {
			return nil, fmt.Errorf("load bundled adapter %s: %w", bundle.name, err)
		}
		registry.put(registeredAdapter{
			manifest:     manifest,
			manifestPath: bundle.manifestPath,
			bundleDir:    bundle.bundleDir,
			bundled:      true,
		})
	}
	return registry, nil
}

// NewMaterializedBundledRegistry copies bundled adapter artifacts to
// installRoot/<adapter-id>/_bundle and registers those launchable copies.
func NewMaterializedBundledRegistry(fsys fs.FS, root, installRoot string, options ResolveOptions) (*Registry, error) {
	registry := &Registry{
		options: options,
		items:   make(map[messaging.AdapterID]registeredAdapter),
	}
	bundles, err := discoverBundledAdapters(fsys, root)
	if err != nil {
		return nil, err
	}
	for _, bundle := range bundles {
		raw, err := fs.ReadFile(fsys, bundle.manifestPath)
		if err != nil {
			return nil, fmt.Errorf("read bundled adapter %s manifest: %w", bundle.name, err)
		}
		manifest, err := parseManifestBytes(raw)
		if err != nil {
			return nil, fmt.Errorf("load bundled adapter %s: %w", bundle.name, err)
		}
		destDir, err := materializedBundleDir(installRoot, manifest.ID)
		if err != nil {
			return nil, err
		}
		if err := materializeFSDir(fsys, bundle.bundleDir, destDir); err != nil {
			return nil, fmt.Errorf("materialize bundled adapter %s: %w", manifest.ID, err)
		}
		if err := registry.AddBundleDir(destDir); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

// AddBundleDir loads one adapter manifest from a filesystem bundle directory.
func (r *Registry) AddBundleDir(dir string) error {
	if r == nil {
		return fmt.Errorf("external adapter registry is nil")
	}
	manifestPath := filepath.Join(dir, manifestFileName)
	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		return err
	}
	bundleDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve adapter bundle dir: %w", err)
	}
	r.put(registeredAdapter{
		manifest:     manifest,
		manifestPath: manifestPath,
		bundleDir:    bundleDir,
	})
	return nil
}

// List returns all discovered external adapters in stable order.
func (r *Registry) List() []AdapterInfo {
	if r == nil {
		return nil
	}
	ids := make([]string, 0, len(r.items))
	for id := range r.items {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	infos := make([]AdapterInfo, 0, len(ids))
	for _, id := range ids {
		info, ok := r.Info(messaging.AdapterID(id))
		if ok {
			infos = append(infos, info)
		}
	}
	return infos
}

// Info returns UI/metadata for one discovered external adapter.
func (r *Registry) Info(id messaging.AdapterID) (AdapterInfo, bool) {
	if r == nil {
		return AdapterInfo{}, false
	}
	item, ok := r.items[id]
	if !ok {
		return AdapterInfo{}, false
	}
	return AdapterInfo{
		Adapter:      item.manifest.Adapter(),
		Settings:     append([]Setting(nil), item.manifest.Settings...),
		Actions:      append([]Action(nil), item.manifest.Actions...),
		ManifestPath: item.manifestPath,
		BundleDir:    item.bundleDir,
		Bundled:      item.bundled,
	}, true
}

// ProcessSpec resolves an external adapter into a supervised process spec.
func (r *Registry) ProcessSpec(id messaging.AdapterID) (messagingruntime.ProcessSpec, error) {
	if r == nil {
		return messagingruntime.ProcessSpec{}, fmt.Errorf("external adapter registry is nil")
	}
	item, ok := r.items[id]
	if !ok {
		return messagingruntime.ProcessSpec{}, fmt.Errorf("external messaging adapter %q is not registered", id)
	}
	if item.bundled {
		return messagingruntime.ProcessSpec{}, fmt.Errorf("bundled external adapter %q must be materialized before launch", id)
	}
	return item.manifest.ProcessSpec(item.bundleDir, r.options)
}

func (r *Registry) put(item registeredAdapter) {
	r.items[item.manifest.ID] = item
}

func parseManifestBytes(raw []byte) (Manifest, error) {
	manifest, err := decodeManifest(raw)
	if err != nil {
		return Manifest{}, err
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

type bundledAdapter struct {
	name         string
	bundleDir    string
	manifestPath string
}

func discoverBundledAdapters(fsys fs.FS, root string) ([]bundledAdapter, error) {
	root = cleanFSRoot(root)
	entries, err := fs.ReadDir(fsys, root)
	if err != nil {
		return nil, fmt.Errorf("read bundled adapters: %w", err)
	}
	bundles := make([]bundledAdapter, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		bundleDir := path.Join(root, entry.Name())
		manifestPath := path.Join(bundleDir, manifestFileName)
		if _, err := fs.Stat(fsys, manifestPath); err != nil {
			continue
		}
		bundles = append(bundles, bundledAdapter{
			name:         entry.Name(),
			bundleDir:    bundleDir,
			manifestPath: manifestPath,
		})
	}
	sort.Slice(bundles, func(i, j int) bool { return bundles[i].name < bundles[j].name })
	return bundles, nil
}

func cleanFSRoot(root string) string {
	root = strings.TrimSpace(root)
	if root == "" || root == "." || root == "/" {
		return "."
	}
	return strings.Trim(path.Clean(strings.Trim(root, "/")), "/")
}

func materializedBundleDir(installRoot string, adapterID messaging.AdapterID) (string, error) {
	installRoot = strings.TrimSpace(installRoot)
	if installRoot == "" {
		return "", fmt.Errorf("external adapter install root is required")
	}
	segment, err := safeAdapterPathSegment(string(adapterID))
	if err != nil {
		return "", err
	}
	return filepath.Join(installRoot, segment, materializedBundleDirName), nil
}

func safeAdapterPathSegment(value string) (string, error) {
	value = strings.TrimSpace(value)
	switch {
	case value == "":
		return "", fmt.Errorf("adapter id is required")
	case value == ".", value == "..":
		return "", fmt.Errorf("adapter id %q is not safe for filesystem materialization", value)
	case strings.ContainsAny(value, `/\`):
		return "", fmt.Errorf("adapter id %q is not safe for filesystem materialization", value)
	case filepath.VolumeName(value) != "":
		return "", fmt.Errorf("adapter id %q is not safe for filesystem materialization", value)
	default:
		return value, nil
	}
}

func materializeFSDir(fsys fs.FS, srcRoot, destRoot string) error {
	srcRoot = cleanFSRoot(srcRoot)
	destRoot = strings.TrimSpace(destRoot)
	if destRoot == "" {
		return fmt.Errorf("materialized adapter destination is required")
	}
	if err := os.RemoveAll(destRoot); err != nil {
		return fmt.Errorf("clear materialized adapter destination: %w", err)
	}
	return fs.WalkDir(fsys, srcRoot, func(srcPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel := bundledPathRel(srcRoot, srcPath)
		if rel == "." {
			return os.MkdirAll(destRoot, 0o755)
		}
		destPath, err := materializedChildPath(destRoot, rel)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect bundled adapter path %s: %w", srcPath, err)
		}
		if entry.IsDir() {
			mode := info.Mode().Perm()
			if mode == 0 || mode&0o700 != 0o700 {
				mode = 0o755
			}
			if err := os.MkdirAll(destPath, mode); err != nil {
				return fmt.Errorf("create materialized adapter dir %s: %w", destPath, err)
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		raw, err := fs.ReadFile(fsys, srcPath)
		if err != nil {
			return fmt.Errorf("read bundled adapter file %s: %w", srcPath, err)
		}
		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return fmt.Errorf("create materialized adapter file dir %s: %w", filepath.Dir(destPath), err)
		}
		mode := info.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		mode |= 0o600
		if err := os.WriteFile(destPath, raw, mode); err != nil {
			return fmt.Errorf("write materialized adapter file %s: %w", destPath, err)
		}
		return nil
	})
}

func bundledPathRel(root, value string) string {
	if value == root {
		return "."
	}
	if root == "." {
		return strings.TrimPrefix(value, "./")
	}
	return strings.TrimPrefix(value, root+"/")
}

func materializedChildPath(root, rel string) (string, error) {
	if path.IsAbs(rel) || filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" {
		return "", fmt.Errorf("bundled adapter path %q must be relative", rel)
	}
	if strings.Contains(rel, "\\") {
		return "", fmt.Errorf("bundled adapter path %q must use slash-separated relative paths", rel)
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve materialized adapter root: %w", err)
	}
	target := filepath.Join(root, filepath.FromSlash(path.Clean(rel)))
	back, err := filepath.Rel(root, target)
	if err != nil {
		return "", fmt.Errorf("resolve materialized adapter path: %w", err)
	}
	if back == "." || back == ".." || strings.HasPrefix(back, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("bundled adapter path %q escapes materialized adapter root", rel)
	}
	return target, nil
}
