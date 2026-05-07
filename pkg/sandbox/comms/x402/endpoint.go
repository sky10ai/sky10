package x402

import (
	"net/http"
	"time"

	"github.com/sky10/sky10/pkg/sandbox/comms"
)

// EndpointPath is the URL path the metered-services comms endpoint registers at.
// The daemon mounts the endpoint by calling RegisterOnMux which uses
// this path; importers don't need to spell it themselves.
const EndpointPath = "/comms/metered-services/ws"

// handlers groups the per-envelope handlers around their shared
// Backend dependency. Methods on this type are the actual envelope
// handlers; the constructor wires them into a comms.Endpoint.
//
// One handler per method, one method per file (see list_services.go,
// service_call.go, budget_status.go), per the discipline rules in
// docs/work/current/sandbox-bridge/handler-discipline.md.
type handlers struct {
	backend Backend
}

// NewEndpoint builds a configured comms.Endpoint serving the metered-service
// envelope set against the supplied Backend. Caller mounts the
// returned http.HandlerFunc on its mux at EndpointPath.
//
// IdentityResolver is required and must be supplied by the daemon —
// this package does not assume any particular auth primitive. Tests
// pass a static resolver; production wires whatever the daemon uses
// for its other authenticated websocket endpoints.
func NewEndpoint(backend Backend, resolver comms.IdentityResolver, opts ...comms.Option) *comms.Endpoint {
	if backend == nil {
		panic("x402: NewEndpoint requires a non-nil Backend")
	}
	h := &handlers{backend: backend}
	e := comms.NewEndpoint("metered-services", resolver, opts...)
	e.Register(comms.TypeSpec{
		Name:           "x402.list_services",
		Direction:      comms.DirectionRequestResponse,
		MaxPayloadSize: 4 * 1024,
		RateLimit: comms.RateLimit{
			PerAgent: 60,
			Burst:    10,
			Window:   time.Minute,
		},
		NonceWindow: 5 * time.Minute,
		AuditLevel:  comms.AuditHeaders,
		Handler:     h.handleListServices,
	})
	e.Register(comms.TypeSpec{
		Name:           "x402.service_call",
		Direction:      comms.DirectionRequestResponse,
		MaxPayloadSize: 256 * 1024,
		RateLimit: comms.RateLimit{
			PerAgent: 30,
			Burst:    5,
			Window:   time.Minute,
		},
		NonceWindow: 10 * time.Minute,
		AuditLevel:  comms.AuditFull,
		Handler:     h.handleServiceCall,
	})
	e.Register(comms.TypeSpec{
		Name:           "x402.budget_status",
		Direction:      comms.DirectionRequestResponse,
		MaxPayloadSize: 1 * 1024,
		RateLimit: comms.RateLimit{
			PerAgent: 60,
			Burst:    10,
			Window:   time.Minute,
		},
		NonceWindow: 5 * time.Minute,
		AuditLevel:  comms.AuditHeaders,
		Handler:     h.handleBudgetStatus,
	})
	return e
}

// RegisterOnMux is a small helper that builds the endpoint and mounts
// it at EndpointPath on mux. Use this when you don't need the *Endpoint
// for any other purpose.
func RegisterOnMux(mux *http.ServeMux, backend Backend, resolver comms.IdentityResolver, opts ...comms.Option) {
	e := NewEndpoint(backend, resolver, opts...)
	mux.HandleFunc("GET "+EndpointPath, e.Handler())
}
