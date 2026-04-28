package x402

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func sampleSolanaRequirement() PaymentRequirements {
	return PaymentRequirements{
		Scheme:            "exact",
		Network:           "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp",
		MaxAmountRequired: "1000",
		PayTo:             "9WzDXwBbmkg8ZTbNMqUxvQRAyrZzDsGYdLVL9zYtAWWM",
		Asset:             "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
		MaxTimeoutSeconds: 60,
		Extra: map[string]interface{}{
			"feePayer": "FeeP4yereeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
			"memo":     "service:perplexity",
		},
	}
}

func TestCanonicalizeNetworkAcceptsCAIP2(t *testing.T) {
	t.Parallel()
	cases := map[string]Network{
		"base":        NetworkBase,
		"BASE":        NetworkBase,
		"eip155:8453": NetworkBase,
		"solana":      NetworkSolana,
		"solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp": NetworkSolana,
		"SOLANA:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp": NetworkSolana,
	}
	for in, want := range cases {
		got, ok := canonicalizeNetwork(in)
		if !ok {
			t.Errorf("canonicalizeNetwork(%q) returned !ok", in)
			continue
		}
		if got != want {
			t.Errorf("canonicalizeNetwork(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCanonicalizeNetworkRejectsUnknown(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"", "polygon", "btc", "lightning"} {
		if _, ok := canonicalizeNetwork(in); ok {
			t.Errorf("canonicalizeNetwork(%q) returned ok, want false", in)
		}
	}
}

func TestSolanaAmountAcceptsBaseUnitAndDecimal(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		want    uint64
		wantErr bool
	}{
		{"0", 0, true},
		{"1000", 1000, false},
		{"5000000", 5_000_000, false},
		{"0.005", 5000, false},
		{"0.000001", 1, false},
		{"abc", 0, true},
	}
	for _, tc := range cases {
		got, err := solanaAmount(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("solanaAmount(%q) err = nil, want error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("solanaAmount(%q) err = %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("solanaAmount(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestOWSSignerSolanaHappyPath(t *testing.T) {
	t.Parallel()
	const fakeUnsignedHex = "deadbeef00"
	const fakeSignedHex = "cafebabe11"
	signTxCalls := 0
	buildCalls := 0
	signer := &OWSSigner{
		WalletName: "agent-wallet",
		Now:        nowFromString("2026-04-28T12:00:00Z"),
		AddressForChain: func(_ context.Context, _, chain string) (string, error) {
			if chain != "solana" {
				t.Fatalf("AddressForChain chain = %q, want solana", chain)
			}
			return "ClientSoLAddrxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", nil
		},
		BuildSolanaTx: func(_ context.Context, from, to, feePayer, mint string, amount uint64, memo string) (string, error) {
			buildCalls++
			if from != "ClientSoLAddrxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" {
				t.Fatalf("from = %q", from)
			}
			if to != "9WzDXwBbmkg8ZTbNMqUxvQRAyrZzDsGYdLVL9zYtAWWM" {
				t.Fatalf("to = %q", to)
			}
			if feePayer != "FeeP4yereeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee" {
				t.Fatalf("feePayer = %q", feePayer)
			}
			if mint != "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v" {
				t.Fatalf("mint = %q", mint)
			}
			if amount != 1000 {
				t.Fatalf("amount = %d, want 1000", amount)
			}
			if memo != "service:perplexity" {
				t.Fatalf("memo = %q", memo)
			}
			return fakeUnsignedHex, nil
		},
		SignTx: func(_ context.Context, walletName, chain, txHex string) (string, error) {
			signTxCalls++
			if walletName != "agent-wallet" {
				t.Fatalf("wallet = %q", walletName)
			}
			if chain != "solana" {
				t.Fatalf("chain = %q", chain)
			}
			if txHex != fakeUnsignedHex {
				t.Fatalf("txHex = %q, want %q", txHex, fakeUnsignedHex)
			}
			return fakeSignedHex, nil
		},
	}

	payload, err := signer.Sign(context.Background(), sampleSolanaRequirement())
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if buildCalls != 1 || signTxCalls != 1 {
		t.Fatalf("build=%d sign=%d, want 1/1", buildCalls, signTxCalls)
	}
	if payload.X402Version != X402ProtocolVersion {
		t.Fatalf("version = %d", payload.X402Version)
	}
	if !strings.HasPrefix(strings.ToLower(payload.Network), "solana") {
		t.Fatalf("network = %q", payload.Network)
	}
	var inner SolanaExactPayload
	if err := json.Unmarshal(payload.Payload, &inner); err != nil {
		t.Fatalf("decode inner: %v", err)
	}
	wantBytes, _ := hex.DecodeString(fakeSignedHex)
	wantB64 := base64.StdEncoding.EncodeToString(wantBytes)
	if inner.Transaction != wantB64 {
		t.Fatalf("transaction = %q, want %q", inner.Transaction, wantB64)
	}
}

func TestOWSSignerSolanaRejectsMissingFeePayer(t *testing.T) {
	t.Parallel()
	signer := &OWSSigner{
		WalletName:      "agent-wallet",
		Now:             nowFromString("2026-04-28T12:00:00Z"),
		AddressForChain: func(_ context.Context, _, _ string) (string, error) { return "client", nil },
		BuildSolanaTx: func(_ context.Context, _, _, _, _ string, _ uint64, _ string) (string, error) {
			t.Fatal("BuildSolanaTx should not be called when feePayer is missing")
			return "", nil
		},
		SignTx: func(_ context.Context, _, _, _ string) (string, error) {
			t.Fatal("SignTx should not be called when feePayer is missing")
			return "", nil
		},
	}
	req := sampleSolanaRequirement()
	delete(req.Extra, "feePayer")
	if _, err := signer.Sign(context.Background(), req); err == nil {
		t.Fatal("expected error for missing feePayer")
	}
}

func TestOWSSignerSolanaSurfacesAddressError(t *testing.T) {
	t.Parallel()
	signer := &OWSSigner{
		WalletName: "agent-wallet",
		Now:        nowFromString("2026-04-28T12:00:00Z"),
		AddressForChain: func(_ context.Context, _, _ string) (string, error) {
			return "", errors.New("ows offline")
		},
		BuildSolanaTx: func(_ context.Context, _, _, _, _ string, _ uint64, _ string) (string, error) {
			t.Fatal("BuildSolanaTx should not run when address fails")
			return "", nil
		},
		SignTx: func(_ context.Context, _, _, _ string) (string, error) {
			t.Fatal("SignTx should not run when address fails")
			return "", nil
		},
	}
	if _, err := signer.Sign(context.Background(), sampleSolanaRequirement()); err == nil {
		t.Fatal("expected error from AddressForChain")
	}
}
