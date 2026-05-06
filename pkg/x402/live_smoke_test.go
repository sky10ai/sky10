package x402

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	skywallet "github.com/sky10/sky10/pkg/wallet"
	"github.com/sky10/sky10/pkg/x402/siwx"
)

// liveSmokeCase describes one paid call against a live x402 service.
// Run via `X402_LIVE=1 go test -run TestX402Live -count=1 -v
// ./pkg/x402/`. Each case fires a real call (charges real USDC) and
// captures the full HTTP exchange — challenge, X-PAYMENT, retry,
// receipt — into testdata/<id>.json so structural tests can replay
// the wire bytes offline.
type liveSmokeCase struct {
	id           string // fixture file stem
	manifestID   string
	displayName  string
	endpointHost string
	path         string
	method       string
	body         []byte
	maxPriceUSDC string
	// networks restricts which network this call can settle on.
	// Defaults to Base when empty. Solana cases must list NetworkSolana
	// explicitly so PreferAndCheapest skips the Base entries servers
	// like Quicknode advertise alongside Solana.
	networks []Network
	// siwxDomain enables Sign-In-With-X authentication for services
	// like Venice that require a wallet-signed session header on
	// every request. Empty disables SIWX (the default).
	siwxDomain string
	// expectFreeOK marks a case where 200 is expected without any
	// payment exchange — Venice's /balance endpoint, for instance,
	// is SIWX-only and does not charge. Defaults to false (the
	// regular paid-call assertion model).
	expectFreeOK bool
}

func liveSmokeCases() []liveSmokeCase {
	return []liveSmokeCase{
		// --- v2 services ---
		{
			id:           "exa-contents",
			manifestID:   "exa-ai",
			displayName:  "Exa",
			endpointHost: "https://api.exa.ai",
			path:         "/contents",
			method:       "POST",
			body:         []byte(`{"urls":["https://example.com"],"text":true}`),
			maxPriceUSDC: "0.020",
		},
		{
			id:           "blockrun-polymarket-markets",
			manifestID:   "blockrun-ai",
			displayName:  "Blockrun",
			endpointHost: "https://blockrun.ai",
			path:         "/api/v1/pm/polymarket/markets",
			method:       "GET",
			maxPriceUSDC: "0.005",
		},
		{
			id:           "smartflow-health",
			manifestID:   "api-smartflowproai-com",
			displayName:  "Smartflow",
			endpointHost: "https://api.smartflowproai.com",
			path:         "/bazaar/health-check",
			method:       "GET",
			maxPriceUSDC: "0.005",
		},

		// --- v1 services (body-based challenge, integer-units-in-v1-field) ---
		// x402-browserbase requires estimatedMinutes >= 5 in the
		// request body and charges per minute, so this case spends
		// $0.010 per run rather than $0.001 like the others. Worth it
		// for the v1 fixture coverage. (We previously tried Heurist's
		// /check_job_status — also v1 — but its server rejects every
		// payment with "Failed to parse JSON", including OWS pay
		// request. Dropped.)
		{
			id:           "browserbase-session-create",
			manifestID:   "x402-browserbase-com",
			displayName:  "Browserbase x402",
			endpointHost: "https://x402.browserbase.com",
			path:         "/browser/session/create",
			method:       "POST",
			body:         []byte(`{"estimatedMinutes":5}`),
			maxPriceUSDC: "0.020",
		},

		// --- Solana mainnet (v2) ---
		// Alchemy's Solana RPC settles cleanly through our v0
		// versioned tx (compute-budget × 2, transfer-checked, memo).
		// Quicknode advertises the same wire shape and the same
		// SVM facilitator (BENrLoU…) but rejects with an opaque
		// "Unexpected error verifying payment" — service-specific
		// quirk we have not been able to debug without packet
		// captures from a known-working Solana x402 client. Keep
		// Alchemy as the smoke target; drop Quicknode for now.
		{
			id:           "alchemy-solana-mainnet",
			manifestID:   "x402-alchemy-com-solana",
			displayName:  "Alchemy Solana RPC",
			endpointHost: "https://x402.alchemy.com",
			path:         "/solana-mainnet/v2",
			method:       "POST",
			body:         []byte(`{"jsonrpc":"2.0","id":1,"method":"getSlot"}`),
			maxPriceUSDC: "0.005",
			networks:     []Network{NetworkSolana},
		},
		// Coingecko's onchain APIs use a third-party SVM facilitator
		// (D6ZhtNQ5nT9…), not the BENrLoU… Coinbase one used by
		// Alchemy/Quicknode. Useful for catching facilitator-specific
		// regressions. Costs $0.01 per call rather than $0.001.
		{
			id:           "coingecko-solana-trending-pools",
			manifestID:   "pro-api-coingecko-com-solana",
			displayName:  "Coingecko OnChain (Solana)",
			endpointHost: "https://pro-api.coingecko.com",
			path:         "/api/v3/x402/onchain/networks/solana/trending_pools",
			method:       "GET",
			maxPriceUSDC: "0.020",
			networks:     []Network{NetworkSolana},
		},

		// Messari exposes the same paid endpoint on both Base and
		// Solana mainnet (the catalog only lists the Base side; the
		// live 402 challenge advertises both). Using two manifest
		// IDs and the networks field to drive PreferAndCheapest to
		// the right tier per case. Each call costs $0.10 — pricier
		// than the others but worth it to cover Messari's distinct
		// SVM facilitator (Hc3sdEAs…) and confirm the same code path
		// settles cleanly across multiple operators.
		{
			id:           "messari-roi-base",
			manifestID:   "api-messari-io-base",
			displayName:  "Messari Metrics (Base)",
			endpointHost: "https://api.messari.io",
			path:         "/metrics/v2/assets/roi?slugs=bitcoin",
			method:       "GET",
			maxPriceUSDC: "0.150",
			networks:     []Network{NetworkBase},
		},
		{
			id:           "messari-roi-solana",
			manifestID:   "api-messari-io-solana",
			displayName:  "Messari Metrics (Solana)",
			endpointHost: "https://api.messari.io",
			path:         "/metrics/v2/assets/roi?slugs=bitcoin",
			method:       "GET",
			maxPriceUSDC: "0.150",
			networks:     []Network{NetworkSolana},
		},

		// --- SIWX-authenticated services ---
		// Venice's /x402/balance/{wallet} is gated on SIWX but
		// doesn't charge — perfect free smoke for the SIWX header
		// path. Backend.Call attaches X-Sign-In-With-X (built from
		// our wallet) and Venice returns the wallet's spendable
		// balance. Verifies the SIWE message construction and
		// EIP-191 signature reach Venice's verifier intact.
		{
			id:           "venice-balance",
			manifestID:   "venice-ai",
			displayName:  "Venice AI",
			endpointHost: "https://api.venice.ai",
			path:         "/api/v1/x402/balance/0xdD12DEcbea4bd0Bc414af635a3398f50FA291e45",
			method:       "GET",
			maxPriceUSDC: "0.005",
			networks:     []Network{NetworkBase},
			siwxDomain:   "api.venice.ai",
			expectFreeOK: true,
		},
	}
}

