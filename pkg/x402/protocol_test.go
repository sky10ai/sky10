package x402

import (
	"testing"
)

func TestPaymentHeaderRoundTrip(t *testing.T) {
	t.Parallel()
	original := PaymentHeader{
		Network:   NetworkBase,
		Amount:    "0.005",
		Recipient: "0xabc",
		Nonce:     "n1",
		Signature: "sig123",
	}
	encoded, err := original.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	decoded, err := DecodePaymentHeader(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded != original {
		t.Fatalf("round trip = %+v, want %+v", decoded, original)
	}
}

func TestPaymentHeaderEncodeDeterministic(t *testing.T) {
	t.Parallel()
	h := PaymentHeader{Network: NetworkBase, Amount: "0.005", Recipient: "0xabc", Nonce: "n1", Signature: "sig"}
	a, _ := h.Encode()
	b, _ := h.Encode()
	if a != b {
		t.Fatalf("Encode is not deterministic: %q vs %q", a, b)
	}
}
