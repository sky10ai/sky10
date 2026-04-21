package runtime

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/sky10/sky10/pkg/messaging/protocol"
)

const (
	defaultStartupTimeout      = 5 * time.Second
	defaultHealthCheckInterval = 30 * time.Second
	defaultRestartBackoffBase  = 250 * time.Millisecond
	defaultRestartBackoffMax   = 10 * time.Second
)

// ErrAdapterUnavailable reports that a managed adapter is not currently ready.
var ErrAdapterUnavailable = errors.New("adapter unavailable")

// ManagedAdapterSpec describes one supervised adapter instance.
type ManagedAdapterSpec struct {
	Key                 string
	Process             ProcessSpec
	Notify              NotificationHandler
	StartupTimeout      time.Duration
	HealthCheckInterval time.Duration
	RestartBackoffBase  time.Duration
	RestartBackoffMax   time.Duration
}

// Validate reports whether the managed adapter spec is usable.
func (s ManagedAdapterSpec) Validate() error {
	if s.Key == "" {
		return fmt.Errorf("managed adapter key is required")
	}
	if err := s.Process.Validate(); err != nil {
		return fmt.Errorf("managed adapter process: %w", err)
	}
	return nil
}

func (s ManagedAdapterSpec) withDefaults() ManagedAdapterSpec {
	if s.StartupTimeout <= 0 {
		s.StartupTimeout = defaultStartupTimeout
	}
	if s.HealthCheckInterval < 0 {
		s.HealthCheckInterval = 0
	}
	if s.HealthCheckInterval == 0 {
		s.HealthCheckInterval = defaultHealthCheckInterval
	}
	if s.RestartBackoffBase <= 0 {
		s.RestartBackoffBase = defaultRestartBackoffBase
	}
	if s.RestartBackoffMax <= 0 {
		s.RestartBackoffMax = defaultRestartBackoffMax
	}
	if s.RestartBackoffMax < s.RestartBackoffBase {
		s.RestartBackoffMax = s.RestartBackoffBase
	}
	return s
}

// ManagedAdapterState is the observable lifecycle state of a supervised adapter.
type ManagedAdapterState struct {
	Key                 string                 `json:"key"`
	Running             bool                   `json:"running"`
	LaunchCount         int                    `json:"launch_count"`
	RestartCount        int                    `json:"restart_count"`
	ConsecutiveFailures int                    `json:"consecutive_failures"`
	PID                 int                    `json:"pid,omitempty"`
	AdapterID           string                 `json:"adapter_id,omitempty"`
	ProtocolVersion     string                 `json:"protocol_version,omitempty"`
	LastStartedAt       time.Time              `json:"last_started_at,omitempty"`
	LastExitedAt        time.Time              `json:"last_exited_at,omitempty"`
	LastError           string                 `json:"last_error,omitempty"`
	NextRestartAt       time.Time              `json:"next_restart_at,omitempty"`
	LastHealth          *protocol.HealthStatus `json:"last_health,omitempty"`
}

// ManagedAdapter supervises one adapter process and restarts it on unexpected
// exit with exponential backoff.
type ManagedAdapter struct {
	spec   ManagedAdapterSpec
	ctx    context.Context
	cancel context.CancelFunc

	done chan struct{}

	mu      sync.RWMutex
	client  *AdapterClient
	state   ManagedAdapterState
	stateCh chan struct{}
}

// StartManagedAdapter launches a supervised adapter loop.
func StartManagedAdapter(ctx context.Context, spec ManagedAdapterSpec) (*ManagedAdapter, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	spec = spec.withDefaults()

	runCtx, cancel := context.WithCancel(ctx)
	m := &ManagedAdapter{
		spec:    spec,
		ctx:     runCtx,
		cancel:  cancel,
		done:    make(chan struct{}),
		stateCh: make(chan struct{}),
		state: ManagedAdapterState{
			Key: spec.Key,
		},
	}
	go m.run()
	return m, nil
}

// Snapshot returns the current observable state.
func (m *ManagedAdapter) Snapshot() ManagedAdapterState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state := m.state
	if m.state.LastHealth != nil {
		health := *m.state.LastHealth
		state.LastHealth = &health
	}
	return state
}