// TestX402LiveSmoke fires one paid call per case against a real x402
// service, captures the full HTTP exchange to testdata/<id>.json,
// and asserts that the response settled with a non-empty receipt.
//
//   - Gated on X402_LIVE=1 because it charges real USDC.
//   - Wallet is selected via X402_LIVE_WALLET (defaults to "default").
//   - Skip individual cases by setting X402_LIVE_ONLY=<id>.
//   - Captured fixtures are loaded by TestParseChallengeFromFixture
//     and TestParseReceiptFromFixture in fixtures_test.go.
func TestX402LiveSmoke(t *testing.T) {
	if os.Getenv("X402_LIVE") == "" {
		t.Skip("set X402_LIVE=1 to run; this charges real USDC")
	}

	walletName := os.Getenv("X402_LIVE_WALLET")
	if walletName == "" {
		walletName = "default"
	}
	only := os.Getenv("X402_LIVE_ONLY")

	client := skywallet.NewClient()
	if client == nil {
		t.Fatal("ows binary not found via skywallet.NewClient — install ows or set PATH")
	}
	signer := NewOWSSigner(client, walletName)
	if signer == nil {
		t.Fatal("NewOWSSigner returned nil — wallet client missing or empty wallet name")
	}

	for _, tc := range liveSmokeCases() {
		tc := tc
		if only != "" && only != tc.id {
			continue
		}
		t.Run(tc.id, func(t *testing.T) {
			runLiveCase(t, signer, tc)
		})
	}
}

