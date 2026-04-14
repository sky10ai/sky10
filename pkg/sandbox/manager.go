package sandbox

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	skyapps "github.com/sky10/sky10/pkg/apps"
	"github.com/sky10/sky10/pkg/config"
)

const (
	providerLima                   = "lima"
	templateUbuntu                 = "ubuntu"
	templateUbuntuAsset            = "ubuntu-sky10.yaml"
	templateOpenClaw               = "openclaw"
	templateOpenClawYAML           = "openclaw-sky10.yaml"
	templateOpenClawDep            = "openclaw-sky10.dependency.sh"
	templateOpenClawSys            = "openclaw-sky10.system.sh"
	templateOpenClawUser           = "openclaw-sky10.user.sh"
	templateHostsHelper            = "update-lima-hosts.sh"
	templateOpenClawPluginDir      = "openclaw-sky10-channel"
	templateOpenClawPluginPackage  = templateOpenClawPluginDir + "/package.json"
	templateOpenClawPluginManifest = templateOpenClawPluginDir + "/openclaw.plugin.json"
	templateOpenClawPluginIndex    = templateOpenClawPluginDir + "/src/index.js"
	templateOpenClawPluginClient   = templateOpenClawPluginDir + "/src/sky10.js"
	templateRemoteBase             = "https://raw.githubusercontent.com/sky10ai/sky10/main/templates/lima/"
	logFileName                    = "boot.log"
	templateNameToken              = "__SKY10_SANDBOX_NAME__"
	templateSharedToken            = "__SKY10_SHARED_DIR__"
	openClawReadyTimeout           = 2 * time.Minute
	guestSky10ReadyURL             = "http://127.0.0.1:9101/health"
	openClawReadyURL               = "http://127.0.0.1:18789/health"
)

var openClawSharedAssetFiles = []string{
	templateOpenClawPluginPackage,
	templateOpenClawPluginManifest,
	templateOpenClawPluginIndex,
	templateOpenClawPluginClient,
}

var slugWordPattern = regexp.MustCompile(`[a-z0-9]+`)

type Emitter func(event string, data interface{})

type CreateParams struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Template string `json:"template"`
	Model    string `json:"model,omitempty"`
}

type NamedParams struct {
	Name string `json:"name,omitempty"`
	Slug string `json:"slug,omitempty"`
}

type LogsParams struct {
	Name  string `json:"name,omitempty"`
	Slug  string `json:"slug,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

type Record struct {
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	Provider  string `json:"provider"`
	Template  string `json:"template"`
	Model     string `json:"model,omitempty"`
	Status    string `json:"status"`
	VMStatus  string `json:"vm_status,omitempty"`
	SharedDir string `json:"shared_dir,omitempty"`
	IPAddress string `json:"ip_address,omitempty"`
	Shell     string `json:"shell,omitempty"`
	LastError string `json:"last_error,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	LastLogAt string `json:"last_log_at,omitempty"`
}

type LogEntry struct {
	Time   string `json:"time"`
	Stream string `json:"stream"`
	Line   string `json:"line"`
}

type ListResult struct {
	Sandboxes []Record `json:"sandboxes"`
}

type LogsResult struct {
	Name    string     `json:"name"`
	Slug    string     `json:"slug"`
	Entries []LogEntry `json:"entries"`
}

type stateFile struct {
	Sandboxes []Record `json:"sandboxes"`
}

type templateDefinition struct {
	mainAsset string
	assets    []string
}

type Manager struct {
	mu                       sync.Mutex
	records                  map[string]Record
	rootDir                  string
	emit                     Emitter
	logger                   *slog.Logger
	running                  map[string]bool
	appStatus                func(id skyapps.ID) (*skyapps.Status, error)
	appUpgr                  func(id skyapps.ID, onProgress skyapps.ProgressFunc) (*skyapps.ReleaseInfo, error)
	runCmd                   func(ctx context.Context, bin string, args []string, onLine func(stream, line string)) error
	outputCmd                func(ctx context.Context, bin string, args []string) ([]byte, error)
	resolveOpenClawSharedEnv func(context.Context) (map[string]string, error)
}

