package link

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"sort"
	"sync"
	"time"
)

const (
	defaultManagerDebounce    = 300 * time.Millisecond
	defaultManagerRetryMin    = 2 * time.Second
	defaultManagerRetryMax    = 30 * time.Second
	defaultManagerRunTimeout  = 20 * time.Second
	defaultManagerTriggerBuf  = 64
	internalReasonRetry       = "retry"
	internalReasonSafetySweep = "safety_sweep"
)

// Trigger describes one reason to run a convergence pass.
type Trigger struct {
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
}

// ConvergenceBatch is the set of reasons collapsed into one reconcile pass.
type ConvergenceBatch struct {
	Reasons   []Trigger `json:"reasons"`
	Retry     int       `json:"retry"`
	SafetyRun bool      `json:"safety_run,omitempty"`
}

// ReconcileFunc performs one convergence pass for the current daemon state.
type ReconcileFunc func(context.Context, ConvergenceBatch) error

// ManagerOption configures a convergence manager.
type ManagerOption func(*Manager)

// Manager serializes private-network convergence work behind a debounced,
// bounded-retry controller.
type Manager struct {
	logger      *slog.Logger
	reconcile   ReconcileFunc
	debounce    time.Duration
	retryMin    time.Duration
	retryMax    time.Duration
	runTimeout  time.Duration
	safetySweep time.Duration

	jitterMu sync.Mutex
	rand     *rand.Rand
	jitter   func(time.Duration) time.Duration

	triggerCh chan triggerRequest
}

type triggerRequest struct {
	trigger  Trigger
	internal bool
}

type runResult struct {
	batch ConvergenceBatch
	err   error
}

