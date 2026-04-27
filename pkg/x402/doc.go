// Package x402 implements the host-side x402 protocol logic, service
// catalog, approval policy, manifest pinning, and per-agent budget
// enforcement. It is the Backend that pkg/sandbox/comms/x402/ delegates
// to: agent envelope handlers do nothing on their own; everything that
// touches the wallet, the catalog, or the budget happens here.
//
// The package is split along single-responsibility lines:
//
//   - protocol.go    wire types: PaymentChallenge, PaymentReceipt,
//     ServiceManifest
//   - policy.go      Tier, ApprovalState, per-service approval record
//   - pin.go         Pin record + enforcement on every Call
//   - registry.go    in-memory service catalog with read-mostly access
//   - registry_store.go  on-disk JSON cache; survives restart
//   - budget.go      per-call cap, per-service cap, daily total cap,
//     receipt log
//   - sign.go        Signer interface + OWS-backed implementation
//   - transport.go   http.RoundTripper that detects 402, signs via the
//     Signer, and retries with X-PAYMENT
//   - backend.go     implements comms/x402.Backend; wires the pieces
//     above into the agent-facing handlers
//
// See docs/work/current/x402/ for the design and rationale.
package x402
