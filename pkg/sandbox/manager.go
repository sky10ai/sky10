package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	skyapps "github.com/sky10/sky10/pkg/apps"
	"github.com/sky10/sky10/pkg/config"
)

const (
	providerLima                     = "lima"
	templateUbuntu                   = "ubuntu"
	templateUbuntuAsset              = "ubuntu-sky10.yaml"
	templateOpenClaw                 = "openclaw"
	templateOpenClawYAML             = "openclaw-sky10.yaml"
	templateOpenClawDep              = "openclaw-sky10.dependency.sh"
	templateOpenClawSys              = "openclaw-sky10.system.sh"
	templateOpenClawUser             = "openclaw-sky10.user.sh"
	templateOpenClawDocker           = "openclaw-docker"
	templateOpenClawDockerYAML       = "openclaw-docker-sky10.yaml"
	templateOpenClawDockerDep        = "openclaw-docker-sky10.dependency.sh"
	templateOpenClawDockerSys        = "openclaw-docker-sky10.system.sh"
	templateOpenClawDockerUser       = "openclaw-docker-sky10.user.sh"
	templateHermes                   = "hermes"
	templateHermesYAML               = "hermes-sky10.yaml"
	templateHermesDep                = "hermes-sky10.dependency.sh"
	templateHermesSys                = "hermes-sky10.system.sh"
	templateHermesUser               = "hermes-sky10.user.sh"
	templateHermesDocker             = "hermes-docker"
	templateHermesDockerYAML         = "hermes-docker-sky10.yaml"
	templateHermesDockerDep          = "hermes-docker-sky10.dependency.sh"
	templateHermesDockerSys          = "hermes-docker-sky10.system.sh"
	templateHermesDockerUser         = "hermes-docker-sky10.user.sh"
	templateHermesBridgeAsset        = "hermes-sky10-bridge.py"
	templateHermesBridgeConfig       = "bridge.json"
	templateHostsHelper              = "update-lima-hosts.sh"
	templateOpenClawPluginDir        = "openclaw-sky10-channel"
	templateOpenClawPluginPackage    = templateOpenClawPluginDir + "/package.json"
	templateOpenClawPluginManifest   = templateOpenClawPluginDir + "/openclaw.plugin.json"
	templateOpenClawPluginIndex      = templateOpenClawPluginDir + "/src/index.js"
	templateOpenClawPluginMedia      = templateOpenClawPluginDir + "/src/media.js"
	templateOpenClawPluginClient     = templateOpenClawPluginDir + "/src/sky10.js"
	templateOpenClawDockerRuntimeDir = "openclaw-docker-runtime"
	templateOpenClawDockerfile       = templateOpenClawDockerRuntimeDir + "/Dockerfile"
	templateOpenClawDockerEntrypoint = templateOpenClawDockerRuntimeDir + "/entrypoint.sh"
	templateHermesDockerRuntimeDir   = "hermes-docker-runtime"
	templateHermesDockerfile         = templateHermesDockerRuntimeDir + "/Dockerfile"
	templateHermesDockerEntrypoint   = templateHermesDockerRuntimeDir + "/entrypoint.sh"
	templateOpenClawInviteFile       = "join.json"
	templateRemoteBase               = "https://raw.githubusercontent.com/sky10ai/sky10/main/templates/lima/"
	logFileName                      = "boot.log"
	templateNameToken                = "__SKY10_SANDBOX_NAME__"
	templateSharedToken              = "__SKY10_SHARED_DIR__"
	templateStateToken               = "__SKY10_STATE_DIR__"
	sandboxStateDirName              = "state"
	sandboxLogsDirName               = "logs"
	agentWorkspaceDirName            = "workspace"
	agentDriveRootName               = "Agents"
	agentDriveNamePrefix             = "agent-"
	openClawReadyTimeout             = 2 * time.Minute
	guestSky10ReadyURL               = "http://127.0.0.1:9101/health"
	guestSky10LocalRPCURL            = "http://127.0.0.1:9101/rpc"
	openClawReadyURL                 = "http://127.0.0.1:18789/health"
	progressMarkerPrefix             = "SKY10_PROGRESS "
)

var slugWordPattern = regexp.MustCompile(`[a-z0-9]+`)

