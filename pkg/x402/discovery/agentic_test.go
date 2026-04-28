package discovery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sky10/sky10/pkg/x402"
)

const fakeAgenticPayload = `{
  "services": [
    {
      "id": "exa",
      "name": "Exa",
      "description": "AI-powered web search",
      "category": "Search",
      "networks": ["Base"],
      "endpoints": [
        {"url":"https://api.exa.ai/search","pricing":{"amount":"0.007","currency":"USDC","network":"Base"},"method":"POST"},
        {"url":"https://api.exa.ai/contents","pricing":{"amount":"0.001","currency":"USDC","network":"Base"},"method":"POST"}
      ]
    },
    {
      "id": "anthropic",
      "name": "Anthropic",
      "description": "Claude models",
      "category": "Inference",
      "networks": ["Base", "Solana", "Polygon"],
      "endpoints": [
        {"url":"https://api.venice.ai/api/v1/chat/completions","pricing":{"amount":"0.001","currency":"USDC","network":"Base"},"method":"POST"},
        {"url":"https://llm.bankr.bot/v1/messages","pricing":{"amount":"","currency":"USDC","network":"Base"},"method":"POST"}
      ]
    },
    {
      "id": "broken",
      "name": "Broken",
      "description": "no endpoints",
      "endpoints": []
    },
    {
      "id": "unparseable",
      "name": "Unparseable",
      "endpoints": [
        {"url":"::: not a url"}
      ]
    }
  ]
}`

func TestAgenticMarketSourceFetchHappyPath(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/services" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fakeAgenticPayload))
	}))
	defer srv.Close()

	src := NewAgenticMarketSource(srv.URL, srv.Client())
	got, err := src.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2 (broken + unparseable should be dropped)", len(got))
	}

	exa := findManifest(got, "exa")
	if exa.Endpoint != "https://api.exa.ai" {
		t.Fatalf("exa endpoint = %q, want https://api.exa.ai", exa.Endpoint)
	}
	if exa.MaxPriceUSDC != "0.007" {
		t.Fatalf("exa max price = %q, want 0.007 (the larger of 0.001/0.007)", exa.MaxPriceUSDC)
	}
	if len(exa.Networks) != 1 || exa.Networks[0] != x402.NetworkBase {
		t.Fatalf("exa networks = %+v, want [base]", exa.Networks)
	}

	anthropic := findManifest(got, "anthropic")
	if want := []x402.Network{x402.NetworkBase, x402.NetworkSolana}; !networksEqual(anthropic.Networks, want) {
		t.Fatalf("anthropic networks = %+v, want %+v (Polygon dropped)", anthropic.Networks, want)
	}
	if anthropic.MaxPriceUSDC != "0.001" {
		t.Fatalf("anthropic max price = %q, want 0.001 (the only quoted endpoint)", anthropic.MaxPriceUSDC)
	}
}

func TestAgenticMarketSourceHandlesHTTPError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	src := NewAgenticMarketSource(srv.URL, srv.Client())
	_, err := src.Fetch(context.Background())
	if err == nil {
		t.Fatal("expected HTTP error from Fetch")
	}
}

func TestAgenticMarketSourceCancellable(t *testing.T) {
	t.Parallel()
	src := NewAgenticMarketSource("https://api.agentic.market", &http.Client{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := src.Fetch(ctx); err == nil {
		t.Fatal("expected cancelled-context error")
	}
}

func TestNewAgenticMarketSourceUsesDefaults(t *testing.T) {
	t.Parallel()
	src := NewAgenticMarketSource("", nil)
	if src.baseURL != DefaultAgenticMarketBaseURL {
		t.Fatalf("baseURL = %q, want default %q", src.baseURL, DefaultAgenticMarketBaseURL)
	}
	if src.Name() != "agentic.market" {
		t.Fatalf("Name = %q, want agentic.market", src.Name())
	}
	if src.client.Timeout == 0 {
		t.Fatal("default client should have a non-zero timeout")
	}
}

func findManifest(in []x402.ServiceManifest, id string) x402.ServiceManifest {
	for _, m := range in {
		if m.ID == id {
			return m
		}
	}
	return x402.ServiceManifest{}
}

func networksEqual(a, b []x402.Network) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
