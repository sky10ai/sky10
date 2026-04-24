package sandbox

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

func sandboxNeedsForwardedGuestEndpoint(rec Record) bool {
	return rec.Provider == providerLima && (isOpenClawTemplate(rec.Template) || isHermesTemplate(rec.Template))
}

func (m *Manager) ensureForwardedEndpoint(name string) (*Record, error) {
	m.mu.Lock()
	rec, ok := m.records[name]
	if !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("sandbox %q not found", name)
	}
	changed, err := m.assignForwardedEndpointLocked(&rec)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if !changed {
		copy := rec
		m.mu.Unlock()
		return &copy, nil
	}
	rec.UpdatedAt = nowRFC3339()
	m.records[name] = rec
	if err := m.saveLocked(); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	m.mu.Unlock()
	m.emitState(rec)
	return &rec, nil
}

func (m *Manager) assignForwardedEndpointLocked(rec *Record) (bool, error) {
	if rec == nil || !sandboxNeedsForwardedGuestEndpoint(*rec) {
		return false, nil
	}

	changed := false
	if strings.TrimSpace(rec.ForwardedHost) == "" {
		rec.ForwardedHost = defaultForwardedGuestHost
		changed = true
	}
	if rec.ForwardedPort > 0 {
		return changed, nil
	}

	start := m.forwardedPortStart
	if start <= 0 {
		start = defaultForwardedGuestPortStart
	}
	available := m.localPortAvailable
	if available == nil {
		available = localForwardedPortAvailable
	}
	used := m.assignedForwardedPortsLocked(rec.Slug)
	for port := start; port <= 65535; port++ {
		if used[port] {
			continue
		}
		if !available(rec.ForwardedHost, port) {
			continue
		}
		rec.ForwardedPort = port
		return true, nil
	}
	return false, fmt.Errorf("no available forwarded host port starting at %d", start)
}

func (m *Manager) assignedForwardedPortsLocked(exceptSlug string) map[int]bool {
	used := map[int]bool{}
	for slug, rec := range m.records {
		if slug == exceptSlug {
			continue
		}
		if rec.ForwardedPort > 0 {
			used[rec.ForwardedPort] = true
		}
	}
	return used
}

func localForwardedPortAvailable(host string, port int) bool {
	addr := net.JoinHostPort(strings.TrimSpace(host), strconv.Itoa(port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}
