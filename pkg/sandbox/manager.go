package sandbox

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
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
	skyfs "github.com/sky10/sky10/pkg/fs"
	skyid "github.com/sky10/sky10/pkg/id"
	skyrpc "github.com/sky10/sky10/pkg/rpc"
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
	templateHermes                 = "hermes"
	templateHermesYAML             = "hermes-sky10.yaml"
	templateHermesDep              = "hermes-sky10.dependency.sh"
	templateHermesSys              = "hermes-sky10.system.sh"
	templateHermesUser             = "hermes-sky10.user.sh"
	templateHermesBridgeAsset      = "hermes-sky10-bridge.py"
	templateHermesBridgeConfig     = ".sky10-hermes-bridge.json"
	templateHostsHelper            = "update-lima-hosts.sh"
	templateOpenClawPluginDir      = "openclaw-sky10-channel"
	templateOpenClawPluginPackage  = templateOpenClawPluginDir + "/package.json"
	templateOpenClawPluginManifest = templateOpenClawPluginDir + "/openclaw.plugin.json"
	templateOpenClawPluginIndex    = templateOpenClawPluginDir + "/src/index.js"
	templateOpenClawPluginClient   = templateOpenClawPluginDir + "/src/sky10.js"
	templateOpenClawInviteFile     = ".sky10-join.json"
	templateRemoteBase             = "https://raw.githubusercontent.com/sky10ai/sky10/main/templates/lima/"
	logFileName                    = "boot.log"
	templateNameToken              = "__SKY10_SANDBOX_NAME__"
	templateSharedToken            = "__SKY10_SHARED_DIR__"
	openClawReadyTimeout           = 2 * time.Minute
	guestSky10ReadyURL             = "http://127.0.0.1:9101/health"
	guestSky10LocalRPCURL          = "http://127.0.0.1:9101/rpc"
	openClawReadyURL               = "http://127.0.0.1:18789/health"
)

var openClawSharedAssetFiles = []string{
	templateOpenClawPluginPackage,
	templateOpenClawPluginManifest,
	templateOpenClawPluginIndex,
	templateOpenClawPluginClient,
}

var hermesSharedAssetFiles = []string{
	templateHermesBridgeAsset,
}

