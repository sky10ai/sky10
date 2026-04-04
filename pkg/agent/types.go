// Package agent manages local agent registration and cross-device routing.
// Agents are separate processes that register with the sky10 daemon via
// HTTP RPC, declaring their skills. The daemon routes messages between
// agents and humans via SSE (local) and libp2p (cross-device).
package agent

import (
	"encoding/json"
	"errors"
	"time"

	skykey "github.com/sky10/sky10/pkg/key"
)

// Sentinel errors for agent operations.
var (
	ErrAgentNotFound    = errors.New("agent not found")
	ErrAgentUnavailable = errors.New("agent unavailable")
	ErrDuplicateName    = errors.New("agent name already registered")
)

// AgentInfo is the public view of a registered agent.
type AgentInfo struct {
	ID          string    `json:"id"`          // A-<16 chars>
	Name        string    `json:"name"`        // human-chosen name
	DeviceID    string    `json:"device_id"`   // D-<8 chars> of hosting device
	DeviceName  string    `json:"device_name"` // hostname of hosting device
	Skills      []string  `json:"skills"`
	Status      string    `json:"status"` // "connected" or "disconnected"
	ConnectedAt time.Time `json:"connected_at"`
}

// HasSkill reports whether the agent declares the given skill.
func (a *AgentInfo) HasSkill(skill string) bool {
	for _, s := range a.Skills {
		if s == skill {
			return true
		}
	}
	return false
}

// RegisterParams is the input to agent.register.
type RegisterParams struct {
	Name   string   `json:"name"`
	Skills []string `json:"skills"`
}

// RegisterResult is the response from agent.register.
type RegisterResult struct {
	AgentID string `json:"agent_id"`
	Status  string `json:"status"`
}

// DeregisterParams is the input to agent.deregister.
type DeregisterParams struct {
	AgentID string `json:"agent_id"`
}

// Message is a routable message between agents and/or humans. The daemon
// routes by `To` — locally via SSE, cross-device via libp2p.
type Message struct {
	ID        string          `json:"id"`
	SessionID string          `json:"session_id"`
	From      string          `json:"from"`                // agent ID or identity address
	To        string          `json:"to"`                  // agent ID or identity address
	DeviceID  string          `json:"device_id,omitempty"` // target device for routing
	Type      string          `json:"type"`                // "text", "tool_call", "diff", "permission", "done"
	Content   json.RawMessage `json:"content"`
	Timestamp time.Time       `json:"timestamp"`
}

// SendParams is the input to agent.send.
type SendParams struct {
	To        string          `json:"to"`                  // agent ID or identity address
	DeviceID  string          `json:"device_id,omitempty"` // empty = local
	SessionID string          `json:"session_id"`
	Type      string          `json:"type"` // "text", "tool_call", "diff", "permission", "done"
	Content   json.RawMessage `json:"content"`
}

// GenerateAgentID creates a new keypair and returns the A- prefixed ID
// and the key. Uses 16 chars (80 bits) for global uniqueness.
func GenerateAgentID() (string, *skykey.Key, error) {
	k, err := skykey.Generate()
	if err != nil {
		return "", nil, err
	}
	addr := k.Address()
	if len(addr) > 21 {
		return "A-" + addr[5:21], k, nil
	}
	return "A-" + k.ShortID(), k, nil
}
