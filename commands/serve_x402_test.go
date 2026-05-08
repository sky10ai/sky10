package commands

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
	bridgex402 "github.com/sky10/sky10/pkg/sandbox/bridge/x402"
	"github.com/sky10/sky10/pkg/x402"
)

// fakeX402Server is a minimal x402-compliant v1 test server: 402 with
// the v1 challenge body on first hit, 200 with X-PAYMENT-RESPONSE on
// retry. Used to exercise the adapter end-to-end against a real
// pkg/x402.Backend. The v1 wire shape is hand-rolled here (rather
// than via x402's internal types) so the test fixture is explicit
// about what's being matched.
func fakeX402Server(t *testing.T) *httptest.Server {
	t.Helper()
	challenge, _ := json.Marshal(map[string]any{
		"x402Version": x402.X402ProtocolV1,
		"accepts": []map[string]any{{
			"scheme":            "exact",
			"network":           "base",
			"maxAmountRequired": "0.005",
			"payTo":             "0x000000000000000000000000000000000000beef",
			"asset":             "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
			"maxTimeoutSeconds": 60,
			"extra":             map[string]any{"name": "USD Coin", "version": "2"},
		}},
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-PAYMENT") == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPaymentRequired)
			_, _ = w.Write(challenge)
			return
		}
		w.Header().Set("X-PAYMENT-RESPONSE", `{"tx":"0xtest","network":"base","amount_usdc":"0.005"}`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"answer":"42"}`))
	}))
	return srv
}

func newTestAdapter(t *testing.T, srv *httptest.Server) *x402Adapter {
	t.Helper()
	clock := func() time.Time { return time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC) }
	registry, err := x402.NewRegistry(x402.NewMemoryRegistryStore(), clock)
	if err != nil {
		t.Fatal(err)
	}
	manifest := x402.ServiceManifest{
		ID: "perplexity", DisplayName: "Perplexity",
		Description: "Current events search",
		Endpoint:    srv.URL, Networks: []x402.Network{x402.NetworkBase},
		ServiceURL: "https://perplexity.ai",
		Endpoints: []x402.ServiceEndpoint{
			{URL: srv.URL + "/search", Method: "POST", Description: "Search", PriceUSDC: "0.005", Network: x402.NetworkBase},
		},
		MaxPriceUSDC: "0.005",
	}
	if err := registry.AddManifest(manifest); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetPolicy(x402.PolicyEntry{
		ServiceID: "perplexity", Tier: x402.TierPrimitive,
		DefaultOn: true, Hint: "use for current events",
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Approve("A-1", "perplexity", "0.005"); err != nil {
		t.Fatal(err)
	}

	budget := x402.NewBudget(clock, nil)
	transport := x402.NewTransport(x402.NewFakeSigner("0x0000000000000000000000000000000000000abc"))
	backend := x402.NewBackend(x402.BackendOptions{
		Registry: registry, Transport: transport, Budget: budget, Clock: clock,
	})
	return newX402Adapter(backend, budget, defaultX402BudgetConfig(), nil)
}

func TestAdapterListServicesTranslatesFields(t *testing.T) {
	t.Parallel()
	srv := fakeX402Server(t)
	defer srv.Close()
	a := newTestAdapter(t, srv)
	listings, err := a.ListServices(context.Background(), "A-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(listings) != 1 {
		t.Fatalf("len(listings) = %d, want 1", len(listings))
	}
	got := listings[0]
	if got.ID != "perplexity" {
		t.Fatalf("ID = %q", got.ID)
	}
	if got.DisplayName != "Perplexity" {
		t.Fatalf("DisplayName = %q", got.DisplayName)
	}
	if got.Tier != string(x402.TierPrimitive) {
		t.Fatalf("Tier = %q, want primitive", got.Tier)
	}
	if got.Hint == "" {
		t.Fatal("Hint missing — overlay metadata should propagate")
	}
	if got.Description != "Current events search" || got.ServiceURL != "https://perplexity.ai" {
		t.Fatalf("metadata = %+v, want manifest description and service URL", got)
	}
	if len(got.Endpoints) != 1 || got.Endpoints[0].URL == "" || got.Endpoints[0].Network != string(x402.NetworkBase) {
		t.Fatalf("endpoints = %+v, want manifest endpoint metadata", got.Endpoints)
	}
	if len(got.Networks) != 1 || got.Networks[0] != string(x402.NetworkBase) {
		t.Fatalf("networks = %+v, want base", got.Networks)
	}
}

func TestAdapterCallTranslatesRequestAndReceipt(t *testing.T) {
	t.Parallel()
	srv := fakeX402Server(t)
	defer srv.Close()
	a := newTestAdapter(t, srv)
	resp, err := a.Call(context.Background(), bridgex402.CallParams{
		AgentID:      "A-1",
		ServiceID:    "perplexity",
		Path:         "/search",
		Method:       "POST",
		Body:         json.RawMessage(`{"q":"hi"}`),
		MaxPriceUSDC: "0.005",
		PaymentNonce: "p1",
	})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("Status = %d, want 200", resp.Status)
	}
	if string(resp.Body) != `{"answer":"42"}` {
		t.Fatalf("Body = %q", resp.Body)
	}
	if resp.Receipt == nil {
		t.Fatal("Receipt missing")
	}
	if resp.Receipt.Tx != "0xtest" {
		t.Fatalf("Receipt.Tx = %q", resp.Receipt.Tx)
	}
	if resp.Receipt.Network != string(x402.NetworkBase) {
		t.Fatalf("Receipt.Network = %q", resp.Receipt.Network)
	}
	if resp.Receipt.AmountUSDC != "0.005" {
		t.Fatalf("Receipt.AmountUSDC = %q", resp.Receipt.AmountUSDC)
	}
	if _, err := time.Parse(time.RFC3339Nano, resp.Receipt.SettledAt); err != nil {
		t.Fatalf("SettledAt %q not RFC3339Nano: %v", resp.Receipt.SettledAt, err)
	}
}

func TestAdapterCommsWebSocketCallsThroughPaymentBackend(t *testing.T) {
	t.Parallel()
	srv := fakeX402Server(t)
	defer srv.Close()
	a := newTestAdapter(t, srv)

	mux := http.NewServeMux()
	bridgex402.RegisterOnMux(mux, a, func(*http.Request) (string, string, error) {
		return "A-1", "D-1", nil
	})
	wsServer := httptest.NewServer(mux)
	defer wsServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(wsServer.URL, "http") + bridgex402.EndpointPath
	conn, dialResp, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if dialResp != nil && dialResp.Body != nil {
		_ = dialResp.Body.Close()
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	if err := wsjson.Write(ctx, conn, map[string]any{
		"type":       "x402.list_services",
		"request_id": "list-1",
		"nonce":      "nonce-list-1",
		"payload":    map[string]any{},
	}); err != nil {
		t.Fatalf("write list_services: %v", err)
	}
	var listResp struct {
		RequestID string `json:"request_id"`
		Error     *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
		Payload struct {
			Services []bridgex402.ServiceListing `json:"services"`
		} `json:"payload"`
	}
	if err := wsjson.Read(ctx, conn, &listResp); err != nil {
		t.Fatalf("read list_services: %v", err)
	}
	if listResp.Error != nil {
		t.Fatalf("list_services error: %+v", listResp.Error)
	}
	if listResp.RequestID != "list-1" || len(listResp.Payload.Services) != 1 || listResp.Payload.Services[0].ID != "perplexity" {
		t.Fatalf("list response = %+v, want one perplexity service", listResp)
	}

	if err := wsjson.Write(ctx, conn, map[string]any{
		"type":       "x402.service_call",
		"request_id": "call-1",
		"nonce":      "nonce-call-1",
		"payload": map[string]any{
			"service_id":     "perplexity",
			"path":           "/search",
			"method":         "POST",
			"body":           json.RawMessage(`{"q":"hi"}`),
			"max_price_usdc": "0.005",
			"payment_nonce":  "payment-1",
		},
	}); err != nil {
		t.Fatalf("write service_call: %v", err)
	}
	var callResp struct {
		RequestID string `json:"request_id"`
		Error     *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
		Payload bridgex402.CallResult `json:"payload"`
	}
	if err := wsjson.Read(ctx, conn, &callResp); err != nil {
		t.Fatalf("read service_call: %v", err)
	}
	if callResp.Error != nil {
		t.Fatalf("service_call error: %+v", callResp.Error)
	}
	if callResp.RequestID != "call-1" {
		t.Fatalf("request_id = %q, want call-1", callResp.RequestID)
	}
	if callResp.Payload.Status != http.StatusOK || string(callResp.Payload.Body) != `{"answer":"42"}` {
		t.Fatalf("payload = %+v, want paid upstream response", callResp.Payload)
	}
	if callResp.Payload.Receipt == nil || callResp.Payload.Receipt.Tx != "0xtest" || callResp.Payload.Receipt.Network != string(x402.NetworkBase) {
		t.Fatalf("receipt = %+v, want base x402 payment receipt", callResp.Payload.Receipt)
	}
}

func TestAdapterBudgetStatusTranslatesPerService(t *testing.T) {
	t.Parallel()
	srv := fakeX402Server(t)
	defer srv.Close()
	a := newTestAdapter(t, srv)

	// Configure a per-service cap so the snapshot has something to
	// translate beyond the defaults.
	if err := a.budget.SetAgentBudget("A-1", x402.BudgetConfig{
		PerCallMaxUSDC: "0.10",
		DailyCapUSDC:   "5.00",
		PerService:     map[string]string{"perplexity": "1.00"},
	}); err != nil {
		t.Fatal(err)
	}
	a.enrolled["A-1"] = true

	if _, err := a.Call(context.Background(), bridgex402.CallParams{
		AgentID:      "A-1",
		ServiceID:    "perplexity",
		Path:         "/search",
		MaxPriceUSDC: "0.005",
		PaymentNonce: "p1",
	}); err != nil {
		t.Fatalf("Call: %v", err)
	}

	snap, err := a.BudgetStatus(context.Background(), "A-1")
	if err != nil {
		t.Fatal(err)
	}
	if snap.PerCallMaxUSDC != "0.1" {
		t.Fatalf("PerCallMaxUSDC = %q, want 0.1", snap.PerCallMaxUSDC)
	}
	if snap.SpentTodayUSDC != "0.005" {
		t.Fatalf("SpentTodayUSDC = %q, want 0.005", snap.SpentTodayUSDC)
	}
	if len(snap.PerService) != 1 || snap.PerService[0].ServiceID != "perplexity" {
		t.Fatalf("PerService = %+v", snap.PerService)
	}
}

func TestAdapterEnsureBudgetOnFirstSight(t *testing.T) {
	t.Parallel()
	srv := fakeX402Server(t)
	defer srv.Close()
	a := newTestAdapter(t, srv)
	// Agent has not been explicitly configured. The first Call
	// should auto-apply the default config and proceed.
	if _, err := a.Call(context.Background(), bridgex402.CallParams{
		AgentID:      "A-1",
		ServiceID:    "perplexity",
		Path:         "/search",
		MaxPriceUSDC: "0.005",
		PaymentNonce: "p1",
	}); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !a.enrolled["A-1"] {
		t.Fatal("expected agent to be marked enrolled after first call")
	}
}

func TestMeteredServicesEndpointBackendGuestModeRequiresBridge(t *testing.T) {
	t.Setenv(sandboxGuestEnv, "1")
	srv := fakeX402Server(t)
	defer srv.Close()
	local := newTestAdapter(t, srv)
	forwarder := bridgex402.NewForwardingBackend()

	backend := meteredServicesEndpointBackend(local, forwarder)
	if backend != forwarder {
		t.Fatalf("backend = %T, want guest forwarding backend", backend)
	}

	_, err := backend.ListServices(context.Background(), "A-1")
	var bridgeErr *bridge.Error
	if !errors.As(err, &bridgeErr) {
		t.Fatalf("ListServices err = %T %v, want *bridge.Error", err, err)
	}
	if bridgeErr.Code != bridgex402.ErrCodeHostBridgeDisconnected {
		t.Fatalf("bridge error code = %q, want %q", bridgeErr.Code, bridgex402.ErrCodeHostBridgeDisconnected)
	}
}

func TestMeteredServicesEndpointBackendHostModeUsesLocalFallback(t *testing.T) {
	t.Setenv(sandboxGuestEnv, "")
	srv := fakeX402Server(t)
	defer srv.Close()
	local := newTestAdapter(t, srv)
	forwarder := bridgex402.NewForwardingBackend()

	backend := meteredServicesEndpointBackend(local, forwarder)
	services, err := backend.ListServices(context.Background(), "A-1")
	if err != nil {
		t.Fatalf("ListServices: %v", err)
	}
	if len(services) != 1 || services[0].ID != "perplexity" {
		t.Fatalf("services = %+v, want local fallback", services)
	}
}