func NewManager(emit Emitter, logger *slog.Logger) (*Manager, error) {
	root, err := config.RootDir()
	if err != nil {
		return nil, err
	}
	m := &Manager{
		records:   map[string]Record{},
		rootDir:   filepath.Join(root, "sandboxes"),
		emit:      emit,
		logger:    componentLogger(logger),
		running:   map[string]bool{},
		appStatus: skyapps.StatusFor,
		appUpgr:   skyapps.Upgrade,
		runCmd:    defaultRunCommand,
		outputCmd: defaultOutputCommand,
	}
	if err := os.MkdirAll(m.rootDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating sandbox state dir: %w", err)
	}
	if err := m.load(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) SetOpenClawSharedEnvResolver(fn func(context.Context) (map[string]string, error)) {
	m.resolveOpenClawSharedEnv = fn
}

func (m *Manager) List(ctx context.Context) (*ListResult, error) {
	if err := m.refreshRuntime(ctx); err != nil {
		m.logger.Debug("sandbox runtime refresh failed", "error", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	items := make([]Record, 0, len(m.records))
	for _, rec := range m.records {
		items = append(items, rec)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return &ListResult{Sandboxes: items}, nil
}

func (m *Manager) Get(ctx context.Context, name string) (*Record, error) {
	key, err := normalizeLookup(name)
	if err != nil {
		return nil, err
	}
	if err := m.refreshRuntime(ctx); err != nil {
		m.logger.Debug("sandbox runtime refresh failed", "error", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	slug, ok := m.findRecordKeyLocked(key)
	if !ok {
		return nil, fmt.Errorf("sandbox %q not found", key)
	}
	rec, ok := m.records[slug]
	if !ok {
		return nil, fmt.Errorf("sandbox %q not found", key)
	}
	copy := rec
	return &copy, nil
}

func (m *Manager) Logs(name string, limit int) (*LogsResult, error) {
	key, err := normalizeLookup(name)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 200
	}
	rec, err := m.requireRecord(key)
	if err != nil {
		return nil, err
	}
	path := m.logPath(rec.Slug)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &LogsResult{Name: rec.Name, Slug: rec.Slug, Entries: []LogEntry{}}, nil
		}
		return nil, fmt.Errorf("reading sandbox logs: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	entries := make([]LogEntry, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry LogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			entries = append(entries, LogEntry{Line: line})
			continue
		}
		entries = append(entries, entry)
	}
	return &LogsResult{Name: rec.Name, Slug: rec.Slug, Entries: entries}, nil
}

func (m *Manager) Create(ctx context.Context, params CreateParams) (*Record, error) {
	displayName, slug, provider, template, model, sharedDir, err := normalizeCreateParams(params)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	rec := Record{
		Name:      displayName,
		Slug:      slug,
		Provider:  provider,
		Template:  template,
		Model:     model,
		Status:    "creating",
		SharedDir: sharedDir,
		Shell:     fmt.Sprintf("limactl shell %s", slug),
		CreatedAt: now,
		UpdatedAt: now,
	}

	m.mu.Lock()
	if _, exists := m.records[slug]; exists {
		m.mu.Unlock()
		return nil, fmt.Errorf("sandbox %q already exists", displayName)
	}
	if m.running[slug] {
		m.mu.Unlock()
		return nil, fmt.Errorf("sandbox %q is already being created", displayName)
	}
	m.records[slug] = rec
	m.running[slug] = true
	if err := m.saveLocked(); err != nil {
		delete(m.running, slug)
		delete(m.records, slug)
		m.mu.Unlock()
		return nil, err
	}
	m.mu.Unlock()

	m.emitState(rec)
	go m.runCreate(context.Background(), rec)

	return &rec, nil
}

func (m *Manager) Ensure(ctx context.Context, params CreateParams) (*Record, error) {
	displayName, slug, provider, template, model, sharedDir, err := normalizeCreateParams(params)
	if err != nil {
		return nil, err
	}

	limactl, err := m.ensureManagedApp(ctx, skyapps.AppLima, true)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	rec, recordExists := m.records[slug]
	running := m.running[slug]
	m.mu.Unlock()

	if recordExists {
		if running || rec.Status == "creating" || rec.Status == "starting" {
			copy := rec
			return &copy, nil
		}

		vmStatus, exists, err := m.lookupLimaInstanceStatus(ctx, limactl, slug)
		if err != nil {
			return nil, err
		}
		if exists {
			if vmStatus == "Running" {
				if err := m.updateVMStatus(slug, vmStatus); err != nil {
					return nil, err
				}
				if err := m.updateStatus(slug, "ready", ""); err != nil {
					return nil, err
				}
				if ipAddr, err := lookupLimaInstanceIPv4(ctx, m.outputCmd, limactl, slug); err == nil && ipAddr != "" {
					if err := m.updateIPAddress(slug, ipAddr); err != nil {
						return nil, err
					}
				}
				return m.Get(ctx, slug)
			}
			return m.Start(ctx, slug)
		}

		if _, err := m.Delete(ctx, slug); err != nil {
			return nil, err
		}
	}

	vmStatus, exists, err := m.lookupLimaInstanceStatus(ctx, limactl, slug)
	if err != nil {
		return nil, err
	}
	if exists {
		now := time.Now().UTC().Format(time.RFC3339)
		rec = Record{
			Name:      displayName,
			Slug:      slug,
			Provider:  provider,
			Template:  template,
			Model:     model,
			Status:    sandboxStatusFromVMStatus(vmStatus),
			VMStatus:  vmStatus,
			SharedDir: sharedDir,
			Shell:     fmt.Sprintf("limactl shell %s", slug),
			CreatedAt: now,
			UpdatedAt: now,
		}
		if vmStatus == "Running" {
			if ipAddr, err := lookupLimaInstanceIPv4(ctx, m.outputCmd, limactl, slug); err == nil && ipAddr != "" {
				rec.IPAddress = ipAddr
			}
		}

		m.mu.Lock()
		m.records[slug] = rec
		err = m.saveLocked()
		m.mu.Unlock()
		if err != nil {
			return nil, err
		}
		m.emitState(rec)

		if vmStatus == "Running" {
			return m.Get(ctx, slug)
		}
		return m.Start(ctx, slug)
	}

	return m.Create(ctx, params)
}

func (m *Manager) Start(ctx context.Context, name string) (*Record, error) {
	rec, err := m.requireRecord(name)
	if err != nil {
		return nil, err
	}
	if err := m.prepareTemplateSharedDir(ctx, *rec); err != nil {
		return nil, err
	}
	limactl, err := m.ensureManagedApp(ctx, skyapps.AppLima, true)
	if err != nil {
		return nil, err
	}
	if err := m.updateStatus(rec.Slug, "starting", ""); err != nil {
		return nil, err
	}
	go func() {
		err := m.runCmd(context.Background(), limactl, []string{"start", "--tty=false", rec.Slug}, func(stream, line string) {
			m.appendLog(rec.Slug, stream, line)
		})
		if err != nil {
			_ = m.updateStatus(rec.Slug, "error", err.Error())
			return
		}
		_ = m.finishReady(context.Background(), rec.Slug, limactl)
	}()
	return m.Get(context.Background(), rec.Slug)
}

func (m *Manager) Stop(ctx context.Context, name string) (*Record, error) {
	rec, err := m.requireRecord(name)
	if err != nil {
		return nil, err
	}
	limactl, err := m.ensureManagedApp(ctx, skyapps.AppLima, true)
	if err != nil {
		return nil, err
	}
	exists, err := m.limaInstanceExists(ctx, limactl, rec.Slug)
	if err != nil {
		return nil, err
	}
	if !exists {
		if err := m.updateVMStatus(rec.Slug, "Stopped"); err != nil {
			return nil, err
		}
		if err := m.updateStatus(rec.Slug, "stopped", ""); err != nil {
			return nil, err
		}
		return m.Get(ctx, rec.Slug)
	}
	if err := m.runCmd(ctx, limactl, []string{"stop", rec.Slug}, func(stream, line string) {
		m.appendLog(rec.Slug, stream, line)
	}); err != nil {
		return nil, fmt.Errorf("stopping sandbox %q: %w", rec.Name, err)
	}
	if err := m.updateVMStatus(rec.Slug, "Stopped"); err != nil {
		return nil, err
	}
	if err := m.updateStatus(rec.Slug, "stopped", ""); err != nil {
		return nil, err
	}
	return m.Get(ctx, rec.Slug)
}

func (m *Manager) Delete(ctx context.Context, name string) (*Record, error) {
	rec, err := m.requireRecord(name)
	if err != nil {
		return nil, err
	}
	limactl, err := m.ensureManagedApp(ctx, skyapps.AppLima, true)
	if err != nil {
		return nil, err
	}
	exists, err := m.limaInstanceExists(ctx, limactl, rec.Slug)
	if err != nil {
		return nil, err
	}
	if exists {
		_ = m.runCmd(ctx, limactl, []string{"stop", rec.Slug}, func(stream, line string) {
			m.appendLog(rec.Slug, stream, line)
		})
		if err := m.runCmd(ctx, limactl, []string{"delete", "--force", rec.Slug}, func(stream, line string) {
			m.appendLog(rec.Slug, stream, line)
		}); err != nil {
			exists, checkErr := m.limaInstanceExists(ctx, limactl, rec.Slug)
			if checkErr != nil {
				return nil, checkErr
			}
			if exists {
				return nil, fmt.Errorf("deleting sandbox %q: %w", rec.Name, err)
			}
		}
	}
	if err := cleanupLimaInstanceDir(rec.Slug); err != nil {
		return nil, err
	}

	m.mu.Lock()
	delete(m.records, rec.Slug)
	delete(m.running, rec.Slug)
	err = m.saveLocked()
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if m.emit != nil {
		m.emit("sandbox:state", map[string]any{
			"name":   rec.Name,
			"slug":   rec.Slug,
			"status": "deleted",
		})
	}
	return rec, nil
}

func (m *Manager) runCreate(ctx context.Context, rec Record) {
	defer func() {
		m.mu.Lock()
		delete(m.running, rec.Slug)
		m.mu.Unlock()
	}()

	limactl, err := m.ensureManagedApp(ctx, skyapps.AppLima, true)
	if err != nil {
		_ = m.updateStatus(rec.Slug, "error", err.Error())
		return
	}

	templatePath, err := m.materializeTemplate(ctx, rec)
	if err != nil {
		_ = m.updateStatus(rec.Slug, "error", err.Error())
		return
	}

	args, err := m.buildStartArgs(ctx, rec, templatePath)
	if err != nil {
		_ = m.updateStatus(rec.Slug, "error", err.Error())
		return
	}
	if err := m.runCmd(ctx, limactl, args, func(stream, line string) {
		m.appendLog(rec.Slug, stream, line)
	}); err != nil {
		_ = m.updateStatus(rec.Slug, "error", err.Error())
		return
	}

	if err := m.finishReady(ctx, rec.Slug, limactl); err != nil {
		_ = m.updateStatus(rec.Slug, "error", err.Error())
		return
	}
}

func (m *Manager) buildStartArgs(_ context.Context, rec Record, templatePath string) ([]string, error) {
	args := []string{
		"start",
		"--tty=false",
		"--progress",
		"--name", rec.Slug,
	}
	if rec.Template == templateOpenClaw && strings.TrimSpace(rec.Model) != "" {
		args = append(args, "--set", fmt.Sprintf(".param.model = %q", strings.TrimSpace(rec.Model)))
	}
	args = append(args, templatePath)
	return args, nil
}

func (m *Manager) finishReady(ctx context.Context, name, limactl string) error {
	rec, err := m.requireRecord(name)
	if err != nil {
		return err
	}
	if rec.Template == templateOpenClaw {
		if err := waitForOpenClawGateway(ctx, m.outputCmd, limactl, name, openClawReadyTimeout); err != nil {
			return err
		}
		if err := waitForGuestSky10(ctx, m.outputCmd, limactl, name, openClawReadyTimeout); err != nil {
			return err
		}
		if err := waitForGuestOpenClawAgent(ctx, m.outputCmd, limactl, name, openClawReadyTimeout); err != nil {
			return err
		}
	}
	ipAddr, err := lookupLimaInstanceIPv4(ctx, m.outputCmd, limactl, name)
	if err != nil {
		m.logger.Debug("sandbox ip lookup failed", "name", name, "error", err)
	}
	if ipAddr != "" {
		if err := m.updateIPAddress(name, ipAddr); err != nil {
			return err
		}
	}
	if err := m.updateVMStatus(name, "Running"); err != nil {
		return err
	}
	return m.updateStatus(name, "ready", "")
}

func (m *Manager) requireRecord(name string) (*Record, error) {
	key, err := normalizeLookup(name)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	slug, ok := m.findRecordKeyLocked(key)
	if !ok {
		return nil, fmt.Errorf("sandbox %q not found", key)
	}
	rec, ok := m.records[slug]
	if !ok {
		return nil, fmt.Errorf("sandbox %q not found", key)
	}
	copy := rec
	return &copy, nil
}

func (m *Manager) ensureManagedApp(_ context.Context, id skyapps.ID, install bool) (string, error) {
	status, err := m.appStatus(id)
	if err != nil {
		return "", err
	}
	if status.ActivePath != "" {
		return status.ActivePath, nil
	}
	if !install {
		return "", nil
	}
	if _, err := m.appUpgr(id, nil); err != nil {
		return "", fmt.Errorf("installing %s: %w", id, err)
	}
	status, err = m.appStatus(id)
	if err != nil {
		return "", err
	}
	if status.ActivePath == "" {
		return "", fmt.Errorf("%s installed but no active binary was found", id)
	}
	return status.ActivePath, nil
}

func (m *Manager) materializeTemplate(ctx context.Context, rec Record) (string, error) {
	spec, err := sandboxTemplateDefinition(rec.Template)
	if err != nil {
		return "", err
	}
	cacheDir := filepath.Join(m.rootDir, "templates", rec.Slug)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("creating sandbox template dir: %w", err)
	}

	if err := m.prepareTemplateSharedDir(ctx, rec); err != nil {
		return "", err
	}

	renderedPath := filepath.Join(cacheDir, rec.Slug+"-"+spec.mainAsset)
	for _, assetName := range spec.assets {
		body, err := loadSandboxAsset(ctx, assetName)
		if err != nil {
			return "", err
		}
		targetPath := filepath.Join(cacheDir, assetName)
		data := body
		mode := os.FileMode(0o644)
		if strings.HasSuffix(assetName, ".sh") {
			mode = 0o755
		}
		if assetName == spec.mainAsset {
			targetPath = renderedPath
			data = renderSandboxTemplate(body, rec.Slug, rec.SharedDir)
		}
		if err := os.WriteFile(targetPath, data, mode); err != nil {
			return "", fmt.Errorf("writing sandbox template asset %q: %w", assetName, err)
		}
	}
	return renderedPath, nil
}

func (m *Manager) prepareTemplateSharedDir(ctx context.Context, rec Record) error {
	if rec.Template != templateOpenClaw {
		if err := os.MkdirAll(rec.SharedDir, 0o755); err != nil {
			return fmt.Errorf("creating shared directory: %w", err)
		}
		return nil
	}

	hostsHelper, err := loadSandboxAsset(ctx, templateHostsHelper)
	if err != nil {
		return err
	}
	pluginAssets, err := loadSandboxAssets(ctx, openClawSharedAssetFiles)
	if err != nil {
		return err
	}
	resolvedEnv := map[string]string{}
	if m.resolveOpenClawSharedEnv != nil {
		values, err := m.resolveOpenClawSharedEnv(ctx)
		if err != nil {
			m.logger.Warn("failed to resolve host secrets for sandbox env", "sandbox", rec.Slug, "error", err)
		} else {
			resolvedEnv = values
		}
	}
	if err := prepareOpenClawSharedDir(rec.SharedDir, hostsHelper, pluginAssets, resolvedEnv); err != nil {
		return err
	}
	return nil
}

func (m *Manager) limaInstanceExists(_ context.Context, _ string, name string) (bool, error) {
	path, err := limaInstanceConfigPath(name)
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(path); err == nil {
		return true, nil
	} else if os.IsNotExist(err) {
		return false, nil
	} else {
		return false, fmt.Errorf("stat Lima instance config %q: %w", path, err)
	}
}

func (m *Manager) lookupLimaInstanceStatus(ctx context.Context, limactl, name string) (string, bool, error) {
	out, err := m.outputCmd(ctx, limactl, []string{"list", "--json"})
	if err != nil {
		return "", false, err
	}
	type vm struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var v vm
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			continue
		}
		if v.Name == name {
			return v.Status, true, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", false, err
	}
	return "", false, nil
}

func normalizeCreateParams(params CreateParams) (displayName, slug, provider, template, model, sharedDir string, err error) {
	displayName = normalizeDisplayName(params.Name)
	provider = strings.ToLower(strings.TrimSpace(params.Provider))
	template = strings.ToLower(strings.TrimSpace(params.Template))
	model = strings.TrimSpace(params.Model)
	if displayName == "" {
		err = fmt.Errorf("sandbox name is required")
		return
	}
	slug = slugifySandboxName(displayName)
	if slug == "" {
		err = fmt.Errorf("sandbox name must include letters or numbers")
		return
	}
	if provider != providerLima {
		err = fmt.Errorf("unsupported sandbox provider %q (supported: %s)", provider, providerLima)
		return
	}
	if _, checkErr := sandboxTemplateDefinition(template); checkErr != nil {
		err = checkErr
		return
	}
	if template == templateOpenClaw && runtime.GOOS != "darwin" {
		err = fmt.Errorf("sandbox template %q is macOS-only for now (the current Lima template uses vz)", templateOpenClaw)
		return
	}
	sharedDir, err = defaultSharedDir(slug)
	return
}

func sandboxStatusFromVMStatus(vmStatus string) string {
	if strings.EqualFold(strings.TrimSpace(vmStatus), "running") {
		return "ready"
	}
	return "stopped"
}

func limaInstanceConfigPath(name string) (string, error) {
	dir, err := limaInstanceDirPath(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "lima.yaml"), nil
}

func limaInstanceDirPath(name string) (string, error) {
	root := strings.TrimSpace(os.Getenv("LIMA_HOME"))
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("finding home directory: %w", err)
		}
		root = filepath.Join(home, ".lima")
	}
	return filepath.Join(root, name), nil
}

