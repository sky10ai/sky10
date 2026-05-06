package x402

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// canonicalSampleRequirement returns a canonical PaymentRequirements
// most signing/typed-data tests use as a baseline. AmountMicros is
// 5000 = 0.005 USDC at 6 decimals.
func canonicalSampleRequirement() PaymentRequirements {
	return PaymentRequirements{
		Scheme:            "exact",
		Network:           "base",
		AmountMicros:      "5000",
		PayTo:             "0x000000000000000000000000000000000000beef",
		Asset:             "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
		MaxTimeoutSeconds: 60,
		Extra: map[string]interface{}{
			"name":    "USD Coin",
			"version": "2",
		},
	}
}

// --- v1 wire tests ----------------------------------------------------------

// TestParseChallengeV1Body demonstrates the v1 wire format and round-
// trips it into the canonical PaymentChallenge. v1 quotes amount as
// decimal USDC under `maxAmountRequired`.
func TestParseChallengeV1Body(t *testing.T) {
	t.Parallel()
	wire := []byte(`{
        "x402Version": 1,
        "accepts": [{
            "scheme": "exact",
            "network": "base",
            "maxAmountRequired": "0.005",
            "payTo": "0x000000000000000000000000000000000000beef",
            "asset": "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
            "maxTimeoutSeconds": 60,
            "extra": {"name": "USD Coin", "version": "2"}
        }]
    }`)
	c, err := parseChallengeV1Body(wire)
	if err != nil {
		t.Fatalf("parseChallengeV1Body: %v", err)
	}
	if c.Version != X402ProtocolV1 {
		t.Fatalf("Version = %d, want 1", c.Version)
	}
	if len(c.Accepts) != 1 {
		t.Fatalf("len(Accepts) = %d, want 1", len(c.Accepts))
	}
	got := c.Accepts[0]
	if got.AmountMicros != "5000" {
		t.Fatalf("AmountMicros = %q, want %q (0.005 USDC = 5000 micros)", got.AmountMicros, "5000")
	}
	if got.Scheme != "exact" || got.Network != "base" {
		t.Fatalf("scheme/network = %s/%s", got.Scheme, got.Network)
	}
	if got.PayTo != "0x000000000000000000000000000000000000beef" {
		t.Fatalf("PayTo = %q", got.PayTo)
	}
}

