package x402

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	skywallet "github.com/sky10/sky10/pkg/wallet"
)

// SolanaExactPayload is the shape of PaymentPayload.Payload when
// scheme == "exact" and network is solana. The single `transaction`
// field carries a base64-encoded, partially-signed v0 versioned
// Solana transaction; the facilitator fills the remaining fee-payer
// signature server-side and submits it on-chain.
type SolanaExactPayload struct {
	Transaction string `json:"transaction"`
}

// signSolanaExact handles the Solana branch of OWSSigner.Sign. The
// flow:
//
//  1. resolve the client's Solana address from the bound wallet
//  2. parse the facilitator's fee-payer pubkey out of `extra.feePayer`
//  3. build a v0 versioned partial-sign transfer transaction in
//     pkg/wallet
//  4. hand the hex-encoded bytes to OWS for signing as the client
//  5. base64-encode the signed bytes and wrap them as the
//     `transaction` field of an x402 SolanaExactPayload
//
// Behavior is best-effort against the published x402 SVM spec; the
// path has not yet been verified end-to-end against a live Solana
// x402 facilitator. When something rejects the constructed tx, the
// signing-time logs make it easy to localize the failure.
func (s *OWSSigner) signSolanaExact(ctx context.Context, req PaymentRequirements) (SignedPayload, error) {
	if s.SignTx == nil || s.AddressForChain == nil || s.BuildSolanaTx == nil {
		return SignedPayload{}, ErrSignerNotConfigured
	}
	addr, err := s.AddressForChain(ctx, s.WalletName, string(NetworkSolana))
	if err != nil {
		return SignedPayload{}, fmt.Errorf("ows signer: resolving wallet %q solana address: %w", s.WalletName, err)
	}
	if strings.TrimSpace(addr) == "" {
		return SignedPayload{}, fmt.Errorf("ows signer: wallet %q has no solana address", s.WalletName)
	}
	feePayer, ok := req.Extra["feePayer"].(string)
	if !ok || strings.TrimSpace(feePayer) == "" {
		return SignedPayload{}, errors.New("ows signer: solana requirement missing extra.feePayer")
	}
	if strings.TrimSpace(req.PayTo) == "" {
		return SignedPayload{}, errors.New("ows signer: solana requirement missing payTo")
	}
	if strings.TrimSpace(req.Asset) == "" {
		return SignedPayload{}, errors.New("ows signer: solana requirement missing asset (token mint)")
	}
	amount, err := solanaAmount(req.AmountMicros)
	if err != nil {
		return SignedPayload{}, fmt.Errorf("ows signer: parse amount: %w", err)
	}
	memo, _ := req.Extra["memo"].(string)

	fullUnsignedHex, messageHex, err := s.BuildSolanaTx(ctx, addr, req.PayTo, feePayer, req.Asset, amount, memo)
	if err != nil {
		return SignedPayload{}, fmt.Errorf("ows signer: build solana tx: %w", err)
	}
	unsignedBytes, err := hex.DecodeString(strings.TrimPrefix(strings.TrimPrefix(fullUnsignedHex, "0x"), "0X"))
	if err != nil {
		return SignedPayload{}, fmt.Errorf("ows signer: decode unsigned tx hex: %w", err)
	}

	// OWS's `sign tx` expects the full serialized transaction (with
	// placeholder signature slots) as input. It internally extracts
	// the message portion, signs that, and returns the raw 64-byte
	// ed25519 signature.
	_ = messageHex
	signedHex, err := s.SignTx(ctx, s.WalletName, string(NetworkSolana), fullUnsignedHex)
	if err != nil {
		return SignedPayload{}, fmt.Errorf("ows signer: %w", err)
	}
	sigBytes, err := hex.DecodeString(strings.TrimPrefix(strings.TrimPrefix(signedHex, "0x"), "0X"))
	if err != nil {
		return SignedPayload{}, fmt.Errorf("ows signer: decode signature hex: %w", err)
	}
	if len(sigBytes) != 64 {
		return SignedPayload{}, fmt.Errorf("ows signer: expected 64-byte ed25519 signature, got %d bytes", len(sigBytes))
	}

	// Assemble the partially-signed transaction: take the unsigned
	// bytes (with two 64-byte zero signature slots) and overwrite
	// slot 1 (client signer; slot 0 is the facilitator's fee payer
	// that the server fills server-side after verifying our payload).
	// compactU16(2) is one byte (0x02) since 2 < 0x80; slot 0
	// occupies bytes [1..65), slot 1 occupies bytes [65..129).
	if len(unsignedBytes) < 1+64+64 {
		return SignedPayload{}, fmt.Errorf("ows signer: unsigned tx too short to hold two signatures (%d bytes)", len(unsignedBytes))
	}
	signedTxBytes := make([]byte, len(unsignedBytes))
	copy(signedTxBytes, unsignedBytes)
	copy(signedTxBytes[1+64:1+64+64], sigBytes)
	encoded := base64.StdEncoding.EncodeToString(signedTxBytes)
	inner, err := json.Marshal(SolanaExactPayload{Transaction: encoded})
	if err != nil {
		return SignedPayload{}, err
	}
	return SignedPayload{
		Scheme:  req.Scheme,
		Network: req.Network,
		Inner:   inner,
	}, nil
}

// solanaAmount parses the requirement's amount into the integer SPL
// token base unit. The x402 SVM spec quotes amounts as the integer
// base unit directly (e.g. "1000" for 0.001 USDC at 6 decimals),
// unlike the EVM path which uses a decimal string. We accept both:
// integers pass through unchanged, decimals are converted to micros
// via the shared parser.
func solanaAmount(s string) (uint64, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return 0, errors.New("amount required")
	}
	if !strings.Contains(trimmed, ".") {
		v, err := strconv.ParseUint(trimmed, 10, 64)
		if err != nil {
			return 0, err
		}
		if v == 0 {
			return 0, errors.New("amount must be positive")
		}
		return v, nil
	}
	micros, err := parseUSDC(trimmed)
	if err != nil {
		return 0, err
	}
	if micros.Sign() <= 0 {
		return 0, errors.New("amount must be positive")
	}
	if !micros.IsUint64() {
		return 0, errors.New("amount overflows uint64")
	}
	return micros.Uint64(), nil
}

// solanaAmountForBig is a small helper kept around for future
// callers (e.g. budget reconciliation) that already hold a big.Int.
func solanaAmountForBig(v *big.Int) (uint64, error) {
	if v == nil || v.Sign() < 0 {
		return 0, errors.New("amount required")
	}
	if !v.IsUint64() {
		return 0, errors.New("amount overflows uint64")
	}
	return v.Uint64(), nil
}

// owsBuildSolanaTx is the production wiring for OWSSigner.BuildSolanaTx;
// it forwards to the wallet package builder. Tests substitute a fake.
func owsBuildSolanaTx(ctx context.Context, from, to, feePayer, mint string, amount uint64, memo string) (fullUnsignedTxHex, messageHex string, err error) {
	return skywallet.BuildX402SolanaTransferTx(ctx, from, to, feePayer, mint, amount, memo)
}
