package link

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// NostrRelayHealth is the operator-facing health snapshot for one configured
// Nostr relay.
type NostrRelayHealth struct {
	URL              string     `json:"url"`
	Successes        int        `json:"successes"`
	Failures         int        `json:"failures"`
	LastSuccessAt    *time.Time `json:"last_success_at,omitempty"`
	LastFailureAt    *time.Time `json:"last_failure_at,omitempty"`
	LastError        string     `json:"last_error,omitempty"`
	LastLatencyMS    int64      `json:"last_latency_ms,omitempty"`
	AverageLatencyMS int64      `json:"average_latency_ms,omitempty"`
}

type nostrRelayState struct {
	successes      int
	failures       int
	lastSuccessAt  time.Time
	lastFailureAt  time.Time
	lastError      string
	lastLatency    time.Duration
	totalLatency   time.Duration
	latencySamples int
}

// NostrRelayTracker keeps lightweight health and ranking state for the relay
// set used across discovery and public-network mailbox coordination.
type NostrRelayTracker struct {
	mu     sync.RWMutex
	order  []string
	states map[string]*nostrRelayState
}

// NewNostrRelayTracker creates a tracker for the configured relay set.
func NewNostrRelayTracker(relays []string) *NostrRelayTracker {
	order := normalizeRelayList(relays)
	states := make(map[string]*nostrRelayState, len(order))
	for _, relay := range order {
		states[relay] = &nostrRelayState{}
	}
	return &NostrRelayTracker{
		order:  order,
		states: states,
	}
}

// Record marks one relay operation as successful or failed.
func (t *NostrRelayTracker) Record(relay string, latency time.Duration, err error) {
	if t == nil {
		return
	}
	relay = strings.TrimSpace(relay)
	if relay == "" {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	state := t.ensureLocked(relay)
	if latency < 0 {
		latency = 0
	}
	state.lastLatency = latency
	now := time.Now().UTC()
	if err != nil {
		state.failures++
		state.lastFailureAt = now
		state.lastError = err.Error()
		return
	}
	state.successes++
	state.lastSuccessAt = now
	state.lastError = ""
	state.totalLatency += latency
	state.latencySamples++
}

// Ordered returns relays sorted by recent health, with stable fallback to the
// configured relay order.
func (t *NostrRelayTracker) Ordered(relays []string) []string {
	ordered := normalizeRelayList(relays)
	if len(ordered) == 0 {
		if t == nil {
			return nil
		}
		t.mu.RLock()
		ordered = append([]string(nil), t.order...)
		t.mu.RUnlock()
	}
	if t == nil || len(ordered) == 0 {
		return ordered
	}

	type candidate struct {
		relay string
		index int
		state nostrRelayState
	}

	t.mu.RLock()
	candidates := make([]candidate, 0, len(ordered))
	for idx, relay := range ordered {
		state := nostrRelayState{}
		if existing, ok := t.states[relay]; ok && existing != nil {
			state = *existing
		}
		candidates = append(candidates, candidate{relay: relay, index: idx, state: state})
	}
	t.mu.RUnlock()

	sort.SliceStable(candidates, func(i, j int) bool {
		a := candidates[i]
		b := candidates[j]
		if ar, br := relayRank(a.state), relayRank(b.state); ar != br {
			return ar > br
		}
		if as, bs := relaySuccessRate(a.state), relaySuccessRate(b.state); as != bs {
			return as > bs
		}
		if aa, ba := relayAverageLatency(a.state), relayAverageLatency(b.state); aa != ba {
			if aa == 0 {
				return false
			}
			if ba == 0 {
				return true
			}
			return aa < ba
		}
		if !a.state.lastSuccessAt.Equal(b.state.lastSuccessAt) {
			return a.state.lastSuccessAt.After(b.state.lastSuccessAt)
		}
		return a.index < b.index
	})

	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, candidate.relay)
	}
	return out
}

// Snapshot returns relay health ordered by current preference.
func (t *NostrRelayTracker) Snapshot() []NostrRelayHealth {
	if t == nil {
		return nil
	}

	ordered := t.Ordered(nil)
	t.mu.RLock()
	defer t.mu.RUnlock()

	out := make([]NostrRelayHealth, 0, len(ordered))
	for _, relay := range ordered {
		state := t.states[relay]
		if state == nil {
			state = &nostrRelayState{}
		}
		out = append(out, NostrRelayHealth{
			URL:              relay,
			Successes:        state.successes,
			Failures:         state.failures,
			LastSuccessAt:    timePtr(state.lastSuccessAt),
			LastFailureAt:    timePtr(state.lastFailureAt),
			LastError:        state.lastError,
			LastLatencyMS:    durationMS(state.lastLatency),
			AverageLatencyMS: durationMS(relayAverageLatency(*state)),
		})
	}
	return out
}

func (t *NostrRelayTracker) ensureLocked(relay string) *nostrRelayState {
	if state, ok := t.states[relay]; ok && state != nil {
		return state
	}
	if t.states == nil {
		t.states = map[string]*nostrRelayState{}
	}
	t.states[relay] = &nostrRelayState{}
	if !containsString(t.order, relay) {
		t.order = append(t.order, relay)
	}
	return t.states[relay]
}

func normalizeRelayList(relays []string) []string {
	seen := make(map[string]struct{}, len(relays))
	out := make([]string, 0, len(relays))
	for _, relay := range relays {
		relay = strings.TrimSpace(relay)
		if relay == "" {
			continue
		}
		if _, ok := seen[relay]; ok {
			continue
		}
		seen[relay] = struct{}{}
		out = append(out, relay)
	}
	return out
}

func relayRank(state nostrRelayState) int {
	switch {
	case state.successes == 0 && state.failures == 0:
		return 1
	case state.lastFailureAt.IsZero():
		return 2
	case state.lastSuccessAt.After(state.lastFailureAt):
		return 2
	default:
		return 0
	}
}

func relaySuccessRate(state nostrRelayState) float64 {
	total := state.successes + state.failures
	if total == 0 {
		return 0.5
	}
	return float64(state.successes) / float64(total)
}

func relayAverageLatency(state nostrRelayState) time.Duration {
	if state.latencySamples == 0 {
		return 0
	}
	return time.Duration(int64(state.totalLatency) / int64(state.latencySamples))
}

func durationMS(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	return d.Milliseconds()
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
