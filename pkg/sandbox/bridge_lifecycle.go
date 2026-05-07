package sandbox

import (
	"context"
	"fmt"
	"strings"
)

func (m *Manager) ensureSandboxBridge(ctx context.Context, rec Record) error {
	if m.bridgeConnect == nil {
		return nil
	}
	if !isOpenClawTemplate(rec.Template) && !isHermesTemplate(rec.Template) {
		return nil
	}
	if strings.TrimSpace(guestSky10RPCAddress(rec)) == "" {
		return fmt.Errorf("resolving guest bridge endpoint for sandbox %q: endpoint unavailable", rec.Name)
	}
	if err := m.bridgeConnect(ctx, rec); err != nil {
		return fmt.Errorf("connecting sandbox bridge for %q: %w", rec.Name, err)
	}
	m.appendLog(rec.Slug, "stdout", "host connected metered-services bridge")
	return nil
}

func (m *Manager) closeSandboxBridge(slug string) {
	if m.bridgeClose == nil {
		return
	}
	m.bridgeClose(slug)
}