func cleanupLimaInstanceDir(name string) error {
	dir, err := limaInstanceDirPath(name)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("removing Lima instance dir %q: %w", dir, err)
	}
	return nil
}

func renderSandboxTemplate(body []byte, name, sharedDir string) []byte {
	rendered := strings.ReplaceAll(string(body), templateNameToken, name)
	rendered = strings.ReplaceAll(rendered, templateSharedToken, sharedDir)
	return []byte(rendered)
}

func sandboxTemplateDefinition(template string) (templateDefinition, error) {
	switch template {
	case templateUbuntu:
		return templateDefinition{
			mainAsset: templateUbuntuAsset,
			assets:    []string{templateUbuntuAsset},
		}, nil
	case templateOpenClaw:
		return templateDefinition{
			mainAsset: templateOpenClawYAML,
			assets: []string{
				templateOpenClawYAML,
				templateOpenClawDep,
				templateOpenClawSys,
				templateOpenClawUser,
			},
		}, nil
	default:
		return templateDefinition{}, fmt.Errorf("unsupported sandbox template %q (supported: %s, %s)", template, templateUbuntu, templateOpenClaw)
	}
}

func prepareOpenClawSharedDir(sharedDir string, hostsHelper []byte, pluginAssets map[string][]byte, resolvedEnv map[string]string) error {
	if err := os.MkdirAll(sharedDir, 0o700); err != nil {
		return fmt.Errorf("creating shared directory: %w", err)
	}

	envPath := filepath.Join(sharedDir, ".env")
	existingEnv, err := os.ReadFile(envPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("checking shared env file: %w", err)
	}
	if err := os.WriteFile(envPath, BuildOpenClawSharedEnv(existingEnv, resolvedEnv), 0o600); err != nil {
		return fmt.Errorf("writing shared env file: %w", err)
	}

	if len(hostsHelper) > 0 {
		helperPath := filepath.Join(sharedDir, templateHostsHelper)
		if err := os.WriteFile(helperPath, hostsHelper, 0o755); err != nil {
			return fmt.Errorf("writing hosts helper: %w", err)
		}
	}

	for relPath, body := range pluginAssets {
		targetPath := filepath.Join(sharedDir, relPath)
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return fmt.Errorf("creating bundled plugin dir: %w", err)
		}
		if err := os.WriteFile(targetPath, body, 0o644); err != nil {
			return fmt.Errorf("writing bundled plugin asset %q: %w", relPath, err)
		}
	}

	return nil
}