func runLiveCase(t *testing.T, signer Signer, tc liveSmokeCase) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	registry, err := NewRegistry(nil, time.Now)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	networks := tc.networks
	if len(networks) == 0 {
		networks = []Network{NetworkBase}
	}
	manifest := ServiceManifest{
		ID:           tc.manifestID,
		DisplayName:  tc.displayName,
		Endpoint:     tc.endpointHost,
		Networks:     networks,
		MaxPriceUSDC: tc.maxPriceUSDC,
		UpdatedAt:    time.Now().UTC(),
		SIWXDomain:   tc.siwxDomain,
	}
	if err := registry.AddManifest(manifest); err != nil {
		t.Fatalf("AddManifest: %v", err)
	}
	if err := registry.SetUserEnabled(manifest.ID, tc.maxPriceUSDC); err != nil {
		t.Fatalf("SetUserEnabled: %v", err)
	}

	budget := NewBudget(time.Now, NewMemoryReceiptStore())
	if err := budget.SetAgentBudget("smoke-agent", BudgetConfig{
		PerCallMaxUSDC: tc.maxPriceUSDC,
		DailyCapUSDC:   "5.000",
	}); err != nil {
		t.Fatalf("SetAgentBudget: %v", err)
	}

	rt := newCapturingRoundTripper(http.DefaultTransport)
	transport := &Transport{
		HTTP:   &http.Client{Transport: rt, Timeout: 3 * time.Minute},
		Signer: signer,
	}

	backendOpts := BackendOptions{
		Registry:  registry,
		Transport: transport,
		Budget:    budget,
	}
	if tc.siwxDomain != "" {
		// Resolve the wallet's Base address — SIWX uses the
		// EVM-style checksummed pubkey as the message subject.
		owsSigner, ok := signer.(*OWSSigner)
		if !ok {
			t.Skipf("SIWX smoke needs an OWSSigner; got %T", signer)
		}
		addr, err := owsSigner.AddressForChain(ctx, owsSigner.WalletName, string(NetworkBase))
		if err != nil {
			t.Fatalf("resolve Base address for SIWX: %v", err)
		}
		backendOpts.SIWX = &SIWXContext{
			WalletAddress: addr,
			Signer:        siwx.NewOWSSigner(owsSigner.Client, owsSigner.WalletName),
		}
	}
	backend := NewBackend(backendOpts)

	headers := map[string]string{}
	if tc.method == "POST" {
		headers["Content-Type"] = "application/json"
	}
	t.Logf("%s %s", tc.method, tc.endpointHost+tc.path)

	result, err := backend.Call(ctx, CallParams{
		AgentID:      "smoke-agent",
		ServiceID:    manifest.ID,
		Path:         tc.path,
		Method:       tc.method,
		Headers:      headers,
		Body:         tc.body,
		MaxPriceUSDC: tc.maxPriceUSDC,
	})
	// Always write the fixture, even on call failure — partial captures
	// are useful when debugging a regression.
	if writeErr := writeLiveFixture(tc, rt); writeErr != nil {
		t.Logf("write fixture: %v", writeErr)
	}
	if err != nil {
		t.Fatalf("Backend.Call: %v", err)
	}

	if result.Status != 200 {
		t.Fatalf("status = %d, body=%s", result.Status, truncate(result.Body, 200))
	}
	// 200 status is itself proof of settlement (the service ran the
	// real work). receipt + tx hash are bonus, logged when present;
	// some services (Browserbase) don't echo a Payment-Response header
	// at all even though the on-chain transfer happened. Free SIWX
	// endpoints (Venice /balance) never produce a receipt.
	if result.Receipt != nil {
		t.Logf("receipt: tx=%s network=%s amount=%s",
			result.Receipt.Tx, result.Receipt.Network, result.Receipt.AmountUSDC)
	} else if !tc.expectFreeOK {
		t.Logf("receipt: <none — server did not echo a Payment-Response header>")
	}
	t.Logf("response body (first 200 bytes): %s", truncate(result.Body, 200))
}

// capturingRoundTripper records every request/response pair so the
// live smoke can serialize the full wire exchange to a fixture file.
type capturingRoundTripper struct {
	inner    http.RoundTripper
	captured []capturedExchange
}

type capturedExchange struct {
	RequestMethod   string      `json:"request_method"`
	RequestURL      string      `json:"request_url"`
	RequestHeaders  http.Header `json:"request_headers"`
	RequestBody     string      `json:"request_body,omitempty"`
	ResponseStatus  int         `json:"response_status"`
	ResponseHeaders http.Header `json:"response_headers"`
	ResponseBody    string      `json:"response_body,omitempty"`
}

func newCapturingRoundTripper(inner http.RoundTripper) *capturingRoundTripper {
	return &capturingRoundTripper{inner: inner}
}

func (c *capturingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := capturedExchange{
		RequestMethod:  req.Method,
		RequestURL:     req.URL.String(),
		RequestHeaders: req.Header.Clone(),
	}
	if req.Body != nil {
		body, err := io.ReadAll(req.Body)
		if err == nil {
			rec.RequestBody = string(body)
			req.Body = io.NopCloser(bytes.NewReader(body))
		}
	}
	resp, err := c.inner.RoundTrip(req)
	if err != nil {
		c.captured = append(c.captured, rec)
		return nil, err
	}
	body, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	rec.ResponseStatus = resp.StatusCode
	rec.ResponseHeaders = resp.Header.Clone()
	if readErr == nil {
		rec.ResponseBody = string(body)
	}
	c.captured = append(c.captured, rec)
	return resp, nil
}

// liveFixture is the on-disk shape of a captured live exchange. The
// challenge is the first response with status 402, the retry is the
// next request/response pair carrying the X-PAYMENT envelope.
type liveFixture struct {
	Service       string             `json:"service"`
	Method        string             `json:"method"`
	URL           string             `json:"url"`
	CapturedAtUTC string             `json:"captured_at_utc"`
	Exchanges     []capturedExchange `json:"exchanges"`
}

func writeLiveFixture(tc liveSmokeCase, rt *capturingRoundTripper) error {
	out := liveFixture{
		Service:       tc.manifestID,
		Method:        tc.method,
		URL:           tc.endpointHost + tc.path,
		CapturedAtUTC: time.Now().UTC().Format(time.RFC3339),
		Exchanges:     rt.captured,
	}
	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join("testdata", tc.id+".json")
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
