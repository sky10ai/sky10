package x402

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	skywallet "github.com/sky10/sky10/pkg/wallet"
)

// TestX402LiveExaContents fires one real paid call against Exa's
// /contents endpoint on Base mainnet. It is gated on the X402_LIVE
// env var and requires:
//
//   - OWS installed and discoverable via skywallet.NewClient
//   - A funded Base USDC wallet under the name supplied by
//     X402_LIVE_WALLET (defaults to "default")
//
// The test is intentionally end-to-end: it constructs the Backend
// the way the production daemon does, points it at the live
// agentic.market service URL, and verifies a 200 + non-empty receipt
// comes back. Any failure here is a real defect in our protocol or
// signing implementation.
//
// Run with:
//
//	X402_LIVE=1 go test -run TestX402LiveExaContents \
//	  -count=1 -v ./pkg/x402/
func TestX402LiveExaContents(t *testing.T) {
	if os.Getenv("X402_LIVE") == "" {
		t.Skip("set X402_LIVE=1 to run; this charges real USDC")
	}

	walletName := os.Getenv("X402_LIVE_WALLET")
	if walletName == "" {
		walletName = "default"
	}

	client := skywallet.NewClient()
	if client == nil {
		t.Fatal("ows binary not found via skywallet.NewClient — install ows or set PATH")
	}
	signer := NewOWSSigner(client, walletName)
	if signer == nil {
		t.Fatal("NewOWSSigner returned nil — wallet client missing or empty wallet name")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	addr, err := signer.AddressForChain(ctx, walletName, string(NetworkBase))
	if err != nil {
		t.Fatalf("resolve Base address for wallet %q: %v", walletName, err)
	}
	t.Logf("wallet %q → Base address %s", walletName, addr)

	registry, err := NewRegistry(nil, time.Now)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	manifest := ServiceManifest{
		ID:           "exa-ai",
		DisplayName:  "Exa",
		Category:     "Search",
		Description:  "AI-powered web search + content retrieval",
		Endpoint:     "https://api.exa.ai",
		Networks:     []Network{NetworkBase},
		MaxPriceUSDC: "0.020",
		UpdatedAt:    time.Now().UTC(),
	}
	if err := registry.AddManifest(manifest); err != nil {
		t.Fatalf("AddManifest: %v", err)
	}
	if err := registry.SetUserEnabled(manifest.ID, "0.020"); err != nil {
		t.Fatalf("SetUserEnabled: %v", err)
	}

	budget := NewBudget(time.Now, NewMemoryReceiptStore())
	if err := budget.SetAgentBudget("smoke-agent", BudgetConfig{
		PerCallMaxUSDC: "0.050",
		DailyCapUSDC:   "1.000",
	}); err != nil {
		t.Fatalf("SetAgentBudget: %v", err)
	}

	transport := NewTransport(signer)
	backend := NewBackend(BackendOptions{
		Registry:  registry,
		Transport: transport,
		Budget:    budget,
	})

	body := []byte(`{"urls":["https://example.com"],"text":true}`)
	t.Logf("POST https://api.exa.ai/contents body=%s", body)

	result, err := backend.Call(ctx, CallParams{
		AgentID:      "smoke-agent",
		ServiceID:    manifest.ID,
		Path:         "/contents",
		Method:       "POST",
		Headers:      map[string]string{"Content-Type": "application/json"},
		Body:         body,
		MaxPriceUSDC: "0.020",
	})
	if err != nil {
		t.Fatalf("Backend.Call: %v", err)
	}

	if result.Status != 200 {
		t.Fatalf("status = %d, body=%s", result.Status, truncate(result.Body, 200))
	}
	if result.Receipt == nil {
		t.Fatal("receipt is nil — facilitator did not return Payment-Response")
	}
	t.Logf("receipt: tx=%s network=%s amount=%s",
		result.Receipt.Tx, result.Receipt.Network, result.Receipt.AmountUSDC)
	t.Logf("response body (first 200 bytes): %s", truncate(result.Body, 200))

	if strings.TrimSpace(result.Receipt.Tx) == "" {
		t.Fatal("receipt.Tx is empty — settlement may not have happened")
	}
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
