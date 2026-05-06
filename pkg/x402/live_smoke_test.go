package x402

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	skywallet "github.com/sky10/sky10/pkg/wallet"
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
}

func liveSmokeCases() []liveSmokeCase {
	return []liveSmokeCase{
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
	manifest := ServiceManifest{
		ID:           tc.manifestID,
		DisplayName:  tc.displayName,
		Endpoint:     tc.endpointHost,
		Networks:     []Network{NetworkBase},
		MaxPriceUSDC: tc.maxPriceUSDC,
		UpdatedAt:    time.Now().UTC(),
	}
	if err := registry.AddManifest(manifest); err != nil {
		t.Fatalf("AddManifest: %v", err)
	}
	if err := registry.SetUserEnabled(manifest.ID, tc.maxPriceUSDC); err != nil {
		t.Fatalf("SetUserEnabled: %v", err)
	}

	budget := NewBudget(time.Now, NewMemoryReceiptStore())
	if err := budget.SetAgentBudget("smoke-agent", BudgetConfig{
		PerCallMaxUSDC: "0.050",
		DailyCapUSDC:   "1.000",
	}); err != nil {
		t.Fatalf("SetAgentBudget: %v", err)
	}

	rt := newCapturingRoundTripper(http.DefaultTransport)
	transport := &Transport{
		HTTP:   &http.Client{Transport: rt, Timeout: 3 * time.Minute},
		Signer: signer,
	}
	backend := NewBackend(BackendOptions{
		Registry:  registry,
		Transport: transport,
		Budget:    budget,
	})

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
