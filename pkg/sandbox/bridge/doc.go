// Package bridge provides reusable WebSocket request/response plumbing for
// sandbox capability bridges.
//
// A bridge connection is intentionally generic only at the transport level:
// it moves typed JSON frames, tracks pending calls, dispatches inbound
// requests to one handler, and returns structured errors. Capability routing
// stays outside this package. Mount one WebSocket endpoint per capability,
// such as /bridge/metered-services/ws, instead of creating a generic RPC
// tunnel or multiplexing unrelated capabilities over one socket.
package bridge
