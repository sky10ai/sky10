---
created: 2026-05-08
updated: 2026-05-08
---

# Sandbox Bridge Host-Owned Metered Services

This archives the completed sandbox bridge current planning thread. The
remaining bridge follow-up is now tracked in
[`docs/work/todo/sandbox-bridge-followup.md`](../../../todo/sandbox-bridge-followup.md).

The bridge is the host-owned WebSocket path that lets a sandboxed agent
runtime use narrow host-side capabilities without the guest dialing host
callback addresses. The first capability is metered services/x402.

## Boundary

For Lima `user-v2` sandboxes:

- host sky10 dials guest sky10 over the host-owned forwarded guest endpoint
- guest sky10 must not dial host daemon RPC, host loopback, host gateway
  aliases, or host Unix sockets
- the sandbox bridge path does not use Skylink; Skylink remains for services
  and agents on different machines
- this path must not become generic RPC tunneling
- each capability gets its own WebSocket endpoint and handler set

Canonical first endpoint:

`/bridge/metered-services/ws`

The old `/comms/metered-services/ws` and `/comms/x402/ws` routes are not
mounted and are not supported bridge paths.

## What Landed

- [x] Folded the older sandbox-comms planning thread into the sandbox bridge
  architecture.
- [x] Made `pkg/sandbox/bridge/` the generic request/response frame package:
  envelope shape, endpoint helper, trusted identity stamping, replay checks,
  structured bridge errors, audit hooks, quota hooks, context-bound calls, and
  close semantics for pending requests.
- [x] Moved the old `pkg/sandbox/comms/*` package tree under
  `pkg/sandbox/bridge/*`.
- [x] Kept `pkg/sandbox/bridge/x402/` as the metered-services capability
  package for `x402.list_services`, `x402.budget_status`, and
  `x402.service_call`.
- [x] Removed legacy `/comms/...` metered-services route mounting.
- [x] Added a host-side sandbox bridge manager keyed by sandbox slug and
  capability.
- [x] Wired host sky10 to dial the guest `/bridge/metered-services/ws` endpoint
  after guest sky10 readiness and reconnect while the sandbox is alive.
- [x] Wired guest sky10 to record the host-opened socket as the active upstream
  for metered services and to replace dead upstreams.
- [x] Implemented the guest forwarding backend for list, budget, and call.
- [x] Returned `host_bridge_disconnected` when no upstream is attached.
- [x] Added the host metered-services bridge handler that stamps trusted
  sandbox identity and calls the existing `pkg/x402` backend.
- [x] Preserved x402 approval, budget, max-price, signer, receipt, and upstream
  payment handling in `pkg/x402`.
- [x] Updated OpenClaw to use the guest-local bridge route and approved service
  descriptors.
- [x] Updated Hermes to install a guest-local `sky10-x402` helper, inject
  approved service context into tool-call prompts, and avoid host callback
  URLs.
- [x] Added integration coverage for guest-local WebSocket client ->
  `/bridge/metered-services/ws` -> host fake x402 backend.
- [x] Regression-tested that host gateway/callback origins are rejected.
- [x] Updated `AGENTS.md` and `CLAUDE.md` with the Lima/VM boundary: the guest
  cannot and should not call the host directly.

## Architecture

```text
Guest VM

  Runtime adapter
  OpenClaw helper / Hermes bridge
     |
     | ws://127.0.0.1:9101/bridge/metered-services/ws
     v
  guest sky10
     |
     | pkg/sandbox/bridge/x402 validates envelope
     | guest bridge backend forwards over host-held socket
     v
  accepted WebSocket connection opened by host sky10
     ^
     | host dials guest forwarded sky10 endpoint
     |
Host sky10

  sandbox bridge manager
     |
     | metered-services handler
     v
  pkg/x402 backend
     |
     | registry, approvals, budget, wallet signing, receipts
     v
  upstream x402/MPP services
```

The only network dial across the VM boundary is host to guest. After the
WebSocket upgrade, the guest can send logical requests back over that
host-owned socket.

## Request Flow

