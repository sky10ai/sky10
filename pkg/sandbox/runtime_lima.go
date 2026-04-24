package sandbox

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	skyapps "github.com/sky10/sky10/pkg/apps"
)

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
	spec, checkErr := lookupSandboxTemplateSpec(template)
	if checkErr != nil {
		err = checkErr
		return
	}
	if spec.macOSOnly && runtime.GOOS != "darwin" {
		err = fmt.Errorf("sandbox template %q is macOS-only for now (the current Lima template uses vz)", template)
		return
	}
	sharedDir, err = defaultSharedDir(slug)
	return
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

func renderSandboxTemplate(body []byte, name, sharedDir, stateDir string, forwardedPort int) []byte {
	rendered := strings.ReplaceAll(string(body), templateNameToken, name)
	rendered = strings.ReplaceAll(rendered, templateSharedToken, sharedDir)
	rendered = strings.ReplaceAll(rendered, templateStateToken, stateDir)
	if forwardedPort > 0 {
		rendered = strings.ReplaceAll(rendered, templateForwardedGuestPortToken, strconv.Itoa(forwardedPort))
		rendered = strings.ReplaceAll(rendered, templateOpenClawGatewayPortToken, strconv.Itoa(forwardedPort+1))
	}
	return []byte(rendered)
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if isShellSafeToken(value) {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func withCurrentShellCommand(rec Record) Record {
	if rec.Provider == providerLima {
		rec.Shell = defaultShellCommand(rec.Slug, rec.Template)
	}
	return rec
}

func isShellSafeToken(value string) bool {
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '/', r == '.', r == '_', r == '-', r == ':':
		default:
			return false
		}
	}
	return value != ""
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

	guest, err := m.readGuestIdentity(ctx, copy)
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