// TestEncodePaymentV1 demonstrates the v1 outgoing X-PAYMENT envelope:
// flat top-level scheme/network with a JSON-encoded ExactSchemePayload
// under `payload`. No `accepted` or `resource` fields.
func TestEncodePaymentV1(t *testing.T) {
	t.Parallel()
	req := canonicalSampleRequirement()
	exact := ExactSchemePayload{
		Signature: "0xabc",
		Authorization: EIP3009Authorization{
			From: "0xfrom", To: "0xto", Value: "5000",
			ValidAfter: "0", ValidBefore: "1", Nonce: "0xnonce",
		},
	}
	inner, _ := json.Marshal(exact)

	encoded, err := encodePaymentV1(req, inner)
	if err != nil {
		t.Fatalf("encodePaymentV1: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	var top map[string]any
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatalf("decode top: %v", err)
	}
	if top["x402Version"] != float64(X402ProtocolV1) {
		t.Fatalf("x402Version = %v, want %d", top["x402Version"], X402ProtocolV1)
	}
	if top["scheme"] != "exact" || top["network"] != "base" {
		t.Fatalf("scheme/network at top level: %v/%v", top["scheme"], top["network"])
	}
	if _, exists := top["accepted"]; exists {
		t.Fatalf("v1 envelope should NOT carry `accepted`; got %v", top["accepted"])
	}
	if _, exists := top["resource"]; exists {
		t.Fatalf("v1 envelope should NOT carry `resource`; got %v", top["resource"])
	}
	if _, ok := top["payload"]; !ok {
		t.Fatalf("v1 envelope missing payload")
	}
}

// TestParseReceiptPlainJSON exercises the v1-era plain-JSON receipt
// shape. parseReceipt is version-blind by design (some servers ship
// a v2-encoded base64 receipt under the v1 X-PAYMENT-RESPONSE
// header, so dispatching on the header name doesn't track the
// content reliably).
func TestParseReceiptPlainJSON(t *testing.T) {
	t.Parallel()
	r, err := parseReceipt(`{"tx":"0xdead","network":"base","amount_usdc":"0.005"}`)
	if err != nil {
		t.Fatal(err)
	}
	if r.Tx != "0xdead" || r.Network != NetworkBase || r.AmountUSDC != "0.005" {
		t.Fatalf("unexpected receipt: %+v", r)
	}
}

// --- v2 wire tests ----------------------------------------------------------

// TestParseChallengeV2Header demonstrates the v2 wire format. The
// challenge is delivered base64-encoded in a Payment-Required header,
// the amount is integer base units under `amount`, and a `resource`
// block at the top level describes the paid endpoint.
func TestParseChallengeV2Header(t *testing.T) {
	t.Parallel()
	inner := map[string]any{
		"x402Version": 2,
		"resource": map[string]any{
			"url":         "https://api.exa.ai/contents",
			"description": "Exa /contents",
			"mimeType":    "application/json",
		},
		"accepts": []map[string]any{{
			"scheme":            "exact",
			"network":           "eip155:8453",
			"amount":            "1000",
			"payTo":             "0xpayto",
			"asset":             "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
			"maxTimeoutSeconds": 60,
			"extra":             map[string]any{"name": "USD Coin", "version": "2"},
		}},
	}
	raw, _ := json.Marshal(inner)
	headerValue := base64.StdEncoding.EncodeToString(raw)

	c, err := parseChallengeV2Header(headerValue)
	if err != nil {
		t.Fatalf("parseChallengeV2Header: %v", err)
	}
	if c.Version != X402ProtocolV2 {
		t.Fatalf("Version = %d, want 2", c.Version)
	}
	if c.Resource == nil || c.Resource.URL != "https://api.exa.ai/contents" {
		t.Fatalf("Resource = %+v", c.Resource)
	}
	if len(c.Accepts) != 1 {
		t.Fatalf("len(Accepts) = %d, want 1", len(c.Accepts))
	}
	got := c.Accepts[0]
	if got.AmountMicros != "1000" {
		t.Fatalf("AmountMicros = %q, want 1000 (v2 amount passes through as base units)", got.AmountMicros)
	}
	if got.Network != "eip155:8453" {
		t.Fatalf("Network = %q, want eip155:8453 (CAIP-2 form preserved)", got.Network)
	}
}

// TestEncodePaymentV2 demonstrates the v2 outgoing X-PAYMENT envelope:
// no top-level scheme/network (those live inside `accepted`), with the
// resource block echoed back.
func TestEncodePaymentV2(t *testing.T) {
	t.Parallel()
	req := PaymentRequirements{
		Scheme:            "exact",
		Network:           "eip155:8453",
		AmountMicros:      "1000",
		PayTo:             "0xpayto",
		Asset:             "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
		MaxTimeoutSeconds: 60,
		Extra:             map[string]interface{}{"name": "USD Coin", "version": "2"},
	}
	resource := &Resource{
		URL:         "https://api.exa.ai/contents",
		Description: "Exa /contents",
		MimeType:    "application/json",
	}
	exact := ExactSchemePayload{
		Signature: "0xabc",
		Authorization: EIP3009Authorization{
			From: "0xfrom", To: "0xto", Value: "1000",
			ValidAfter: "0", ValidBefore: "1", Nonce: "0xnonce",
		},
	}
	inner, _ := json.Marshal(exact)

	encoded, err := encodePaymentV2(req, inner, resource)
	if err != nil {
		t.Fatalf("encodePaymentV2: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	var top map[string]any
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatalf("decode top: %v", err)
	}
	if top["x402Version"] != float64(X402ProtocolV2) {
		t.Fatalf("x402Version = %v, want %d", top["x402Version"], X402ProtocolV2)
	}
	if _, exists := top["scheme"]; exists {
		t.Fatalf("v2 envelope should NOT carry top-level `scheme`; got %v", top["scheme"])
	}
	if _, exists := top["network"]; exists {
		t.Fatalf("v2 envelope should NOT carry top-level `network`; got %v", top["network"])
	}
	accepted, ok := top["accepted"].(map[string]any)
	if !ok {
		t.Fatalf("v2 envelope missing `accepted` object: %v", top["accepted"])
	}
	if accepted["amount"] != "1000" {
		t.Fatalf("accepted.amount = %v, want \"1000\"", accepted["amount"])
	}
	if accepted["scheme"] != "exact" || accepted["network"] != "eip155:8453" {
		t.Fatalf("accepted scheme/network: %v/%v", accepted["scheme"], accepted["network"])
	}
	res, ok := top["resource"].(map[string]any)
	if !ok {
		t.Fatalf("v2 envelope missing `resource` object: %v", top["resource"])
	}
	if res["url"] != "https://api.exa.ai/contents" {
		t.Fatalf("resource.url = %v", res["url"])
	}
	if _, ok := top["payload"]; !ok {
		t.Fatalf("v2 envelope missing payload")
	}
}

// TestParseReceiptBase64Transaction exercises the v2-era encoding:
// base64-wrapped JSON with the tx hash under the `transaction` field
// instead of `tx`. parseReceipt is permissive about both axes.
func TestParseReceiptBase64Transaction(t *testing.T) {
	t.Parallel()
	body, _ := json.Marshal(map[string]any{
		"success":     true,
		"transaction": "0xdeadbeef",
		"network":     "eip155:8453",
		"payer":       "0xclient",
	})
	headerValue := base64.StdEncoding.EncodeToString(body)
	r, err := parseReceipt(headerValue)
	if err != nil {
		t.Fatalf("parseReceipt: %v", err)
	}
	if r.Tx != "0xdeadbeef" {
		t.Fatalf("Tx = %q, want 0xdeadbeef (parsed from `transaction` field)", r.Tx)
	}
	if r.Network != "eip155:8453" {
		t.Fatalf("Network = %q", r.Network)
	}
}

// --- shared / non-version-specific tests -----------------------------------

func TestSelectRequirementsPicksFirstSupported(t *testing.T) {
	t.Parallel()
	c := PaymentChallenge{
		Version: X402ProtocolV2,
		Accepts: []PaymentRequirements{
			{Scheme: "exact", Network: "polygon", AmountMicros: "5000", PayTo: "0xa", Asset: "0x"},
			canonicalSampleRequirement(),
		},
	}
	req, err := c.SelectRequirements()
	if err != nil {
		t.Fatalf("SelectRequirements: %v", err)
	}
	if req.Network != "base" {
		t.Fatalf("selected network = %q, want base", req.Network)
	}
}

func TestSelectRequirementsNoMatch(t *testing.T) {
	t.Parallel()
	c := PaymentChallenge{
		Version: X402ProtocolV1,
		Accepts: []PaymentRequirements{
			{Scheme: "lightning", Network: "btc", AmountMicros: "1", PayTo: "lnbc...", Asset: "btc"},
		},
	}
	if _, err := c.SelectRequirements(); !errors.Is(err, ErrNoCompatibleRequirements) {
		t.Fatalf("err = %v, want ErrNoCompatibleRequirements", err)
	}
}

func TestBuildTransferWithAuthorizationTypedData(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	td, auth, err := BuildTransferWithAuthorizationTypedData(
		canonicalSampleRequirement(),
		"0x0000000000000000000000000000000000000abc",
		"5000",
		"0x"+strings.Repeat("11", 32),
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if td.PrimaryType != "TransferWithAuthorization" {
		t.Fatalf("primary type = %q", td.PrimaryType)
	}
	if td.Domain.ChainID != 8453 {
		t.Fatalf("chainID = %d, want 8453", td.Domain.ChainID)
	}
	if td.Domain.Name != "USD Coin" || td.Domain.Version != "2" {
		t.Fatalf("domain name/version: %+v", td.Domain)
	}
	if auth.From != "0x0000000000000000000000000000000000000abc" || auth.Value != "5000" {
		t.Fatalf("auth: %+v", auth)
	}
	if auth.ValidAfter != "0" {
		t.Fatalf("validAfter = %q, want 0", auth.ValidAfter)
	}
	wantValidBefore := now.Add(60 * time.Second).Unix()
	if auth.ValidBefore != fmtInt(wantValidBefore) {
		t.Fatalf("validBefore = %q, want %d", auth.ValidBefore, wantValidBefore)
	}
}

func fmtInt(v int64) string { return formatInt64(v) }

func formatInt64(v int64) string {
	if v == 0 {
		return "0"
	}
	out := ""
	negative := v < 0
	if negative {
		v = -v
	}
	for v > 0 {
		d := byte('0' + (v % 10))
		out = string(d) + out
		v /= 10
	}
	if negative {
		out = "-" + out
	}
	return out
}
