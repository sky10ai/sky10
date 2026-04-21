package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

const jsonRPCVersion = "2.0"

// Request is one outbound JSON-RPC request on the adapter transport.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      uint64          `json:"id"`
}

// Response is one inbound JSON-RPC response on the adapter transport.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *ResponseError  `json:"error,omitempty"`
	ID      uint64          `json:"id"`
}

// ResponseError is a JSON-RPC error object with optional structured data.
type ResponseError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *ResponseError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("json-rpc error %d: %s", e.Code, e.Message)
}

type notification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// NotificationHandler receives unsolicited adapter notifications.
type NotificationHandler func(method string, params json.RawMessage)

// Client is a framed JSON-RPC client over stdio-like streams.
type Client struct {
	enc *Encoder
	dec *Decoder

	notify NotificationHandler

	nextID  atomic.Uint64
	done    chan struct{}
	closers []io.Closer

	mu      sync.Mutex
	pending map[uint64]chan Response
	loopErr error
}

// NewClient creates a client and starts its read loop.
func NewClient(reader io.Reader, writer io.Writer, notify NotificationHandler) *Client {
	c := &Client{
		enc:     NewEncoder(writer),
		dec:     NewDecoder(reader),
		notify:  notify,
		done:    make(chan struct{}),
		pending: make(map[uint64]chan Response),
	}
	if closer, ok := reader.(io.Closer); ok {
		c.closers = append(c.closers, closer)
	}
	if closer, ok := writer.(io.Closer); ok {
		c.closers = append(c.closers, closer)
	}
	go c.readLoop()
	return c
}

// Done closes when the read loop exits.
func (c *Client) Done() <-chan struct{} {
	return c.done
}

// Err returns the terminal read-loop error, if any.
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loopErr
}

// Close closes any underlying closers and waits for the read loop to finish.
func (c *Client) Close() error {
	var closeErr error
	for _, closer := range c.closers {
		if err := closer.Close(); err != nil && !errors.Is(err, io.EOF) {
			closeErr = err
		}
	}
	<-c.done
	if closeErr != nil {
		return closeErr
	}
	return c.Err()
}

// Call sends one request and waits for a typed result.
func (c *Client) Call(ctx context.Context, method string, params any, out any) error {
	id := c.nextID.Add(1)
	req := Request{
		JSONRPC: jsonRPCVersion,
		Method:  method,
		ID:      id,
	}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("marshal params for %s: %w", method, err)
		}
		req.Params = raw
	}

	respCh := make(chan Response, 1)
	c.mu.Lock()
	if c.loopErr != nil {
		err := c.loopErr
		c.mu.Unlock()
		return err
	}
	c.pending[id] = respCh
	c.mu.Unlock()

	if err := c.enc.Write(req); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}

	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return ctx.Err()
	case <-c.done:
		return c.Err()
	case resp, ok := <-respCh:
		if !ok {
			return c.Err()
		}
		if resp.Error != nil {
			return resp.Error
		}
		if out == nil || len(resp.Result) == 0 {
			return nil
		}
		if err := json.Unmarshal(resp.Result, out); err != nil {
			return fmt.Errorf("unmarshal result for %s: %w", method, err)
		}
		return nil
	}
}

func (c *Client) readLoop() {
	defer close(c.done)
	for {
		body, err := c.dec.ReadMessage()
		if err != nil {
			c.failPending(err)
			return
		}
		if len(body) == 0 {
			continue
		}

		var probe struct {
			Method string `json:"method"`
		}
		if err := json.Unmarshal(body, &probe); err != nil {
			c.failPending(fmt.Errorf("decode rpc envelope: %w", err))
			return
		}
		if probe.Method != "" {
			var n notification
			if err := json.Unmarshal(body, &n); err != nil {
				c.failPending(fmt.Errorf("decode notification: %w", err))
				return
			}
			if c.notify != nil {
				c.notify(n.Method, n.Params)
			}
			continue
		}

		var resp Response
		if err := json.Unmarshal(body, &resp); err != nil {
			c.failPending(fmt.Errorf("decode response: %w", err))
			return
		}

		c.mu.Lock()
		respCh, ok := c.pending[resp.ID]
		if ok {
			delete(c.pending, resp.ID)
		}
		c.mu.Unlock()
		if ok {
			respCh <- resp
		}
	}
}

func (c *Client) failPending(err error) {
	c.mu.Lock()
	c.loopErr = err
	pending := c.pending
	c.pending = make(map[uint64]chan Response)
	c.mu.Unlock()
	for _, respCh := range pending {
		close(respCh)
	}
}
