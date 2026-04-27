package comms

import (
	"errors"
	"sync"
	"time"
)

// ErrReplay indicates an envelope was rejected because its (agent_id,
// type, nonce) tuple was seen within the configured window.
var ErrReplay = errors.New("comms: envelope replayed within window")

// ErrTimestampOutOfRange indicates the envelope's caller-supplied ts
// was outside the skew tolerance against the host clock.
var ErrTimestampOutOfRange = errors.New("comms: envelope timestamp out of range")

// ReplayStore tracks recently-seen (agent_id, type, nonce) tuples and
// rejects duplicates within each type's NonceWindow. It also enforces
// a global ts skew tolerance so callers cannot pre-mint envelopes far
// in the future or replay very old ones.
//
// The store is in-memory and bounded by a periodic sweep that removes
// entries older than the largest configured nonce window. Daemon
// restart clears the store; the worst case is one extra accepted
// replay per restart, which is acceptable.
type ReplayStore struct {
	mu     sync.Mutex
	seen   map[string]time.Time
	skew   time.Duration
	now    func() time.Time
	maxAge time.Duration
}

// NewReplayStore constructs a store with the given ts skew tolerance
// (envelopes more than `skew` from host time are rejected) and
// expected max nonce window (used for sweep timing).
//
// `now` may be nil to use time.Now; tests pass a fake clock.
func NewReplayStore(skew time.Duration, maxNonceWindow time.Duration, now func() time.Time) *ReplayStore {
	if now == nil {
		now = time.Now
	}
	return &ReplayStore{
		seen:   make(map[string]time.Time),
		skew:   skew,
		now:    now,
		maxAge: maxNonceWindow,
	}
}

// Check validates an envelope against the replay store and records its
// nonce as seen. Returns ErrTimestampOutOfRange if the caller-supplied
// ts is outside the skew window, ErrReplay if the (agent, type, nonce)
// tuple was already recorded within the type's window, or nil on
// acceptance.
func (r *ReplayStore) Check(agentID, envelopeType, nonce string, callerTs time.Time, window time.Duration) error {
	now := r.now()
	if r.skew > 0 {
		delta := now.Sub(callerTs)
		if delta > r.skew || delta < -r.skew {
			return ErrTimestampOutOfRange
		}
	}
	key := agentID + "\x00" + envelopeType + "\x00" + nonce
	r.mu.Lock()
	defer r.mu.Unlock()
	if seenAt, ok := r.seen[key]; ok {
		if now.Sub(seenAt) < window {
			return ErrReplay
		}
	}
	r.seen[key] = now
	r.sweepLocked(now)
	return nil
}

// sweepLocked is called with r.mu held. It removes entries older than
// maxAge so the map cannot grow unbounded under sustained traffic.
// The amortized cost is O(1) per insert; the per-call work is the
// number of entries actually evicted, which over time matches the
// insertion rate.
func (r *ReplayStore) sweepLocked(now time.Time) {
	if r.maxAge <= 0 {
		return
	}
	cutoff := now.Add(-r.maxAge)
	for k, t := range r.seen {
		if t.Before(cutoff) {
			delete(r.seen, k)
		}
	}
}
