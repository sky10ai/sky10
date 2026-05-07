---
created: 2026-04-26
updated: 2026-05-07
---

# Sandbox Bridge

Sandbox bridge is the host-owned WebSocket path that lets a sandboxed
agent runtime use narrow host-side capabilities without the guest dialing
host callback addresses.

The first capability is metered services/x402. It should reuse the work
already done in:

- `pkg/sandbox/comms/` for envelope, identity stamping, replay, audit,
  quota, and per-type handler plumbing
- `pkg/sandbox/comms/x402/` for x402 envelope validation and handler
  shape
- `pkg/x402/` for the actual service catalog, approvals, budgets,
  wallet signing, receipts, and upstream x402/MPP calls

This directory replaces the older `sandbox-comms` planning thread. The
package name has not been renamed yet; the current plan is to build the
missing bridge around the existing `pkg/sandbox/comms` internals first,
then decide whether a package rename is worth the churn.

## Boundary

For Lima `user-v2` sandboxes:

- host sky10 dials guest sky10 over the existing host-owned forwarded
  guest endpoint
- guest sky10 must not dial host daemon RPC, host loopback, host gateway
  aliases, or host Unix sockets
- this path must not use Skylink; Skylink remains for services and agents
  on different machines
- this path must not become generic RPC tunneling
- each capability gets its own WebSocket endpoint and handler set

Canonical first endpoint:

`/bridge/metered-services/ws`

Existing code currently uses `/comms/metered-services/ws`; renaming that
route is an explicit milestone, not completed work.

## Working Model

```text
Runtime adapter inside guest
  (OpenClaw helper, Hermes bridge, etc.)
  -> guest-local sky10 /bridge/metered-services/ws
  -> pkg/sandbox/comms/x402 validates x402 envelopes
  -> guest bridge forwards request over host-opened socket

Host sky10
  -> owns the socket into the guest
  -> receives the forwarded metered-services request
  -> stamps trusted sandbox/agent identity
  -> calls pkg/x402
  -> returns response/receipt over the same socket
```

The runtime sees only the guest-local bridge endpoint. It never sees host
URLs, wallet state, x402 challenges, payment headers, or host sockets.

## Existing Work To Keep

| Area | Status | Notes |
|---|---:|---|
| `pkg/sandbox/comms/` | done | WebSocket endpoint helper, envelope shape, identity stamping, replay checks, audit, quota, tests |
| `pkg/sandbox/comms/x402/` | done | `x402.list_services`, `x402.budget_status`, `x402.service_call` handlers and validation |
| `pkg/x402/` | done enough for bridge | Registry, discovery overlay, approvals/user-enabled services, budgets, transport, wallet signer, receipts |
| OpenClaw helper | partial | Can list/budget/call through a WebSocket endpoint, but still needs the final bridge route/defaults |
| Hermes bridge | partial | Already registers Hermes with sky10 and reads tools from `bridge.json` or `/shared/agent-manifest.json`; needs x402 tool metadata and bridge calls wired to the guest-local endpoint |
| Host-owned bridge | missing | Host must dial guest and hold the capability socket |
| Guest forwarding backend | missing | Guest must forward local metered-service calls over the host-opened bridge |

## Documents

- [Architecture](architecture.md) - final host/guest bridge shape and how
  the existing comms/x402 code fits
- [Handler discipline](handler-discipline.md) - rules for capability
  envelope handlers; still applies to `pkg/sandbox/comms/x402`
- [Milestones](milestones.md) - checklist for finishing the bridge
- [Implementation plan](implementation-plan.md) - short index of done vs
  remaining implementation work

## Non-Goals

- Replacing `/rpc/agents/{agent}/chat`. Existing OpenClaw and Hermes chat
  paths remain their own host-to-guest WebSocket proxies.
- Replacing host-side daemon RPC used by CLI, UI, and host-local tools.
- Using Skylink for sandbox-local host/guest capability calls.
- Moving secrets over the bridge. Secrets are pre-staged into the VM at
  sandbox creation.
- Building one multiplexed "everything bridge" WebSocket.

## Open Questions

1. Should the final Go package be renamed from `pkg/sandbox/comms` to
   `pkg/sandbox/bridge` after x402 works end to end?
2. How should the host-opened connection be authenticated or attached:
   lifecycle-owned dial only, WebSocket subprotocol/header, or a
   daemon-issued ephemeral handshake?
3. Should guest-local agent callers use the same route as the host-held
   upstream connection, or should the host-held connection be internal-only
   while the agent-facing route remains `/bridge/metered-services/ws`?
4. Where should bridge status appear: sandbox state, daemon logs, or both?
5. What is the exact failure response when the host bridge is disconnected?
6. Should Hermes x402 tools be written into generated `bridge.json`,
   `/shared/agent-manifest.json`, or both?
