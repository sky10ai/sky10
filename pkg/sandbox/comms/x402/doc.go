// Package x402 is the per-intent bridge endpoint for metered service
// access from sandboxed agents. It mounts at /bridge/metered-services/ws
// and accepts a small set of envelope types whose handlers delegate to
// a Backend (typically pkg/x402 on the host).
//
// The endpoint is the agent-facing surface. The Backend implementation
// owns catalog, discovery, budget enforcement, and the actual x402/MPP
// 402-round-trip; none of that is reachable from the guest. Agents
// see a narrow envelope contract, nothing more.
//
// See docs/work/current/sandbox-bridge/ for the bridge architecture and
// docs/work/current/x402/ for the host-side x402 design behind the
// Backend interface.
package x402
