---
created: 2026-04-26
updated: 2026-04-26
model: claude-opus-4-7
---

# x402 Implementation Plan

Milestones in dependency order. Each is a coherent unit of work with
its own tests; later milestones build on the contracts shipped by
earlier ones.

## Pre-flight

- [ ] Confirm answers to the open questions in the
  [README](README.md#open-questions).
- [ ] Verify state of `pkg/wallet/{base.go,evm.go}` — what is already
  capable on Base mainnet, what gaps need filling for SIWE + USDC
  transfers.
- [ ] Decide MCP server scope: same milestone or follow-up.

## M1 — protocol core

`pkg/x402` implements the protocol against a manually-pinned
manifest; no discovery yet.

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

`pkg/x402/discovery` adds catalog ingestion.

Files:

- `pkg/x402/discovery/client.go`
- `pkg/x402/discovery/refresh.go`
- `pkg/x402/discovery/diff.go`
- `pkg/x402/discovery/overlay.go` + `overlay.json`
- `pkg/x402/discovery/sources.go`
- Tests: diff classifier, refresh loop with fake source, overlay
  merge.

Exit criteria: `x402.refreshCatalog` populates the local registry from
the live `/v1/services` endpoint and per-service `/.well-known`
manifests.

## M3 — daemon RPC

`pkg/x402/rpc` wires the registry, transport, budget, and discovery
into the daemon's existing RPC dispatcher.

Files:

- `pkg/x402/rpc/handler.go`
- daemon integration: register `x402.*` handler alongside existing
  ones in `commands/`
- Tests: RPC contract tests for each method.

Exit criteria: `sky10 market list/search/call/approve/budget/receipts`
work from the CLI; manual call against a sandbox service succeeds.

## M4 — subwallet

Wallet binding so x402 calls only ever sign with the dedicated
subwallet.

Files:

- `pkg/wallet/x402.go` — derive, label, fund, refill
- daemon refuses `x402.serviceCall` against the main wallet
- Tests: derivation deterministic, balance and fund flows on testnet.

Exit criteria: a clean install can fund the subwallet, make a paid
call, and the main wallet is never touched.

## M5 — OpenClaw plugin

`external/runtimebundles/openclaw/sky10-openclaw/src/x402-tools.js`
registers approved services as OpenClaw tools.

Exit criteria: an OpenClaw agent makes a real paid call to a service
through the plugin; budget and policy errors surface as tool errors.

## M6 — MCP server

`cmd/sky10-x402-mcp` exposes the registry as MCP tools.

Exit criteria: a runtime configured with the MCP endpoint sees the
same tools as OpenClaw and can call them.

## M7 — Web UI

`web/src/x402/`:

- Services browser with search, tier filter, approval state
- Approve / revoke per service
- Budget panel with caps + today's spend + receipt log
- Changes panel (new / review / removed)

Exit criteria: a user can install sky10, fund the subwallet, approve
a service, and see receipts populate from the UI alone.

## M8 — telemetry and overlay tuning

- Receipt records carry `was_browser_attempted_first` from the
  agent's recent tool-call log.
- Aggregate dashboard surfaces "paid services that beat
  browse-it-yourself".
- Iterate `overlay.json` defaults from this signal.

## Out of scope for first cut

- Per-service typed Go clients (`pkg/x402/wrappers/...`) — driven
  later by usage signal.
- Non-x402 payment protocols.
- Replacing existing API-key flows where users already have
  credentials.
- Webhooks / SSE from agentic.market for push catalog updates (poll
  is fine to start).
