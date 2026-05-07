package x402

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/sky10/sky10/pkg/sandbox/bridge"
)

func TestForwardingBackendDisconnected(t *testing.T) {
	t.Parallel()
	backend := NewForwardingBackend()
	_, err := backend.BudgetStatus(context.Background(), "A-guest")
	var bridgeErr *bridge.Error
	if !errors.As(err, &bridgeErr) {
		t.Fatalf("BudgetStatus err = %T %v, want *bridge.Error", err, err)
	}
	if bridgeErr.Code != ErrCodeHostBridgeDisconnected {
		t.Fatalf("bridge error code = %q, want %q", bridgeErr.Code, ErrCodeHostBridgeDisconnected)
	}
}

func TestBridgeHandlerUsesTrustedAgentID(t *testing.T) {
	t.Parallel()
	hostBackend := &fakeBackend{
		callResult: &CallResult{
			Status: 200,
			Body:   json.RawMessage(`{"ok":true}`),
		},
	}
	handler := NewBridgeHandler(hostBackend, "A-trusted")
	raw, err := handler(context.Background(), bridge.Request{
		Type: TypeServiceCall,
		Payload: json.RawMessage(`{
			"agent_id":"A-payload",
			"service_id":"travel.search",
			"path":"/search",
			"method":"POST",
			"max_price_usdc":"0.05",
			"payment_nonce":"p1"
		}`),
	})
	if err != nil {
		t.Fatalf("handler err = %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("handler returned empty payload")
	}
	if len(hostBackend.callCalls) != 1 {
		t.Fatalf("call count = %d, want 1", len(hostBackend.callCalls))
	}
	if got := hostBackend.callCalls[0].AgentID; got != "A-trusted" {
		t.Fatalf("Call AgentID = %q, want trusted host identity", got)
	}
}

func TestMeteredServicesBridgeEndToEnd(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	forwarder := NewForwardingBackend()
	hostBackend := &fakeBackend{
		listResult: []ServiceListing{{
			ID:          "travel.search",
			DisplayName: "Travel Search",
			Category:    "travel",
			Tier:        "primitive",
			PriceUSDC:   "0.01",
		}},
	}

	mux := http.NewServeMux()
	localEndpoint := NewEndpoint(forwarder, staticResolver("A-guest", "D-guest"))
	mux.HandleFunc("GET "+EndpointPath, HandlerWithHostBridge(localEndpoint.Handler(), forwarder))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + EndpointPath
	hostConn, hostResp, err := bridge.Dial(ctx, wsURL+"?"+BridgeRoleQuery+"="+BridgeRoleHost, NewBridgeHandler(hostBackend, "A-host"))
	if hostResp != nil && hostResp.Body != nil {
		_ = hostResp.Body.Close()
	}
	if err != nil {
		t.Fatalf("host bridge Dial: %v", err)
	}
	defer hostConn.Close(websocket.StatusNormalClosure, "")
	go func() { _ = hostConn.Run(ctx) }()

	waitForForwarderConnected(t, ctx, forwarder)

	localConn, localResp, err := websocket.Dial(ctx, wsURL, nil)
	if localResp != nil && localResp.Body != nil {
		_ = localResp.Body.Close()
	}
	if err != nil {
		t.Fatalf("local Dial: %v", err)
	}
	defer localConn.Close(websocket.StatusNormalClosure, "")

	if err := wsjson.Write(ctx, localConn, map[string]any{
		"type":       TypeListServices,
		"request_id": "r1",
		"nonce":      "n1",
		"payload":    map[string]any{},
	}); err != nil {
		t.Fatalf("local write: %v", err)
	}
	var resp struct {
		RequestID string          `json:"request_id"`
		Payload   json.RawMessage `json:"payload"`
		Error     *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := wsjson.Read(ctx, localConn, &resp); err != nil {
		t.Fatalf("local read: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected local error: %+v", resp.Error)
	}
	var result listServicesResult
	if err := json.Unmarshal(resp.Payload, &result); err != nil {
		t.Fatalf("unmarshal services: %v", err)
	}
	if len(result.Services) != 1 || result.Services[0].ID != "travel.search" {
		t.Fatalf("services = %+v, want travel.search", result.Services)
	}
	if len(hostBackend.listCalls) != 1 || hostBackend.listCalls[0] != "A-host" {
		t.Fatalf("host list calls = %+v, want trusted host agent", hostBackend.listCalls)
	}
}

func waitForForwarderConnected(t *testing.T, ctx context.Context, forwarder *ForwardingBackend) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if forwarder.Connected() {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for host bridge attachment: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}
