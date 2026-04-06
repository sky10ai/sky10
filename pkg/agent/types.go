// Package agent manages local agent registration and cross-device routing.
// Agents are separate processes that register with the sky10 daemon via
// HTTP RPC, declaring their skills. The daemon routes messages between
// agents and humans via SSE (local) and libp2p (cross-device).
package agent

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
	KeyName     string    `json:"-"`           // stable identity slug
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
	Name    string   `json:"name"`
	KeyName string   `json:"key_name,omitempty"`
	Skills  []string `json:"skills"`
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

// EffectiveKeyName returns the stable agent identity slug for registration.
// key_name is preferred so display-name changes do not rotate identity.
// For backward compatibility, name remains the fallback.
func (p RegisterParams) EffectiveKeyName() string {
	if s := normalizeAgentKeyName(p.KeyName); s != "" {
		return s
	}
	return normalizeAgentKeyName(p.Name)
}

func normalizeAgentKeyName(v string) string {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(v)))
	return strings.Join(fields, "-")
}

// GenerateAgentID deterministically derives an agent keypair and returns the
// A- prefixed ID and the key. The key is stable for a given owner identity and
// effective key name.
func GenerateAgentID(owner *skykey.Key, keyName string) (string, *skykey.Key, error) {
	k, err := DeriveAgentKey(owner, keyName)
	if err != nil {
		return "", nil, err
	}
	addr := k.Address()
	if len(addr) > 21 {
		return "A-" + addr[5:21], k, nil
	}
	return "A-" + k.ShortID(), k, nil
}

// DeriveAgentKey deterministically derives an Ed25519 keypair for an agent
// from the owner identity seed and a stable agent key name.
func DeriveAgentKey(owner *skykey.Key, keyName string) (*skykey.Key, error) {
	if owner == nil {
		return nil, fmt.Errorf("owner key is required")
	}
	if !owner.IsPrivate() {
		return nil, fmt.Errorf("owner key must have private component")
	}
	keyName = normalizeAgentKeyName(keyName)
	if keyName == "" {
		return nil, fmt.Errorf("agent key name is required")
	}

	seed, err := skykey.DeriveKey(owner.PrivateKey.Seed(), []byte(keyName), "sky10-agent-key-v1")
	if err != nil {
		return nil, fmt.Errorf("deriving agent seed: %w", err)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	pub := make(ed25519.PublicKey, ed25519.PublicKeySize)
	copy(pub, priv.Public().(ed25519.PublicKey))
	return &skykey.Key{
		PublicKey:  pub,
		PrivateKey: priv,
	}, nil
}
