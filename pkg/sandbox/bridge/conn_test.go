package bridge

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestConnCallRoundTrip(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := Accept(w, r, func(_ context.Context, req Request) (json.RawMessage, error) {
			if req.Type != "echo" {
				t.Errorf("request type = %q, want echo", req.Type)
			}
			return req.Payload, nil
		})
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		_ = conn.Run(r.Context())
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := Dial(ctx, "ws"+srv.URL[len("http"):], nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	go func() { _ = conn.Run(ctx) }()

	raw, err := conn.Call(ctx, "echo", map[string]string{"hello": "world"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got["hello"] != "world" {
		t.Fatalf("response = %#v, want hello=world", got)
	}
}

func TestConnKeepaliveClosesUnresponsivePeer(t *testing.T) {
	t.Parallel()

	done := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := Accept(w, r, nil,
			WithKeepaliveInterval(10*time.Millisecond),
			WithKeepaliveTimeout(20*time.Millisecond),
		)
		if err != nil {
			done <- err
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		done <- conn.runKeepalive(r.Context())
	}))
	defer srv.Close()

	rawConn := dialRawWebSocket(t, srv.URL)
	defer rawConn.Close()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("runKeepalive returned nil, want keepalive failure")
		}
	case <-time.After(time.Second):
		t.Fatal("runKeepalive did not return after keepalive timeout")
	}
}

func dialRawWebSocket(t *testing.T, rawURL string) net.Conn {
	t.Helper()

	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	conn, err := net.DialTimeout("tcp", parsed.Host, time.Second)
	if err != nil {
		t.Fatalf("dial raw websocket: %v", err)
	}

	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	if _, err := fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", parsed.Host, key); err != nil {
		conn.Close()
		t.Fatalf("write websocket handshake: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		conn.Close()
		t.Fatalf("read websocket handshake response: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		conn.Close()
		t.Fatalf("handshake status = %d, want %d", resp.StatusCode, http.StatusSwitchingProtocols)
	}
	return conn
}

func TestConnReturnsHandlerError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := Accept(w, r, func(context.Context, Request) (json.RawMessage, error) {
			return nil, HandlerError("denied", "not allowed")
		})
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		_ = conn.Run(r.Context())
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := Dial(ctx, "ws"+srv.URL[len("http"):], nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	go func() { _ = conn.Run(ctx) }()

	_, err = conn.Call(ctx, "metered.call", map[string]string{"service_id": "x"})
	var bridgeErr *Error
	if !errors.As(err, &bridgeErr) {
		t.Fatalf("Call err = %T %v, want *Error", err, err)
	}
	if bridgeErr.Code != "denied" || bridgeErr.Message != "not allowed" {
		t.Fatalf("bridge error = %+v, want denied/not allowed", bridgeErr)
	}
}

func TestConnStreamRoundTrip(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := AcceptStream(w, r, func(_ context.Context, req Request, send StreamSender) (json.RawMessage, error) {
			if req.Type != "stream.echo" {
				t.Errorf("request type = %q, want stream.echo", req.Type)
			}
			if err := send(json.RawMessage(`{"delta":"hel"}`)); err != nil {
				return nil, err
			}
			if err := send(json.RawMessage(`{"delta":"lo"}`)); err != nil {
				return nil, err
			}
			return json.RawMessage(`{"done":true}`), nil
		})
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		_ = conn.Run(r.Context())
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := Dial(ctx, "ws"+srv.URL[len("http"):], nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	go func() { _ = conn.Run(ctx) }()

	var got string
	raw, err := conn.Stream(ctx, "stream.echo", nil, func(payload json.RawMessage) error {
		var chunk struct {
			Delta string `json:"delta"`
		}
		if err := json.Unmarshal(payload, &chunk); err != nil {
			return err
		}
		got += chunk.Delta
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if got != "hello" {
		t.Fatalf("streamed = %q, want hello", got)
	}
	var done struct {
		Done bool `json:"done"`
	}
	if err := json.Unmarshal(raw, &done); err != nil {
		t.Fatalf("decode terminal payload: %v", err)
	}
	if !done.Done {
		t.Fatalf("terminal payload = %s", raw)
	}
}

func TestConnCallContextCancelRemovesPending(t *testing.T) {
	t.Parallel()
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := Accept(w, r, func(context.Context, Request) (json.RawMessage, error) {
			<-block
			return json.RawMessage(`{"ok":true}`), nil
		})
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		_ = conn.Run(r.Context())
	}))
	defer srv.Close()
	defer close(block)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := Dial(ctx, "ws"+srv.URL[len("http"):], nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	go func() { _ = conn.Run(ctx) }()

	callCtx, callCancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer callCancel()
	if _, err := conn.Call(callCtx, "slow", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Call err = %v, want deadline exceeded", err)
	}

	conn.mu.Lock()
	pending := len(conn.pending)
	conn.mu.Unlock()
	if pending != 0 {
		t.Fatalf("pending calls = %d, want 0", pending)
	}
}

func TestConnCloseFailsPendingCalls(t *testing.T) {
	t.Parallel()
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := Accept(w, r, func(context.Context, Request) (json.RawMessage, error) {
			<-block
			return json.RawMessage(`{"ok":true}`), nil
		})
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		_ = conn.Run(r.Context())
	}))
	defer srv.Close()
	defer close(block)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := Dial(ctx, "ws"+srv.URL[len("http"):], nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	go func() { _ = conn.Run(ctx) }()

	errCh := make(chan error, 1)
	go func() {
		_, err := conn.Call(ctx, "slow", nil)
		errCh <- err
	}()

	waitForPendingCalls(t, ctx, conn, 1)
	if err := conn.Close(websocket.StatusNormalClosure, "test close"); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-errCh:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("Call err = %v, want ErrClosed", err)
		}
	case <-ctx.Done():
		t.Fatalf("timed out waiting for pending call failure: %v", ctx.Err())
	}
}

func waitForPendingCalls(t *testing.T, ctx context.Context, conn *Conn, want int) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		conn.mu.Lock()
		got := len(conn.pending)
		conn.mu.Unlock()
		if got == want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("pending calls did not reach %d: %v", want, ctx.Err())
		case <-ticker.C:
		}
	}
}