func (m *Manager) refreshRuntime(ctx context.Context) error {
	limactl, err := m.ensureManagedApp(ctx, skyapps.AppLima, false)
	if err != nil {
		return err
	}
	if limactl == "" {
		return nil
	}
	out, err := m.outputCmd(ctx, limactl, []string{"list", "--json"})
	if err != nil {
		return err
	}
	type vm struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	statuses := map[string]string{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var v vm
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			continue
		}
		statuses[v.Name] = v.Status
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	m.mu.Lock()
	changed := false
	for name, rec := range m.records {
		if status, ok := statuses[name]; ok && rec.VMStatus != status {
			rec.VMStatus = status
			if status == "Running" && rec.Status == "stopped" {
				rec.Status = "ready"
			}
			if status != "Running" && rec.Status == "ready" {
				rec.Status = "stopped"
			}
			rec.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			m.records[name] = rec
			changed = true
		}
	}
	if changed {
		err = m.saveLocked()
	}
	m.mu.Unlock()
	return err
}

func (m *Manager) appendLog(slug, stream, line string) {
	line = strings.TrimRight(line, "\r\n")
	if strings.TrimSpace(line) == "" {
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
	m.mu.Lock()
	rec, ok := m.records[slug]
	if ok {
		rec.LastLogAt = entry.Time
		rec.UpdatedAt = entry.Time
		m.records[slug] = rec
		_ = m.saveLocked()
	}
	m.mu.Unlock()
	if m.emit != nil {
		displayName := slug
		if ok && strings.TrimSpace(rec.Name) != "" {
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
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.records[name]
	if !ok {
		return fmt.Errorf("sandbox %q not found", name)
	}
	rec.Status = status
	rec.LastError = lastErr
	rec.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	m.records[name] = rec
	if err := m.saveLocked(); err != nil {
		return err
	}
	m.emitState(rec)
	return nil
}

func (m *Manager) updateVMStatus(name, vmStatus string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.records[name]
	if !ok {
		return fmt.Errorf("sandbox %q not found", name)
	}
	rec.VMStatus = vmStatus
	rec.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	m.records[name] = rec
	if err := m.saveLocked(); err != nil {
		return err
	}
	m.emitState(rec)
	return nil
}

func (m *Manager) updateIPAddress(name, ip string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.records[name]
	if !ok {
		return fmt.Errorf("sandbox %q not found", name)
	}
	rec.IPAddress = ip
	rec.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	m.records[name] = rec
	if err := m.saveLocked(); err != nil {
		return err
	}
	m.emitState(rec)
	return nil
}

func (m *Manager) emitState(rec Record) {
	if m.emit == nil {
		return
	}
	m.emit("sandbox:state", rec)
}

func (m *Manager) statePath() string {
	return filepath.Join(m.rootDir, "state.json")
}

func (m *Manager) sandboxDir(name string) string {
	return filepath.Join(m.rootDir, name)
}

func (m *Manager) logPath(name string) string {
	return filepath.Join(m.sandboxDir(name), logFileName)
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
		originalName := rec.Name
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
		if err := m.migrateSandboxLogDir(originalName, rec.Slug); err == nil && originalName != rec.Slug {
			changed = true
		}
		if dir, err := defaultSharedDir(rec.Slug); err == nil && rec.SharedDir != dir {
			rec.SharedDir = dir
			changed = true
		}
		if shell := fmt.Sprintf("limactl shell %s", rec.Slug); rec.Shell != shell {
			rec.Shell = shell
			changed = true
		}
		if rec.SharedDir == "" {
			if dir, err := defaultSharedDir(rec.Slug); err == nil {
				rec.SharedDir = dir
			}
		}
		m.records[rec.Slug] = rec
	}
	if changed {
		return m.saveLocked()
	}
	return nil
}

func (m *Manager) migrateSandboxLogDir(name, slug string) error {
	name = strings.TrimSpace(name)
	slug = strings.TrimSpace(slug)
	if name == "" || slug == "" || name == slug {
		return nil
	}
	oldPath := m.sandboxDir(name)
	newPath := m.sandboxDir(slug)
	if oldPath == newPath {
		return nil
	}
	if _, err := os.Stat(oldPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if _, err := os.Stat(newPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.Rename(oldPath, newPath)
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

func defaultSharedDir(slug string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding home directory: %w", err)
	}
	return filepath.Join(home, "sky10", "sandboxes", slug), nil
}

func normalizeDisplayName(name string) string {
	return strings.TrimSpace(name)
}

func normalizeLookup(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("sandbox name is required")
	}
	return name, nil
}

func slugifySandboxName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	parts := slugWordPattern.FindAllString(name, -1)
	return strings.Join(parts, "-")
}

func (m *Manager) findRecordKeyLocked(key string) (string, bool) {
	if _, ok := m.records[key]; ok {
		return key, true
	}
	for slug, rec := range m.records {
		if rec.Name == key {
			return slug, true
		}
	}
	return "", false
}

func (m *Manager) resolveRecordKey(key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	slug, ok := m.findRecordKeyLocked(key)
	if !ok {
		return "", fmt.Errorf("sandbox %q not found", key)
	}
	return slug, nil
}

func findLocalTemplateFile(name string) (string, error) {
	candidates := make([]string, 0, 2)
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, cwd)
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Dir(exe))
	}
	for _, start := range candidates {
		for _, dir := range walkUp(start) {
			path := filepath.Join(dir, "templates", "lima", name)
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				return path, nil
			}
		}
	}
	return "", fmt.Errorf("template %q not found locally", name)
}

