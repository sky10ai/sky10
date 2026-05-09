package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/google/uuid"
)

var ErrClosed = errors.New("bridge: connection closed")

// Request is an inbound capability-local request frame.
type Request struct {
	ID      string
	Type    string
	Payload json.RawMessage
}

// Handler handles one inbound bridge request.
type Handler func(context.Context, Request) (json.RawMessage, error)

// StreamSender emits one response chunk for the currently handled request.
type StreamSender func(json.RawMessage) error

// StreamHandler handles one inbound bridge request and may emit response chunks
// before returning the terminal response payload.
type StreamHandler func(context.Context, Request, StreamSender) (json.RawMessage, error)

type responseResult struct {
	frame Frame
	err   error
}

// Conn wraps a WebSocket with request/response bridge semantics.
type Conn struct {
	ws      *websocket.Conn
	handler StreamHandler
	opts    options

	writeMu sync.Mutex

	mu      sync.Mutex
	pending map[string]chan responseResult
	closed  bool
}

// NewConn wraps an already-upgraded WebSocket.
func NewConn(ws *websocket.Conn, handler Handler, opts ...Option) *Conn {
	var streamHandler StreamHandler
	if handler != nil {
		streamHandler = func(ctx context.Context, req Request, _ StreamSender) (json.RawMessage, error) {
			return handler(ctx, req)
		}
	}
	return NewStreamConn(ws, streamHandler, opts...)
}

// NewStreamConn wraps an already-upgraded WebSocket with a handler that can
// emit stream frames while processing a request.
func NewStreamConn(ws *websocket.Conn, handler StreamHandler, opts ...Option) *Conn {
	cfg := newOptions(opts...)
	if cfg.maxFrameSize > 0 {
		ws.SetReadLimit(cfg.maxFrameSize)
	}
	return &Conn{
		ws:      ws,
		handler: handler,
		opts:    cfg,
		pending: make(map[string]chan responseResult),
	}
}

// Accept upgrades an HTTP request and returns a bridge connection. The caller
// owns Run and Close.
func Accept(w http.ResponseWriter, r *http.Request, handler Handler, opts ...Option) (*Conn, error) {
	var streamHandler StreamHandler
	if handler != nil {
		streamHandler = func(ctx context.Context, req Request, _ StreamSender) (json.RawMessage, error) {
			return handler(ctx, req)
		}
	}
	return AcceptStream(w, r, streamHandler, opts...)
}

// AcceptStream upgrades an HTTP request and returns a bridge connection with a
// stream-capable request handler. The caller owns Run and Close.
func AcceptStream(w http.ResponseWriter, r *http.Request, handler StreamHandler, opts ...Option) (*Conn, error) {
	cfg := newOptions(opts...)
	ws, err := websocket.Accept(w, r, cfg.acceptOptions)
	if err != nil {
		return nil, err
	}
	return NewStreamConn(ws, handler, opts...), nil
}

// Dial opens a bridge WebSocket. The caller owns Run and Close.
func Dial(ctx context.Context, url string, handler Handler, opts ...Option) (*Conn, *http.Response, error) {
	var streamHandler StreamHandler
	if handler != nil {
		streamHandler = func(ctx context.Context, req Request, _ StreamSender) (json.RawMessage, error) {
			return handler(ctx, req)
		}
	}
	return DialStream(ctx, url, streamHandler, opts...)
}

// DialStream opens a bridge WebSocket with a stream-capable request handler.
// The caller owns Run and Close.
func DialStream(ctx context.Context, url string, handler StreamHandler, opts ...Option) (*Conn, *http.Response, error) {
	cfg := newOptions(opts...)
	ws, resp, err := websocket.Dial(ctx, url, cfg.dialOptions)
	if err != nil {
		return nil, resp, err
	}
	return NewStreamConn(ws, handler, opts...), resp, nil
}

// Run reads frames until the context is cancelled or the WebSocket closes.
// It must run while Call is in use so responses can be delivered.
func (c *Conn) Run(ctx context.Context) error {
	if c == nil || c.ws == nil {
		return ErrClosed
	}
	for {
		var frame Frame
		if err := wsjson.Read(ctx, c.ws, &frame); err != nil {
			c.failPending(err)
			return err
		}
		if err := validateInboundFrame(frame); err != nil {
			c.failPending(err)
			return err
		}
		switch frame.Kind {
		case KindResponse:
			c.deliver(frame, true)
		case KindStream:
			c.deliver(frame, false)
		case KindRequest:
			go c.handle(ctx, frame)
		}
	}
}

