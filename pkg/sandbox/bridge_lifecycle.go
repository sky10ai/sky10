package sandbox

import (
	"context"
	"fmt"
	"strings"
)

func (m *Manager) ensureSandboxBridge(ctx context.Context, rec Record) error {
	if len(m.bridgeConnectors) == 0 {
		return nil
	}
	if !isOpenClawTemplate(rec.Template) && !isHermesTemplate(rec.Template) {
		return nil
	}
	if strings.TrimSpace(guestSky10RPCAddress(rec)) == "" {
		return fmt.Errorf("resolving guest bridge endpoint for sandbox %q: endpoint unavailable", rec.Name)
	}
	for _, connector := range m.bridgeConnectors {
		if connector.connect == nil {
			continue
		}
		if err := connector.connect(ctx, rec); err != nil {
			return fmt.Errorf("connecting sandbox bridge for %q: %w", rec.Name, err)
		}
	}
	m.appendLog(rec.Slug, "stdout", "host connected sandbox bridges")
	return nil
}

func (m *Manager) closeSandboxBridge(slug string) {
	if len(m.bridgeConnectors) == 0 {
		return
	}
	for _, connector := range m.bridgeConnectors {
		if connector.close != nil {
			connector.close(slug)
		}
	}
}
