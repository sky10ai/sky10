package wallet

import (
	"encoding/hex"
	"math/big"
	"testing"
)

func TestParseEVMAmount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		decimals int
		want     string
		wantErr  bool
	}{
		{name: "whole eth", input: "2", decimals: 18, want: "2000000000000000000"},
		{name: "fractional eth", input: "1.5", decimals: 18, want: "1500000000000000000"},
		{name: "leading dot", input: ".25", decimals: 6, want: "250000"},
		{name: "zero", input: "0", decimals: 6, want: "0"},
		{name: "too many decimals", input: "1.0000001", decimals: 6, wantErr: true},
		{name: "invalid chars", input: "1e6", decimals: 6, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseEVMAmount(tt.input, tt.decimals)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseEVMAmount(%q, %d) unexpectedly succeeded", tt.input, tt.decimals)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseEVMAmount(%q, %d): %v", tt.input, tt.decimals, err)
			}
			if got.String() != tt.want {
				t.Fatalf("parseEVMAmount(%q, %d) = %s, want %s", tt.input, tt.decimals, got.String(), tt.want)
			}
		})
	}
}

func TestEncodeERC20Transfer(t *testing.T) {
	t.Parallel()

	got, err := encodeERC20Transfer("0x00000000000000000000000000000000000000ff", big.NewInt(1234567))
	if err != nil {
		t.Fatalf("encodeERC20Transfer: %v", err)
	}

	const want = "a9059cbb00000000000000000000000000000000000000000000000000000000000000ff000000000000000000000000000000000000000000000000000000000012d687"
	if hex.EncodeToString(got) != want {
		t.Fatalf("encodeERC20Transfer() = %q, want %q", hex.EncodeToString(got), want)
	}
}

func TestEncodeEIP1559UnsignedTx(t *testing.T) {
	t.Parallel()

	got, err := encodeEIP1559UnsignedTx(
		baseChainID,
		7,
		big.NewInt(1_000_000),
		big.NewInt(2_500_000),
		21_000,
		"0x1111111111111111111111111111111111111111",
		big.NewInt(1_234_500),
		nil,
	)
	if err != nil {
		t.Fatalf("encodeEIP1559UnsignedTx: %v", err)
	}

	const want = "02ea82210507830f4240832625a08252089411111111111111111111111111111111111111118312d64480c0"
	if hex.EncodeToString(got) != want {
		t.Fatalf("encodeEIP1559UnsignedTx() = %q, want %q", hex.EncodeToString(got), want)
	}
}

func TestParseBroadcastResult(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "transaction_hash", body: `{"transaction_hash":"0xabc"}`, want: "0xabc"},
		{name: "txHash", body: `{"txHash":"0xdef"}`, want: "0xdef"},
		{name: "tx_response", body: `{"tx_response":{"txhash":"0x123"}}`, want: "0x123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := &PayResult{}
			parseBroadcastResult([]byte(tt.body), result)
			if result.TxHash != tt.want {
				t.Fatalf("parseBroadcastResult(%s) tx hash = %q, want %q", tt.name, result.TxHash, tt.want)
			}
		})
	}
}
