package sandbox

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

type forwardedEndpointSpec struct {
	name      string
	offset    int
	guestHost string
	guestPort int
	protocol  string
}

func sandboxNeedsForwardedGuestEndpoint(rec Record) bool {
	return rec.Provider == providerLima && len(forwardedEndpointSpecsForTemplate(rec.Template)) > 0
}

func forwardedEndpointSpecsForTemplate(template string) []forwardedEndpointSpec {
	switch {
	case isOpenClawTemplate(template):
		return []forwardedEndpointSpec{
			{
				name:      ForwardedEndpointSky10,
				offset:    0,
				guestHost: defaultForwardedGuestHost,
				guestPort: guestSky10Port,
				protocol:  "tcp",
			},
			{
				name:      ForwardedEndpointOpenClawGateway,
				offset:    1,
				guestHost: defaultForwardedGuestHost,
				guestPort: openClawGatewayGuestPort,
				protocol:  "tcp",
			},
		}
	case isHermesTemplate(template):
		return []forwardedEndpointSpec{
			{
				name:      ForwardedEndpointSky10,
				offset:    0,
				guestHost: defaultForwardedGuestHost,
				guestPort: guestSky10Port,
				protocol:  "tcp",
			},
		}
	default:
		return nil
	}
}

func ForwardedPortBlockSize(template string) int {
	size := 0
	for _, spec := range forwardedEndpointSpecsForTemplate(template) {
		if spec.offset+1 > size {
			size = spec.offset + 1
		}
	}
	return size
}

func ForwardedHostPortForTemplate(template, endpointName string, basePort int) int {
	if basePort <= 0 {
		return 0
	}
	for _, spec := range forwardedEndpointSpecsForTemplate(template) {
		if spec.name == endpointName {
			return basePort + spec.offset
		}
	}
	return 0
}

func ForwardedEndpointsForTemplate(template, host string, basePort int) []ForwardedEndpoint {
	if basePort <= 0 {
		return nil
	}
	host = strings.TrimSpace(host)
	if host == "" {
		host = defaultForwardedGuestHost
	}
	specs := forwardedEndpointSpecsForTemplate(template)
	if len(specs) == 0 {
		return nil
	}
	endpoints := make([]ForwardedEndpoint, 0, len(specs))
	for _, spec := range specs {
		endpoints = append(endpoints, ForwardedEndpoint{
			Name:      spec.name,
			Host:      host,
			HostPort:  basePort + spec.offset,
			GuestHost: spec.guestHost,
			GuestPort: spec.guestPort,
			Offset:    spec.offset,
			Protocol:  spec.protocol,
		})
	}
	return endpoints
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
		if syncForwardedEndpoints(rec) {
			changed = true
		}
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
	blockSize := ForwardedPortBlockSize(rec.Template)
	if blockSize <= 0 {
		return changed, nil
	}
	for port := start; port <= 65535-blockSize+1; port++ {
		if !forwardedPortBlockAvailable(rec.ForwardedHost, port, rec.Template, used, available) {
			continue
		}
		rec.ForwardedPort = port
		syncForwardedEndpoints(rec)
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
		for _, port := range forwardedHostPortsForRecord(rec) {
			used[port] = true
		}
	}
	return used
}

func forwardedPortBlockAvailable(host string, basePort int, template string, used map[int]bool, available func(string, int) bool) bool {
	for _, spec := range forwardedEndpointSpecsForTemplate(template) {
		port := basePort + spec.offset
		if port > 65535 || used[port] {
			return false
		}
		if !available(host, port) {
			return false
		}
	}
	return true
}

func syncForwardedEndpoints(rec *Record) bool {
	endpoints := ForwardedEndpointsForTemplate(rec.Template, rec.ForwardedHost, rec.ForwardedPort)
	if forwardedEndpointsEqual(rec.ForwardedEndpoints, endpoints) {
		return false
	}
	rec.ForwardedEndpoints = endpoints
	return true
}

func forwardedEndpointsEqual(a, b []ForwardedEndpoint) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func forwardedHostPortsForRecord(rec Record) []int {
	seen := map[int]bool{}
	ports := make([]int, 0, len(rec.ForwardedEndpoints)+1)
	add := func(port int) {
		if port <= 0 || seen[port] {
			return
		}
		seen[port] = true
		ports = append(ports, port)
	}
	for _, endpoint := range rec.ForwardedEndpoints {
		add(endpoint.HostPort)
	}
	if rec.ForwardedPort > 0 {
		add(rec.ForwardedPort)
		for _, endpoint := range ForwardedEndpointsForTemplate(rec.Template, rec.ForwardedHost, rec.ForwardedPort) {
			add(endpoint.HostPort)
		}
	}
	return ports
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
