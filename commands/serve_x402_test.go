package commands

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	commsx402 "github.com/sky10/sky10/pkg/sandbox/comms/x402"
	"github.com/sky10/sky10/pkg/x402"
)

// fakeX402Server is a minimal x402-compliant test server: 402 with
// challenge on first hit, 200 with X-PAYMENT-RESPONSE on retry. Used
// to exercise the adapter end-to-end against a real pkg/x402.Backend.
func fakeX402Server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-PAYMENT") == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPaymentRequired)
			_ = json.NewEncoder(w).Encode(x402.PaymentChallenge{
				X402Version: x402.X402ProtocolVersion,
				Accepts: []x402.PaymentRequirements{
					{
						Scheme:            "exact",
						Network:           "base",
						MaxAmountRequired: "0.005",
						PayTo:             "0x000000000000000000000000000000000000beef",
						Asset:             "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
						MaxTimeoutSeconds: 60,
						Extra: map[string]interface{}{
							"name":    "USD Coin",
							"version": "2",
						},
					},
				},
			})
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
		Endpoint: srv.URL, Networks: []x402.Network{x402.NetworkBase},
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
}

func TestAdapterCallTranslatesRequestAndReceipt(t *testing.T) {
	t.Parallel()
	srv := fakeX402Server(t)
	defer srv.Close()
	a := newTestAdapter(t, srv)
	resp, err := a.Call(context.Background(), commsx402.CallParams{
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

	if _, err := a.Call(context.Background(), commsx402.CallParams{
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
	if _, err := a.Call(context.Background(), commsx402.CallParams{
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
