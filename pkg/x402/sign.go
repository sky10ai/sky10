package x402

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	skywallet "github.com/sky10/sky10/pkg/wallet"
)

// ErrSignerNotConfigured indicates the host has no Signer wired in
// (typically: OWS not installed or no wallet bound). Returned by the
// Backend when a Call would need to sign but cannot.
var ErrSignerNotConfigured = errors.New("x402: signer not configured")

// Signer turns a PaymentRequirements directive into a fully-signed
// PaymentPayload the transport encodes onto the X-PAYMENT header.
//
// Implementations bridge to whichever wallet primitive the host has —
// OWS in production, a fake in tests.
//
// Signer.Sign must be safe to call concurrently from multiple
// in-flight envelopes; the underlying wallet is expected to handle
// its own concurrency.
type Signer interface {
	Sign(ctx context.Context, req PaymentRequirements) (PaymentPayload, error)
}

// FakeSigner is the test-side Signer. It produces deterministic
// signatures derived from the request fields so test assertions are
// stable. Real verifiers will reject the signature; tests that need
// a working signature against a real verifier should use OWSSigner
// against a configured wallet.
type FakeSigner struct {
	From string
	Now  func() time.Time
}

// NewFakeSigner constructs a FakeSigner with the given client-side
// address. Tests can override Now via the struct field.
func NewFakeSigner(fromAddress string) FakeSigner {
	return FakeSigner{From: fromAddress, Now: time.Now}
}

// Sign implements Signer. The returned payload contains a
// deterministic, syntactically-valid ExactScheme authorization with
// a synthetic signature ("fake-sig:<random-nonce>"); transport tests
// observe the structure end-to-end without performing any real
// signing.
func (s FakeSigner) Sign(_ context.Context, req PaymentRequirements) (PaymentPayload, error) {
	if !isSupportedScheme(req.Scheme) {
		return PaymentPayload{}, fmt.Errorf("fake signer: unsupported scheme %q", req.Scheme)
	}
	if !isSupportedNetwork(req.Network) {
		return PaymentPayload{}, fmt.Errorf("fake signer: unsupported network %q", req.Network)
	}
	nonceHex, err := randomNonceHex()
	if err != nil {
		return PaymentPayload{}, err
	}
	value, err := microsForRequirement(req)
	if err != nil {
		return PaymentPayload{}, err
	}
	now := s.Now()
	if now.IsZero() {
		now = time.Now()
	}
	from := s.From
	if from == "" {
		from = "0x0000000000000000000000000000000000000001"
	}
	_, auth, err := BuildTransferWithAuthorizationTypedData(req, from, value, nonceHex, now)
	if err != nil {
		return PaymentPayload{}, err
	}
	exact := ExactSchemePayload{
		Signature:     "fake-sig:" + nonceHex,
		Authorization: auth,
	}
	return marshalExactPayload(req, exact)
}

// StubSigner is the placeholder signer for production when OWS is
// unavailable. It returns ErrSignerNotConfigured for every call.
// NewBackend uses it as the default; daemon wiring replaces it with
// a real implementation when OWS is installed.
type StubSigner struct {
	Reason string
}

// NewStubSigner constructs a stub with an explanatory reason that is
// included in every Sign error.
func NewStubSigner(reason string) StubSigner {
	return StubSigner{Reason: reason}
}

// Sign implements Signer; always returns ErrSignerNotConfigured.
func (s StubSigner) Sign(_ context.Context, _ PaymentRequirements) (PaymentPayload, error) {
	if s.Reason != "" {
		return PaymentPayload{}, fmt.Errorf("%w: %s", ErrSignerNotConfigured, s.Reason)
	}
	return PaymentPayload{}, ErrSignerNotConfigured
}

// OWSSigner produces real x402 payment payloads by shelling out to
// the OWS CLI. It uses two OWS commands per Sign:
//
//   - `ows wallet list` (cached on the wrapped Client) to resolve
//     the wallet's address on the requirement's network
//   - `ows sign message --typed-data <json> --json` to sign the
//     EIP-712 TransferWithAuthorization
//
// The Solana network path is not yet wired; signing on Solana
// requires a different message construction and falls through to a
// clear error so callers know the OWS-side work is the gap.
type OWSSigner struct {
	Client     *skywallet.Client
	WalletName string

	// Now is the clock used for validBefore computation. Tests
	// override; production uses time.Now.
	Now func() time.Time

	// SignTypedData is the function that actually invokes OWS. It
	// receives the wallet name, the network ("base" / "solana"),
	// and the typed-data JSON; it returns the signature hex with a
	// 0x prefix. Tests substitute a fake; production uses
	// owsSignTypedData.
	SignTypedData func(ctx context.Context, walletName, network string, typedData []byte) (string, error)

	// AddressForChain resolves the wallet's address for a given
	// chain. Tests substitute a fake; production wraps the OWS
	// client.
	AddressForChain func(ctx context.Context, walletName, chain string) (string, error)

	// SignTx signs an unsigned transaction (e.g. a partially-signed
	// Solana versioned tx) and returns the signed bytes as hex.
	// Tests substitute a fake; production calls
	// `ows sign tx --json` via the wallet client.
	SignTx func(ctx context.Context, walletName, chain, unsignedTxHex string) (string, error)

	// BuildSolanaTx constructs the partially-signed v0 versioned
	// Solana transfer transaction the Solana branch hands to OWS
	// for signing. Tests substitute a fake; production wraps
	// pkg/wallet.BuildX402SolanaTransferTx.
	BuildSolanaTx func(ctx context.Context, from, to, feePayer, mint string, amount uint64, memo string) (string, error)
}

