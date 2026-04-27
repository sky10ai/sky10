package comms

import (
	"sync"
	"time"
)

// QuotaStore enforces per-(agent_id, envelope_type) token-bucket rate
// limits. Each bucket has capacity Burst, refills at PerAgent/Window
// tokens per second, and a successful Check call consumes one token.
//
// QuotaStore is in-memory; daemon restart resets all buckets. This is
// acceptable for the sandbox-comms threat model: an agent that wants
// to evade per-window limits would need to crash the daemon repeatedly,
// which is louder than abuse.
type QuotaStore struct {
	mu      sync.Mutex
	buckets map[string]*tokenBucket
	now     func() time.Time
}

// NewQuotaStore constructs an empty store. Pass nil for `now` to use
// time.Now; tests pass a fake clock.
func NewQuotaStore(now func() time.Time) *QuotaStore {
	if now == nil {
		now = time.Now
	}
	return &QuotaStore{
		buckets: make(map[string]*tokenBucket),
		now:     now,
	}
}

// Check returns true if a token is available for the given
// (agent_id, envelope_type) under the supplied limit. On true the
// token is consumed; on false no state changes (the call is rejected
// without consuming budget).
func (q *QuotaStore) Check(agentID, envelopeType string, limit RateLimit) bool {
	now := q.now()
	key := agentID + "\x00" + envelopeType
	q.mu.Lock()
	defer q.mu.Unlock()

	b, ok := q.buckets[key]
	if !ok {
		b = &tokenBucket{
			tokens:     float64(limit.Burst),
			capacity:   float64(limit.Burst),
			refillRate: float64(limit.PerAgent) / limit.Window.Seconds(),
			last:       now,
		}
		q.buckets[key] = b
	} else {
		// Refill before checking. We don't update capacity/refillRate
		// from limit — limits are TypeSpec-immutable, so the bucket's
		// configuration set on first sight remains correct.
		elapsed := now.Sub(b.last).Seconds()
		if elapsed > 0 {
			b.tokens += elapsed * b.refillRate
			if b.tokens > b.capacity {
				b.tokens = b.capacity
			}
			b.last = now
		}
	}

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

type tokenBucket struct {
	tokens     float64
	capacity   float64
	refillRate float64 // tokens/sec
	last       time.Time
}
