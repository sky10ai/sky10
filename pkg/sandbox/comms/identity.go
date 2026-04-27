package comms

import (
	"errors"
	"net/http"
)

// IdentityResolver extracts the trusted agent and device identity for a
// new connection from the HTTP request that opened it. Endpoints
// configure this when they're constructed; the plumbing calls it once
// per accepted websocket connection, before any envelope is read.
//
// The resolver should consult whatever authentication primitive the
// embedding daemon already uses for cross-trust-boundary connections —
// e.g. a session token bound to an agent identity, or a libp2p peer
// identity for cross-device skylink streams. The resolver returns the
// trusted agent_id and device_id; the plumbing stamps these on every
// envelope received over that connection.
//
// A non-nil error rejects the connection with HTTP 401.
//
// IdentityResolver intentionally takes only *http.Request, not the
// upgraded websocket. Identity is established at upgrade time; per-
// envelope identity claims from the wire are explicitly forbidden by
// design.
type IdentityResolver func(r *http.Request) (agentID, deviceID string, err error)

// ErrUnauthenticated is the canonical error to return from an
// IdentityResolver when the request does not carry a valid identity.
// The plumbing maps it to a 401 response.
var ErrUnauthenticated = errors.New("comms: unauthenticated connection")