// Call sends one request and waits for its response.
func (c *Conn) Call(ctx context.Context, typ string, payload any) (json.RawMessage, error) {
	if c == nil || c.ws == nil {
		return nil, ErrClosed
	}
	if err := validateOutboundRequest(typ); err != nil {
		return nil, err
	}
	raw, err := marshalPayload(payload)
	if err != nil {
		return nil, fmt.Errorf("bridge: marshal payload: %w", err)
	}
	id := uuid.NewString()
	ch := make(chan responseResult, 1)
	if err := c.addPending(id, ch); err != nil {
		return nil, err
	}
	frame := Frame{
		Kind:    KindRequest,
		ID:      id,
		Type:    typ,
		Payload: raw,
	}
	if err := c.write(ctx, frame); err != nil {
		c.removePending(id)
		return nil, err
	}
	select {
	case result := <-ch:
		if result.err != nil {
			return nil, result.err
		}
		if result.frame.Kind == KindStream {
			c.removePending(id)
			return nil, fmt.Errorf("bridge: unexpected stream frame for request %s", id)
		}
		if result.frame.Error != nil {
			return nil, result.frame.Error
		}
		return result.frame.Payload, nil
	case <-ctx.Done():
		c.removePending(id)
		return nil, ctx.Err()
	}
}

// Stream sends one request, passes each stream frame to onChunk, and returns
// the terminal response payload.
func (c *Conn) Stream(ctx context.Context, typ string, payload any, onChunk func(json.RawMessage) error) (json.RawMessage, error) {
	if c == nil || c.ws == nil {
		return nil, ErrClosed
	}
	if err := validateOutboundRequest(typ); err != nil {
		return nil, err
	}
	raw, err := marshalPayload(payload)
	if err != nil {
		return nil, fmt.Errorf("bridge: marshal payload: %w", err)
	}
	id := uuid.NewString()
	ch := make(chan responseResult, 8)
	if err := c.addPending(id, ch); err != nil {
		return nil, err
	}
	frame := Frame{
		Kind:    KindRequest,
		ID:      id,
		Type:    typ,
		Payload: raw,
	}
	if err := c.write(ctx, frame); err != nil {
		c.removePending(id)
		return nil, err
	}
	for {
		select {
		case result := <-ch:
			if result.err != nil {
				return nil, result.err
			}
			if result.frame.Error != nil {
				c.removePending(id)
				return nil, result.frame.Error
			}
			switch result.frame.Kind {
			case KindStream:
				if onChunk != nil {
					if err := onChunk(result.frame.Payload); err != nil {
						c.removePending(id)
						return nil, err
					}
				}
			case KindResponse:
				return result.frame.Payload, nil
			default:
				c.removePending(id)
				return nil, fmt.Errorf("bridge: unexpected frame kind %q", result.frame.Kind)
			}
		case <-ctx.Done():
			c.removePending(id)
			return nil, ctx.Err()
		}
	}
}

// Close closes the underlying WebSocket and fails pending calls.
func (c *Conn) Close(status websocket.StatusCode, reason string) error {
	if c == nil || c.ws == nil {
		return nil
	}
	c.failPending(ErrClosed)
	return c.ws.Close(status, reason)
}

func (c *Conn) handle(ctx context.Context, frame Frame) {
	if c.handler == nil {
		_ = c.write(ctx, errorFrame(frame.ID, frame.Type, bridgeError("not_handled", "bridge request handler is not configured")))
		return
	}
	send := func(payload json.RawMessage) error {
		return c.write(ctx, Frame{
			Kind:    KindStream,
			ID:      frame.ID,
			Type:    frame.Type,
			Payload: payload,
		})
	}
	payload, err := c.handler(ctx, Request{
		ID:      frame.ID,
		Type:    frame.Type,
		Payload: frame.Payload,
	}, send)
	if err != nil {
		_ = c.write(ctx, errorFrame(frame.ID, frame.Type, err))
		return
	}
	_ = c.write(ctx, Frame{
		Kind:    KindResponse,
		ID:      frame.ID,
		Type:    frame.Type,
		Payload: payload,
	})
}

func (c *Conn) write(ctx context.Context, frame Frame) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return wsjson.Write(ctx, c.ws, frame)
}

func (c *Conn) addPending(id string, ch chan responseResult) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrClosed
	}
	c.pending[id] = ch
	return nil
}

func (c *Conn) removePending(id string) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *Conn) deliver(frame Frame, terminal bool) {
	c.mu.Lock()
	ch, ok := c.pending[frame.ID]
	if ok && terminal {
		delete(c.pending, frame.ID)
	}
	c.mu.Unlock()
	if ok {
		ch <- responseResult{frame: frame}
	}
}

func (c *Conn) failPending(err error) {
	if err == nil {
		err = ErrClosed
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	pending := c.pending
	c.pending = make(map[string]chan responseResult)
	c.mu.Unlock()

	for _, ch := range pending {
		ch <- responseResult{err: err}
	}
}
