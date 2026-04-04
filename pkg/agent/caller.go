package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

// Caller makes JSON-RPC 2.0 calls to agent HTTP endpoints.
type Caller struct {
	client *http.Client
	nextID atomic.Int64
}

// NewCaller creates a caller with sensible defaults.
func NewCaller() *Caller {
	return &Caller{
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// rpcRequest is the JSON-RPC 2.0 request sent to agents.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      int64           `json:"id"`
}

// rpcResponse is the JSON-RPC 2.0 response from agents.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
	ID      int64           `json:"id"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Call sends a JSON-RPC request to the agent's endpoint and returns the
// raw result. The endpoint should be a full URL like
// "http://localhost:8200/rpc".
func (c *Caller) Call(ctx context.Context, endpoint, method string, params json.RawMessage) (json.RawMessage, error) {
	req := rpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		ID:      c.nextID.Add(1),
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("calling agent: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent returned HTTP %d", resp.StatusCode)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	var rpcResp rpcResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("agent error (%d): %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	return rpcResp.Result, nil
}

// Ping sends a health check to the agent endpoint. Returns nil if the
// agent responds successfully.
func (c *Caller) Ping(ctx context.Context, endpoint string) error {
	_, err := c.Call(ctx, endpoint, "ping", nil)
	return err
}
