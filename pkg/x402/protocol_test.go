package x402

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func sampleRequirement() PaymentRequirements {
	return PaymentRequirements{
		Scheme:            "exact",
		Network:           "base",
		MaxAmountRequired: "0.005",
		PayTo:             "0x000000000000000000000000000000000000beef",
		Asset:             "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
		MaxTimeoutSeconds: 60,
		Extra: map[string]interface{}{
			"name":    "USD Coin",
			"version": "2",
		},
	}
}

func TestPaymentPayloadEncodeRoundTrip(t *testing.T) {
	t.Parallel()
	original := PaymentPayload{
		X402Version: 1,
		Scheme:      "exact",
		Network:     "base",
		Payload:     json.RawMessage(`{"hello":"world"}`),
	}
	encoded, err := original.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	decoded, err := DecodePaymentPayload(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.X402Version != original.X402Version {
		t.Fatalf("version mismatch: %d vs %d", decoded.X402Version, original.X402Version)
	}
	if decoded.Scheme != original.Scheme || decoded.Network != original.Network {
		t.Fatalf("scheme/network mismatch")
	}
	if string(decoded.Payload) != string(original.Payload) {
		t.Fatalf("payload mismatch: %s vs %s", decoded.Payload, original.Payload)
	}
}

func TestSelectRequirementsPreferences(t *testing.T) {
	t.Parallel()
	c := PaymentChallenge{
		X402Version: 1,
		Accepts: []PaymentRequirements{
			{Scheme: "exact", Network: "polygon", MaxAmountRequired: "0.005", PayTo: "0xa", Asset: "0x"},
			sampleRequirement(),
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
		X402Version: 1,
		Accepts: []PaymentRequirements{
			{Scheme: "lightning", Network: "btc", MaxAmountRequired: "1", PayTo: "lnbc...", Asset: "btc"},
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
		sampleRequirement(),
		"0x0000000000000000000000000000000000000abc",
		"5000",
		"0x"+repeatHex("11", 32),
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
	if auth.From != "0x0000000000000000000000000000000000000abc" {
		t.Fatalf("from = %q", auth.From)
	}
	if auth.Value != "5000" {
		t.Fatalf("value = %q", auth.Value)
	}
	if auth.ValidAfter != "0" {
		t.Fatalf("validAfter = %q, want 0", auth.ValidAfter)
	}
	wantValidBefore := now.Add(60 * time.Second).Unix()
	if auth.ValidBefore != intToStr(wantValidBefore) {
		t.Fatalf("validBefore = %q, want %d", auth.ValidBefore, wantValidBefore)
	}
}

func repeatHex(b string, n int) string {
	s := ""
	for i := 0; i < n; i++ {
		s += b
	}
	return s
}

func intToStr(v int64) string {
	return formatInt64(v)
}

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