// NewOWSSigner builds a signer that uses the supplied wallet client.
// Returns nil if client is nil, so callers can pattern-match the
// "OWS not installed" case without nil-checking after every Sign.
func NewOWSSigner(client *skywallet.Client, walletName string) *OWSSigner {
	if client == nil || strings.TrimSpace(walletName) == "" {
		return nil
	}
	s := &OWSSigner{
		Client:     client,
		WalletName: walletName,
		Now:        time.Now,
	}
	s.SignTypedData = func(ctx context.Context, name, network string, data []byte) (string, error) {
		return owsSignTypedData(ctx, client, name, network, data)
	}
	s.AddressForChain = func(ctx context.Context, name, chain string) (string, error) {
		return client.AddressForChain(ctx, name, chain)
	}
	s.SignTx = func(ctx context.Context, name, chain, txHex string) (string, error) {
		return owsSignTx(ctx, client, name, chain, txHex)
	}
	s.BuildSolanaTx = owsBuildSolanaTx
	return s
}

// Sign implements Signer.
func (s *OWSSigner) Sign(ctx context.Context, req PaymentRequirements) (PaymentPayload, error) {
	if s == nil {
		return PaymentPayload{}, ErrSignerNotConfigured
	}
	if !isSupportedScheme(req.Scheme) {
		return PaymentPayload{}, fmt.Errorf("ows signer: unsupported scheme %q", req.Scheme)
	}
	network, ok := canonicalizeNetwork(req.Network)
	if !ok {
		return PaymentPayload{}, fmt.Errorf("ows signer: network %q not supported", req.Network)
	}
	switch network {
	case NetworkBase:
		return s.signEVMExact(ctx, req)
	case NetworkSolana:
		return s.signSolanaExact(ctx, req)
	default:
		return PaymentPayload{}, fmt.Errorf("ows signer: network %q has no signing path", network)
	}
}

// signEVMExact handles EIP-3009 TransferWithAuthorization signing on
// Base. The flow lifts straight from the x402 spec for EVM.
func (s *OWSSigner) signEVMExact(ctx context.Context, req PaymentRequirements) (PaymentPayload, error) {
	if s.SignTypedData == nil || s.AddressForChain == nil {
		return PaymentPayload{}, ErrSignerNotConfigured
	}
	addr, err := s.AddressForChain(ctx, s.WalletName, string(NetworkBase))
	if err != nil {
		return PaymentPayload{}, fmt.Errorf("ows signer: resolving wallet %q address: %w", s.WalletName, err)
	}
	if strings.TrimSpace(addr) == "" {
		return PaymentPayload{}, fmt.Errorf("ows signer: wallet %q has no base address", s.WalletName)
	}
	nonceHex, err := randomNonceHex()
	if err != nil {
		return PaymentPayload{}, err
	}
	value, err := microsForRequirement(req)
	if err != nil {
		return PaymentPayload{}, err
	}
	td, auth, err := BuildTransferWithAuthorizationTypedData(req, addr, value, nonceHex, s.Now())
	if err != nil {
		return PaymentPayload{}, err
	}
	tdJSON, err := json.Marshal(td)
	if err != nil {
		return PaymentPayload{}, err
	}
	sig, err := s.SignTypedData(ctx, s.WalletName, string(NetworkBase), tdJSON)
	if err != nil {
		return PaymentPayload{}, fmt.Errorf("ows signer: %w", err)
	}
	exact := ExactSchemePayload{
		Signature:     normalizeSignature(sig),
		Authorization: auth,
	}
	return marshalExactPayload(req, exact)
}

