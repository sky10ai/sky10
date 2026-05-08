package messengers

import (
	"net/http"
	"time"

	"github.com/sky10/sky10/pkg/sandbox/bridge"
)

const (
	// EndpointPath is the canonical URL path the messenger bridge endpoint registers at.
	EndpointPath = "/bridge/messengers/ws"

	TypeListConnections   = "messengers.list_connections"
	TypeListConversations = "messengers.list_conversations"
	TypeListEvents        = "messengers.list_events"
	TypeGetMessages       = "messengers.get_messages"
	TypeCreateDraft       = "messengers.create_draft"
	TypeRequestSend       = "messengers.request_send"
)

// handlers groups the per-envelope handlers around their shared Backend.
type handlers struct {
	backend Backend
}

// NewEndpoint builds a configured bridge.Endpoint serving messenger envelopes
// against the supplied Backend.
func NewEndpoint(backend Backend, resolver bridge.IdentityResolver, opts ...bridge.EndpointOption) *bridge.Endpoint {
	if backend == nil {
		panic("messengers: NewEndpoint requires a non-nil Backend")
	}
	h := &handlers{backend: backend}
	e := bridge.NewEndpoint("messengers", resolver, opts...)
	e.Register(bridge.TypeSpec{
		Name:           TypeListConnections,
		Direction:      bridge.DirectionRequestResponse,
		MaxPayloadSize: 4 * 1024,
		RateLimit: bridge.RateLimit{
			PerAgent: 120,
			Burst:    20,
			Window:   time.Minute,
		},
		NonceWindow: 5 * time.Minute,
		AuditLevel:  bridge.AuditHeaders,
		Handler:     h.handleListConnections,
	})
	e.Register(bridge.TypeSpec{
		Name:           TypeListConversations,
		Direction:      bridge.DirectionRequestResponse,
		MaxPayloadSize: 4 * 1024,
		RateLimit: bridge.RateLimit{
			PerAgent: 120,
			Burst:    20,
			Window:   time.Minute,
		},
		NonceWindow: 5 * time.Minute,
		AuditLevel:  bridge.AuditHeaders,
		Handler:     h.handleListConversations,
	})
	e.Register(bridge.TypeSpec{
		Name:           TypeListEvents,
		Direction:      bridge.DirectionRequestResponse,
		MaxPayloadSize: 4 * 1024,
		RateLimit: bridge.RateLimit{
			PerAgent: 120,
			Burst:    20,
			Window:   time.Minute,
		},
		NonceWindow: 5 * time.Minute,
		AuditLevel:  bridge.AuditHeaders,
		Handler:     h.handleListEvents,
	})
	e.Register(bridge.TypeSpec{
		Name:           TypeGetMessages,
		Direction:      bridge.DirectionRequestResponse,
		MaxPayloadSize: 4 * 1024,
		RateLimit: bridge.RateLimit{
			PerAgent: 120,
			Burst:    20,
			Window:   time.Minute,
		},
		NonceWindow: 5 * time.Minute,
		AuditLevel:  bridge.AuditHeaders,
		Handler:     h.handleGetMessages,
	})
	e.Register(bridge.TypeSpec{
		Name:           TypeCreateDraft,
		Direction:      bridge.DirectionRequestResponse,
		MaxPayloadSize: 128 * 1024,
		RateLimit: bridge.RateLimit{
			PerAgent: 60,
			Burst:    10,
			Window:   time.Minute,
		},
		NonceWindow: 10 * time.Minute,
		AuditLevel:  bridge.AuditFull,
		Handler:     h.handleCreateDraft,
	})
	e.Register(bridge.TypeSpec{
		Name:           TypeRequestSend,
		Direction:      bridge.DirectionRequestResponse,
		MaxPayloadSize: 4 * 1024,
		RateLimit: bridge.RateLimit{
			PerAgent: 60,
			Burst:    10,
			Window:   time.Minute,
		},
		NonceWindow: 10 * time.Minute,
		AuditLevel:  bridge.AuditFull,
		Handler:     h.handleRequestSend,
	})
	return e
}

// RegisterOnMux builds the endpoint and mounts it at EndpointPath.
func RegisterOnMux(mux *http.ServeMux, backend Backend, resolver bridge.IdentityResolver, opts ...bridge.EndpointOption) {
	e := NewEndpoint(backend, resolver, opts...)
	mux.HandleFunc("GET "+EndpointPath, e.Handler())
}
