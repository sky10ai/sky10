package siwx

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// fakeSigner returns a deterministic signature so the encoded
// envelope is byte-stable across runs.
type fakeSigner struct {
	sig     string
	calledN int
	gotMsg  string
}

func (f *fakeSigner) SignPersonalMessage(_ context.Context, message string) (string, error) {
	f.calledN++
	f.gotMsg = message
	return f.sig, nil
}

func TestBuildSiweMessageMatchesEIP4361Shape(t *testing.T) {
	t.Parallel()
	got := buildSiweMessage(siweFields{
		Domain:         "api.venice.ai",
		Address:        "0xdD12DEcbea4bd0Bc414af635a3398f50FA291e45",
		Statement:      "Sign in to Venice AI",
		URI:            "https://api.venice.ai/api/v1/chat/completions",
		Version:        "1",
		ChainID:        8453,
		Nonce:          "deadbeefcafebabe",
		IssuedAt:       "2026-05-06T11:00:00.000Z",
		ExpirationTime: "2026-05-06T11:05:00.000Z",
	})
	want := `api.venice.ai wants you to sign in with your Ethereum account:
0xdD12DEcbea4bd0Bc414af635a3398f50FA291e45

Sign in to Venice AI

URI: https://api.venice.ai/api/v1/chat/completions
Version: 1
Chain ID: 8453
Nonce: deadbeefcafebabe
Issued At: 2026-05-06T11:00:00.000Z
Expiration Time: 2026-05-06T11:05:00.000Z`
	if got != want {
		t.Fatalf("siwe message mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestHeaderProducesEnvelopeMatchingVeniceSDK(t *testing.T) {
	t.Parallel()
	signer := &fakeSigner{sig: "0x" + strings.Repeat("ab", 65)}
	b := &Builder{
		Address:      "0xdD12DEcbea4bd0Bc414af635a3398f50FA291e45",
		Domain:       "api.venice.ai",
		ChainID:      8453,
		Statement:    "Sign in to Venice AI",
		SignInExpiry: 5 * time.Minute,
		Signer:       signer,
		Now: func() time.Time {
			return time.Date(2026, 5, 6, 11, 0, 0, 0, time.UTC)
		},
		Nonce: func() string { return "deadbeefcafebabe" },
	}
	header, err := b.Header(context.Background(), "/api/v1/chat/completions")
	if err != nil {
		t.Fatalf("Header: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(header)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}

	// The envelope shape is locked in to match what venice-x402-client
	// emits — five keys exactly: address, message, signature,
	// timestamp, chainId. Servers that grew up around the SDK may
	// silently fail on extra keys.
	wantKeys := []string{"address", "chainId", "message", "signature", "timestamp"}
	gotKeys := keysOf(got)
	if strings.Join(gotKeys, ",") != strings.Join(wantKeys, ",") {
		t.Fatalf("envelope keys = %v, want %v", gotKeys, wantKeys)
	}
	if got["address"] != "0xdD12DEcbea4bd0Bc414af635a3398f50FA291e45" {
		t.Fatalf("address = %v", got["address"])
	}
	if got["chainId"] != float64(8453) {
		t.Fatalf("chainId = %v", got["chainId"])
	}
	if !strings.HasPrefix(got["signature"].(string), "0x") {
		t.Fatalf("signature not 0x-prefixed: %v", got["signature"])
	}
	// timestamp is unix-millis of the Now hook
	wantTs := float64(time.Date(2026, 5, 6, 11, 0, 0, 0, time.UTC).UnixMilli())
	if got["timestamp"] != wantTs {
		t.Fatalf("timestamp = %v, want %v", got["timestamp"], wantTs)
	}
	// And the message must contain the SIWE structure, not just be
	// the literal resource URL.
	msg := got["message"].(string)
	if !strings.Contains(msg, "URI: https://api.venice.ai/api/v1/chat/completions") {
		t.Fatalf("message missing URI line: %s", msg)
	}
	if !strings.Contains(msg, "Nonce: deadbeefcafebabe") {
		t.Fatalf("message missing Nonce line: %s", msg)
	}
	// Signer was invoked exactly once with the SIWE message text.
	if signer.calledN != 1 || signer.gotMsg != msg {
		t.Fatalf("signer state: calledN=%d gotMsg=%q", signer.calledN, signer.gotMsg)
	}
}

func TestHeaderRequiresWalletAddress(t *testing.T) {
	t.Parallel()
	b := &Builder{Domain: "x", Signer: &fakeSigner{sig: "0x00"}}
	if _, err := b.Header(context.Background(), "/"); err == nil {
		t.Fatal("expected error for empty address")
	}
}

func TestNormalizeResourceURLResolvesRelative(t *testing.T) {
	t.Parallel()
	got, err := normalizeResourceURL("/api/v1/chat/completions", "api.venice.ai")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://api.venice.ai/api/v1/chat/completions" {
		t.Fatalf("got %q", got)
	}
}

func TestDetectExtractsHintFromExtensions(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{
		"info": {
			"domain": "api.venice.ai",
			"uri": "https://api.venice.ai/api/v1/x402/transactions/0xWALLET",
			"version": "1",
			"statement": "Sign in to Venice AI",
			"nonce": "BMIIgHDI2TbDWsweNbR-N",
			"issuedAt": "2026-05-06T10:53:11.258Z",
			"expirationTime": "2026-05-06T10:58:11.258Z"
		},
		"supportedChains": [
			{"chainId": "eip155:8453", "type": "eip191"},
			{"chainId": "eip155:8453", "type": "eip1271"}
		]
	}`)
	hint, ok := Detect(map[string]json.RawMessage{
		"sign-in-with-x": raw,
	})
	if !ok {
		t.Fatal("Detect returned !ok")
	}
	if hint.Domain != "api.venice.ai" {
		t.Fatalf("Domain = %q", hint.Domain)
	}
	if len(hint.SupportedChains) != 2 {
		t.Fatalf("SupportedChains = %d, want 2", len(hint.SupportedChains))
	}
}

func TestDetectAbsentExtensionReturnsFalse(t *testing.T) {
	t.Parallel()
	if _, ok := Detect(nil); ok {
		t.Fatal("expected !ok for nil extensions")
	}
	if _, ok := Detect(map[string]json.RawMessage{"bazaar": []byte("{}")}); ok {
		t.Fatal("expected !ok when sign-in-with-x missing")
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// stable order
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}