var (
	sandboxAppStatusFor   = skyapps.StatusFor
	sandboxAppUpgrade     = skyapps.Upgrade
	sandboxAppManagedPath = skyapps.ManagedPath
)

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
	Name              string    `json:"name"`
	Slug              string    `json:"slug"`
	Provider          string    `json:"provider"`
	Template          string    `json:"template"`
	Model             string    `json:"model,omitempty"`
	Status            string    `json:"status"`
	VMStatus          string    `json:"vm_status,omitempty"`
	SharedDir         string    `json:"shared_dir,omitempty"`
	IPAddress         string    `json:"ip_address,omitempty"`
	Shell             string    `json:"shell,omitempty"`
	LastError         string    `json:"last_error,omitempty"`
	Progress          *Progress `json:"progress,omitempty"`
	GuestDeviceID     string    `json:"guest_device_id,omitempty"`
	GuestDevicePubKey string    `json:"guest_device_pubkey,omitempty"`
	CreatedAt         string    `json:"created_at"`
	UpdatedAt         string    `json:"updated_at"`
	LastLogAt         string    `json:"last_log_at,omitempty"`
}

type LogEntry struct {
	Time   string `json:"time"`
	Stream string `json:"stream"`
	Line   string `json:"line"`
}

type Progress struct {
	StepID  string `json:"step_id,omitempty"`
	Summary string `json:"summary,omitempty"`
	Percent int    `json:"percent"`
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

type progressEvent struct {
	Event   string `json:"event"`
	ID      string `json:"id"`
	Summary string `json:"summary,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

type progressStep struct {
	ID      string
	Summary string
}

type progressTracker struct {
	plan        []progressStep
	index       map[string]int
	completed   map[string]bool
	open        []string
	openSummary map[string]string
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

func newProgressTracker(provider, template string) *progressTracker {
	plan := sandboxProgressPlan(provider, template)
	if len(plan) == 0 {
		return nil
	}
	index := make(map[string]int, len(plan))
	for i, step := range plan {
		index[step.ID] = i
	}
	return &progressTracker{
		plan:        plan,
		index:       index,
		completed:   make(map[string]bool, len(plan)),
		openSummary: make(map[string]string, len(plan)),
	}
}

func (t *progressTracker) current() (string, string) {
	for i := len(t.open) - 1; i >= 0; i-- {
		id := t.open[i]
		if summary := strings.TrimSpace(t.openSummary[id]); summary != "" {
			return id, summary
		}
	}
	return "", ""
}

func (t *progressTracker) removeOpen(id string) {
	if id == "" {
		return
	}
	filtered := t.open[:0]
	for _, item := range t.open {
		if item != id {
			filtered = append(filtered, item)
		}
	}
	t.open = filtered
	delete(t.openSummary, id)
}

func (t *progressTracker) percent() int {
	total := len(t.plan)
	if total == 0 {
		return 0
	}
	completed := 0
	for _, step := range t.plan {
		if t.completed[step.ID] {
			completed++
		}
	}
	return (completed * 100) / total
}

func (t *progressTracker) apply(event progressEvent) *Progress {
	id := strings.TrimSpace(event.ID)
	summary := strings.TrimSpace(event.Summary)
	switch event.Event {
	case "begin":
		t.removeOpen(id)
		t.open = append(t.open, id)
		if summary != "" {
			t.openSummary[id] = summary
		}
	case "end", "skip":
		if _, ok := t.index[id]; ok {
			t.completed[id] = true
		}
		t.removeOpen(id)
	case "fail":
		t.removeOpen(id)
	}

	currentID, currentSummary := t.current()
	if currentSummary == "" {
		switch event.Event {
		case "begin", "fail":
			currentID = id
			currentSummary = summary
		case "end", "skip":
			if summary != "" {
				currentID = id
				currentSummary = summary
			}
		}
	}

	return &Progress{
		StepID:  currentID,
		Summary: currentSummary,
		Percent: t.percent(),
	}
}

type Manager struct {
	mu                       sync.Mutex
	records                  map[string]Record
	progress                 map[string]*progressTracker
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
		progress:  map[string]*progressTracker{},
		rootDir:   filepath.Join(root, "sandboxes"),
		emit:      emit,
		logger:    componentLogger(logger),
		running:   map[string]bool{},
		appStatus: sandboxAppStatusFor,
		appUpgr:   sandboxAppUpgrade,
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

func newInitialProgress(provider, template string) (*Progress, *progressTracker) {
	tracker := newProgressTracker(provider, template)
	if tracker == nil || len(tracker.plan) == 0 {
		return nil, nil
	}
	first := tracker.plan[0]
	return tracker.apply(progressEvent{
		Event:   "begin",
		ID:      first.ID,
		Summary: first.Summary,
	}), tracker
}

func parseProgressMarker(line string) (progressEvent, bool) {
	line = strings.TrimSpace(line)
	payload, ok := extractProgressPayload(line)
	if !ok {
		return progressEvent{}, false
	}
	var event progressEvent
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		unescaped, unescapeErr := strconv.Unquote(`"` + payload + `"`)
		if unescapeErr != nil {
			return progressEvent{}, false
		}
		if err := json.Unmarshal([]byte(unescaped), &event); err != nil {
			return progressEvent{}, false
		}
	}
	event.Event = strings.ToLower(strings.TrimSpace(event.Event))
	event.ID = strings.TrimSpace(event.ID)
	event.Summary = strings.TrimSpace(event.Summary)
	event.Detail = strings.TrimSpace(event.Detail)
	switch event.Event {
	case "begin", "end", "skip", "fail":
	default:
		return progressEvent{}, false
	}
	if event.ID == "" {
		return progressEvent{}, false
	}
	return event, true
}

func extractProgressPayload(line string) (string, bool) {
	idx := strings.Index(line, progressMarkerPrefix)
	if idx < 0 {
		return "", false
	}
	if idx > 0 && line[idx-1] == '\'' {
		return "", false
	}
	payload := strings.TrimSpace(line[idx+len(progressMarkerPrefix):])
	if payload == "" {
		return "", false
	}
	start := strings.Index(payload, "{")
	if start < 0 {
		return "", false
	}
	payload = payload[start:]
	end := progressPayloadEnd(payload)
	if end < 0 {
		return "", false
	}
	return payload[:end+1], true
}

func progressPayloadEnd(payload string) int {
	depth := 0
	for i, r := range payload {
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
			if depth < 0 {
				return -1
			}
		}
	}
	return -1
}

func (m *Manager) List(ctx context.Context) (*ListResult, error) {
	if err := m.refreshRuntime(ctx); err != nil {
		m.logger.Debug("sandbox runtime refresh failed", "error", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	items := make([]Record, 0, len(m.records))
	for _, rec := range m.records {
		items = append(items, withCurrentShellCommand(rec))
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
	copy := withCurrentShellCommand(rec)
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
	if err := m.ensureAgentHome(ctx, slug, sharedDir); err != nil {
		return nil, err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	initialProgress, tracker := newInitialProgress(provider, template)
	rec := Record{
		Name:      displayName,
		Slug:      slug,
		Provider:  provider,
		Template:  template,
		Model:     model,
		Status:    "creating",
		SharedDir: sharedDir,
		Shell:     defaultShellCommand(slug, template),
		Progress:  initialProgress,
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
	if tracker != nil {
		m.progress[slug] = tracker
	}
	if err := m.saveLocked(); err != nil {
		delete(m.progress, slug)
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
	if err := m.ensureAgentHome(ctx, slug, sharedDir); err != nil {
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
	if err := m.resetProgress(rec.Slug); err != nil {
		return nil, err
	}
	if err := m.updateProgress(rec.Slug, progressEvent{
		Event:   "end",
		ID:      "sandbox.prepare",
		Summary: "Sandbox prepared.",
	}); err != nil {
		return nil, err
	}
	if err := m.updateProgress(rec.Slug, progressEvent{
		Event:   "begin",
		ID:      "vm.start",
		Summary: "Booting device...",
	}); err != nil {
		return nil, err
	}
	go func() {
		err := m.runCmd(context.Background(), limactl, []string{"start", "--tty=false", rec.Slug}, func(stream, line string) {
			m.appendLog(rec.Slug, stream, line)
		})
		if err != nil {
			_ = m.failCurrentProgress(rec.Slug, err.Error())
			_ = m.updateStatus(rec.Slug, "error", err.Error())
			return
		}
		_ = m.updateProgress(rec.Slug, progressEvent{
			Event:   "end",
			ID:      "vm.start",
			Summary: "Device booted.",
		})
		if err := m.finishReady(context.Background(), rec.Slug, limactl); err != nil {
			_ = m.failCurrentProgress(rec.Slug, err.Error())
			_ = m.updateStatus(rec.Slug, "error", err.Error())
		}
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

func sandboxStatusFromVMStatus(vmStatus string) string {
	if strings.EqualFold(strings.TrimSpace(vmStatus), "running") {
		return "ready"
	}
	return "stopped"
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