func loadSandboxAsset(ctx context.Context, name string) ([]byte, error) {
	if local, err := findLocalTemplateFile(name); err == nil {
		data, err := os.ReadFile(local)
		if err != nil {
			return nil, fmt.Errorf("reading local sandbox template asset: %w", err)
		}
		return data, nil
	}
	if data, err := readBundledTemplateAsset(name); err == nil {
		return data, nil
	}
	return httpRequest(ctx, templateRemoteBase+name)
}

func loadSandboxAssets(ctx context.Context, names []string) (map[string][]byte, error) {
	assets := make(map[string][]byte, len(names))
	for _, name := range names {
		body, err := loadSandboxAsset(ctx, name)
		if err != nil {
			return nil, err
		}
		assets[name] = body
	}
	return assets, nil
}

func walkUp(start string) []string {
	start = filepath.Clean(start)
	var dirs []string
	for {
		dirs = append(dirs, start)
		parent := filepath.Dir(start)
		if parent == start {
			break
		}
		start = parent
	}
	return dirs
}

func httpRequest(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("building request for %q: %w", url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading sandbox template %q: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading sandbox template %q: unexpected HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading sandbox template %q: %w", url, err)
	}
	return body, nil
}

func waitForOpenClawGateway(
	ctx context.Context,
	outputCmd func(context.Context, string, []string) ([]byte, error),
	limactl, name string,
	timeout time.Duration,
) error {
	return waitForGuestHTTPHealth(ctx, outputCmd, limactl, name, openClawReadyURL, "OpenClaw gateway", timeout)
}