1. Host sky10 starts or reconnects a sandbox.
2. Host sky10 dials the guest forwarded sky10 endpoint at
   `/bridge/metered-services/ws`.
3. Guest sky10 records that host-owned socket as the active upstream for
   metered services.
4. The runtime adapter opens guest-local `/bridge/metered-services/ws`.
   Sandbox guest daemons run with `SKY10_SANDBOX_GUEST=1`, so runtime calls are
   bridge-only and return `host_bridge_disconnected` until the host upstream is
   attached.
5. Guest sky10 validates the local x402 envelope.
6. Guest bridge backend forwards the typed request over the host-owned socket.
7. Host bridge handler stamps trusted identity and calls `pkg/x402`.
8. Host returns response and receipt metadata over the same socket.
9. Guest returns the response to the local agent connection.

## Package Map

- `pkg/sandbox/bridge/` - capability-neutral WebSocket transport, request and
  response frames, endpoint lifecycle, structured errors, and close semantics.
- `pkg/sandbox/bridge/x402/` - metered-services bridge capability, payload
  validation, guest forwarding backend, and host handler.
- `pkg/x402/` - host-side domain engine: service registry, discovery,
  Settings-enabled/user-approved services, per-agent approvals, budget
  authorization, x402/MPP transport, wallet signing, and receipt persistence.
- `commands/serve_x402.go` - daemon wiring for the metered-services bridge.
- OpenClaw and Hermes sandbox templates - runtime-facing helper/context wiring.

The x402 bridge subpackage intentionally stays named
`pkg/sandbox/bridge/x402` for now. A later rename to
`pkg/sandbox/bridge/meteredservices` should wait until another capability
endpoint exists and the naming boundary is clearer.

## Handler Discipline

The bridge handler rules from the current planning docs remain the review
standard for new capability endpoints:

1. No auto-binding. Payload is opaque bytes until a handler explicitly parses
   and validates it.
2. Identity is plumbing infrastructure, never payload. `agent_id`,
   `device_id`, timestamps, and nonce values are stamped by trusted transport
   plumbing.
3. One handler per file and one envelope type per handler.
4. Envelope type registration must declare metadata such as direction,
   payload limit, rate limit, nonce window, audit level, and handler.
5. Validation must happen before business logic.
6. Every handler file should carry a header comment making the sandboxed input
   boundary explicit.

These rules matter because JSON-RPC defaults, Go file organization habits, and
convenient deserialization all push toward code that feels more trusted than it
is. Capability endpoints must stay narrow and explicit.

## Relation To Chat

Agent chat remains separate.

```text
UI -> host sky10 /rpc/agents/{agent}/chat
host sky10 -> guest sky10 /rpc/agents/{agent}/chat
guest sky10 -> agent runtime
```

OpenClaw and Hermes keep their existing chat bridges. Metered services are
different because the runtime initiates the call inside the guest. The sandbox
bridge gives that local call a host-owned return path without letting the guest
dial the host.

## Relation To Skylink

Skylink is not part of this sandbox bridge path. It remains the cross-device
transport for services and agents on different machines. Sandbox bridge is
local host/guest VM plumbing.

## Non-Goals

- Replacing `/rpc/agents/{agent}/chat`.
- Replacing host-side daemon RPC used by CLI, UI, and host-local tools.
- Using Skylink for sandbox-local host/guest capability calls.
- Moving secrets over the bridge. Secrets are pre-staged into the VM at sandbox
  creation.
- Building one multiplexed "everything bridge" WebSocket.

## Branch Commits

- `f93cf2ea` `docs(sandbox): plan bridge milestones`
- `13513b9f` `docs(sandbox): fold comms plan into bridge`
- `f3833bff` `feat(sandbox): bridge metered x402 services`
- `33e2ba04` `fix(agent): route sandbox calls over direct bridge`
- `46534660` `fix(sandbox): reconnect declared bridge endpoints`
- `08b5a2c0` `refactor(sandbox): move comms under bridge`
- `b4c88f0d` `fix(sandbox): remove legacy comms x402 route`
- `f96be5a0` `fix(agent): stop surfacing sandbox bridge transport`
