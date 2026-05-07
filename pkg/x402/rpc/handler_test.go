package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/sky10/sky10/pkg/x402"
)

func sampleManifest() x402.ServiceManifest {
	return x402.ServiceManifest{
		ID:           "perplexity",
		DisplayName:  "Perplexity",
		Category:     "search",
		Description:  "AI-powered search.",
		Endpoint:     "https://api.perplexity.ai",
		Networks:     []x402.Network{x402.NetworkBase},
		MaxPriceUSDC: "0.005",
	}
}

func newHandler(t *testing.T) *Handler {
	t.Helper()
	r, err := x402.NewRegistry(x402.NewMemoryRegistryStore(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.AddManifest(sampleManifest()); err != nil {
		t.Fatal(err)
	}
	if err := r.SetPolicy(x402.PolicyEntry{
		ServiceID: "perplexity",
		Tier:      x402.TierPrimitive,
		DefaultOn: false,
		Hint:      "Use for current events",
	}); err != nil {
		t.Fatal(err)
	}
	return NewHandler(r, x402.NewBudget(nil, nil))
}

func TestDispatchOnlyHandlesX402Methods(t *testing.T) {
	t.Parallel()
	h := newHandler(t)
	_, _, handled := h.Dispatch(context.Background(), "wallet.status", nil)
	if handled {
		t.Fatal("handler should not claim non-x402.* methods")
	}
}

func TestListServicesReturnsCatalog(t *testing.T) {
	t.Parallel()
	h := newHandler(t)
	result, err, handled := h.Dispatch(context.Background(), "x402.listServices", nil)
	if !handled {
		t.Fatal("expected handler to handle x402.listServices")
	}
	if err != nil {
		t.Fatal(err)
	}
	listing, ok := result.(ListServicesResult)
	if !ok {
		t.Fatalf("result type = %T, want ListServicesResult", result)
	}
	if len(listing.Services) != 1 {
		t.Fatalf("services = %d, want 1", len(listing.Services))
	}
	got := listing.Services[0]
	if got.ID != "perplexity" {
		t.Fatalf("ID = %q", got.ID)
	}
	if got.Tier != x402.TierPrimitive {
		t.Fatalf("Tier = %q, want primitive (from overlay)", got.Tier)
	}
	if got.Hint == "" {
		t.Fatal("Hint should be populated from overlay")
	}
	if got.Endpoint != "https://api.perplexity.ai" {
		t.Fatalf("Endpoint = %q, want https://api.perplexity.ai", got.Endpoint)
	}
	if got.ServiceURL != "https://perplexity.ai" {
		t.Fatalf("ServiceURL = %q, want https://perplexity.ai fallback", got.ServiceURL)
	}
	if len(got.Endpoints) != 1 || got.Endpoints[0].URL != "https://api.perplexity.ai" {
		t.Fatalf("Endpoints = %+v, want endpoint fallback", got.Endpoints)
	}
	if got.Enabled {
		t.Fatal("freshly-loaded service should be Enabled=false")
	}
}

func TestListServicesNormalizesServiceLinkDomain(t *testing.T) {
	t.Parallel()
	r, err := x402.NewRegistry(x402.NewMemoryRegistryStore(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.AddManifest(x402.ServiceManifest{
		ID:           "run402",
		DisplayName:  "Run402",
		Endpoint:     "https://api.run402.com",
		Networks:     []x402.Network{x402.NetworkBase},
		MaxPriceUSDC: "0.001",
	}); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(r, x402.NewBudget(nil, nil))
	res, _, _ := h.Dispatch(context.Background(), "x402.listServices", nil)
	listing := res.(ListServicesResult).Services[0]
	if listing.ServiceURL != "https://run402.com" {
		t.Fatalf("ServiceURL = %q, want https://run402.com", listing.ServiceURL)
	}
}

func TestSetEnabledRoundTrip(t *testing.T) {
	t.Parallel()
	h := newHandler(t)
	params, _ := json.Marshal(SetEnabledParams{ServiceID: "perplexity", Enabled: true})
	res, err, handled := h.Dispatch(context.Background(), "x402.setEnabled", params)
	if !handled || err != nil {
		t.Fatalf("setEnabled err = %v, handled = %v", err, handled)
	}
	got := res.(SetEnabledResult)
	if !got.Enabled || got.ServiceID != "perplexity" {
		t.Fatalf("result = %+v", got)
	}

	// listServices should reflect the toggle.
	res2, _, _ := h.Dispatch(context.Background(), "x402.listServices", nil)
	listing := res2.(ListServicesResult)
	if !listing.Services[0].Enabled {
		t.Fatal("listServices should reflect enable=true")
	}

	// Disable and verify.
	disable, _ := json.Marshal(SetEnabledParams{ServiceID: "perplexity", Enabled: false})
	if _, err, _ := h.Dispatch(context.Background(), "x402.setEnabled", disable); err != nil {
		t.Fatal(err)
	}
	res3, _, _ := h.Dispatch(context.Background(), "x402.listServices", nil)
	listing = res3.(ListServicesResult)
	if listing.Services[0].Enabled {
		t.Fatal("listServices should reflect enable=false after disable")
	}
}

func TestSetEnabledRejectsMissingServiceID(t *testing.T) {
	t.Parallel()
	h := newHandler(t)
	params, _ := json.Marshal(SetEnabledParams{Enabled: true})
	_, err, _ := h.Dispatch(context.Background(), "x402.setEnabled", params)
	if err == nil {
		t.Fatal("expected error for missing service_id")
	}
}

func TestSetEnabledRejectsUnknownService(t *testing.T) {
	t.Parallel()
	h := newHandler(t)
	params, _ := json.Marshal(SetEnabledParams{ServiceID: "ghost", Enabled: true})
	_, err, _ := h.Dispatch(context.Background(), "x402.setEnabled", params)
	if !errors.Is(err, x402.ErrServiceUnknown) {
		t.Fatalf("err = %v, want ErrServiceUnknown", err)
	}
}

func TestBudgetStatusEmptyWhenNoAgents(t *testing.T) {
	t.Parallel()
	h := newHandler(t)
	res, err, handled := h.Dispatch(context.Background(), "x402.budgetStatus", nil)
	if !handled || err != nil {
		t.Fatalf("err=%v handled=%v", err, handled)
	}
	got := res.(BudgetStatusResult)
	if got.Agents != 0 {
		t.Fatalf("Agents = %d, want 0", got.Agents)
	}
}

func TestBudgetStatusReportsAggregate(t *testing.T) {
	t.Parallel()
	r, _ := x402.NewRegistry(x402.NewMemoryRegistryStore(), nil)
	if err := r.AddManifest(sampleManifest()); err != nil {
		t.Fatal(err)
	}
	budget := x402.NewBudget(nil, nil)
	if err := budget.SetAgentBudget("A-1", x402.BudgetConfig{PerCallMaxUSDC: "0.10", DailyCapUSDC: "5.00"}); err != nil {
		t.Fatal(err)
	}
	if err := budget.Charge(x402.Receipt{AgentID: "A-1", ServiceID: "perplexity", AmountUSDC: "0.005", Network: x402.NetworkBase, Tx: "0xabc"}); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(r, budget)
	res, _, _ := h.Dispatch(context.Background(), "x402.budgetStatus", nil)
	got := res.(BudgetStatusResult)
	if got.Agents != 1 {
		t.Fatalf("Agents = %d, want 1", got.Agents)
	}
	if got.SpentTodayUSDC != "0.005" {
		t.Fatalf("SpentTodayUSDC = %q, want 0.005", got.SpentTodayUSDC)
	}
}

func TestReceiptsJoinsServiceName(t *testing.T) {
	t.Parallel()
	r, _ := x402.NewRegistry(x402.NewMemoryRegistryStore(), nil)
	if err := r.AddManifest(sampleManifest()); err != nil {
		t.Fatal(err)
	}
	budget := x402.NewBudget(nil, nil)
	_ = budget.SetAgentBudget("A-1", x402.BudgetConfig{PerCallMaxUSDC: "0.10", DailyCapUSDC: "5.00"})
	_ = budget.Charge(x402.Receipt{AgentID: "A-1", ServiceID: "perplexity", AmountUSDC: "0.005", Network: x402.NetworkBase, Tx: "0xtest"})

	h := NewHandler(r, budget)
	res, _, _ := h.Dispatch(context.Background(), "x402.receipts", json.RawMessage(`{}`))
	got := res.(ReceiptsResult)
	if len(got.Receipts) != 1 {
		t.Fatalf("receipts = %d, want 1", len(got.Receipts))
	}
	if got.Receipts[0].ServiceName != "Perplexity" {
		t.Fatalf("ServiceName = %q, want Perplexity (from manifest)", got.Receipts[0].ServiceName)
	}
	if got.Receipts[0].AmountUSDC != "0.005" {
		t.Fatalf("AmountUSDC = %q", got.Receipts[0].AmountUSDC)
	}
}

func TestListServicesIncludesApprovalDetails(t *testing.T) {
	t.Parallel()
	h := newHandler(t)
	if err := h.registry.SetUserEnabled("perplexity", "0.003"); err != nil {
		t.Fatal(err)
	}
	res, _, _ := h.Dispatch(context.Background(), "x402.listServices", nil)
	listing := res.(ListServicesResult).Services[0]
	if !listing.Enabled {
		t.Fatal("Enabled = false, want true after SetUserEnabled")
	}
	if listing.ApprovedMaxPriceUSDC != "0.003" {
		t.Fatalf("ApprovedMaxPriceUSDC = %q, want 0.003", listing.ApprovedMaxPriceUSDC)
	}
	if listing.ApprovedAt == "" {
		t.Fatal("ApprovedAt should be populated")
	}
}

func TestUnknownX402MethodReturnsError(t *testing.T) {
	t.Parallel()
	h := newHandler(t)
	_, err, handled := h.Dispatch(context.Background(), "x402.bogus", nil)
	if !handled {
		t.Fatal("x402.* handler should claim x402.bogus")
	}
	if err == nil {
		t.Fatal("expected error for unknown method")
	}
}