func waitForGuestSky10(
	ctx context.Context,
	outputCmd func(context.Context, string, []string) ([]byte, error),
	limactl, name string,
	timeout time.Duration,
) error {
	return waitForGuestHTTPHealth(ctx, outputCmd, limactl, name, guestSky10ReadyURL, "guest sky10", timeout)
}

func waitForGuestOpenClawAgent(
	ctx context.Context,
	outputCmd func(context.Context, string, []string) ([]byte, error),
	limactl, name string,
	timeout time.Duration,
) error {
	return waitForGuestCommand(
		ctx,
		outputCmd,
		limactl,
		name,
		fmt.Sprintf(`curl -fsS http://127.0.0.1:9101/rpc -H 'Content-Type: application/json' -d '{"jsonrpc":"2.0","method":"agent.list","params":{},"id":1}' | grep -F '"name":"%s"' >/dev/null`, name),
		"guest OpenClaw agent registration",
		timeout,
	)
}

func waitForGuestHTTPHealth(
	ctx context.Context,
	outputCmd func(context.Context, string, []string) ([]byte, error),
	limactl, name, url, label string,
	timeout time.Duration,
) error {
	return waitForGuestCommand(
		ctx,
		outputCmd,
		limactl,
		name,
		fmt.Sprintf("curl -fsS %s >/dev/null", url),
		label,
		timeout,
	)
}