var defaultHermesBridgeSkills = []string{
	"code",
	"shell",
	"web-search",
	"file-ops",
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
	Name              string `json:"name"`
	Slug              string `json:"slug"`
	Provider          string `json:"provider"`
	Template          string `json:"template"`
	Model             string `json:"model,omitempty"`
	Status            string `json:"status"`
	VMStatus          string `json:"vm_status,omitempty"`
	SharedDir         string `json:"shared_dir,omitempty"`
	IPAddress         string `json:"ip_address,omitempty"`
	Shell             string `json:"shell,omitempty"`
	LastError         string `json:"last_error,omitempty"`
	GuestDeviceID     string `json:"guest_device_id,omitempty"`
	GuestDevicePubKey string `json:"guest_device_pubkey,omitempty"`
	CreatedAt         string `json:"created_at"`
	UpdatedAt         string `json:"updated_at"`
	LastLogAt         string `json:"last_log_at,omitempty"`
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

type IdentityInvite struct {
	HostIdentity string
	Code         string
}

type openClawJoinPayload struct {
	HostIdentity string `json:"host_identity"`
	Code         string `json:"code"`
	HostRPCURL   string `json:"host_rpc_url,omitempty"`
	SandboxSlug  string `json:"sandbox_slug,omitempty"`
}

type hermesBridgeConfig struct {
	HostRPCURL   string   `json:"host_rpc_url"`
	AgentName    string   `json:"agent_name"`
	AgentKeyName string   `json:"agent_key_name,omitempty"`
	Skills       []string `json:"skills,omitempty"`
}

type ReconnectGuestParams struct {
	Slug       string   `json:"slug"`
	IPAddress  string   `json:"ip_address,omitempty"`
	PeerID     string   `json:"peer_id"`
	Multiaddrs []string `json:"multiaddrs"`
}

type ReconnectGuestResult struct {
	Connected bool   `json:"connected"`
	Slug      string `json:"slug"`
	IPAddress string `json:"ip_address,omitempty"`
}

type guestSkylinkStatus struct {
	PeerID string   `json:"peer_id"`
	Addrs  []string `json:"addrs"`
}

type guestIdentity struct {
	Address      string `json:"address"`
	DeviceID     string `json:"device_id"`
	DevicePubKey string `json:"device_pubkey"`
	DeviceCount  int    `json:"device_count"`
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
	resolveHermesSharedEnv   func(context.Context) (map[string]string, error)
	hostIdentity             func(context.Context) (string, error)
	issueIdentityInvite      func(context.Context) (*IdentityInvite, error)
	hostRPC                  func(context.Context, string, interface{}, interface{}) error
	guestRPC                 func(context.Context, string, string, interface{}, interface{}) error
	reconnectInterval        time.Duration
	reconnectSweepTimeout    time.Duration
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
		hostRPC:   hostRPCCall,
		guestRPC:  guestRPCCall,
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

func (m *Manager) SetHermesSharedEnvResolver(fn func(context.Context) (map[string]string, error)) {
	m.resolveHermesSharedEnv = fn
}

func (m *Manager) SetHostIdentityProvider(fn func(context.Context) (string, error)) {
	m.hostIdentity = fn
}

func (m *Manager) SetIdentityInviteIssuer(fn func(context.Context) (*IdentityInvite, error)) {
	m.issueIdentityInvite = fn
}

func (m *Manager) ReconnectRunningOpenClawSandboxes(ctx context.Context) error {
	if m.hostIdentity == nil || m.hostRPC == nil || m.guestRPC == nil {
		return nil
	}
	if err := m.refreshRuntime(ctx); err != nil {
		return err
	}

	limactl, err := m.ensureManagedApp(ctx, skyapps.AppLima, false)
	if err != nil {
		return err
	}
	if limactl == "" {
		return nil
	}

	hostIdentity, err := m.hostIdentity(ctx)
	if err != nil {
		return err
	}
	hostIdentity = strings.TrimSpace(hostIdentity)
	if hostIdentity == "" {
		return nil
	}

	m.mu.Lock()
	items := make([]Record, 0, len(m.records))
	for _, rec := range m.records {
		if rec.Template != templateOpenClaw && rec.Template != templateHermes {
			continue
		}
		if rec.VMStatus != "Running" {
			continue
		}
		items = append(items, rec)
	}
	m.mu.Unlock()

	for _, rec := range items {
		if rec.IPAddress == "" {
			ipAddr, err := lookupLimaInstanceIPv4(ctx, m.outputCmd, limactl, rec.Slug)
			if err != nil {
				m.logger.Warn("sandbox reconnect skipped: guest IP lookup failed", "sandbox", rec.Slug, "error", err)
				continue
			}
			if strings.TrimSpace(ipAddr) == "" {
				m.logger.Warn("sandbox reconnect skipped: guest IP unavailable", "sandbox", rec.Slug)
				continue
			}
			rec.IPAddress = strings.TrimSpace(ipAddr)
			if err := m.updateIPAddress(rec.Slug, rec.IPAddress); err != nil {
				return err
			}
		}

		reconnectCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := m.waitForGuestIdentityMatch(reconnectCtx, rec, hostIdentity)
		if err == nil {
			err = m.ensureHostConnectedGuestAgent(reconnectCtx, rec, hostIdentity)
		}
		cancel()
		if err != nil {
			m.logger.Warn("sandbox reconnect failed", "sandbox", rec.Slug, "error", err)
			continue
		}
		if err := m.updateVMStatus(rec.Slug, "Running"); err != nil {
			return err
		}
		if err := m.updateStatus(rec.Slug, "ready", ""); err != nil {
			return err
		}
	}

	return nil
}

func (m *Manager) RunManagedReconnectLoop(ctx context.Context) {
	interval := m.reconnectInterval
	if interval <= 0 {
		interval = 15 * time.Second
	}
	sweepTimeout := m.reconnectSweepTimeout
	if sweepTimeout <= 0 {
		sweepTimeout = 45 * time.Second
	}

	runSweep := func() {
		sweepCtx, cancel := context.WithTimeout(ctx, sweepTimeout)
		defer cancel()
		if err := m.ReconnectRunningOpenClawSandboxes(sweepCtx); err != nil && sweepCtx.Err() == nil {
			m.logger.Warn("sandbox reconnect sweep failed", "error", err)
		}
	}

	runSweep()

	timer := time.NewTimer(interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			runSweep()
			timer.Reset(interval)
		}
	}
}

func (m *Manager) waitForGuestIdentityMatch(ctx context.Context, rec Record, hostIdentity string) error {
	if m.guestRPC == nil {
		return nil
	}
	hostIdentity = strings.TrimSpace(hostIdentity)
	if hostIdentity == "" {
		return nil
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var lastErr error
	for {
		guest, err := m.readGuestIdentity(ctx, rec, rec.IPAddress)
		if err != nil {
			lastErr = err
		} else {
			if err := m.recordGuestIdentity(rec.Slug, guest); err != nil {
				return err
			}
			guestIdentity := strings.TrimSpace(guest.Address)
			if strings.EqualFold(guestIdentity, hostIdentity) {
				return nil
			}
			return fmt.Errorf("guest identity %q for sandbox %q does not match host %q", guestIdentity, rec.Name, hostIdentity)
		}

		select {
		case <-ctx.Done():
			if lastErr != nil {
				return lastErr
			}
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (m *Manager) ReconnectGuest(ctx context.Context, params ReconnectGuestParams) (*ReconnectGuestResult, error) {
	rec, err := m.requireRecord(params.Slug)
	if err != nil {
		return nil, err
	}

	ipAddr := strings.TrimSpace(params.IPAddress)
	if ipAddr != "" && ipAddr != rec.IPAddress {
		if err := m.updateIPAddress(rec.Slug, ipAddr); err != nil {
			return nil, err
		}
		rec, err = m.requireRecord(rec.Slug)
		if err != nil {
			return nil, err
		}
	}

	guest := guestSkylinkStatus{
		PeerID: strings.TrimSpace(params.PeerID),
		Addrs:  append([]string(nil), params.Multiaddrs...),
	}
	if err := m.connectHostToGuestPeer(ctx, *rec, guest); err != nil {
		return nil, err
	}
	m.appendLog(rec.Slug, "stdout", "guest sky10 requested host reconnect")
	return &ReconnectGuestResult{
		Connected: true,
		Slug:      rec.Slug,
		IPAddress: rec.IPAddress,
	}, nil
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
		Shell:     defaultShellCommand(slug, template),
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
			Shell:     defaultShellCommand(slug, template),
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
		rec = m.captureGuestDeviceIdentity(ctx, rec, limactl)
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

	if err := m.removeSandboxDevice(ctx, *rec); err != nil {
		return nil, err
	}
	if err := m.forgetRecord(*rec); err != nil {
		return nil, err
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
	if (rec.Template == templateOpenClaw || rec.Template == templateHermes) && strings.TrimSpace(rec.Model) != "" {
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
		hostIdentity, err := m.ensureGuestJoinedHostIdentity(ctx, *rec, limactl)
		if err != nil {
			return err
		}
		if err := waitForGuestOpenClawAgent(ctx, m.outputCmd, limactl, name, openClawReadyTimeout); err != nil {
			return err
		}
		updatedRec, err := m.requireRecord(name)
		if err != nil {
			return err
		}
		if err := m.ensureHostConnectedGuestAgent(ctx, *updatedRec, hostIdentity); err != nil {
			return err
		}
	}
	if rec.Template == templateHermes {
		if err := waitForGuestHermesCLI(ctx, m.outputCmd, limactl, name, openClawReadyTimeout); err != nil {
			return err
		}
		if err := waitForGuestSky10(ctx, m.outputCmd, limactl, name, openClawReadyTimeout); err != nil {
			return err
		}
		hostIdentity, err := m.ensureGuestJoinedHostIdentity(ctx, *rec, limactl)
		if err != nil {
			return err
		}
		if err := waitForGuestHermesAgent(ctx, m.outputCmd, limactl, name, openClawReadyTimeout); err != nil {
			return err
		}
		updatedRec, err := m.requireRecord(name)
		if err != nil {
			return err
		}
		if err := m.ensureHostConnectedGuestAgent(ctx, *updatedRec, hostIdentity); err != nil {
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
	if id == skyapps.AppLima {
		if bin, err := exec.LookPath("limactl"); err == nil {
			return bin, nil
		}
		status, err := m.appStatus(id)
		if err != nil {
			return "", err
		}
		if status.ActivePath != "" && !status.Managed {
			return status.ActivePath, nil
		}
		if !install {
			return "", nil
		}
		return "", fmt.Errorf("limactl not found on PATH; managed Lima installs are not used by sandbox flows yet")
	}

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
	switch rec.Template {
	case templateHermes:
		sharedAssets, err := loadSandboxAssets(ctx, hermesSharedAssetFiles)
		if err != nil {
			return err
		}
		resolvedEnv := map[string]string{}
		if m.resolveHermesSharedEnv != nil {
			values, err := m.resolveHermesSharedEnv(ctx)
			if err != nil {
				m.logger.Warn("failed to resolve host secrets for sandbox env", "sandbox", rec.Slug, "error", err)
			} else {
				resolvedEnv = values
			}
		}
		var invite *IdentityInvite
		if m.issueIdentityInvite != nil {
			value, err := m.issueIdentityInvite(ctx)
			if err != nil {
				m.logger.Warn("failed to issue host invite for hermes sandbox bootstrap", "sandbox", rec.Slug, "error", err)
			} else {
				invite = value
			}
		}
		hostRPCURL := ""
		if m.hostRPC != nil {
			value, err := m.guestReachableHostRPCURL(ctx)
			if err != nil {
				m.logger.Warn("failed to resolve host http rpc url for hermes sandbox bootstrap", "sandbox", rec.Slug, "error", err)
			} else {
				hostRPCURL = value
			}
		}
		return prepareHermesSharedDir(rec.SharedDir, resolvedEnv, sharedAssets, buildHermesBridgeConfig(rec), invite, rec.Slug, hostRPCURL)
	case templateOpenClaw:
	default:
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
	var invite *IdentityInvite
	if m.issueIdentityInvite != nil {
		value, err := m.issueIdentityInvite(ctx)
		if err != nil {
			m.logger.Warn("failed to issue host invite for sandbox bootstrap", "sandbox", rec.Slug, "error", err)
		} else {
			invite = value
		}
	}
	hostRPCURL := ""
	if m.hostRPC != nil {
		value, err := m.guestReachableHostRPCURL(ctx)
		if err != nil {
			m.logger.Warn("failed to resolve host http rpc url for sandbox bootstrap", "sandbox", rec.Slug, "error", err)
		} else {
			hostRPCURL = value
		}
	}
	if err := prepareOpenClawSharedDir(rec.SharedDir, hostsHelper, pluginAssets, resolvedEnv, invite, rec.Slug, hostRPCURL); err != nil {
		return err
	}
	return nil
}

func buildHermesBridgeConfig(rec Record) *hermesBridgeConfig {
	config := &hermesBridgeConfig{
		HostRPCURL:   guestSky10LocalRPCURL,
		AgentName:    strings.TrimSpace(rec.Name),
		AgentKeyName: strings.TrimSpace(rec.Slug),
		Skills:       append([]string(nil), defaultHermesBridgeSkills...),
	}
	if config.AgentName == "" {
		config.AgentName = strings.TrimSpace(rec.Slug)
	}
	if config.AgentKeyName == "" {
		config.AgentKeyName = strings.TrimSpace(rec.Name)
	}
	return config
}

func (m *Manager) ensureGuestJoinedHostIdentity(ctx context.Context, rec Record, limactl string) (string, error) {
	if m.hostIdentity == nil || m.issueIdentityInvite == nil {
		return "", nil
	}

	hostIdentity, err := m.hostIdentity(ctx)
	if err != nil {
		return "", fmt.Errorf("resolving host identity for sandbox %q: %w", rec.Name, err)
	}
	hostIdentity = strings.TrimSpace(hostIdentity)
	if hostIdentity == "" {
		return "", fmt.Errorf("resolving host identity for sandbox %q: empty identity", rec.Name)
	}

	ipAddr, err := lookupLimaInstanceIPv4(ctx, m.outputCmd, limactl, rec.Slug)
	if err != nil {
		return "", fmt.Errorf("resolving guest IP for sandbox %q: %w", rec.Name, err)
	}
	if strings.TrimSpace(ipAddr) == "" {
		return "", fmt.Errorf("resolving guest IP for sandbox %q: guest IP unavailable", rec.Name)
	}
	if err := m.updateIPAddress(rec.Slug, ipAddr); err != nil {
		return "", err
	}

	guest, err := m.readGuestIdentity(ctx, rec, ipAddr)
	if err != nil {
		return "", err
	}
	if err := m.recordGuestIdentity(rec.Slug, guest); err != nil {
		return "", err
	}
	guestIdentity := strings.TrimSpace(guest.Address)
	if strings.EqualFold(guestIdentity, hostIdentity) {
		m.appendLog(rec.Slug, "stdout", "guest sky10 already joined to host identity")
		return hostIdentity, nil
	}
	if guest.DeviceCount > 1 {
		return "", fmt.Errorf("guest sky10 in sandbox %q is already linked to identity %q", rec.Name, guestIdentity)
	}

	invite, err := m.issueIdentityInvite(ctx)
	if err != nil {
		return "", fmt.Errorf("creating host invite for sandbox %q: %w", rec.Name, err)
	}
	if invite == nil {
		return "", fmt.Errorf("creating host invite for sandbox %q: no invite returned", rec.Name)
	}
	if strings.TrimSpace(invite.Code) == "" {
		return "", fmt.Errorf("creating host invite for sandbox %q: empty invite code", rec.Name)
	}
	m.appendLog(rec.Slug, "stdout", "joining guest sky10 to host identity")

	params := map[string]string{
		"code": strings.TrimSpace(invite.Code),
		"role": skyid.DeviceRoleSandbox,
	}
	var joinResult struct {
		DeviceID     string `json:"device_id"`
		DevicePubKey string `json:"device_pubkey"`
	}
	if err := m.guestRPC(ctx, ipAddr, "identity.join", params, &joinResult); err != nil {
		return "", fmt.Errorf("joining guest sky10 for sandbox %q: %w", rec.Name, err)
	}
	if err := waitForGuestSky10(ctx, m.outputCmd, limactl, rec.Slug, openClawReadyTimeout); err != nil {
		return "", fmt.Errorf("waiting for guest sky10 after join: %w", err)
	}
	if err := m.recordGuestDevice(rec.Slug, joinResult.DeviceID, joinResult.DevicePubKey); err != nil {
		return "", err
	}
	m.appendLog(rec.Slug, "stdout", "guest sky10 joined host identity")
	return hostIdentity, nil
}

func (m *Manager) ensureHostConnectedGuestAgent(ctx context.Context, rec Record, hostIdentity string) error {
	if m.hostRPC == nil {
		return nil
	}
	hostIdentity = strings.TrimSpace(hostIdentity)
	if hostIdentity == "" {
		return nil
	}

	waitCtx, cancel := context.WithTimeout(ctx, openClawReadyTimeout)
	defer cancel()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var lastErr error
	for {
		attemptedDirect := false
		if m.guestRPC != nil && strings.TrimSpace(rec.IPAddress) != "" {
			var guest guestSkylinkStatus
			if err := m.guestRPC(waitCtx, rec.IPAddress, "skylink.status", nil, &guest); err != nil {
				lastErr = fmt.Errorf("reading guest skylink status for sandbox %q: %w", rec.Name, err)
			} else if strings.TrimSpace(guest.PeerID) != "" && len(guest.Addrs) > 0 {
				attemptedDirect = true
				if err := m.connectHostToGuestPeer(waitCtx, rec, guest); err != nil {
					lastErr = fmt.Errorf("connecting host sky10 directly to guest peer %q: %w", guest.PeerID, err)
				}
			}
		}
		if !attemptedDirect {
			if err := m.hostRPC(waitCtx, "skylink.connect", map[string]string{"address": hostIdentity}, nil); err != nil {
				lastErr = fmt.Errorf("connecting host sky10 to guest identity %q: %w", hostIdentity, err)
			}
		}

		if err := m.waitForHostAgentVisible(waitCtx, rec); err != nil {
			lastErr = err
		} else {
			m.appendLog(rec.Slug, "stdout", "host sky10 connected to guest peer")
			return nil
		}

		select {
		case <-waitCtx.Done():
			if lastErr != nil {
				return lastErr
			}
			return fmt.Errorf("timed out waiting for host sky10 to connect to sandbox %q", rec.Name)
		case <-ticker.C:
		}
	}
}

func (m *Manager) connectHostToGuestPeer(ctx context.Context, rec Record, guest guestSkylinkStatus) error {
	if m.hostRPC == nil {
		return nil
	}
	peerID := strings.TrimSpace(guest.PeerID)
	if peerID == "" {
		return fmt.Errorf("guest peer id is required")
	}
	multiaddrs := filterGuestMultiaddrsForIPAddress(guest.Addrs, rec.IPAddress)
	if len(multiaddrs) == 0 {
		return fmt.Errorf("no guest multiaddrs match sandbox ip %q", rec.IPAddress)
	}
	params := map[string]interface{}{
		"peer_id":    peerID,
		"multiaddrs": multiaddrs,
	}
	return m.hostRPC(ctx, "skylink.connectPeer", params, nil)
}

func (m *Manager) waitForHostAgentVisible(ctx context.Context, rec Record) error {
	type agentListResult struct {
		Agents []struct {
			Name string `json:"name"`
		} `json:"agents"`
	}

	var listed agentListResult
	if err := m.hostRPC(ctx, "agent.list", nil, &listed); err != nil {
		return fmt.Errorf("listing host agents after guest join: %w", err)
	}
	for _, agent := range listed.Agents {
		if agent.Name == rec.Name || agent.Name == rec.Slug {
			return nil
		}
	}
	return fmt.Errorf("guest agent %q not yet visible on host", rec.Name)
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
	if (template == templateOpenClaw || template == templateHermes) && runtime.GOOS != "darwin" {
		err = fmt.Errorf("sandbox template %q is macOS-only for now (the current Lima template uses vz)", template)
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

func defaultShellCommand(slug, template string) string {
	if template == templateHermes {
		return fmt.Sprintf("limactl shell %s -- bash -lc 'hermes-shared'", slug)
	}
	return fmt.Sprintf("limactl shell %s", slug)
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
	case templateHermes:
		return templateDefinition{
			mainAsset: templateHermesYAML,
			assets: []string{
				templateHermesYAML,
				templateHermesDep,
				templateHermesSys,
				templateHermesUser,
			},
		}, nil
	default:
		return templateDefinition{}, fmt.Errorf("unsupported sandbox template %q (supported: %s, %s, %s)", template, templateUbuntu, templateOpenClaw, templateHermes)
	}
}

func prepareOpenClawSharedDir(sharedDir string, hostsHelper []byte, pluginAssets map[string][]byte, resolvedEnv map[string]string, invite *IdentityInvite, sandboxSlug, hostRPCURL string) error {
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

	if invite != nil && strings.TrimSpace(invite.Code) != "" {
		if err := writeSandboxJoinPayload(sharedDir, invite, sandboxSlug, hostRPCURL); err != nil {
			return err
		}
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

func prepareHermesSharedDir(sharedDir string, resolvedEnv map[string]string, sharedAssets map[string][]byte, bridgeConfig *hermesBridgeConfig, invite *IdentityInvite, sandboxSlug, hostRPCURL string) error {
	if err := os.MkdirAll(sharedDir, 0o700); err != nil {
		return fmt.Errorf("creating shared directory: %w", err)
	}

	envPath := filepath.Join(sharedDir, ".env")
	existingEnv, err := os.ReadFile(envPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("checking shared env file: %w", err)
	}
	if err := os.WriteFile(envPath, BuildHermesSharedEnv(existingEnv, resolvedEnv), 0o600); err != nil {
		return fmt.Errorf("writing shared env file: %w", err)
	}

	if bridgeConfig != nil {
		body, err := json.Marshal(bridgeConfig)
		if err != nil {
			return fmt.Errorf("marshaling hermes bridge config: %w", err)
		}
		configPath := filepath.Join(sharedDir, templateHermesBridgeConfig)
		if err := os.WriteFile(configPath, append(body, '\n'), 0o600); err != nil {
			return fmt.Errorf("writing hermes bridge config: %w", err)
		}
	}

	if invite != nil && strings.TrimSpace(invite.Code) != "" {
		if err := writeSandboxJoinPayload(sharedDir, invite, sandboxSlug, hostRPCURL); err != nil {
			return err
		}
	}

	for relPath, body := range sharedAssets {
		targetPath := filepath.Join(sharedDir, relPath)
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return fmt.Errorf("creating hermes shared asset dir: %w", err)
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(relPath, ".py") || strings.HasSuffix(relPath, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(targetPath, body, mode); err != nil {
			return fmt.Errorf("writing hermes shared asset %q: %w", relPath, err)
		}
	}

	return nil
}

func writeSandboxJoinPayload(sharedDir string, invite *IdentityInvite, sandboxSlug, hostRPCURL string) error {
	payload := openClawJoinPayload{
		HostIdentity: strings.TrimSpace(invite.HostIdentity),
		Code:         strings.TrimSpace(invite.Code),
		HostRPCURL:   strings.TrimSpace(hostRPCURL),
		SandboxSlug:  strings.TrimSpace(sandboxSlug),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling sandbox join payload: %w", err)
	}
	invitePath := filepath.Join(sharedDir, templateOpenClawInviteFile)
	if err := os.WriteFile(invitePath, append(body, '\n'), 0o600); err != nil {
		return fmt.Errorf("writing sandbox join payload: %w", err)
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
	missing := make([]Record, 0)
	for name, rec := range m.records {
		status, ok := statuses[name]
		if ok {
			if rec.VMStatus != status {
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
			continue
		}
		if m.shouldAutoRemoveMissingRecord(rec) {
			missing = append(missing, rec)
		}
	}
	if changed {
		err = m.saveLocked()
	}
	m.mu.Unlock()
	if err != nil {
		return err
	}
	for _, rec := range missing {
		if err := m.cleanupMissingSandbox(ctx, rec); err != nil {
			m.logger.Warn("sandbox cleanup after missing runtime failed", "sandbox", rec.Slug, "error", err)
		}
	}
	return nil
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

func (m *Manager) recordGuestIdentity(name string, guest guestIdentity) error {
	return m.recordGuestDevice(name, guest.DeviceID, guest.DevicePubKey)
}

func (m *Manager) recordGuestDevice(name, deviceID, devicePubKey string) error {
	deviceID = strings.TrimSpace(deviceID)
	devicePubKey = strings.ToLower(strings.TrimSpace(devicePubKey))
	if deviceID == "" && devicePubKey == "" {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	rec, ok := m.records[name]
	if !ok {
		return fmt.Errorf("sandbox %q not found", name)
	}

	changed := false
	if deviceID != "" && rec.GuestDeviceID != deviceID {
		rec.GuestDeviceID = deviceID
		changed = true
	}
	if devicePubKey != "" && rec.GuestDevicePubKey != devicePubKey {
		rec.GuestDevicePubKey = devicePubKey
		changed = true
	}
	if !changed {
		return nil
	}

	rec.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	m.records[name] = rec
	if err := m.saveLocked(); err != nil {
		return err
	}
	m.emitState(rec)
	return nil
}

func (m *Manager) readGuestIdentity(ctx context.Context, rec Record, ipAddr string) (guestIdentity, error) {
	var guest guestIdentity
	if err := m.guestRPC(ctx, ipAddr, "identity.show", nil, &guest); err != nil {
		return guestIdentity{}, fmt.Errorf("reading guest identity for sandbox %q: %w", rec.Name, err)
	}
	return guest, nil
}

func (m *Manager) captureGuestDeviceIdentity(ctx context.Context, rec *Record, limactl string) *Record {
	if rec == nil || m.guestRPC == nil || strings.TrimSpace(rec.GuestDevicePubKey) != "" {
		return rec
	}

	copy := *rec
	ipAddr := strings.TrimSpace(copy.IPAddress)
	if ipAddr == "" {
		value, err := lookupLimaInstanceIPv4(ctx, m.outputCmd, limactl, copy.Slug)
		if err == nil && strings.TrimSpace(value) != "" {
			ipAddr = strings.TrimSpace(value)
			if err := m.updateIPAddress(copy.Slug, ipAddr); err == nil {
				copy.IPAddress = ipAddr
			}
		}
	}
	if ipAddr == "" {
		return &copy
	}

	guest, err := m.readGuestIdentity(ctx, copy, ipAddr)
	if err != nil {
		m.logger.Debug("sandbox guest identity capture skipped", "sandbox", copy.Slug, "error", err)
		return &copy
	}
	if err := m.recordGuestIdentity(copy.Slug, guest); err != nil {
		m.logger.Debug("sandbox guest identity capture failed", "sandbox", copy.Slug, "error", err)
		return &copy
	}
	copy.GuestDeviceID = strings.TrimSpace(guest.DeviceID)
	copy.GuestDevicePubKey = strings.ToLower(strings.TrimSpace(guest.DevicePubKey))
	return &copy
}

func (m *Manager) removeSandboxDevice(ctx context.Context, rec Record) error {
	if m.hostRPC == nil {
		return nil
	}

	pubKey := strings.ToLower(strings.TrimSpace(rec.GuestDevicePubKey))
	if pubKey == "" {
		return nil
	}
	if err := m.hostRPC(ctx, "identity.deviceRemove", map[string]string{"pubkey": pubKey}, nil); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "device not found in private network") {
			return nil
		}
		return fmt.Errorf("removing sandbox device for %q: %w", rec.Name, err)
	}
	return nil
}

func (m *Manager) forgetRecord(rec Record) error {
	m.mu.Lock()
	delete(m.records, rec.Slug)
	delete(m.running, rec.Slug)
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

func (m *Manager) shouldAutoRemoveMissingRecord(rec Record) bool {
	if m.running[rec.Slug] || rec.Status == "creating" || rec.Status == "starting" {
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
		if shell := defaultShellCommand(rec.Slug, rec.Template); rec.Shell != shell {
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

func waitForGuestHermesAgent(
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
		"guest Hermes agent registration",
		timeout,
	)
}

func waitForGuestHermesCLI(
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
		`export PATH="$HOME/.local/bin:$HOME/.cargo/bin:$PATH"; command -v hermes >/dev/null`,
		"Hermes CLI",
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

func guestRPCCall(ctx context.Context, address, method string, params interface{}, out interface{}) error {
	url := guestSky10RPCURL(address)
	var rawParams json.RawMessage
	if params != nil {
		body, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("marshal guest rpc params for %s: %w", method, err)
		}
		rawParams = body
	}
	reqBody, err := json.Marshal(skyrpc.Request{
		JSONRPC: "2.0",
		Method:  method,
		Params:  rawParams,
		ID:      1,
	})
	if err != nil {
		return fmt.Errorf("marshal guest rpc request for %s: %w", method, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("build guest rpc request for %s: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("post guest rpc %s: %w", method, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("guest rpc %s: unexpected HTTP %d: %s", method, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var rpcResp struct {
		Result json.RawMessage `json:"result,omitempty"`
		Error  *skyrpc.Error   `json:"error,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return fmt.Errorf("decode guest rpc %s response: %w", method, err)
	}
	if rpcResp.Error != nil {
		return fmt.Errorf("%s", rpcResp.Error.Message)
	}
	if out == nil || len(rpcResp.Result) == 0 || bytes.Equal(rpcResp.Result, []byte("null")) {
		return nil
	}
	if err := json.Unmarshal(rpcResp.Result, out); err != nil {
		return fmt.Errorf("decode guest rpc %s result: %w", method, err)
	}
	return nil
}

func hostRPCCall(ctx context.Context, method string, params interface{}, out interface{}) error {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", skyfs.DaemonSocketPath())
	if err != nil {
		return fmt.Errorf("dial host daemon for %s: %w", method, err)
	}
	defer conn.Close()

	var rawParams json.RawMessage
	if params != nil {
		body, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("marshal host rpc params for %s: %w", method, err)
		}
		rawParams = body
	}

	req := skyrpc.Request{
		JSONRPC: "2.0",
		Method:  method,
		Params:  rawParams,
		ID:      1,
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return fmt.Errorf("send host rpc %s: %w", method, err)
	}

	var rpcResp struct {
		Result json.RawMessage `json:"result,omitempty"`
		Error  *skyrpc.Error   `json:"error,omitempty"`
	}
	if err := json.NewDecoder(conn).Decode(&rpcResp); err != nil {
		return fmt.Errorf("decode host rpc %s response: %w", method, err)
	}
	if rpcResp.Error != nil {
		return fmt.Errorf("%s", rpcResp.Error.Message)
	}
	if out == nil || len(rpcResp.Result) == 0 || bytes.Equal(rpcResp.Result, []byte("null")) {
		return nil
	}
	if err := json.Unmarshal(rpcResp.Result, out); err != nil {
		return fmt.Errorf("decode host rpc %s result: %w", method, err)
	}
	return nil
}

func guestSky10RPCURL(address string) string {
	base := strings.TrimSpace(address)
	if strings.HasPrefix(base, "http://") || strings.HasPrefix(base, "https://") {
		return strings.TrimRight(base, "/") + "/rpc"
	}
	return fmt.Sprintf("http://%s:9101/rpc", base)
}

func (m *Manager) guestReachableHostRPCURL(ctx context.Context) (string, error) {
	var health struct {
		HTTPAddr string `json:"http_addr"`
	}
	if err := m.hostRPC(ctx, "skyfs.health", nil, &health); err != nil {
		return "", fmt.Errorf("reading host http address: %w", err)
	}
	port := httpPortFromAddr(strings.TrimSpace(health.HTTPAddr))
	if port == "" {
		return "", fmt.Errorf("host http address is empty")
	}
	return fmt.Sprintf("http://host.lima.internal:%s/rpc", port), nil
}

func httpPortFromAddr(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	if strings.HasPrefix(addr, ":") {
		return strings.TrimPrefix(addr, ":")
	}
	if host, port, err := net.SplitHostPort(addr); err == nil {
		if host == "" || port == "" {
			return ""
		}
		return port
	}
	if i := strings.LastIndex(addr, ":"); i >= 0 && i < len(addr)-1 {
		return addr[i+1:]
	}
	return ""
}

func filterGuestMultiaddrsForIPAddress(addrs []string, ipAddr string) []string {
	ipAddr = strings.TrimSpace(ipAddr)
	if ipAddr == "" {
		return nil
	}
	needle := "/ip4/" + ipAddr + "/"
	filtered := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		addr = strings.TrimSpace(addr)
		if strings.Contains(addr, needle) {
			filtered = append(filtered, addr)
		}
	}
	return filtered
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
