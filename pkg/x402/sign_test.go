package x402

import (
	"context"
	"errors"
	"testing"
)

func TestFakeSignerProducesDeterministicHeader(t *testing.T) {
	t.Parallel()
	c := PaymentChallenge{
		Network:   NetworkBase,
		Currency:  CurrencyUSDC,
		Amount:    "0.005",
		Recipient: "0xabc",
		Nonce:     "n1",
	}
	a, err := NewFakeSigner().Sign(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewFakeSigner().Sign(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("FakeSigner not deterministic: %+v vs %+v", a, b)
	}
	if a.Signature != "fake-sig:n1" {
		t.Fatalf("Signature = %q, want fake-sig:n1", a.Signature)
	}
}

func TestFakeSignerRequiresNonce(t *testing.T) {
	t.Parallel()
	_, err := NewFakeSigner().Sign(context.Background(), PaymentChallenge{
		Network: NetworkBase,
		Amount:  "0.005",
	})
	if err == nil {
		t.Fatal("expected error for missing nonce")
	}
}

func TestStubSignerReturnsErrSignerNotConfigured(t *testing.T) {
	t.Parallel()
	_, err := NewStubSigner("OWS not installed").Sign(context.Background(), PaymentChallenge{Nonce: "n1"})
	if !errors.Is(err, ErrSignerNotConfigured) {
		t.Fatalf("err = %v, want ErrSignerNotConfigured", err)
	}
}
