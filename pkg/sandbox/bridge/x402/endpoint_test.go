package x402

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/sky10/sky10/pkg/sandbox/bridge"
)

func staticResolver(agentID, deviceID string) bridge.IdentityResolver {
	return func(*http.Request) (string, string, error) {
		return agentID, deviceID, nil
	}
}

func TestNewEndpointRequiresBackend(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for nil backend")
		}
	}()
	NewEndpoint(nil, staticResolver("A-1", "D-1"))
}

func TestRegisterOnMuxIntegration(t *testing.T) {
	t.Parallel()
	backend := &fakeBackend{
		listResult: []ServiceListing{
			{ID: "perplexity", DisplayName: "Perplexity", Tier: "primitive", PriceUSDC: "0.005"},
		},
	}
	mux := http.NewServeMux()
	RegisterOnMux(mux, backend, staticResolver("A-trusted", "D-1"))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	url := "ws" + strings.TrimPrefix(srv.URL, "http") + EndpointPath
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, dialResp, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial err = %v", err)
	}
	if dialResp != nil && dialResp.Body != nil {
		_ = dialResp.Body.Close()
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	if err := wsjson.Write(ctx, conn, map[string]any{
		"type":       "x402.list_services",
		"request_id": "r1",
		"nonce":      "n1",
		"payload":    map[string]any{},
	}); err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Type      string          `json:"type"`
		RequestID string          `json:"request_id"`
		Payload   json.RawMessage `json:"payload"`
		Error     *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := wsjson.Read(ctx, conn, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	if resp.RequestID != "r1" {
		t.Fatalf("response request_id = %q, want r1", resp.RequestID)
	}
	var listing listServicesResult
	if err := json.Unmarshal(resp.Payload, &listing); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if len(listing.Services) != 1 || listing.Services[0].ID != "perplexity" {
		t.Fatalf("services = %+v, want [perplexity]", listing.Services)
	}
	if backend.listCalls[0] != "A-trusted" {
		t.Fatalf("backend got agentID %q, want A-trusted (bus-stamped)", backend.listCalls[0])
	}
}

func TestRegisterOnMuxDoesNotMountOldCommsPaths(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	RegisterOnMux(mux, &fakeBackend{}, staticResolver("A-trusted", "D-1"))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	for _, path := range []string{"/comms/x402/ws", "/comms/metered-services/ws"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404", path, resp.StatusCode)
		}
	}
}

func TestEndpointRoutesServiceCallToBackend(t *testing.T) {
	t.Parallel()
	backend := &fakeBackend{
		callResult: &CallResult{
			Status: 200,
			Body:   json.RawMessage(`{"answer":"42"}`),
			Receipt: &Receipt{
				Tx: "0xdeadbeef", Network: "base", AmountUSDC: "0.003",
			},
		},
	}
	mux := http.NewServeMux()
	RegisterOnMux(mux, backend, staticResolver("A-trusted", "D-1"))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	url := "ws" + strings.TrimPrefix(srv.URL, "http") + EndpointPath
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, dialResp, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if dialResp != nil && dialResp.Body != nil {
		_ = dialResp.Body.Close()
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	if err := wsjson.Write(ctx, conn, map[string]any{
		"type":       "x402.service_call",
		"request_id": "r1",
		"nonce":      "n1",
		"payload": map[string]any{
			"service_id":     "perplexity",
			"path":           "/search",
			"method":         "POST",
			"max_price_usdc": "0.005",
			"payment_nonce":  "p1",
		},
	}); err != nil {
		t.Fatal(err)
	}
	var resp struct {
		RequestID string          `json:"request_id"`
		Payload   json.RawMessage `json:"payload"`
		Error     *struct {
			Code string `json:"code"`
		} `json:"error,omitempty"`
	}
	if err := wsjson.Read(ctx, conn, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	var got CallResult
	if err := json.Unmarshal(resp.Payload, &got); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if got.Status != 200 || got.Receipt == nil || got.Receipt.Tx != "0xdeadbeef" {
		t.Fatalf("response = %+v, want status 200 with receipt", got)
	}
	call := backend.callCalls[0]
	if call.AgentID != "A-trusted" {
		t.Fatalf("backend got AgentID %q, want A-trusted", call.AgentID)
	}
	if call.PaymentNonce != "p1" {
		t.Fatalf("backend got PaymentNonce %q, want p1", call.PaymentNonce)
	}
}

func TestEndpointRejectsServiceCallValidationFailure(t *testing.T) {
	t.Parallel()
	backend := &fakeBackend{}
	mux := http.NewServeMux()
	RegisterOnMux(mux, backend, staticResolver("A-trusted", "D-1"))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	url := "ws" + strings.TrimPrefix(srv.URL, "http") + EndpointPath
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, dialResp, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if dialResp != nil && dialResp.Body != nil {
		_ = dialResp.Body.Close()
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	if err := wsjson.Write(ctx, conn, map[string]any{
		"type":       "x402.service_call",
		"request_id": "r1",
		"nonce":      "n1",
		"payload":    map[string]any{}, // missing every required field
	}); err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := wsjson.Read(ctx, conn, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil {
		t.Fatal("expected error response")
	}
	if resp.Error.Code != bridge.ErrCodeHandlerError {
		t.Fatalf("error code = %q, want handler_error", resp.Error.Code)
	}
	if len(backend.callCalls) != 0 {
		t.Fatalf("backend Call should not have been invoked on validation failure (got %d calls)", len(backend.callCalls))
	}
}
