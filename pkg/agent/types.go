// Package agent manages local agent registration and cross-device routing.
// Agents are separate processes that register with the sky10 daemon via
// HTTP RPC, declaring their capabilities. The daemon routes messages
// between agents and humans via SSE (local) and libp2p (cross-device).
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
	ErrMethodNotFound   = errors.New("method not found")
	ErrAgentUnavailable = errors.New("agent unavailable")
	ErrDuplicateName    = errors.New("agent name already registered")
)

// MethodSpec describes a single method an agent exposes.
type MethodSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Params      json.RawMessage `json:"params,omitempty"` // JSON schema or freeform
}

// AgentInfo is the public view of a registered agent.
type AgentInfo struct {
	ID           string       `json:"id"`          // A-<16 chars>
	Name         string       `json:"name"`        // human-chosen name
	DeviceID     string       `json:"device_id"`   // D-<8 chars> of hosting device
	DeviceName   string       `json:"device_name"` // hostname of hosting device
	Capabilities []string     `json:"capabilities"`
	Methods      []MethodSpec `json:"methods,omitempty"`
	Status       string       `json:"status"` // "connected" or "disconnected"
	ConnectedAt  time.Time    `json:"connected_at"`
}

// HasMethod reports whether the agent declares a method with the given name.
func (a *AgentInfo) HasMethod(method string) bool {
	for _, m := range a.Methods {
		if m.Name == method {
			return true
		}
	}
	return false
}

// HasCapability reports whether the agent declares the given capability.
func (a *AgentInfo) HasCapability(cap string) bool {
	for _, c := range a.Capabilities {
		if c == cap {
			return true
		}
	}
	return false
}

// RegisterParams is the input to agent.register.
type RegisterParams struct {
	Name         string       `json:"name"`
	Capabilities []string     `json:"capabilities"`
	Methods      []MethodSpec `json:"methods,omitempty"`
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
