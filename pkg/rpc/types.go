// Package rpc provides a generic JSON-RPC 2.0 server over Unix sockets
// and HTTP. Handlers register for method namespaces and the server routes
// requests to the appropriate handler.
package rpc

import (
	"context"
	"encoding/json"
)

// Request is a JSON-RPC 2.0 request.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      interface{}     `json:"id"`
}

// Response is a JSON-RPC 2.0 response.
type Response struct {
	JSONRPC string      `json:"jsonrpc"`
	Result  interface{} `json:"result,omitempty"`
	Error   *Error      `json:"error,omitempty"`
	ID      interface{} `json:"id"`
}

// Error is a JSON-RPC 2.0 error.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Event is a server-push event sent to all connected subscribers.
type Event struct {
	Name string      `json:"event"`
	Data interface{} `json:"data"`
}

// Handler dispatches RPC methods. The third return value indicates
// whether the handler recognized the method. If false, the server
// tries the next handler.
type Handler interface {
	Dispatch(ctx context.Context, method string, params json.RawMessage) (result interface{}, err error, handled bool)
}
