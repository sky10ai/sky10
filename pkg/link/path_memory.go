package link

import (
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

const (
	pathSuccessTTL = 30 * time.Minute
	pathFailureTTL = 10 * time.Minute
)

// PathFailure is one recent transport or address failure hint.
type PathFailure struct {
	Count  int       `json:"count"`
	LastAt time.Time `json:"last_at"`
}

// PathHint is the resolver's short-lived memory for one peer's recently
// working and recently failing dial paths.
type PathHint struct {
	LastSuccessAt        time.Time              `json:"last_success_at,omitempty"`
	LastSuccessTransport string                 `json:"last_success_transport,omitempty"`
	LastSuccessSource    string                 `json:"last_success_source,omitempty"`
	LastSuccessAddr      string                 `json:"last_success_addr,omitempty"`
	AddrFailures         map[string]PathFailure `json:"-"`
	TransportFailures    map[string]PathFailure `json:"-"`
}

type pathState struct {
	LastSuccessAt        time.Time
	LastSuccessTransport string
	LastSuccessSource    string
	LastSuccessAddr      string
	AddrFailures         map[string]PathFailure
	TransportFailures    map[string]PathFailure
}

// PathMemory stores short-lived dial success and failure history per peer.
type PathMemory struct {
	mu    sync.Mutex
	now   func() time.Time
	peers map[string]*pathState
}

// NewPathMemory returns an empty in-memory path cache.
func NewPathMemory() *PathMemory {
	return &PathMemory{
		now:   time.Now,
		peers: make(map[string]*pathState),
	}
}

// Snapshot returns the current path hint for one peer, pruning expired state.
func (m *PathMemory) Snapshot(id peer.ID) PathHint {
	if m == nil || id == "" {
		return PathHint{}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	state, ok := m.peers[id.String()]
	if !ok || state == nil {
		return PathHint{}
	}
	m.pruneStateLocked(state)
	if state.empty() {
		delete(m.peers, id.String())
		return PathHint{}
	}
	return state.snapshot()
}

// RecordSuccess remembers the currently highest-ranked path as the last known
// good path for the peer and clears matching recent failures.
func (m *PathMemory) RecordSuccess(id peer.ID, source string, info *peer.AddrInfo) {
	if m == nil || id == "" || info == nil || len(info.Addrs) == 0 {
		return
	}

	addr := info.Addrs[0]
	transport := transportClass(addr)
	now := m.now().UTC()

	m.mu.Lock()
	defer m.mu.Unlock()

	state := m.stateLocked(id)
	state.LastSuccessAt = now
	state.LastSuccessTransport = transport
	state.LastSuccessSource = source
	state.LastSuccessAddr = addr.String()
	delete(state.AddrFailures, state.LastSuccessAddr)
	delete(state.TransportFailures, state.LastSuccessTransport)
}

// RecordFailure remembers that the peer's currently advertised addresses did
// not work, so stale paths lose priority for a short period.
func (m *PathMemory) RecordFailure(id peer.ID, info *peer.AddrInfo) {
	if m == nil || id == "" || info == nil || len(info.Addrs) == 0 {
		return
	}

	now := m.now().UTC()

	m.mu.Lock()
	defer m.mu.Unlock()

	state := m.stateLocked(id)
	seenTransports := make(map[string]struct{})
	for _, addr := range info.Addrs {
		raw := addr.String()
		failure := state.AddrFailures[raw]
		failure.Count++
		failure.LastAt = now
		state.AddrFailures[raw] = failure

		transport := transportClass(addr)
		if _, ok := seenTransports[transport]; ok {
			continue
		}
		seenTransports[transport] = struct{}{}

		tf := state.TransportFailures[transport]
		tf.Count++
		tf.LastAt = now
		state.TransportFailures[transport] = tf
	}
}

func (m *PathMemory) stateLocked(id peer.ID) *pathState {
	key := id.String()
	state := m.peers[key]
	if state != nil {
		m.pruneStateLocked(state)
		return state
	}
	state = &pathState{
		AddrFailures:      make(map[string]PathFailure),
		TransportFailures: make(map[string]PathFailure),
	}
	m.peers[key] = state
	return state
}

func (m *PathMemory) pruneStateLocked(state *pathState) {
	if state == nil {
		return
	}
	now := m.now().UTC()
	if !state.LastSuccessAt.IsZero() && now.Sub(state.LastSuccessAt) > pathSuccessTTL {
		state.LastSuccessAt = time.Time{}
		state.LastSuccessTransport = ""
		state.LastSuccessSource = ""
		state.LastSuccessAddr = ""
	}
	for addr, failure := range state.AddrFailures {
		if now.Sub(failure.LastAt) > pathFailureTTL {
			delete(state.AddrFailures, addr)
		}
	}
	for transport, failure := range state.TransportFailures {
		if now.Sub(failure.LastAt) > pathFailureTTL {
			delete(state.TransportFailures, transport)
		}
	}
}

func (s *pathState) empty() bool {
	if s == nil {
		return true
	}
	return s.LastSuccessAt.IsZero() &&
		len(s.AddrFailures) == 0 &&
		len(s.TransportFailures) == 0
}

func (s *pathState) snapshot() PathHint {
	if s == nil {
		return PathHint{}
	}
	hint := PathHint{
		LastSuccessAt:        s.LastSuccessAt,
		LastSuccessTransport: s.LastSuccessTransport,
		LastSuccessSource:    s.LastSuccessSource,
		LastSuccessAddr:      s.LastSuccessAddr,
	}
	if len(s.AddrFailures) > 0 {
		hint.AddrFailures = make(map[string]PathFailure, len(s.AddrFailures))
		for key, failure := range s.AddrFailures {
			hint.AddrFailures[key] = failure
		}
	}
	if len(s.TransportFailures) > 0 {
		hint.TransportFailures = make(map[string]PathFailure, len(s.TransportFailures))
		for key, failure := range s.TransportFailures {
			hint.TransportFailures[key] = failure
		}
	}
	return hint
}

func transportClassForAddrs(addrs []ma.Multiaddr) string {
	if len(addrs) == 0 {
		return ""
	}
	return transportClass(addrs[0])
}