func waitForGuestCommand(
	ctx context.Context,
	outputCmd func(context.Context, string, []string) ([]byte, error),
	limactl, name, script, label string,
	timeout time.Duration,
) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var lastErr error
	for {
		_, err := outputCmd(waitCtx, limactl, []string{
			"shell",
			name,
			"--",
			"bash",
			"-lc",
			script,
		})
		if err == nil {
			return nil
		}
		lastErr = err

		select {
		case <-waitCtx.Done():
			if lastErr != nil {
				return fmt.Errorf("waiting for %s: %w", label, lastErr)
			}
			return fmt.Errorf("timed out waiting for %s", label)
		case <-ticker.C:
		}
	}
}

func lookupLimaInstanceIPv4(ctx context.Context, outputCmd func(context.Context, string, []string) ([]byte, error), limactl, name string) (string, error) {
	commands := []string{
		`ip -4 addr show dev lima0 | awk '/inet / {sub(/\/.*/, "", $2); print $2; exit}'`,
		`ip -4 route get 1.1.1.1 | awk '{for (i = 1; i <= NF; i++) if ($i == "src") {print $(i + 1); exit}}'`,
	}
	var lastErr error
	for _, script := range commands {
		out, err := outputCmd(ctx, limactl, []string{"shell", name, "--", "bash", "-lc", script})
		if err != nil {
			lastErr = err
			continue
		}
		if ip := strings.TrimSpace(string(out)); ip != "" {
			return ip, nil
		}
	}
	if lastErr != nil {
		return "", fmt.Errorf("querying guest IP: %w", lastErr)
	}
	return "", nil
}

func defaultRunCommand(ctx context.Context, bin string, args []string, onLine func(stream, line string)) error {
	cmd := exec.CommandContext(ctx, bin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("opening stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("opening stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	var wg sync.WaitGroup
	readPipe := func(stream string, r io.Reader) {
		defer wg.Done()
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			onLine(stream, scanner.Text())
		}
	}
	wg.Add(2)
	go readPipe("stdout", stdout)
	go readPipe("stderr", stderr)
	wg.Wait()
	return cmd.Wait()
}

func defaultOutputCommand(ctx context.Context, bin string, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	return cmd.Output()
}
