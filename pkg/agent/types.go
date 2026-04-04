// Package agent manages local agent registration and cross-device routing.
// Agents are separate processes that register with the sky10 daemon via
// HTTP RPC, declaring their capabilities and an HTTP callback endpoint.
// The daemon advertises agents to the P2P swarm and routes calls.
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
	ID           string       `json:"id"`          // A-<8 chars>
	Name         string       `json:"name"`        // human-chosen name
	DeviceID     string       `json:"device_id"`   // D-<8 chars> of hosting device
	DeviceName   string       `json:"device_name"` // hostname of hosting device
	Endpoint     string       `json:"endpoint"`    // HTTP RPC callback URL
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
	Endpoint     string       `json:"endpoint"` // e.g. "http://localhost:8200/rpc"
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

// CallParams is the input to agent.call.
type CallParams struct {
	Agent    string          `json:"agent"`               // agent name or ID
	DeviceID string          `json:"device_id,omitempty"` // empty = local
	Method   string          `json:"method"`
	Params   json.RawMessage `json:"params,omitempty"`
}

// CallResult is the response from agent.call.
type CallResult struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// GenerateAgentID creates a new keypair and returns the A- prefixed ID
// and the key. The key is persisted by the caller.
func GenerateAgentID() (string, *skykey.Key, error) {
	k, err := skykey.Generate()
	if err != nil {
		return "", nil, err
	}
	return "A-" + k.ShortID(), k, nil
}
