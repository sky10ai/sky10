// Package comms provides the shared transport plumbing for per-intent
// websocket endpoints that carry cross-trust-boundary operations between
// sandboxed agents and the host daemon.
//
// Each capability that wants to expose operations to sandboxed agents
// builds its own endpoint on top of this package. The capability:
//
//   - imports comms,
//   - constructs an Endpoint with NewEndpoint,
//   - registers one TypeSpec per envelope it accepts via Register,
//   - registers the endpoint's HTTP handler on the daemon's mux at
//     /comms/<capability>/ws.
//
// The plumbing handles connection lifecycle, identity injection, replay
// protection, audit logging, and per-(agent, type) rate limiting. It does
// not handle dispatch logic beyond a small switch on envelope type
// scoped to one endpoint's registered set; per-intent endpoints are the
// only multiplexer in this design.
//
// See docs/work/current/sandbox-comms/ for the full architecture and
// docs/learned/sandbox-rpc-isolation.md for the historical rationale.
package comms
