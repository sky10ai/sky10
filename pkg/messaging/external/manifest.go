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
	Settings     []Setting              `json:"settings,omitempty"`
	Actions      []Action               `json:"actions,omitempty"`
	Runtime      RuntimeSpec            `json:"runtime"`
	Entry        string                 `json:"entry"`
	Sandbox      SandboxSpec            `json:"sandbox,omitempty"`
	Env          map[string]string      `json:"env,omitempty"`
}

// SettingKind identifies how a connection setting should be rendered and
// persisted by generic adapter settings UI.
type SettingKind string

const (
	SettingKindText     SettingKind = "text"
	SettingKindPassword SettingKind = "password"
	SettingKindSecret   SettingKind = "secret"
	SettingKindSelect   SettingKind = "select"
	SettingKindNumber   SettingKind = "number"
	SettingKindBoolean  SettingKind = "boolean"
	SettingKindURL      SettingKind = "url"
)

// SettingTarget identifies where a setting value should be stored.
type SettingTarget string

const (
	SettingTargetMetadata   SettingTarget = "metadata"
	SettingTargetAuth       SettingTarget = "auth"
	SettingTargetCredential SettingTarget = "credential"
)

// Setting describes one generic connection setting for an adapter.
type Setting struct {
	Key         string        `json:"key"`
	Label       string        `json:"label"`
	Kind        SettingKind   `json:"kind"`
	Target      SettingTarget `json:"target"`
	Required    bool          `json:"required,omitempty"`
	Description string        `json:"description,omitempty"`
	Placeholder string        `json:"placeholder,omitempty"`
	Default     string        `json:"default,omitempty"`
	Options     []Option      `json:"options,omitempty"`
	Secret      bool          `json:"secret,omitempty"`
}

// Option is one allowed setting value.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// ActionKind identifies the generic action behavior the UI should use.
type ActionKind string

const (
	ActionKindValidateConfig     ActionKind = "validate_config"
	ActionKindConnect            ActionKind = "connect"
	ActionKindOpenURL            ActionKind = "open_url"
	ActionKindExtractCredentials ActionKind = "extract_credentials"
)

// Action describes one adapter settings button or link.
type Action struct {
	ID          string     `json:"id"`
	Label       string     `json:"label"`
	Kind        ActionKind `json:"kind"`
	Description string     `json:"description,omitempty"`
	URL         string     `json:"url,omitempty"`
	Primary     bool       `json:"primary,omitempty"`
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
	manifest, err := decodeManifest(raw)
	if err != nil {
		return Manifest{}, err
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func decodeManifest(raw []byte) (Manifest, error) {
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("parse adapter manifest: %w", err)
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
	for idx, setting := range m.Settings {
		if err := setting.Validate(); err != nil {
			return fmt.Errorf("settings[%d]: %w", idx, err)
		}
	}
	for idx, action := range m.Actions {
		if err := action.Validate(); err != nil {
			return fmt.Errorf("actions[%d]: %w", idx, err)
		}
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

// Validate reports whether a setting is structurally usable by generic UI.
func (s Setting) Validate() error {
	if strings.TrimSpace(s.Key) == "" {
		return fmt.Errorf("key is required")
	}
	if strings.TrimSpace(s.Label) == "" {
		return fmt.Errorf("label is required")
	}
	switch s.Kind {
	case SettingKindText, SettingKindPassword, SettingKindSecret, SettingKindSelect, SettingKindNumber, SettingKindBoolean, SettingKindURL:
	default:
		return fmt.Errorf("unsupported kind %q", s.Kind)
	}
	switch s.Target {
	case SettingTargetMetadata, SettingTargetAuth, SettingTargetCredential:
	default:
		return fmt.Errorf("unsupported target %q", s.Target)
	}
	if s.Kind == SettingKindSelect && len(s.Options) == 0 {
		return fmt.Errorf("select settings require options")
	}
	for idx, option := range s.Options {
		if strings.TrimSpace(option.Value) == "" {
			return fmt.Errorf("options[%d].value is required", idx)
		}
		if strings.TrimSpace(option.Label) == "" {
			return fmt.Errorf("options[%d].label is required", idx)
		}
	}
	return nil
}

// Validate reports whether an action is structurally usable by generic UI.
func (a Action) Validate() error {
	if strings.TrimSpace(a.ID) == "" {
		return fmt.Errorf("id is required")
	}
	if strings.TrimSpace(a.Label) == "" {
		return fmt.Errorf("label is required")
	}
	switch a.Kind {
	case ActionKindValidateConfig, ActionKindConnect, ActionKindExtractCredentials:
	case ActionKindOpenURL:
		if strings.TrimSpace(a.URL) == "" {
			return fmt.Errorf("url is required for open_url actions")
		}
	default:
		return fmt.Errorf("unsupported kind %q", a.Kind)
	}
	return nil
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
