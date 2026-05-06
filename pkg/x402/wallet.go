package x402

import "errors"

// ErrWalletNotInstalled indicates OWS is not on the PATH (or the
// configured client is nil). Signers wrapping a missing wallet
// return this rather than producing a malformed authorization.
var ErrWalletNotInstalled = errors.New("x402: wallet not installed (ows binary missing or not in PATH)")

// ErrWalletNotFunded indicates the wallet's USDC balance is below
// the requirement amount and a signed authorization would be
// guaranteed to fail at on-chain settlement. Signers do this
// preflight check so callers see a clear typed error before any
// HTTP retry, instead of waiting for the upstream service to time
// out or return an opaque payment-failed response.
//
// The check is best-effort: when the underlying balance probe
// errors (e.g. RPC unreachable) the signer falls back to producing
// the authorization and lets the facilitator's settlement step
// surface any subsequent failure. ErrWalletNotFunded is reserved
// for the unambiguous "we read the balance and it's too low" case.
var ErrWalletNotFunded = errors.New("x402: wallet has insufficient USDC balance to settle this call")
