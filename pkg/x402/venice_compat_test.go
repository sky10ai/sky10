package x402

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Venice's API exposes two x402 paid endpoints today:
//
//   - POST /api/v1/x402/top-up — $5 USDC deposit-style flow that
//     omits the `scheme` field on its accepts entry and substitutes
//     a non-spec `protocol: "x402"` field.
//   - POST /api/v1/chat/completions — $10 USDC standard v2 challenge.
//
// Per-call costs are 5–10× the Coinbase reference budget, so we
// don't fire either endpoint live (a single top-up consumes the
// dev wallet's entire $5 funded balance). Instead we capture the
// raw 402 challenge JSON into testdata/venice-*-challenge.json
// and exercise our parser end-to-end on the static bytes.
//
// Run `make fixtures-venice` (or curl the endpoints by hand and
// dump the base64 of the Payment-Required header) to refresh.

const (
	veniceTopUpFixture = "testdata/challenges/venice-top-up.json"
	veniceChatFixture  = "testdata/challenges/venice-chat-completions.json"
)

// TestVeniceTopUpChallengeParsesWithoutScheme exercises Venice's
// non-spec top-up shape. The accepts entry has no `scheme` field;
// our parser must accept it (treating empty as the implicit
// "exact" default) so PreferAndCheapest doesn't reject the only
// available requirement.
func TestVeniceTopUpChallengeParsesWithoutScheme(t *testing.T) {
	t.Parallel()
	raw := mustReadFixture(t, veniceTopUpFixture)

	// Sanity-check the captured wire shape: scheme is genuinely
	// absent and `protocol` is present. If Venice's wire ever
	// gains a scheme field we want to know.
	var probe map[string]any
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatal(err)
	}
	first := probe["accepts"].([]any)[0].(map[string]any)
	if _, has := first["scheme"]; has {
		t.Fatalf("fixture has scheme field; rebase the test (Venice updated their wire shape)")
	}
	if first["protocol"] != "x402" {
		t.Fatalf("fixture missing the `protocol: \"x402\"` field that motivated the lenient parser; rebase the test")
	}

	headerValue := base64.StdEncoding.EncodeToString(raw)
	c, err := parseChallengeV2Header(headerValue)
	if err != nil {
		t.Fatalf("parseChallengeV2Header: %v", err)
	}
	if c.Version != X402ProtocolV2 {
		t.Fatalf("Version = %d, want %d", c.Version, X402ProtocolV2)
	}
	if len(c.Accepts) != 1 {
		t.Fatalf("len(Accepts) = %d, want 1", len(c.Accepts))
	}
	req, err := c.SelectRequirements()
	if err != nil {
		t.Fatalf("SelectRequirements: %v", err)
	}
	if req.Scheme != "" {
		t.Fatalf("Scheme = %q, want empty (Venice omits it)", req.Scheme)
	}
	if req.Network != "eip155:8453" {
		t.Fatalf("Network = %q, want eip155:8453", req.Network)
	}
	if req.AmountMicros != "5000000" {
		t.Fatalf("AmountMicros = %q, want 5000000 (= $5 USDC)", req.AmountMicros)
	}
	if !strings.HasPrefix(req.PayTo, "0x") {
		t.Fatalf("PayTo = %q, want 0x-prefixed", req.PayTo)
	}
}

// TestVeniceChatCompletionsChallengeParses checks Venice's
// standard-shape v2 challenge (the chat completions endpoint
// uses the spec-compliant scheme/extra fields). It guards against
// regressions in our spec-path parser by replaying a real-world
// capture rather than a hand-built fixture.
func TestVeniceChatCompletionsChallengeParses(t *testing.T) {
	t.Parallel()
	raw := mustReadFixture(t, veniceChatFixture)
	headerValue := base64.StdEncoding.EncodeToString(raw)
	c, err := parseChallengeV2Header(headerValue)
	if err != nil {
		t.Fatalf("parseChallengeV2Header: %v", err)
	}
	req, err := c.SelectRequirements()
	if err != nil {
		t.Fatalf("SelectRequirements: %v", err)
	}
	if req.Scheme != "exact" {
		t.Fatalf("Scheme = %q, want exact", req.Scheme)
	}
	if req.AmountMicros != "10000000" {
		t.Fatalf("AmountMicros = %q, want 10000000 (= $10 USDC)", req.AmountMicros)
	}
	if got, _ := req.Extra["name"].(string); got != "USD Coin" {
		t.Fatalf("Extra.name = %q, want USD Coin", got)
	}
}

func mustReadFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return raw
}
