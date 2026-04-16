package fs

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

const (
	defaultChunkSourceRetryBase = 5 * time.Second
	defaultChunkSourceRetryMax  = 1 * time.Minute
)

type chunkSourcePlanner struct {
	mu        sync.Mutex
	now       func() time.Time
	retryBase time.Duration
	retryMax  time.Duration
	states    map[string]chunkSourceHealthState
}

type chunkSourceHealthState struct {
	ConsecutiveFailures int
	DegradedUntil       time.Time
	LastSuccessAt       time.Time
	LastErrorAt         time.Time
	LastError           string
}

type chunkSourceHealthSnapshot struct {
	ConsecutiveFailures int    `json:"consecutive_failures,omitempty"`
	Degraded            bool   `json:"degraded,omitempty"`
	DegradedUntil       int64  `json:"degraded_until,omitempty"`
	LastSuccessAt       int64  `json:"last_success_at,omitempty"`
	LastErrorAt         int64  `json:"last_error_at,omitempty"`
	LastError           string `json:"last_error,omitempty"`
}

type chunkSourceHealthSnapshots struct {
	Peer chunkSourceHealthSnapshot `json:"peer_source_health"`
	S3   chunkSourceHealthSnapshot `json:"s3_source_health"`
}

func newChunkSourcePlanner() *chunkSourcePlanner {
	return &chunkSourcePlanner{
		now:       func() time.Time { return time.Now().UTC() },
		retryBase: defaultChunkSourceRetryBase,
		retryMax:  defaultChunkSourceRetryMax,
		states:    make(map[string]chunkSourceHealthState),
	}
}

func (p *chunkSourcePlanner) prioritize(sources []chunkSourcePlan) []chunkSourcePlan {
	if len(sources) == 0 {
		return nil
	}
	ordered := append([]chunkSourcePlan(nil), sources...)
	if p == nil {
		return ordered
	}

	now := p.nowUTC()
	sort.SliceStable(ordered, func(i, j int) bool {
		return p.sourceScore(ordered[i].kind, now) < p.sourceScore(ordered[j].kind, now)
	})
	return ordered
}

func (p *chunkSourcePlanner) recordSuccess(kind chunkSourceKind) {
	key := chunkSourceHealthKey(kind)
	if p == nil || key == "" {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	state := p.states[key]
	state.ConsecutiveFailures = 0
	state.DegradedUntil = time.Time{}
	state.LastErrorAt = time.Time{}
	state.LastError = ""
	state.LastSuccessAt = p.nowUTC()
	p.states[key] = state
}

func (p *chunkSourcePlanner) recordFailure(kind chunkSourceKind, err error) {
	key := chunkSourceHealthKey(kind)
	if p == nil || key == "" || err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	now := p.nowUTC()
	state := p.states[key]
	state.ConsecutiveFailures++
	state.LastErrorAt = now
	state.LastError = err.Error()
	state.DegradedUntil = now.Add(p.backoffForFailures(state.ConsecutiveFailures))
	p.states[key] = state
}

func (p *chunkSourcePlanner) nowUTC() time.Time {
	if p == nil || p.now == nil {
		return time.Now().UTC()
	}
	return p.now().UTC()
}

func (p *chunkSourcePlanner) sourceScore(kind chunkSourceKind, now time.Time) int {
	score := baseChunkSourcePriority(kind)
	key := chunkSourceHealthKey(kind)
	if p == nil || key == "" {
		return score
	}

	p.mu.Lock()
	state := p.states[key]
	p.mu.Unlock()

	if !state.DegradedUntil.IsZero() && state.DegradedUntil.After(now) {
		score += 100
	}
	score += minInt(state.ConsecutiveFailures, 10)
	return score
}

func (p *chunkSourcePlanner) backoffForFailures(failures int) time.Duration {
	if p == nil {
		return defaultChunkSourceRetryBase
	}
	if failures <= 0 {
		return 0
	}
	backoff := p.retryBase
	if backoff <= 0 {
		backoff = defaultChunkSourceRetryBase
	}
	maxBackoff := p.retryMax
	if maxBackoff <= 0 {
		maxBackoff = defaultChunkSourceRetryMax
	}
	for i := 1; i < failures; i++ {
		if backoff >= maxBackoff/2 {
			return maxBackoff
		}
		backoff *= 2
	}
	if backoff > maxBackoff {
		return maxBackoff
	}
	return backoff
}

func (p *chunkSourcePlanner) snapshot() chunkSourceHealthSnapshots {
	if p == nil {
		return chunkSourceHealthSnapshots{}
	}

	now := p.nowUTC()

	p.mu.Lock()
	defer p.mu.Unlock()

	return chunkSourceHealthSnapshots{
		Peer: snapshotChunkSourceHealthState(p.states["peer"], now),
		S3:   snapshotChunkSourceHealthState(p.states["s3"], now),
	}
}

func snapshotChunkSourceHealthState(state chunkSourceHealthState, now time.Time) chunkSourceHealthSnapshot {
	return chunkSourceHealthSnapshot{
		ConsecutiveFailures: state.ConsecutiveFailures,
		Degraded:            !state.DegradedUntil.IsZero() && state.DegradedUntil.After(now),
		DegradedUntil:       state.DegradedUntil.Unix(),
		LastSuccessAt:       state.LastSuccessAt.Unix(),
		LastErrorAt:         state.LastErrorAt.Unix(),
		LastError:           state.LastError,
	}
}

func chunkSourceHealthKey(kind chunkSourceKind) string {
	switch kind {
	case chunkSourcePeer:
		return "peer"
	case chunkSourceS3Pack, chunkSourceS3Blob:
		return "s3"
	default:
		return ""
	}
}

func baseChunkSourcePriority(kind chunkSourceKind) int {
	switch kind {
	case chunkSourceLocal:
		return 0
	case chunkSourcePeer:
		return 10
	case chunkSourceS3Pack, chunkSourceS3Blob:
		return 20
	default:
		return 100
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
