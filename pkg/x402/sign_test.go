package x402

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestFakeSignerProducesValidPayload(t *testing.T) {
	t.Parallel()
	payload, err := NewFakeSigner("0x0000000000000000000000000000000000000abc").Sign(context.Background(), sampleRequirement())
	if err != nil {
		t.Fatal(err)
	}
	if payload.X402Version != X402ProtocolVersion {
		t.Fatalf("version = %d", payload.X402Version)
	}
	if payload.Scheme != "exact" || payload.Network != "base" {
		t.Fatalf("scheme/network: %s/%s", payload.Scheme, payload.Network)
	}
	var exact ExactSchemePayload
	if err := json.Unmarshal(payload.Payload, &exact); err != nil {
		t.Fatalf("decode inner: %v", err)
	}
	if !strings.HasPrefix(exact.Signature, "fake-sig:") {
		t.Fatalf("signature = %q, want fake-sig: prefix", exact.Signature)
	}
	if exact.Authorization.From != "0x0000000000000000000000000000000000000abc" {
		t.Fatalf("from = %q", exact.Authorization.From)
	}
	if exact.Authorization.Value != "5000" {
		t.Fatalf("value = %q, want 5000 (0.005 USDC = 5000 micro-USDC)", exact.Authorization.Value)
	}
}

func TestFakeSignerRejectsUnsupportedScheme(t *testing.T) {
	t.Parallel()
	req := sampleRequirement()
	req.Scheme = "lightning"
	if _, err := NewFakeSigner("0x0").Sign(context.Background(), req); err == nil {
		t.Fatal("expected error for unsupported scheme")
	}
}

func TestStubSignerReturnsErrSignerNotConfigured(t *testing.T) {
	t.Parallel()
	_, err := NewStubSigner("OWS not installed").Sign(context.Background(), sampleRequirement())
	if !errors.Is(err, ErrSignerNotConfigured) {
		t.Fatalf("err = %v, want ErrSignerNotConfigured", err)
	}
}

func TestOWSSignerHappyPath(t *testing.T) {
	t.Parallel()
	const expectedSig = "0xdeadbeef00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"
	signCalls := 0
	signer := &OWSSigner{
		WalletName: "agent-wallet",
		Now:        nowFromString("2026-04-28T12:00:00Z"),
		AddressForChain: func(_ context.Context, walletName, chain string) (string, error) {
			if walletName != "agent-wallet" {
				t.Fatalf("wallet = %q", walletName)
			}
			if chain != "base" {
				t.Fatalf("chain = %q", chain)
			}
			return "0x0000000000000000000000000000000000000abc", nil
		},
		SignTypedData: func(_ context.Context, walletName, chain string, td []byte) (string, error) {
			signCalls++
			var parsed EIP712TypedData
			if err := json.Unmarshal(td, &parsed); err != nil {
				t.Fatalf("typed data not JSON: %v", err)
			}
			if parsed.PrimaryType != "TransferWithAuthorization" {
				t.Fatalf("primaryType = %q", parsed.PrimaryType)
			}
			if parsed.Domain.ChainID != 8453 {
				t.Fatalf("chainID = %d", parsed.Domain.ChainID)
			}
			return expectedSig, nil
		},
	}

	payload, err := signer.Sign(context.Background(), sampleRequirement())
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if signCalls != 1 {
		t.Fatalf("SignTypedData called %d times, want 1", signCalls)
	}
	var exact ExactSchemePayload
	if err := json.Unmarshal(payload.Payload, &exact); err != nil {
		t.Fatalf("decode inner: %v", err)
	}
	if exact.Signature != expectedSig {
		t.Fatalf("signature = %q, want %q", exact.Signature, expectedSig)
	}
	if exact.Authorization.From != "0x0000000000000000000000000000000000000abc" {
		t.Fatalf("from = %q", exact.Authorization.From)
	}
}

func TestOWSSignerRejectsSolana(t *testing.T) {
	t.Parallel()
	signer := &OWSSigner{
		WalletName: "agent-wallet",
		Now:        nowFromString("2026-04-28T12:00:00Z"),
		AddressForChain: func(_ context.Context, _, _ string) (string, error) {
			t.Fatal("AddressForChain should not be called")
			return "", nil
		},
		SignTypedData: func(_ context.Context, _, _ string, _ []byte) (string, error) {
			t.Fatal("SignTypedData should not be called")
			return "", nil
		},
	}
	req := sampleRequirement()
	req.Network = "solana"
	if _, err := signer.Sign(context.Background(), req); err == nil {
		t.Fatal("expected error for solana network")
	}
}

func TestOWSSignerSurfacesAddressError(t *testing.T) {
	t.Parallel()
	signer := &OWSSigner{
		WalletName: "agent-wallet",
		Now:        nowFromString("2026-04-28T12:00:00Z"),
		AddressForChain: func(_ context.Context, _, _ string) (string, error) {
			return "", errors.New("ows offline")
		},
		SignTypedData: func(_ context.Context, _, _ string, _ []byte) (string, error) {
			t.Fatal("SignTypedData should not be called when address fails")
			return "", nil
		},
	}
	if _, err := signer.Sign(context.Background(), sampleRequirement()); err == nil {
		t.Fatal("expected error from AddressForChain")
	}
}

func TestNewOWSSignerNilClientReturnsNil(t *testing.T) {
	t.Parallel()
	if s := NewOWSSigner(nil, "x"); s != nil {
		t.Fatal("NewOWSSigner(nil, ...) should return nil")
	}
}

// nowFromString returns a deterministic clock for tests.
func nowFromString(s string) func() time.Time {
	return func() time.Time {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			panic(err)
		}
		return t
	}
}
