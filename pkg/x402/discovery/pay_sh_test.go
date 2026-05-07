package discovery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/x402"
)

func TestPaySHSourceFetchConvertsProviders(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/catalog" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"version": 2,
			"generated_at": "2026-05-05T13:42:43Z",
			"providers": [
				{
					"fqn": "paysponge/perplexity",
					"title": "Perplexity AI API",
					"description": "Search the web with citations.",
					"category": "ai_ml",
					"service_url": "https://pplx.x402.paysponge.com/",
					"max_price_usd": 0.01
				},
				{
					"fqn": "solana-foundation/google/texttospeech",
					"title": "Cloud Text-to-Speech API",
					"description": "Generate speech.",
					"category": "ai_ml",
					"service_url": "https://pay.sh/api/gateway/google/texttospeech",
					"max_price_usd": 0.00003
				},
				{
					"fqn": "missing-url",
					"title": "Missing URL",
					"max_price_usd": 1
				}
			]
		}`))
	}))
	defer srv.Close()

	source := NewPaySHSource(srv.URL+"/api/catalog", srv.Client())
	got, err := source.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("manifests = %d, want 2", len(got))
	}
	first := got[0]
	if first.ID != "pay.sh/paysponge/perplexity" || first.Endpoint != "https://pplx.x402.paysponge.com" {
		t.Fatalf("first manifest = %+v", first)
	}
	if first.MaxPriceUSDC != "0.01" || first.DisplayName != "Perplexity AI API" || first.Category != "ai_ml" {
		t.Fatalf("first metadata = %+v", first)
	}
	if !networksEqual(first.Networks, []x402.Network{x402.NetworkBase, x402.NetworkSolana}) {
		t.Fatalf("networks = %+v", first.Networks)
	}
	if first.UpdatedAt != time.Date(2026, 5, 5, 13, 42, 43, 0, time.UTC) {
		t.Fatalf("UpdatedAt = %s", first.UpdatedAt)
	}
	if got[1].MaxPriceUSDC != "0.00003" {
		t.Fatalf("second max price = %q", got[1].MaxPriceUSDC)
	}
}

func TestFormatPaySHPrice(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"0":         "0",
		"0.0100":    "0.01",
		"0001.50":   "1.5",
		"3e-5":      "0.00003",
		"1e-7":      "0.000001",
		"0.000001":  "0.000001",
		"0.0000019": "0.000002",
	}
	for input, want := range tests {
		if got := formatPaySHPrice(inputJSONNumber(input)); got != want {
			t.Fatalf("formatPaySHPrice(%q) = %q, want %q", input, got, want)
		}
	}
}

func inputJSONNumber(value string) json.Number {
	return json.Number(value)
}
