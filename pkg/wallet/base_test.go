package wallet

import "testing"

func TestFormatEVMUnits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		hexValue string
		decimals int
		want     string
	}{
		{name: "zero", hexValue: "0x0", decimals: 18, want: "0"},
		{name: "whole eth", hexValue: "0xde0b6b3a7640000", decimals: 18, want: "1"},
		{name: "fractional eth", hexValue: "0x14d1120d7b160000", decimals: 18, want: "1.5"},
		{name: "whole usdc", hexValue: "0xf4240", decimals: 6, want: "1"},
		{name: "fractional usdc", hexValue: "0x96b43f", decimals: 6, want: "9.876543"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			value, err := parseHexBigInt(tt.hexValue)
			if err != nil {
				t.Fatalf("parseHexBigInt(%q): %v", tt.hexValue, err)
			}
			if got := formatEVMUnits(value, tt.decimals); got != tt.want {
				t.Fatalf("formatEVMUnits(%q, %d) = %q, want %q", tt.hexValue, tt.decimals, got, tt.want)
			}
		})
	}
}

func TestEncodeERC20BalanceOf(t *testing.T) {
	t.Parallel()

	got, err := encodeERC20BalanceOf("0x00000000000000000000000000000000000000ff")
	if err != nil {
		t.Fatalf("encodeERC20BalanceOf: %v", err)
	}

	const want = "0x70a0823100000000000000000000000000000000000000000000000000000000000000ff"
	if got != want {
		t.Fatalf("encodeERC20BalanceOf() = %q, want %q", got, want)
	}
}