// WaitReady blocks until the adapter is running or the context ends.
func (m *ManagedAdapter) WaitReady(ctx context.Context) error {
	for {
		m.mu.RLock()
		running := m.state.Running && m.client != nil
		stateCh := m.stateCh
		lastErr := m.state.LastError
		m.mu.RUnlock()
		if running {
			return nil
		}
		select {
		case <-ctx.Done():
			if lastErr != "" {
				return fmt.Errorf("%w: %s", ctx.Err(), lastErr)
			}
			return ctx.Err()
		case <-m.done:
			if lastErr != "" {
				return fmt.Errorf("%w: %s", ErrAdapterUnavailable, lastErr)
			}
			return ErrAdapterUnavailable
		case <-stateCh:
		}
	}
}

// Current returns the current typed adapter client when available.
func (m *ManagedAdapter) Current() (*AdapterClient, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.client == nil || !m.state.Running {
		if m.state.LastError != "" {
			return nil, fmt.Errorf("%w: %s", ErrAdapterUnavailable, m.state.LastError)
		}
		return nil, ErrAdapterUnavailable
	}
	return m.client, nil
}

// Close requests supervisor shutdown and waits for completion.
func (m *ManagedAdapter) Close() error {
	if m == nil {
		return nil
	}
	m.cancel()
	<-m.done
	return nil
}

// Done closes when the supervisor stops.
func (m *ManagedAdapter) Done() <-chan struct{} {
	if m == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return m.done
}

func (m *ManagedAdapter) run() {
	defer close(m.done)

	for {
		if err := m.ctx.Err(); err != nil {
			m.clearCurrent("", time.Time{})
			return
		}

		client, err := m.startClient()
		if err != nil {
			if m.ctx.Err() != nil {
				m.clearCurrent("", time.Time{})
				return
			}
			next := m.recordFailure(err)
			if !m.waitBackoff(next) {
				m.clearCurrent("", time.Time{})
				return
			}
			continue
		}

		healthDone := make(chan struct{})
		if m.spec.HealthCheckInterval > 0 {
			go m.healthLoop(client, healthDone)
		} else {
			close(healthDone)
		}

		err = client.Wait()
		<-healthDone

		if m.ctx.Err() != nil {
			m.clearCurrent("", time.Now().UTC())
			return
		}
		next := m.recordFailure(err)
		if !m.waitBackoff(next) {
			m.clearCurrent("", time.Time{})
			return
		}
	}
}

func (m *ManagedAdapter) startClient() (*AdapterClient, error) {
	client, err := StartAdapter(m.ctx, m.spec.Process, m.spec.Notify)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	m.mu.Lock()
	m.state.LaunchCount++
	if m.state.LaunchCount > 1 {
		m.state.RestartCount++
	}
	m.state.LastStartedAt = now
	m.state.PID = client.PID()
	m.signalStateChangeLocked()
	m.mu.Unlock()

	describeCtx, cancel := context.WithTimeout(m.ctx, m.spec.StartupTimeout)
	defer cancel()
	describe, err := client.Describe(describeCtx)
	if err != nil {
		_ = client.Close()
		return nil, err
	}

	m.mu.Lock()
	m.client = client
	m.state.Running = true
	m.state.ConsecutiveFailures = 0
	m.state.LastError = ""
	m.state.NextRestartAt = time.Time{}
	m.state.AdapterID = string(describe.Adapter.ID)
	m.state.ProtocolVersion = describe.Protocol.Version
	m.state.LastHealth = nil
	m.signalStateChangeLocked()
	m.mu.Unlock()
	return client, nil
}

func (m *ManagedAdapter) recordFailure(err error) time.Time {
	now := time.Now().UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.client = nil
	m.state.Running = false
	m.state.PID = 0
	m.state.LastExitedAt = now
	m.state.ConsecutiveFailures++
	if err != nil {
		m.state.LastError = err.Error()
	}
	backoff := restartBackoff(m.spec.RestartBackoffBase, m.spec.RestartBackoffMax, m.state.ConsecutiveFailures)
	m.state.NextRestartAt = now.Add(backoff)
	m.signalStateChangeLocked()
	return m.state.NextRestartAt
}

