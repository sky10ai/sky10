---
created: 2026-04-26
updated: 2026-04-26
model: claude-opus-4-7
---

# x402 Implementation Plan

Milestones in dependency order. Each is a coherent unit of work with
its own tests; later milestones build on the contracts shipped by
earlier ones.

x402's agent-facing surface lives at `pkg/sandbox/comms/x402/` —
a per-intent websocket endpoint at `/comms/x402/ws` whose handlers
delegate to `pkg/x402`. See
[sandbox-comms](../sandbox-comms/) for the per-intent endpoint
architecture and shared transport plumbing. The shared plumbing
(M1 of sandbox-comms) must land before the x402 endpoint can; M1–M4
of x402 below are pure host-side work and can run in parallel.

## Pre-flight

- [ ] Confirm answers to the open questions in the
  [README](README.md#open-questions).
- [ ] Verify state of `pkg/wallet/{base.go,evm.go}` — what is already
  capable on Base mainnet, what gaps need filling for SIWE + USDC
  transfers.
- [ ] Confirm [sandbox-comms M1](../sandbox-comms/implementation-plan.md#m1-shared-transport-plumbing)
  is the foundation this rides on.

## M1 — protocol core

`pkg/x402` implements the protocol against a manually-pinned
manifest; no discovery yet. **Host-side; not blocked by agent bus.**

Files:

- `pkg/x402/doc.go`
- `pkg/x402/protocol.go` — `PaymentChallenge`, `PaymentReceipt`,
  `ServiceManifest`, `Tier`
- `pkg/x402/transport.go` — `http.RoundTripper`
- `pkg/x402/sign.go` — SIWE + SIWS via `pkg/wallet`
- `pkg/x402/registry.go` + `pkg/x402/registry_store.go`
- `pkg/x402/budget.go`
- `pkg/x402/policy.go`
- `pkg/x402/pin.go`
- Tests: 402 round-trip with `httptest`, signing fixtures, pin
  enforcement, budget gate.

Exit criteria: a Go test makes a real x402 call against a sandbox
service (or an `httptest` fake) end-to-end with USDC on a Base
testnet.

## M2 — discovery and refresh

`pkg/x402/discovery` adds catalog ingestion. **Host-side.**

Files:

- `pkg/x402/discovery/client.go`
- `pkg/x402/discovery/refresh.go`
- `pkg/x402/discovery/diff.go`
- `pkg/x402/discovery/overlay.go` + `overlay.json`
- `pkg/x402/discovery/sources.go`
- Tests: diff classifier, refresh loop with fake source, overlay
  merge.

Exit criteria: a manual host-side refresh populates the local
registry from the live `/v1/services` endpoint and per-service
`/.well-known` manifests.

## M3 — host-side RPC for CLI / Web UI

The host's existing unix-socket RPC gets the `x402.*` handlers so
that **host-side consumers** (CLI, Web UI, host tools) can drive
catalog management, approval, and budget. Sandboxed agents do not
use this surface; they use the bus envelopes (see M5).

Files:

- `pkg/x402/rpc/handler.go`
- daemon integration: register `x402.*` handler in `commands/serve.go`
  (host-only — guarded so guest sky10 instances do **not** register
  it; guest x402 calls use the agent bus, which proxies to host)
- Tests: RPC contract tests; guard test that confirms guest does not
  register the handler.

Exit criteria: `sky10 market list/search/approve/budget/receipts`
work from the CLI on the host; manual call against a sandbox service
succeeds.

## M4 — subwallet integration

Even though the user landed on "use existing OWS wallet directly"
(see [`wallet-and-budget.md`](wallet-and-budget.md)), x402 still
needs to gate calls on the wallet being installed and funded.

Files:

- `pkg/x402/wallet.go` — preflight checks (OWS installed, funded for
  the network the call needs)
- Tests: preflight failure modes return clear typed errors.

Exit criteria: an unfunded wallet causes `x402.service_call` to
return `wallet_not_funded` cleanly; the catalog remains browsable
without funding.

## M5 — x402 endpoint on sandbox comms

This is the agent-facing surface. **Blocked by
[sandbox-comms M1](../sandbox-comms/implementation-plan.md#m1-shared-transport-plumbing)
reaching exit. Tracked there as M2 of the sandbox-comms plan.**

Files (in `pkg/sandbox/comms/x402/`):

- `endpoint.go` — registers `/comms/x402/ws` and the four envelope types
- `list_services.go` — `x402.list_services` handler
- `service_call.go` — `x402.service_call` handler (sync, idempotent on payment-binding nonce)
- `budget_status.go` — `x402.budget_status` handler
- `changes.go` — `x402.changes` push (host-initiated)
- Each handler delegates to `pkg/x402` with `requester = env.AgentID`
  for scope filtering
- Daemon wiring in `commands/serve.go` to register the endpoint
  (host only; explicitly **not** registered in guest sky10
  instances)
- Tests: scope (agent only sees services it has been approved for),
  budget enforcement, sync-on-async correlation, idempotency on the
  payment-binding nonce

Exit criteria: an agent inside a Lima VM makes a real paid call to a
service via `/comms/x402/ws`. The wallet stays on host. The agent
never sees the 402 challenge.

## M6 — Web UI

`web/src/x402/`:

- Services browser with search, tier filter, approval state
- Approve / revoke per service
- Budget panel with caps + today's spend + receipt log
- Changes panel (new / review / removed)
- Wallet status banner: "wallet not funded — agents cannot call x402
  services until funded"

Exit criteria: a user can install sky10, fund the wallet, approve a
service, and see receipts populate from the UI alone.

## M7 — telemetry and overlay tuning

- Receipt records carry `was_browser_attempted_first` from the
  agent's recent tool-call log (sourced from the bus audit log).
- Aggregate dashboard surfaces "paid services that beat
  browse-it-yourself".
- Iterate `overlay.json` defaults from this signal.

## M8 — quality and reputation (deferred)

Detect (not just bound) malicious or broken services. See
[`threat-model.md`](threat-model.md) for the full threat list.

- Outcome scoring on every call: caller reports via a follow-up
  envelope (`x402.call_outcome`) whether the response was usable;
  aggregate into a per-service quality score persisted with receipts.
- Auto-quarantine for services with sustained low quality.
- Volume anomaly detection on the receipt log.
- Per-agent-per-service caps so one runtime cannot dominate spend.
- Optional opt-in shared reputation feed (Sybil-resistant design is
  its own problem and is out of scope for the first version).

## Out of scope for first cut

- Per-service typed Go clients (`pkg/x402/wrappers/...`) — driven
  later by usage signal.
- Non-x402 payment protocols.
- Replacing existing API-key flows where users already have
  credentials.
- Webhooks / SSE from agentic.market for push catalog updates (poll
  is fine to start).
- A standalone MCP server for x402. Removed: agents go through the
  bus, not through MCP. If a future runtime really needs MCP, we
  add an MCP→bus adapter then.
