package sandbox

import (
	"context"
	"fmt"
	"strings"
	"time"

	skyid "github.com/sky10/sky10/pkg/id"
)

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

	if strings.TrimSpace(guestSky10RPCAddress(rec)) == "" {
		return "", fmt.Errorf("resolving guest RPC endpoint for sandbox %q: endpoint unavailable", rec.Name)
	}

	guest, err := m.readGuestIdentity(ctx, rec)
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
	if err := m.guestRPC(ctx, guestSky10RPCAddress(rec), "identity.join", params, &joinResult); err != nil {
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
		if err := m.waitForHostAgentVisible(waitCtx, rec); err != nil {
			lastErr = err
		} else {
			m.appendLog(rec.Slug, "stdout", "host sky10 connected to guest peer")
			return nil
		}

		connectCtx, connectCancel := context.WithTimeout(waitCtx, 5*time.Second)
		connectErr := m.hostRPC(connectCtx, "skylink.connect", map[string]string{"address": hostIdentity}, nil)
		connectCancel()
		if connectErr != nil {
			lastErr = fmt.Errorf("connecting host sky10 to guest identity %q: %w", hostIdentity, connectErr)
		} else if err := m.waitForHostAgentVisible(waitCtx, rec); err != nil {
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

func (m *Manager) readGuestIdentity(ctx context.Context, rec Record) (guestIdentity, error) {
	var guest guestIdentity
	if err := m.guestRPC(ctx, guestSky10RPCAddress(rec), "identity.show", nil, &guest); err != nil {
		return guestIdentity{}, fmt.Errorf("reading guest identity for sandbox %q: %w", rec.Name, err)
	}
	return guest, nil
}
