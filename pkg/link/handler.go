package link

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

// Capability describes something this agent can do.
type Capability struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// HandlerFunc processes an incoming skylink request from a peer.
type HandlerFunc func(ctx context.Context, req *PeerRequest) (interface{}, error)

// PeerRequest is an incoming request from a remote peer.
type PeerRequest struct {
	PeerID  peer.ID
	Address string
	Method  string
	Params  json.RawMessage
}

// Registry manages capability handlers for the node.
type Registry struct {
	logger *slog.Logger

	mu           sync.RWMutex
	handlers     map[string]HandlerFunc
	capabilities []Capability
}

// NewRegistry creates a capability registry with built-in handlers.
func NewRegistry(logger *slog.Logger) *Registry {
	if logger == nil {
		logger = slog.Default()
	}
	r := &Registry{
		logger:   logger,
		handlers: make(map[string]HandlerFunc),
	}
	r.Register(Capability{Name: "ping", Description: "health check"}, handlePing)
	return r
}

// Register adds a capability handler.
func (r *Registry) Register(cap Capability, handler HandlerFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[cap.Name] = handler
	r.capabilities = append(r.capabilities, cap)
}

// Capabilities returns all registered capabilities.
func (r *Registry) Capabilities() []Capability {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Capability, len(r.capabilities))
	copy(out, r.capabilities)
	return out
}

// HandleStream is the libp2p stream handler for the skylink protocol.
// It reads a request, dispatches to the right handler, and writes the response.
func (r *Registry) HandleStream(s network.Stream) {
	defer s.Close()

	msg, err := ReadMessage(s)
	if err != nil {
		r.logger.Debug("failed to read request", "error", err)
		return
	}

	addr, _ := AddressFromPeerID(s.Conn().RemotePeer())
	req := &PeerRequest{
		PeerID:  s.Conn().RemotePeer(),
		Address: addr,
		Method:  msg.Method,
		Params:  msg.Params,
	}

	r.mu.RLock()
	handler, ok := r.handlers[msg.Method]
	r.mu.RUnlock()

	var resp *Message
	if !ok {
		resp = &Message{
			ID:    msg.ID,
			Error: &MessageError{Code: -32601, Message: "unknown method: " + msg.Method},
		}
	} else {
		result, err := handler(context.Background(), req)
		if err != nil {
			resp = &Message{
				ID:    msg.ID,
				Error: &MessageError{Code: -32000, Message: err.Error()},
			}
		} else {
			raw, _ := json.Marshal(result)
			resp = &Message{
				ID:     msg.ID,
				Result: raw,
			}
		}
	}

	if err := WriteMessage(s, resp); err != nil {
		r.logger.Debug("failed to write response", "error", err)
	}
}

func handlePing(_ context.Context, _ *PeerRequest) (interface{}, error) {
	return map[string]bool{"pong": true}, nil
}