// NewManager creates a serialized convergence controller.
func NewManager(logger *slog.Logger, reconcile ReconcileFunc, opts ...ManagerOption) *Manager {
	m := &Manager{
		logger:     logger,
		reconcile:  reconcile,
		debounce:   defaultManagerDebounce,
		retryMin:   defaultManagerRetryMin,
		retryMax:   defaultManagerRetryMax,
		runTimeout: defaultManagerRunTimeout,
		triggerCh:  make(chan triggerRequest, defaultManagerTriggerBuf),
	}
	m.rand = rand.New(rand.NewSource(time.Now().UnixNano()))
	m.jitter = m.defaultJitter
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// WithManagerDebounce overrides the debounce window for collapsed triggers.
func WithManagerDebounce(delay time.Duration) ManagerOption {
	return func(m *Manager) {
		if delay > 0 {
			m.debounce = delay
		}
	}
}

// WithManagerRetryBounds overrides the retry backoff bounds.
func WithManagerRetryBounds(min, max time.Duration) ManagerOption {
	return func(m *Manager) {
		if min > 0 {
			m.retryMin = min
		}
		if max > 0 {
			m.retryMax = max
		}
		if m.retryMax < m.retryMin {
			m.retryMax = m.retryMin
		}
	}
}

// WithManagerRunTimeout overrides the timeout for one reconcile pass.
func WithManagerRunTimeout(timeout time.Duration) ManagerOption {
	return func(m *Manager) {
		if timeout > 0 {
			m.runTimeout = timeout
		}
	}
}

// WithManagerSafetySweep enables a periodic safety sweep trigger.
func WithManagerSafetySweep(interval time.Duration) ManagerOption {
	return func(m *Manager) {
		if interval > 0 {
			m.safetySweep = interval
		}
	}
}

// WithManagerJitter is primarily for tests to make retry scheduling deterministic.
func WithManagerJitter(fn func(time.Duration) time.Duration) ManagerOption {
	return func(m *Manager) {
		if fn != nil {
			m.jitter = fn
		}
	}
}

// Trigger requests a convergence pass for an external reason.
func (m *Manager) Trigger(reason, detail string) {
	m.enqueue(triggerRequest{trigger: Trigger{Reason: reason, Detail: detail}})
}

func (m *Manager) enqueue(req triggerRequest) {
	if m == nil {
		return
	}
	select {
	case m.triggerCh <- req:
	default:
		if m.logger != nil {
			m.logger.Warn("dropping convergence trigger", "reason", req.trigger.Reason, "detail", req.trigger.Detail)
		}
	}
}

// Run processes trigger events until ctx is cancelled.
func (m *Manager) Run(ctx context.Context) error {
	if m == nil || m.reconcile == nil {
		return fmt.Errorf("convergence manager requires reconcile callback")
	}

	var (
		timer      *time.Timer
		timerCh    <-chan time.Time
		scheduled  time.Time
		pending    = make(map[string]triggerRequest)
		running    bool
		retryCount int
		resultCh   = make(chan runResult, 1)
		sweepTick  <-chan time.Time
	)

	if m.safetySweep > 0 {
		ticker := time.NewTicker(m.safetySweep)
		defer ticker.Stop()
		sweepTick = ticker.C
	}

	stopTimer := func() {
		if timer == nil {
			return
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timerCh = nil
		scheduled = time.Time{}
	}

	schedule := func(delay time.Duration) {
		if delay < 0 {
			delay = 0
		}
		deadline := time.Now().Add(delay)
		if timer == nil {
			timer = time.NewTimer(delay)
			timerCh = timer.C
			scheduled = deadline
			return
		}
		if !scheduled.IsZero() && !deadline.Before(scheduled) {
			return
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(delay)
		timerCh = timer.C
		scheduled = deadline
	}

	startRun := func() {
		if running || len(pending) == 0 {
			return
		}
		running = true
		stopTimer()
		batch := buildBatch(pending, retryCount)
		clear(pending)
		runCtx := ctx
		cancel := func() {}
		if m.runTimeout > 0 {
			runCtx, cancel = context.WithTimeout(ctx, m.runTimeout)
		}
		go func(batch ConvergenceBatch) {
			defer cancel()
			err := m.reconcile(runCtx, batch)
			resultCh <- runResult{batch: batch, err: err}
		}(batch)
	}

	for {
		select {
		case <-ctx.Done():
			stopTimer()
			return nil
		case <-sweepTick:
			pending[internalReasonSafetySweep] = triggerRequest{
				trigger:  Trigger{Reason: internalReasonSafetySweep},
				internal: true,
			}
			if !running {
				schedule(m.debounce)
			}
		case req := <-m.triggerCh:
			key := triggerKey(req.trigger)
			if key == "" {
				continue
			}
			if existing, ok := pending[key]; !ok || shouldReplaceTrigger(existing.trigger, req.trigger) {
				pending[key] = req
			}
			if !running {
				schedule(m.debounce)
			}
		case <-timerCh:
			startRun()
		case result := <-resultCh:
			running = false
			if result.err != nil {
				retryCount++
				pending[internalReasonRetry] = triggerRequest{
					trigger: Trigger{
						Reason: internalReasonRetry,
						Detail: result.err.Error(),
					},
					internal: true,
				}
				delay := m.retryDelay(retryCount)
				if hasExternalPending(pending) {
					delay = m.debounce
				}
				if m.logger != nil {
					m.logger.Warn("convergence pass failed",
						"retry", retryCount,
						"delay", delay.String(),
						"error", result.err,
					)
				}
				schedule(delay)
				continue
			}

			retryCount = 0
			delete(pending, internalReasonRetry)
			if len(pending) > 0 {
				schedule(m.debounce)
			}
		}
	}
}

func buildBatch(pending map[string]triggerRequest, retryCount int) ConvergenceBatch {
	reasons := make([]Trigger, 0, len(pending))
	safetyRun := false
	for _, req := range pending {
		reasons = append(reasons, req.trigger)
		if req.trigger.Reason == internalReasonSafetySweep {
			safetyRun = true
		}
	}
	sort.Slice(reasons, func(i, j int) bool {
		if reasons[i].Reason == reasons[j].Reason {
			return reasons[i].Detail < reasons[j].Detail
		}
		return reasons[i].Reason < reasons[j].Reason
	})
	return ConvergenceBatch{
		Reasons:   reasons,
		Retry:     retryCount,
		SafetyRun: safetyRun,
	}
}

func (m *Manager) retryDelay(retry int) time.Duration {
	if retry <= 0 {
		return m.debounce
	}
	delay := m.retryMin
	for i := 1; i < retry && delay < m.retryMax; i++ {
		delay *= 2
		if delay > m.retryMax {
			delay = m.retryMax
		}
	}
	if m.jitter != nil {
		return m.jitter(delay)
	}
	return delay
}

func (m *Manager) defaultJitter(delay time.Duration) time.Duration {
	if delay <= 0 {
		return delay
	}
	maxExtra := delay / 4
	if maxExtra <= 0 {
		return delay
	}
	m.jitterMu.Lock()
	extra := time.Duration(m.rand.Int63n(int64(maxExtra) + 1))
	m.jitterMu.Unlock()
	return delay + extra
}

func triggerKey(trigger Trigger) string {
	if trigger.Reason == "" {
		return ""
	}
	return trigger.Reason + "\x00" + trigger.Detail
}

func shouldReplaceTrigger(current, next Trigger) bool {
	if current.Reason != next.Reason {
		return false
	}
	return current.Detail == "" && next.Detail != ""
}

func hasExternalPending(pending map[string]triggerRequest) bool {
	for _, req := range pending {
		if !req.internal {
			return true
		}
	}
	return false
}
