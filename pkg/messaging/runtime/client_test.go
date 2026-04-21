package runtime

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"
)

func TestClientCall(t *testing.T) {
	t.Parallel()

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()

	client := NewClient(clientConn, clientConn, nil)
	defer client.Close()

	go func() {
		dec := NewDecoder(serverConn)
		enc := NewEncoder(serverConn)
		var req Request
		if err := dec.Read(&req); err != nil {
			t.Errorf("server decode request: %v", err)
			return
		}
		if req.Method != "messaging.adapter.health" {
			t.Errorf("method = %q, want messaging.adapter.health", req.Method)
			return
		}
		if err := enc.Write(Response{
			JSONRPC: jsonRPCVersion,
			ID:      req.ID,
			Result:  json.RawMessage(`{"ok":true}`),
		}); err != nil {
			t.Errorf("server encode response: %v", err)
		}
	}()

	var result struct {
		OK bool `json:"ok"`
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Call(ctx, "messaging.adapter.health", map[string]string{"connection_id": "slack/work"}, &result); err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if !result.OK {
		t.Fatal("result.OK = false, want true")
	}
}

func TestClientNotificationHandler(t *testing.T) {
	t.Parallel()

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()

	notified := make(chan string, 1)
	client := NewClient(clientConn, clientConn, func(method string, _ json.RawMessage) {
		notified <- method
	})
	defer client.Close()

	go func() {
		enc := NewEncoder(serverConn)
		_ = enc.Write(map[string]any{
			"jsonrpc": jsonRPCVersion,
			"method":  "messaging.adapter.event",
			"params": map[string]any{
				"type": "message_received",
			},
		})
		_ = serverConn.Close()
	}()

	select {
	case method := <-notified:
		if method != "messaging.adapter.event" {
			t.Fatalf("method = %q, want messaging.adapter.event", method)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for notification")
	}
	<-client.Done()
	if err := client.Err(); err != nil && err != io.EOF && !isClosedPipe(err) {
		t.Fatalf("client.Err() = %v, want EOF/closed pipe", err)
	}
}

func isClosedPipe(err error) bool {
	return err != nil && (err.Error() == "io: read/write on closed pipe" || err.Error() == "read pipe: i/o timeout")
}
