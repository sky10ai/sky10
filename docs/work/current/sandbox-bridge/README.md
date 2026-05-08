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

- `pkg/sandbox/bridge/` for envelope, identity stamping, replay, audit,
  quota, and per-type handler plumbing
- `pkg/sandbox/bridge/x402/` for x402 envelope validation and handler
  shape
- `pkg/x402/` for the actual service catalog, approvals, budgets,
  wallet signing, receipts, and upstream x402/MPP calls

This directory replaces the older `sandbox-comms` planning thread. The
capability plumbing has been moved into `pkg/sandbox/bridge`; the remaining
package-name question is whether the x402-specific subpackage should stay as
`bridge/x402` or move to a more capability-named path later.

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

The old `/comms/metered-services/ws` and `/comms/x402/ws` routes are not
mounted. They are not supported bridge paths.

## Working Model

```text
Runtime adapter inside guest
  (OpenClaw helper, Hermes bridge, etc.)
  -> guest-local sky10 /bridge/metered-services/ws
  -> pkg/sandbox/bridge/x402 validates x402 envelopes
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
OpenClaw and Hermes sandbox templates mark guest sky10 with
`SKY10_SANDBOX_GUEST=1`, which disables the local x402 payment backend for
runtime calls and forces use of the host-opened bridge.

## Existing Work To Keep

| Area | Status | Notes |
|---|---:|---|
| `pkg/sandbox/bridge/` | done | Generic request/response framing plus endpoint helper, envelope shape, identity stamping, replay checks, audit, quota, tests |
| `pkg/sandbox/bridge/x402/` | done | `x402.list_services`, `x402.budget_status`, `x402.service_call` handlers, validation, guest forwarding backend, and host bridge handler |
| `pkg/x402/` | done enough for bridge | Registry, discovery overlay, approvals/user-enabled services, budgets, transport, wallet signer, receipts |
| OpenClaw helper | wired | Lists approved services and calls `list`, `budget`, and `call` through the guest-local bridge route |
| Hermes bridge | wired | Installs a `sky10-x402` guest helper, injects approved service context into tool-call prompts, and uses the guest-local bridge route |
| Host-owned bridge | wired | Daemon connects after guest readiness and reconnects while the sandbox is alive |
| Guest forwarding backend | wired | Guest-local metered-service calls forward over the host-opened socket |

## Documents

- [Architecture](architecture.md) - final host/guest bridge shape and how
  the existing bridge/x402 code fits
- [Handler discipline](handler-discipline.md) - rules for capability
  envelope handlers; still applies to `pkg/sandbox/bridge/x402`
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

1. Should `pkg/sandbox/bridge/x402` move to
   `pkg/sandbox/bridge/meteredservices` after more capabilities land?
2. How should the host-opened connection be authenticated or attached:
   lifecycle-owned dial only, WebSocket subprotocol/header, or a
   daemon-issued ephemeral handshake?
3. Should bridge status also appear in persisted sandbox state, or are
   lifecycle logs enough for the first cut?
4. Should Hermes x402 descriptors later move from prompt/tool context into
   generated `bridge.json`, `/shared/agent-manifest.json`, or both?
5. What live-sandbox smoke should gate the first real-USDC x402 run?
