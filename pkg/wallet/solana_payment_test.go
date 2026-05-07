package wallet

import (
	"bytes"
	"context"
	"encoding/hex"
	"strings"
	"testing"
)

const paymentTestBlockhash = "4sGjMW1sUnHzSxGspuhpqLDx6wiyjNtZAMdL4VZHirAn"

func TestBuildSolanaPaymentTxNativeWithExternalFeePayer(t *testing.T) {
	t.Parallel()
	tx, err := BuildSolanaPaymentTx(context.Background(), SolanaPaymentTxOptions{
		From:            testFrom,
		Recipient:       testTo,
		FeePayer:        testTo,
		Amount:          42_000,
		Memo:            "order-123",
		RecentBlockhash: paymentTestBlockhash,
	})
	if err != nil {
		t.Fatalf("BuildSolanaPaymentTx: %v", err)
	}
	if tx.SignerSlot != 1 {
		t.Fatalf("SignerSlot = %d, want 1 when fee payer is slot 0", tx.SignerSlot)
	}
	raw, err := hex.DecodeString(tx.FullUnsignedHex)
	if err != nil {
		t.Fatalf("decode tx: %v", err)
	}
	if len(raw) < 129 || raw[0] != 2 {
		t.Fatalf("unsigned tx prefix = %x, want compact two-signature tx", raw[:min(len(raw), 3)])
	}

	signature := bytesOf(0x7a, 64)
	signed, err := InsertSolanaSignature(tx.FullUnsignedHex, tx.SignerSlot, signature)
	if err != nil {
		t.Fatalf("InsertSolanaSignature: %v", err)
	}
	if !bytes.Equal(signed[1+64:1+128], signature) {
		t.Fatalf("signer slot was not filled")
	}
}

func TestBuildSolanaPaymentTxToken2022WithSplit(t *testing.T) {
	t.Parallel()
	tx, err := BuildSolanaPaymentTx(context.Background(), SolanaPaymentTxOptions{
		From:            testFrom,
		Recipient:       testTo,
		Mint:            usdcMint,
		TokenProgram:    SolanaToken2022Program,
		Amount:          10_000,
		Decimals:        6,
		RecentBlockhash: paymentTestBlockhash,
		Splits: []SolanaPaymentSplit{{
			Recipient: testTo,
			Amount:    2_500,
			Memo:      "split",
			CreateATA: true,
		}},
	})
	if err != nil {
		t.Fatalf("BuildSolanaPaymentTx: %v", err)
	}
	if tx.SignerSlot != 0 {
		t.Fatalf("SignerSlot = %d, want 0 without external fee payer", tx.SignerSlot)
	}
	if tx.FullUnsignedHex == "" || tx.MessageHex == "" {
		t.Fatalf("expected serialized tx and message")
	}
}

func TestBuildSolanaPaymentTxRejectsNonCanonicalMint(t *testing.T) {
	t.Parallel()
	_, err := BuildSolanaPaymentTx(context.Background(), SolanaPaymentTxOptions{
		From:            testFrom,
		Recipient:       testTo,
		Mint:            "Xo",
		Amount:          1,
		Decimals:        6,
		RecentBlockhash: paymentTestBlockhash,
	})
	if err == nil || !strings.Contains(err.Error(), "non-canonical") {
		t.Fatalf("err = %v, want non-canonical mint error", err)
	}
}

func bytesOf(value byte, count int) []byte {
	out := make([]byte, count)
	for i := range out {
		out[i] = value
	}
	return out
}