func (m *ManagedAdapter) clearCurrent(lastErr string, exitedAt time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.client = nil
	m.state.Running = false
	m.state.PID = 0
	if lastErr != "" {
		m.state.LastError = lastErr
	}
	if !exitedAt.IsZero() {
		m.state.LastExitedAt = exitedAt
	}
	m.state.NextRestartAt = time.Time{}
	m.signalStateChangeLocked()
}

func (m *ManagedAdapter) waitBackoff(next time.Time) bool {
	if next.IsZero() {
		return true
	}
	delay := time.Until(next)
	if delay <= 0 {
		return true
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-m.ctx.Done():
		return false
	case <-timer.C:
		m.mu.Lock()
		m.state.NextRestartAt = time.Time{}
		m.signalStateChangeLocked()
		m.mu.Unlock()
		return true
	}
}

func (m *ManagedAdapter) healthLoop(client *AdapterClient, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(m.spec.HealthCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			healthCtx, cancel := context.WithTimeout(m.ctx, m.spec.HealthCheckInterval)
			result, err := client.Health(healthCtx, protocol.HealthParams{})
			cancel()

			now := time.Now().UTC()
			status := &protocol.HealthStatus{OK: false, CheckedAt: now}
			if err != nil {
				status.Message = err.Error()
			} else {
				copy := result.Health
				if copy.CheckedAt.IsZero() {
					copy.CheckedAt = now
				}
				status = &copy
			}

			m.mu.Lock()
			if m.client == client {
				m.state.LastHealth = status
				m.signalStateChangeLocked()
			}
			m.mu.Unlock()
		case <-client.Host().Done():
			return
		}
	}
}

func (m *ManagedAdapter) signalStateChangeLocked() {
	close(m.stateCh)
	m.stateCh = make(chan struct{})
}

func restartBackoff(base, max time.Duration, failures int) time.Duration {
	if failures <= 0 {
		return 0
	}
	backoff := base
	for i := 1; i < failures; i++ {
		if backoff >= max/2 {
			return max
		}
		backoff *= 2
	}
	if backoff > max {
		return max
	}
	return backoff
}

// Manager tracks multiple supervised adapters by key.
type Manager struct {
	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.RWMutex
	adapters map[string]*ManagedAdapter
}

// NewManager creates a managed adapter registry bound to ctx.
func NewManager(ctx context.Context) *Manager {
	runCtx, cancel := context.WithCancel(ctx)
	return &Manager{
		ctx:      runCtx,
		cancel:   cancel,
		adapters: make(map[string]*ManagedAdapter),
	}
}

// Add starts and registers one managed adapter.
func (m *Manager) Add(spec ManagedAdapterSpec) (*ManagedAdapter, error) {
	adapter, err := StartManagedAdapter(m.ctx, spec)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.adapters[spec.Key]; exists {
		_ = adapter.Close()
		return nil, fmt.Errorf("managed adapter %q already exists", spec.Key)
	}
	m.adapters[spec.Key] = adapter
	return adapter, nil
}

// Get returns one managed adapter by key.
func (m *Manager) Get(key string) (*ManagedAdapter, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	adapter, ok := m.adapters[key]
	return adapter, ok
}

// Remove stops and unregisters one managed adapter by key.
func (m *Manager) Remove(key string) error {
	m.mu.Lock()
	adapter, ok := m.adapters[key]
	if ok {
		delete(m.adapters, key)
	}
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("managed adapter %q not found", key)
	}
	return adapter.Close()
}

// List returns managed adapter snapshots sorted by key.
func (m *Manager) List() []ManagedAdapterState {
	m.mu.RLock()
	adapters := make([]*ManagedAdapter, 0, len(m.adapters))
	for _, adapter := range m.adapters {
		adapters = append(adapters, adapter)
	}
	m.mu.RUnlock()

	snapshots := make([]ManagedAdapterState, 0, len(adapters))
	for _, adapter := range adapters {
		snapshots = append(snapshots, adapter.Snapshot())
	}
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].Key < snapshots[j].Key
	})
	return snapshots
}

// Close stops all managed adapters.
func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	m.cancel()

	m.mu.Lock()
	adapters := make([]*ManagedAdapter, 0, len(m.adapters))
	for key, adapter := range m.adapters {
		adapters = append(adapters, adapter)
		delete(m.adapters, key)
	}
	m.mu.Unlock()

	var errs []error
	for _, adapter := range adapters {
		if err := adapter.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