// owsSignTx runs `ows sign tx --chain <chain> --wallet <name>
// --tx <hex> --json` and returns the signed transaction's hex
// representation. The exact JSON shape OWS emits is parsed
// permissively — accepts a `signed_tx`, `tx`, or `transaction` field
// containing hex bytes.
func owsSignTx(ctx context.Context, client *skywallet.Client, walletName, chain, txHex string) (string, error) {
	out, err := client.RunSignTxJSON(ctx, walletName, chain, txHex)
	if err != nil {
		return "", err
	}
	var resp struct {
		SignedTx    string `json:"signed_tx"`
		Tx          string `json:"tx"`
		Transaction string `json:"transaction"`
		Signature   string `json:"signature"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return "", fmt.Errorf("decode ows sign tx output: %w", err)
	}
	for _, candidate := range []string{resp.SignedTx, resp.Tx, resp.Transaction, resp.Signature} {
		if strings.TrimSpace(candidate) != "" {
			return candidate, nil
		}
	}
	return "", errors.New("ows sign tx output missing signed bytes")
}

// owsSignTypedData runs `ows sign message --chain <network> --wallet
// <name> --typed-data <json> --json --no-passphrase` and returns the
// signature hex.
func owsSignTypedData(ctx context.Context, client *skywallet.Client, walletName, network string, typedData []byte) (string, error) {
	out, err := client.RunSignMessageJSON(ctx, walletName, network, typedData)
	if err != nil {
		return "", err
	}
	var resp struct {
		Signature string `json:"signature"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return "", fmt.Errorf("decode ows sign output: %w", err)
	}
	if resp.Signature == "" {
		return "", errors.New("ows sign output missing signature")
	}
	return resp.Signature, nil
}

// microsForRequirement returns the requirement's amount expressed as
// integer USDC base units (micro-USDC, 6 decimals). The value goes
// into the EIP-3009 authorization. The two wire shapes differ in
// units:
//
//   - v1 services use `maxAmountRequired` as a decimal USDC string
//     (e.g. "0.005" → 5000 micros)
//   - v2 services use `amount` as the integer base unit directly
//     (e.g. "1000" → 1000 micros = 0.001 USDC)
//
// We disambiguate by which field the wire populated: v2's `amount`
// is interpreted as base units, v1's `maxAmountRequired` as decimal.
// A string without a decimal point in the v1 field is treated as
// already-micros, matching the Solana path's tolerance.
func microsForRequirement(req PaymentRequirements) (string, error) {
	if v := strings.TrimSpace(req.Amount); v != "" {
		micros, err := parseIntegerBaseUnits(v)
		if err != nil {
			return "", fmt.Errorf("parse amount %q: %w", v, err)
		}
		return micros.String(), nil
	}
	v := strings.TrimSpace(req.MaxAmountRequired)
	if v == "" {
		return "", errors.New("requirement missing amount")
	}
	if !strings.Contains(v, ".") {
		// v1 server emitting integer base units in the v1 field.
		micros, err := parseIntegerBaseUnits(v)
		if err != nil {
			return "", fmt.Errorf("parse maxAmountRequired %q: %w", v, err)
		}
		return micros.String(), nil
	}
	micros, err := parseUSDC(v)
	if err != nil {
		return "", fmt.Errorf("parse maxAmountRequired %q: %w", v, err)
	}
	if micros.Sign() <= 0 {
		return "", fmt.Errorf("maxAmountRequired %q must be positive", v)
	}
	return micros.String(), nil
}

// parseIntegerBaseUnits parses an integer base-unit amount string
// like "1000" into a positive *big.Int. Rejects empty, non-digit, or
// non-positive values.
func parseIntegerBaseUnits(s string) (*big.Int, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return nil, errors.New("amount required")
	}
	if !isAllDigits(trimmed) {
		return nil, fmt.Errorf("amount %q must be integer base units", trimmed)
	}
	v, ok := new(big.Int).SetString(trimmed, 10)
	if !ok || v.Sign() <= 0 {
		return nil, fmt.Errorf("amount %q must be positive", trimmed)
	}
	return v, nil
}

// randomNonceHex returns a 0x-prefixed 32-byte hex nonce suitable
// for the bytes32 nonce field of EIP-3009.
func randomNonceHex() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return "0x" + hex.EncodeToString(buf[:]), nil
}

// normalizeSignature ensures the signature is 0x-prefixed for the
// wire format. OWS already emits prefixed signatures, but we
// defensively normalize so a future OWS change cannot silently
// produce a malformed payload.
func normalizeSignature(sig string) string {
	sig = strings.TrimSpace(sig)
	if strings.HasPrefix(sig, "0x") || strings.HasPrefix(sig, "0X") {
		return sig
	}
	return "0x" + sig
}

// marshalExactPayload wraps an ExactSchemePayload into the outer
// PaymentPayload envelope. Pulled out so FakeSigner and OWSSigner
// share encoding behavior.
func marshalExactPayload(req PaymentRequirements, exact ExactSchemePayload) (PaymentPayload, error) {
	inner, err := json.Marshal(exact)
	if err != nil {
		return PaymentPayload{}, err
	}
	return PaymentPayload{
		X402Version: X402ProtocolVersion,
		Scheme:      req.Scheme,
		Network:     req.Network,
		Payload:     inner,
	}, nil
}
