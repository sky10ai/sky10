package discovery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/sky10/sky10/pkg/x402"
)

func TestPaySHSourceFetchConvertsProviders(t *testing.T) {
	t.Parallel()
	fixture, err := os.ReadFile("testdata/pay-sh-catalog.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/catalog" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	source := NewPaySHSource(srv.URL+"/api/catalog", srv.Client())
	got, err := source.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("manifests = %d, want 5", len(got))
	}
	tts := findManifest(got, "pay.sh/solana-foundation/google/texttospeech")
	if tts.Endpoint != "https://texttospeech.google.gateway-402.com" || tts.ServiceURL != tts.Endpoint {
		t.Fatalf("tts manifest = %+v", tts)
	}
	if tts.MaxPriceUSDC != "0.00003" || tts.DisplayName != "Cloud Text-to-Speech API" || tts.Category != "ai_ml" {
		t.Fatalf("tts metadata = %+v", tts)
	}
	if len(tts.Endpoints) != 1 || tts.Endpoints[0].URL != tts.Endpoint || tts.Endpoints[0].PriceUSDC != "0.00003" {
		t.Fatalf("tts endpoints = %+v", tts.Endpoints)
	}
	if !networksEqual(tts.Networks, []x402.Network{x402.NetworkBase, x402.NetworkSolana}) {
		t.Fatalf("networks = %+v", tts.Networks)
	}
	if tts.UpdatedAt != time.Date(2026, 5, 7, 1, 23, 54, 0, time.UTC) {
		t.Fatalf("UpdatedAt = %s", tts.UpdatedAt)
	}
	agentmail := findManifest(got, "pay.sh/agentmail/email")
	if agentmail.MaxPriceUSDC != "10" {
		t.Fatalf("agentmail max price = %q", agentmail.MaxPriceUSDC)
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
