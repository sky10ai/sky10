package external

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sky10/sky10/pkg/messaging"
	messagingruntime "github.com/sky10/sky10/pkg/messaging/runtime"
)

const (
	// RuntimeBun identifies bundled JavaScript adapters launched with Bun.
	RuntimeBun = "bun"

	// SandboxNone launches the adapter directly without a sandbox.
	SandboxNone = "none"
	// SandboxZerobox launches the adapter through the Zerobox sandbox.
	SandboxZerobox = "zerobox"
)

// Manifest describes one out-of-process adapter bundle.
type Manifest struct {
	ID           messaging.AdapterID    `json:"id"`
	DisplayName  string                 `json:"display_name,omitempty"`
	Version      string                 `json:"version,omitempty"`
	Description  string                 `json:"description,omitempty"`
	AuthMethods  []messaging.AuthMethod `json:"auth_methods,omitempty"`
	Capabilities messaging.Capabilities `json:"capabilities,omitempty"`
	Runtime      RuntimeSpec            `json:"runtime"`
	Entry        string                 `json:"entry"`
	Sandbox      SandboxSpec            `json:"sandbox,omitempty"`
	Env          map[string]string      `json:"env,omitempty"`
}

// RuntimeSpec declares the runtime needed to execute the adapter bundle.
type RuntimeSpec struct {
	Type    string `json:"type"`
	Version string `json:"version,omitempty"`
}

// SandboxSpec declares the sandbox launcher requested by the adapter bundle.
type SandboxSpec struct {
	Mode       string   `json:"mode,omitempty"`
	AllowRead  []string `json:"allow_read,omitempty"`
	AllowWrite []string `json:"allow_write,omitempty"`
	AllowNet   []string `json:"allow_net,omitempty"`
	Args       []string `json:"args,omitempty"`
}

// ResolveOptions supplies managed helper paths for manifest resolution.
type ResolveOptions struct {
	BunPath     string
	ZeroboxPath string
	ExtraEnv    []string
}

// LoadManifest reads and validates one adapter manifest.
func LoadManifest(path string) (Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("read adapter manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("parse adapter manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// Validate reports whether the manifest is structurally usable.
func (m Manifest) Validate() error {
	if strings.TrimSpace(string(m.ID)) == "" {
		return fmt.Errorf("adapter id is required")
	}
	if strings.TrimSpace(m.Entry) == "" {
		return fmt.Errorf("adapter entry is required")
	}
	switch strings.TrimSpace(m.Runtime.Type) {
	case RuntimeBun:
	default:
		return fmt.Errorf("unsupported adapter runtime %q", m.Runtime.Type)
	}
	if strings.TrimSpace(m.Sandbox.Mode) == "" {
		return fmt.Errorf("adapter sandbox.mode is required")
	}
	switch sandboxMode(m.Sandbox.Mode) {
	case SandboxNone, SandboxZerobox:
	default:
		return fmt.Errorf("unsupported adapter sandbox %q", m.Sandbox.Mode)
	}
	return nil
}

// Adapter returns public adapter metadata from the manifest.
func (m Manifest) Adapter() messaging.Adapter {
	displayName := strings.TrimSpace(m.DisplayName)
	if displayName == "" {
		displayName = string(m.ID)
	}
	return messaging.Adapter{
		ID:           m.ID,
		DisplayName:  displayName,
		Version:      m.Version,
		Description:  m.Description,
		AuthMethods:  append([]messaging.AuthMethod(nil), m.AuthMethods...),
		Capabilities: m.Capabilities,
	}
}

// ResolveProcessSpec loads one manifest and resolves it into an executable
// ProcessSpec rooted at the manifest directory.
func ResolveProcessSpec(manifestPath string, options ResolveOptions) (messagingruntime.ProcessSpec, Manifest, error) {
	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		return messagingruntime.ProcessSpec{}, Manifest{}, err
	}
	spec, err := manifest.ProcessSpec(filepath.Dir(manifestPath), options)
	if err != nil {
		return messagingruntime.ProcessSpec{}, Manifest{}, err
	}
	return spec, manifest, nil
}

// ProcessSpec resolves this manifest into a ProcessSpec rooted at baseDir.
func (m Manifest) ProcessSpec(baseDir string, options ResolveOptions) (messagingruntime.ProcessSpec, error) {
	if err := m.Validate(); err != nil {
		return messagingruntime.ProcessSpec{}, err
	}
	bundleDir, entry, err := secureBundlePaths(baseDir, m.Entry)
	if err != nil {
		return messagingruntime.ProcessSpec{}, err
	}

	switch sandboxMode(m.Sandbox.Mode) {
	case SandboxNone:
		return messagingruntime.ProcessSpec{
			Path: runtimePath(RuntimeBun, options.BunPath),
			Args: []string{entry},
			Env:  processEnv(m, bundleDir, options),
			Dir:  bundleDir,
		}, nil
	case SandboxZerobox:
		return messagingruntime.ProcessSpec{}, fmt.Errorf("zerobox adapter sandbox launch is not wired yet")
	default:
		return messagingruntime.ProcessSpec{}, fmt.Errorf("unsupported adapter sandbox %q", m.Sandbox.Mode)
	}
}

func sandboxMode(mode string) string {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return SandboxNone
	}
	return mode
}

func runtimePath(runtimeType, configured string) string {
	if strings.TrimSpace(configured) != "" {
		return configured
	}
	return runtimeType
}

func processEnv(manifest Manifest, bundleDir string, options ResolveOptions) []string {
	env := append([]string(nil), options.ExtraEnv...)
	keys := make([]string, 0, len(manifest.Env))
	for key := range manifest.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, rawKey := range keys {
		key := strings.TrimSpace(rawKey)
		if key == "" || strings.Contains(key, "=") {
			continue
		}
		env = append(env, key+"="+manifest.Env[rawKey])
	}
	env = append(env, "SKY10_MESSAGING_ADAPTER_ID="+string(manifest.ID))
	env = append(env, "SKY10_MESSAGING_ADAPTER_BUNDLE_DIR="+bundleDir)
	return env
}

func secureBundlePaths(baseDir, relPath string) (string, string, error) {
	if path.IsAbs(relPath) || filepath.IsAbs(relPath) || filepath.VolumeName(relPath) != "" {
		return "", "", fmt.Errorf("adapter entry must be relative")
	}
	if strings.Contains(relPath, "\\") {
		return "", "", fmt.Errorf("adapter entry must use slash-separated relative paths")
	}
	base, err := filepath.Abs(baseDir)
	if err != nil {
		return "", "", fmt.Errorf("resolve adapter base dir: %w", err)
	}
	target := filepath.Join(base, filepath.FromSlash(path.Clean(relPath)))
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return "", "", fmt.Errorf("resolve adapter entry: %w", err)
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("adapter entry escapes bundle directory")
	}
	return base, target, nil
}
