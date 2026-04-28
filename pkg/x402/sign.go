package x402

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	return s
}

// Sign implements Signer.
func (s *OWSSigner) Sign(ctx context.Context, req PaymentRequirements) (PaymentPayload, error) {
	if s == nil || s.SignTypedData == nil || s.AddressForChain == nil {
		return PaymentPayload{}, ErrSignerNotConfigured
	}
	if !isSupportedScheme(req.Scheme) {
		return PaymentPayload{}, fmt.Errorf("ows signer: unsupported scheme %q", req.Scheme)
	}
	network := strings.ToLower(strings.TrimSpace(req.Network))
	if network != string(NetworkBase) {
		// Solana signing requires its own scheme construction; until
		// that lands, refuse cleanly so the agent's routing rubric
		// can fall back.
		return PaymentPayload{}, fmt.Errorf("ows signer: network %q not yet supported", req.Network)
	}
	addr, err := s.AddressForChain(ctx, s.WalletName, network)
	if err != nil {
		return PaymentPayload{}, fmt.Errorf("ows signer: resolving wallet %q address: %w", s.WalletName, err)
	}
	if strings.TrimSpace(addr) == "" {
		return PaymentPayload{}, fmt.Errorf("ows signer: wallet %q has no %s address", s.WalletName, network)
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
	sig, err := s.SignTypedData(ctx, s.WalletName, network, tdJSON)
	if err != nil {
		return PaymentPayload{}, fmt.Errorf("ows signer: %w", err)
	}
	exact := ExactSchemePayload{
		Signature:     normalizeSignature(sig),
		Authorization: auth,
	}
	return marshalExactPayload(req, exact)
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

// microsForRequirement returns the maxAmountRequired field from the
// requirement, expressed as the integer USDC base unit (micro-USDC,
// 6 decimals). The value goes into the EIP-3009 authorization.
func microsForRequirement(req PaymentRequirements) (string, error) {
	amount := strings.TrimSpace(req.MaxAmountRequired)
	if amount == "" {
		return "", errors.New("requirement missing maxAmountRequired")
	}
	micros, err := parseUSDC(amount)
	if err != nil {
		return "", fmt.Errorf("parse maxAmountRequired %q: %w", amount, err)
	}
	if micros.Sign() <= 0 {
		return "", fmt.Errorf("maxAmountRequired %q must be positive", amount)
	}
	return micros.String(), nil
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
