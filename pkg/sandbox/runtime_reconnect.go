package sandbox

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	skyapps "github.com/sky10/sky10/pkg/apps"
)

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
		if !isOpenClawTemplate(rec.Template) && !isHermesTemplate(rec.Template) {
			continue
		}
		if rec.VMStatus != "Running" {
			continue
		}
		items = append(items, rec)
	}
	m.mu.Unlock()

	var wg sync.WaitGroup
	errCh := make(chan error, len(items))
	for _, rec := range items {
		rec := rec
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := m.reconnectRunningSandbox(ctx, limactl, hostIdentity, rec); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			return err
		}
	}

	return nil
}

func (m *Manager) reconnectRunningSandbox(ctx context.Context, limactl, hostIdentity string, rec Record) error {
	ipAddr, err := lookupLimaInstanceIPv4(ctx, m.outputCmd, limactl, rec.Slug)
	if err != nil {
		m.logger.Warn("sandbox reconnect skipped: guest IP lookup failed", "sandbox", rec.Slug, "error", err)
		return nil
	}
	if strings.TrimSpace(ipAddr) == "" {
		m.logger.Warn("sandbox reconnect skipped: guest IP unavailable", "sandbox", rec.Slug)
		return nil
	}
	ipAddr = strings.TrimSpace(ipAddr)
	if rec.IPAddress != ipAddr {
		rec.IPAddress = ipAddr
		if err := m.updateIPAddress(rec.Slug, rec.IPAddress); err != nil {
			return err
		}
	}

	reconnectCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	err = m.waitForGuestIdentityMatch(reconnectCtx, rec, hostIdentity)
	if err == nil {
		err = m.ensureHostConnectedGuestAgent(reconnectCtx, rec, hostIdentity)
	}
	cancel()
	if err != nil {
		m.logger.Warn("sandbox reconnect failed", "sandbox", rec.Slug, "error", err)
		return nil
	}
	if err := m.updateVMStatus(rec.Slug, "Running"); err != nil {
		return err
	}
	if err := m.updateStatus(rec.Slug, "ready", ""); err != nil {
		return err
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
		guest, err := m.readGuestIdentity(ctx, rec)
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
