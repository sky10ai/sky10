package x402

import (
	"context"
	"errors"
	"fmt"
)

// ErrSignerNotConfigured indicates the host has no Signer wired in
// (typically: OWS not installed or not the bound wallet). Returned by
// the Backend when a Call would need to sign but cannot.
var ErrSignerNotConfigured = errors.New("x402: signer not configured")

// Signer turns a PaymentChallenge into a signed PaymentHeader the
// transport applies on retry. Implementations bridge to whichever
// wallet primitive the host has — OWS in production, a fake in
// tests.
//
// Signer.Sign must be safe to call concurrently from multiple
// in-flight envelopes; the underlying wallet is expected to handle
// its own concurrency.
type Signer interface {
	Sign(ctx context.Context, challenge PaymentChallenge) (PaymentHeader, error)
}

// FakeSigner is the test-side Signer. It produces deterministic
// signatures derived from the challenge nonce so test assertions are
// stable. The Signature field carries the literal string
// "fake-sig:<nonce>" — easy to recognize, easy to verify.
type FakeSigner struct{}

// NewFakeSigner constructs a FakeSigner.
func NewFakeSigner() FakeSigner { return FakeSigner{} }

// Sign implements Signer.
func (FakeSigner) Sign(_ context.Context, c PaymentChallenge) (PaymentHeader, error) {
	if c.Nonce == "" {
		return PaymentHeader{}, errors.New("fake signer: challenge missing nonce")
	}
	return PaymentHeader{
		Network:   c.Network,
		Amount:    c.Amount,
		Recipient: c.Recipient,
		Nonce:     c.Nonce,
		Signature: "fake-sig:" + c.Nonce,
	}, nil
}

// StubSigner is the placeholder signer for production. It returns
// ErrSignerNotConfigured for every call. NewBackend uses it as the
// default; daemon wiring replaces it with a real implementation
// (OWS-backed) when available.
//
// Keeping a non-nil Signer in the Backend means handlers don't have
// to nil-check; the failure mode is a clean typed error to the agent
// rather than a panic.
type StubSigner struct {
	Reason string
}

// NewStubSigner constructs a stub with an explanatory reason that is
// included in every Sign error.
func NewStubSigner(reason string) StubSigner {
	return StubSigner{Reason: reason}
}

// Sign implements Signer; always returns ErrSignerNotConfigured.
func (s StubSigner) Sign(_ context.Context, _ PaymentChallenge) (PaymentHeader, error) {
	if s.Reason != "" {
		return PaymentHeader{}, fmt.Errorf("%w: %s", ErrSignerNotConfigured, s.Reason)
	}
	return PaymentHeader{}, ErrSignerNotConfigured
}
