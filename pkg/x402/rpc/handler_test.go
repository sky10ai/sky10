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
	return NewHandler(r)
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
	if got.Enabled {
		t.Fatal("freshly-loaded service should be Enabled=false")
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
