package x402

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func backendOnFake(t *testing.T) (*Backend, *x402TestServer, func()) {
	t.Helper()
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	fake := newX402TestServer(json.RawMessage(`{"answer":"42"}`))
	srv := httptest.NewServer(fake)

	registry, err := NewRegistry(NewMemoryRegistryStore(), clock)
	if err != nil {
		t.Fatal(err)
	}
	manifest := ServiceManifest{
		ID:           "perplexity",
		DisplayName:  "Perplexity",
		Endpoint:     srv.URL,
		Networks:     []Network{NetworkBase},
		MaxPriceUSDC: "0.005",
	}
	if err := registry.AddManifest(manifest); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetPolicy(PolicyEntry{
		ServiceID: "perplexity",
		Tier:      TierPrimitive,
		DefaultOn: false,
		Hint:      "use for current events",
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Approve("A-1", "perplexity", "0.005"); err != nil {
		t.Fatal(err)
	}

	budget := NewBudget(clock, nil)
	if err := budget.SetAgentBudget("A-1", BudgetConfig{
		PerCallMaxUSDC: "0.10",
		DailyCapUSDC:   "5.00",
		PerService: map[string]string{
			"perplexity": "1.00",
		},
	}); err != nil {
		t.Fatal(err)
	}

	tx := NewTransport(NewFakeSigner("0x0000000000000000000000000000000000000abc"))
	backend := NewBackend(BackendOptions{
		Registry:  registry,
		Transport: tx,
		Budget:    budget,
		Clock:     clock,
	})
	cleanup := func() { srv.Close() }
	return backend, fake, cleanup
}

func TestBackendCallEndToEnd(t *testing.T) {
	t.Parallel()
	backend, fake, cleanup := backendOnFake(t)
	defer cleanup()

	resp, err := backend.Call(context.Background(), CallParams{
		AgentID:      "A-1",
		ServiceID:    "perplexity",
		Path:         "/search",
		Method:       "POST",
		Body:         []byte(`{"q":"hi"}`),
		MaxPriceUSDC: "0.005",
		PaymentNonce: "p1",
	})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("status = %d, want 200", resp.Status)
	}
	if resp.Receipt == nil {
		t.Fatal("expected receipt")
	}
	if resp.Receipt.Tx != "0xdeadbeef" {
		t.Fatalf("receipt tx = %q, want 0xdeadbeef", resp.Receipt.Tx)
	}
	if resp.Receipt.AgentID != "A-1" {
		t.Fatalf("receipt agent = %q, want A-1", resp.Receipt.AgentID)
	}
	if got := fake.calls.Load(); got != 2 {
		t.Fatalf("server saw %d calls, want 2", got)
	}
}

func TestBackendListAndStatusReflectCalls(t *testing.T) {
	t.Parallel()
	backend, _, cleanup := backendOnFake(t)
	defer cleanup()

	listed, err := backend.ListServices(context.Background(), "A-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != "perplexity" || listed[0].Tier != TierPrimitive {
		t.Fatalf("listing = %+v", listed)
	}
	if listed[0].Hint == "" {
		t.Fatal("hint should come from overlay")
	}

	if _, err := backend.Call(context.Background(), CallParams{
		AgentID: "A-1", ServiceID: "perplexity", Path: "/x",
		MaxPriceUSDC: "0.005", PaymentNonce: "p1",
	}); err != nil {
		t.Fatal(err)
	}
	snap, err := backend.BudgetStatus(context.Background(), "A-1")
	if err != nil {
		t.Fatal(err)
	}
	if snap.SpentTodayUSDC == "0" {
		t.Fatal("status should reflect call spend")
	}
}

func TestBackendRejectsUnapprovedService(t *testing.T) {
	t.Parallel()
	backend, _, cleanup := backendOnFake(t)
	defer cleanup()
	_, err := backend.Call(context.Background(), CallParams{
		AgentID:      "A-other",
		ServiceID:    "perplexity",
		Path:         "/x",
		MaxPriceUSDC: "0.005",
		PaymentNonce: "p1",
	})
	if !errors.Is(err, ErrServiceNotApproved) {
		t.Fatalf("err = %v, want ErrServiceNotApproved", err)
	}
}

func TestBackendRejectsBudgetExceeded(t *testing.T) {
	t.Parallel()
	backend, _, cleanup := backendOnFake(t)
	defer cleanup()
	// max_price_usdc below the manifest's quote.
	_, err := backend.Call(context.Background(), CallParams{
		AgentID:      "A-1",
		ServiceID:    "perplexity",
		Path:         "/x",
		MaxPriceUSDC: "0.001",
		PaymentNonce: "p1",
	})
	if !errors.Is(err, ErrPriceQuoteTooHigh) {
		t.Fatalf("err = %v, want ErrPriceQuoteTooHigh", err)
	}
}

func TestBackendRejectsPinMismatch(t *testing.T) {
	t.Parallel()
	backend, _, cleanup := backendOnFake(t)
	defer cleanup()
	// Mutate the manifest after approval to simulate an upstream
	// price hike. Backend should fail closed on the next Call.
	manifest, err := backend.registry.Manifest("perplexity")
	if err != nil {
		t.Fatal(err)
	}
	manifest.MaxPriceUSDC = "0.500"
	if err := backend.registry.AddManifest(manifest); err != nil {
		t.Fatal(err)
	}
	_, err = backend.Call(context.Background(), CallParams{
		AgentID:      "A-1",
		ServiceID:    "perplexity",
		Path:         "/x",
		MaxPriceUSDC: "0.500",
		PaymentNonce: "p1",
	})
	if !errors.Is(err, ErrPinMismatch) {
		t.Fatalf("err = %v, want ErrPinMismatch", err)
	}
}

func TestBackendRejectsAbsolutePathInRequest(t *testing.T) {
	t.Parallel()
	backend, _, cleanup := backendOnFake(t)
	defer cleanup()
	_, err := backend.Call(context.Background(), CallParams{
		AgentID:      "A-1",
		ServiceID:    "perplexity",
		Path:         "https://evil.example/exfil",
		MaxPriceUSDC: "0.005",
		PaymentNonce: "p1",
	})
	if err == nil || !strings.Contains(err.Error(), "must be relative") {
		t.Fatalf("err = %v, want absolute-path rejection", err)
	}
}
