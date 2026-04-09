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
	"sort"
	"strings"
	"sync"
	"time"

	skyapps "github.com/sky10/sky10/pkg/apps"
	"github.com/sky10/sky10/pkg/config"
)

const (
	providerLima        = "lima"
	templateUbuntu      = "ubuntu"
	templateUbuntuAsset = "ubuntu-sky10.yaml"
	templateRemoteBase  = "https://raw.githubusercontent.com/sky10ai/sky10/main/templates/lima/"
	logFileName         = "boot.log"
)

type Emitter func(event string, data interface{})

type CreateParams struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Template string `json:"template"`
}

type NamedParams struct {
	Name string `json:"name"`
}

type LogsParams struct {
	Name  string `json:"name"`
	Limit int    `json:"limit,omitempty"`
}

type Record struct {
	Name      string `json:"name"`
	Provider  string `json:"provider"`
	Template  string `json:"template"`
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
	Entries []LogEntry `json:"entries"`
}

type stateFile struct {
	Sandboxes []Record `json:"sandboxes"`
}

type Manager struct {
	mu        sync.Mutex
	records   map[string]Record
	rootDir   string
	emit      Emitter
	logger    *slog.Logger
	running   map[string]bool
	appStatus func(id skyapps.ID) (*skyapps.Status, error)
	appUpgr   func(id skyapps.ID, onProgress skyapps.ProgressFunc) (*skyapps.ReleaseInfo, error)
	runCmd    func(ctx context.Context, bin string, args []string, onLine func(stream, line string)) error
	outputCmd func(ctx context.Context, bin string, args []string) ([]byte, error)
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
	name = normalizeName(name)
	if name == "" {
		return nil, fmt.Errorf("sandbox name is required")
	}
	if err := m.refreshRuntime(ctx); err != nil {
		m.logger.Debug("sandbox runtime refresh failed", "error", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.records[name]
	if !ok {
		return nil, fmt.Errorf("sandbox %q not found", name)
	}
	copy := rec
	return &copy, nil
}

func (m *Manager) Logs(name string, limit int) (*LogsResult, error) {
	name = normalizeName(name)
	if name == "" {
		return nil, fmt.Errorf("sandbox name is required")
	}
	if limit <= 0 {
		limit = 200
	}
	path := m.logPath(name)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &LogsResult{Name: name}, nil
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
	return &LogsResult{Name: name, Entries: entries}, nil
}

func (m *Manager) Create(ctx context.Context, params CreateParams) (*Record, error) {
	name := normalizeName(params.Name)
	provider := strings.ToLower(strings.TrimSpace(params.Provider))
	template := strings.ToLower(strings.TrimSpace(params.Template))
	if name == "" {
		return nil, fmt.Errorf("sandbox name is required")
	}
	if provider != providerLima {
		return nil, fmt.Errorf("unsupported sandbox provider %q (supported: %s)", provider, providerLima)
	}
	if template != templateUbuntu {
		return nil, fmt.Errorf("unsupported sandbox template %q (supported: %s)", template, templateUbuntu)
	}

	sharedDir, err := defaultSharedDir(name)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	rec := Record{
		Name:      name,
		Provider:  provider,
		Template:  template,
		Status:    "creating",
		SharedDir: sharedDir,
		Shell:     fmt.Sprintf("limactl shell %s", name),
		CreatedAt: now,
		UpdatedAt: now,
	}

	m.mu.Lock()
	if _, exists := m.records[name]; exists {
		m.mu.Unlock()
		return nil, fmt.Errorf("sandbox %q already exists", name)
	}
	if m.running[name] {
		m.mu.Unlock()
		return nil, fmt.Errorf("sandbox %q is already being created", name)
	}
	m.records[name] = rec
	m.running[name] = true
	if err := m.saveLocked(); err != nil {
		delete(m.running, name)
		delete(m.records, name)
		m.mu.Unlock()
		return nil, err
	}
	m.mu.Unlock()

	m.emitState(rec)
	go m.runCreate(context.Background(), rec)

	return &rec, nil
}

func (m *Manager) Start(ctx context.Context, name string) (*Record, error) {
	rec, err := m.requireRecord(name)
	if err != nil {
		return nil, err
	}
	limactl, err := m.ensureManagedApp(ctx, skyapps.AppLima, true)
	if err != nil {
		return nil, err
	}
	if err := m.updateStatus(rec.Name, "starting", ""); err != nil {
		return nil, err
	}
	go func() {
		err := m.runCmd(context.Background(), limactl, []string{"start", "--tty=false", rec.Name}, func(stream, line string) {
			m.appendLog(rec.Name, stream, line)
		})
		if err != nil {
			_ = m.updateStatus(rec.Name, "error", err.Error())
			return
		}
		_ = m.finishReady(context.Background(), rec.Name, limactl)
	}()
	return m.Get(context.Background(), rec.Name)
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
	if err := m.runCmd(ctx, limactl, []string{"stop", rec.Name}, func(stream, line string) {
		m.appendLog(rec.Name, stream, line)
	}); err != nil {
		return nil, fmt.Errorf("stopping sandbox %q: %w", rec.Name, err)
	}
	if err := m.updateVMStatus(rec.Name, "Stopped"); err != nil {
		return nil, err
	}
	if err := m.updateStatus(rec.Name, "stopped", ""); err != nil {
		return nil, err
	}
	return m.Get(ctx, rec.Name)
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
	_ = m.runCmd(ctx, limactl, []string{"stop", rec.Name}, func(stream, line string) {
		m.appendLog(rec.Name, stream, line)
	})
	if err := m.runCmd(ctx, limactl, []string{"delete", "--force", rec.Name}, func(stream, line string) {
		m.appendLog(rec.Name, stream, line)
	}); err != nil {
		return nil, fmt.Errorf("deleting sandbox %q: %w", rec.Name, err)
	}

	m.mu.Lock()
	delete(m.records, rec.Name)
	delete(m.running, rec.Name)
	err = m.saveLocked()
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if m.emit != nil {
		m.emit("sandbox:state", map[string]any{
			"name":   rec.Name,
			"status": "deleted",
		})
	}
	return rec, nil
}

func (m *Manager) runCreate(ctx context.Context, rec Record) {
	defer func() {
		m.mu.Lock()
		delete(m.running, rec.Name)
		m.mu.Unlock()
	}()

	limactl, err := m.ensureManagedApp(ctx, skyapps.AppLima, true)
	if err != nil {
		_ = m.updateStatus(rec.Name, "error", err.Error())
		return
	}
	if err := os.MkdirAll(rec.SharedDir, 0o755); err != nil {
		_ = m.updateStatus(rec.Name, "error", fmt.Sprintf("creating shared directory: %v", err))
		return
	}

	templatePath, err := m.materializeTemplate(ctx)
	if err != nil {
		_ = m.updateStatus(rec.Name, "error", err.Error())
		return
	}

	args := []string{
		"start",
		"--tty=false",
		"--progress",
		"--name", rec.Name,
		templatePath,
	}
	if err := m.runCmd(ctx, limactl, args, func(stream, line string) {
		m.appendLog(rec.Name, stream, line)
	}); err != nil {
		_ = m.updateStatus(rec.Name, "error", err.Error())
		return
	}

	if err := m.finishReady(ctx, rec.Name, limactl); err != nil {
		_ = m.updateStatus(rec.Name, "error", err.Error())
		return
	}
}

func (m *Manager) finishReady(ctx context.Context, name, limactl string) error {
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
	name = normalizeName(name)
	if name == "" {
		return nil, fmt.Errorf("sandbox name is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.records[name]
	if !ok {
		return nil, fmt.Errorf("sandbox %q not found", name)
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

func (m *Manager) materializeTemplate(ctx context.Context) (string, error) {
	cacheDir := filepath.Join(m.rootDir, "templates")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("creating sandbox template dir: %w", err)
	}
	dest := filepath.Join(cacheDir, templateUbuntuAsset)
	if local, err := findLocalTemplateFile(templateUbuntuAsset); err == nil {
		data, err := os.ReadFile(local)
		if err != nil {
			return "", fmt.Errorf("reading local sandbox template: %w", err)
		}
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			return "", fmt.Errorf("writing sandbox template cache: %w", err)
		}
		return dest, nil
	}

	req, err := httpRequest(ctx, templateRemoteBase+templateUbuntuAsset)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(dest, req, 0o644); err != nil {
		return "", fmt.Errorf("writing downloaded sandbox template: %w", err)
	}
	return dest, nil
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
			if status == "Running" && rec.Status != "ready" {
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

func (m *Manager) appendLog(name, stream, line string) {
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
	logPath := m.logPath(name)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err == nil {
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = f.Write(append(data, '\n'))
			_ = f.Close()
		}
	}
	m.mu.Lock()
	rec, ok := m.records[name]
	if ok {
		rec.LastLogAt = entry.Time
		rec.UpdatedAt = entry.Time
		m.records[name] = rec
		_ = m.saveLocked()
	}
	m.mu.Unlock()
	if m.emit != nil {
		m.emit("sandbox:log", map[string]any{
			"name":   name,
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
	for _, rec := range state.Sandboxes {
		m.records[rec.Name] = rec
	}
	return nil
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

func defaultSharedDir(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("finding home directory: %w", err)
	}
	return filepath.Join(home, "sky10", "sandboxes", name), nil
}

func normalizeName(name string) string {
	return strings.TrimSpace(name)
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

func lookupLimaInstanceIPv4(ctx context.Context, outputCmd func(context.Context, string, []string) ([]byte, error), limactl, name string) (string, error) {
	out, err := outputCmd(ctx, limactl, []string{"shell", name, "--", "bash", "-lc",
		`ip -4 route get 1.1.1.1 | awk '{for (i = 1; i <= NF; i++) if ($i == "src") {print $(i + 1); exit}}'`})
	if err != nil {
		return "", fmt.Errorf("querying guest IP: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
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
