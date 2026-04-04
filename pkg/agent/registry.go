package agent

import (
	"log/slog"
	"sync"
	"time"
)

// Registry tracks locally connected agents. Thread-safe.
type Registry struct {
	deviceID   string
	deviceName string
	logger     *slog.Logger

	// mu protects agents, byName, and lastHeartbeat.
	mu            sync.RWMutex
	agents        map[string]*AgentInfo // keyed by agent ID
	byName        map[string]string     // name -> agent ID
	lastHeartbeat map[string]time.Time  // agent ID -> last heartbeat
}

// NewRegistry creates an agent registry for the given device.
func NewRegistry(deviceID, deviceName string, logger *slog.Logger) *Registry {
	if logger == nil {
		logger = slog.Default()
	}
	return &Registry{
		deviceID:      deviceID,
		deviceName:    deviceName,
		logger:        logger,
		agents:        make(map[string]*AgentInfo),
		byName:        make(map[string]string),
		lastHeartbeat: make(map[string]time.Time),
	}
}

// Register adds an agent to the registry. Returns the generated agent info.
// Returns ErrDuplicateName if an agent with the same name is already
// registered.
func (r *Registry) Register(p RegisterParams, agentID string) (*AgentInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if existingID, ok := r.byName[p.Name]; ok {
		if _, exists := r.agents[existingID]; exists {
			return nil, ErrDuplicateName
		}
		// Stale entry — clean up.
		delete(r.byName, p.Name)
	}

	now := time.Now().UTC()
	info := &AgentInfo{
		ID:          agentID,
		Name:        p.Name,
		DeviceID:    r.deviceID,
		DeviceName:  r.deviceName,
		Skills:      p.Skills,
		Status:      "connected",
		ConnectedAt: now,
	}

	r.agents[agentID] = info
	r.byName[p.Name] = agentID
	r.lastHeartbeat[agentID] = now
	r.logger.Info("agent registered", "id", agentID, "name", p.Name)
	return info, nil
}

// Deregister removes an agent by ID.
func (r *Registry) Deregister(agentID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	info, ok := r.agents[agentID]
	if !ok {
		return
	}
	delete(r.agents, agentID)
	delete(r.byName, info.Name)
	delete(r.lastHeartbeat, agentID)
	r.logger.Info("agent deregistered", "id", agentID, "name", info.Name)
}

// Heartbeat records a heartbeat from an agent.
func (r *Registry) Heartbeat(agentID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.agents[agentID]; !ok {
		return false
	}
	r.lastHeartbeat[agentID] = time.Now().UTC()
	return true
}

// LastHeartbeat returns the last heartbeat time for an agent.
func (r *Registry) LastHeartbeat(agentID string) (time.Time, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.lastHeartbeat[agentID]
	return t, ok
}

// Get returns an agent by ID. Returns nil if not found.
func (r *Registry) Get(agentID string) *AgentInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	info := r.agents[agentID]
	if info == nil {
		return nil
	}
	cp := *info
	return &cp
}

// GetByName returns an agent by name. Returns nil if not found.
func (r *Registry) GetByName(name string) *AgentInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	id, ok := r.byName[name]
	if !ok {
		return nil
	}
	info := r.agents[id]
	if info == nil {
		return nil
	}
	cp := *info
	return &cp
}

// List returns a snapshot of all registered agents.
func (r *Registry) List() []AgentInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]AgentInfo, 0, len(r.agents))
	for _, info := range r.agents {
		out = append(out, *info)
	}
	return out
}

// Len returns the number of registered agents.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.agents)
}

// Resolve finds an agent by name or ID.
func (r *Registry) Resolve(nameOrID string) *AgentInfo {
	if info := r.Get(nameOrID); info != nil {
		return info
	}
	return r.GetByName(nameOrID)
}

// DeviceID returns this registry's device ID.
func (r *Registry) DeviceID() string {
	return r.deviceID
}
