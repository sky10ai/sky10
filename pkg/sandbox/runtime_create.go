package sandbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	skyapps "github.com/sky10/sky10/pkg/apps"
)

func (m *Manager) runCreate(ctx context.Context, rec Record) {
	defer func() {
		m.mu.Lock()
		delete(m.running, rec.Slug)
		m.mu.Unlock()
	}()

	limactl, err := m.ensureManagedApp(ctx, skyapps.AppLima, true)
	if err != nil {
		_ = m.failCurrentProgress(rec.Slug, err.Error())
		_ = m.updateStatus(rec.Slug, "error", err.Error())
		return
	}

	templatePath, err := m.materializeTemplate(ctx, rec)
	if err != nil {
		_ = m.failCurrentProgress(rec.Slug, err.Error())
		_ = m.updateStatus(rec.Slug, "error", err.Error())
		return
	}
	_ = m.updateProgress(rec.Slug, progressEvent{
		Event:   "end",
		ID:      "sandbox.prepare",
		Summary: "Sandbox prepared.",
	})
	_ = m.updateProgress(rec.Slug, progressEvent{
		Event:   "begin",
		ID:      "vm.start",
		Summary: "Booting device...",
	})

	args, err := m.buildStartArgs(ctx, rec, templatePath)
	if err != nil {
		_ = m.failCurrentProgress(rec.Slug, err.Error())
		_ = m.updateStatus(rec.Slug, "error", err.Error())
		return
	}
	if err := m.runCmd(ctx, limactl, args, func(stream, line string) {
		m.appendLog(rec.Slug, stream, line)
	}); err != nil {
		_ = m.failCurrentProgress(rec.Slug, err.Error())
		_ = m.updateStatus(rec.Slug, "error", err.Error())
		return
	}
	_ = m.updateProgress(rec.Slug, progressEvent{
		Event:   "end",
		ID:      "vm.start",
		Summary: "Device booted.",
	})

	if err := m.finishReady(ctx, rec.Slug, limactl); err != nil {
		_ = m.failCurrentProgress(rec.Slug, err.Error())
		_ = m.updateStatus(rec.Slug, "error", err.Error())
		return
	}
}

func (m *Manager) buildStartArgs(_ context.Context, rec Record, templatePath string) ([]string, error) {
	spec, err := lookupSandboxTemplateSpec(rec.Template)
	if err != nil {
		return nil, err
	}

	args := []string{
		"start",
		"--tty=false",
		"--progress",
		"--name", rec.Slug,
	}
	if spec.supportsModelOverride && strings.TrimSpace(rec.Model) != "" {
		args = append(args, "--set", fmt.Sprintf(".param.model = %q", strings.TrimSpace(rec.Model)))
	}
	args = append(args, templatePath)
	return args, nil
}

func (m *Manager) runReadyProgressStep(name, id, beginSummary, endSummary string, wait func() error) error {
	if err := m.updateProgress(name, progressEvent{
		Event:   "begin",
		ID:      id,
		Summary: beginSummary,
	}); err != nil {
		return err
	}
	if err := wait(); err != nil {
		return err
	}
	return m.updateProgress(name, progressEvent{
		Event:   "end",
		ID:      id,
		Summary: endSummary,
	})
}

func (m *Manager) finishGuestReadyFlow(
	ctx context.Context,
	name, limactl string,
	rec Record,
	waitForGuestAgent func(context.Context, func(context.Context, string, []string) ([]byte, error), string, string, time.Duration) error,
) error {
	if err := m.runReadyProgressStep(name, "ready.guest.sky10", "Waiting for guest sky10...", "Guest sky10 is ready.", func() error {
		return waitForGuestSky10(ctx, m.outputCmd, limactl, name, openClawReadyTimeout)
	}); err != nil {
		return err
	}

	var hostIdentity string
	if err := m.runReadyProgressStep(name, "ready.guest.identity", "Confirming guest identity...", "Guest identity confirmed.", func() error {
		var err error
		hostIdentity, err = m.ensureGuestJoinedHostIdentity(ctx, rec, limactl)
		return err
	}); err != nil {
		return err
	}

	if err := m.runReadyProgressStep(name, "ready.guest.agent", "Waiting for agent registration...", "Agent registered in guest sky10.", func() error {
		return waitForGuestAgent(ctx, m.outputCmd, limactl, name, openClawReadyTimeout)
	}); err != nil {
		return err
	}

	updatedRec, err := m.requireRecord(name)
	if err != nil {
		return err
	}
	return m.runReadyProgressStep(name, "ready.host.connect", "Connecting host to guest...", "Host connected to guest.", func() error {
		return m.ensureHostConnectedGuestAgent(ctx, *updatedRec, hostIdentity)
	})
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
	if sandboxNeedsForwardedGuestEndpoint(rec) && rec.ForwardedPort <= 0 {
		return "", fmt.Errorf("sandbox %q is missing a forwarded guest port", rec.Slug)
	}
	cacheDir := filepath.Join(m.rootDir, "templates", rec.Slug)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("creating sandbox template dir: %w", err)
	}

	if err := m.prepareTemplateSharedDir(ctx, rec); err != nil {
		return "", err
	}

	renderedPath := filepath.Join(cacheDir, rec.Slug+"-"+spec.mainAsset)
	stateDir := m.sandboxStateDir(rec.Slug)
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
			data = renderSandboxTemplate(body, rec.Slug, rec.SharedDir, stateDir, rec.ForwardedPort)
		}
		if err := os.WriteFile(targetPath, data, mode); err != nil {
			return "", fmt.Errorf("writing sandbox template asset %q: %w", assetName, err)
		}
	}
	return renderedPath, nil
}
