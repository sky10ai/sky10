---
created: 2026-04-26
updated: 2026-05-07
---

# Sandbox Bridge Architecture

## High-Level Shape

```text
┌───────────────────────────────────────────────────────────────┐
│ Guest VM                                                      │
│                                                               │
│  Runtime adapter                                              │
│  OpenClaw helper / Hermes bridge                              │
│     │                                                         │
│     │ ws://127.0.0.1:9101/bridge/metered-services/ws          │
│     ▼                                                         │
│  guest sky10                                                  │
│     │                                                         │
│     │ pkg/sandbox/bridge/x402 validates envelope               │
│     │ guest bridge backend forwards over host-held socket     │
│     ▼                                                         │
│  accepted WebSocket connection opened by host sky10           │
└─────▲─────────────────────────────────────────────────────────┘
      │ host dials guest forwarded sky10 endpoint
      │
┌─────┴─────────────────────────────────────────────────────────┐
│ Host sky10                                                    │
│                                                               │
│  sandbox bridge manager                                      │
│     │                                                         │
│     │ metered-services handler                               │
│     ▼                                                         │
│  pkg/x402 backend                                             │
│     │                                                         │
│     │ registry, approvals, budget, wallet signing, receipts   │
│     ▼                                                         │
│  upstream x402/MPP services                                   │
└───────────────────────────────────────────────────────────────┘
```

The only network dial across the VM boundary is host to guest. After the
WebSocket upgrade, the guest can send logical requests back over that
host-owned socket.

## Route

Route:

`/bridge/metered-services/ws`

Old `/comms/...` metered-services routes are intentionally not mounted. The
important rule is that the path is per capability. We do not introduce a
generic `/bridge/ws` endpoint that multiplexes unrelated capabilities.

Future routes, if needed:

- `/bridge/wallet/ws`
- `/bridge/messengers/ws`

## Existing Packages

`pkg/sandbox/bridge/` is the already-built envelope plumbing:

- WebSocket endpoint lifecycle
- envelope struct
- type registry
- trusted identity stamping
- replay protection
- audit logging
- per-agent/type quota
- handler dispatch

`pkg/sandbox/bridge/x402/` is the already-built metered-services
capability layer:

- `x402.list_services`
- `x402.budget_status`
- `x402.service_call`
- payload validation before business logic
- adapter interface named `Backend`
- guest forwarding backend for calls from runtime adapters
- host bridge handler that stamps trusted sandbox identity

`pkg/x402/` is the host-side domain engine:

- service registry and discovery
- settings-enabled/user-approved services
- per-agent approvals and pins
- budget authorization
- x402 and MPP transport
- wallet signing
- receipt persistence

`pkg/sandbox/bridge/` is the capability-neutral host-owned WebSocket
transport used by the metered-services bridge:

- request/response frame shape
- structured bridge errors
- context-bound calls
- close semantics that fail pending calls

The bridge reuses the bridge/x402 and pkg/x402 packages rather than replacing
them.

## Remaining Pieces

The first implementation slice now has the core packages in place. Remaining
work is operational and smoke coverage:

- live sandbox smoke for OpenClaw and Hermes
- live x402 smoke with real USDC behind explicit env/build guards
- optional bridge status in persisted sandbox state
- optional authentication hardening for the host-opened upstream socket
- eventual package rename cleanup after the capability is proven

## Identity

Identity must be host-stamped at the bridge boundary. Agents must not be
able to supply `agent_id` or `device_id` in payloads and have those values
trusted.

Existing `pkg/sandbox/bridge` already enforces the right handler shape:
handlers see an `Envelope` with `AgentID` and `DeviceID` stamped by the
transport layer. The bridge work must preserve that invariant when a
guest-local request is forwarded to the host.

For the current bridge slice, the host bridge manager maps the bridge
connection to the sandbox record and the host handler stamps the trusted
sandbox slug as the x402 agent identity. Payload identity remains untrusted
and cannot override that host-stamped value.

## Request Flow

1. Host sky10 starts or reconnects a sandbox.
2. Host sky10 dials the guest forwarded sky10 endpoint at the metered
   services bridge route.
3. Guest sky10 records that host-owned socket as the active upstream for
   metered services.
4. The runtime adapter opens guest-local `/bridge/metered-services/ws`.
   Sandbox guest daemons run with `SKY10_SANDBOX_GUEST=1`, so the endpoint is
   bridge-only and returns `host_bridge_disconnected` until the host upstream
   is attached.
5. Guest sky10 validates the local x402 envelope with existing
   `pkg/sandbox/bridge/x402` handlers or equivalent handler code.
6. Guest bridge backend forwards the typed request over the host-owned
   socket.
7. Host bridge handler stamps trusted identity and calls `pkg/x402`.
8. Host returns response and receipt metadata over the same socket.
9. Guest returns the response to the local agent connection.

## Error Flow

The guest-local endpoint should return structured errors for:

- host bridge disconnected
- request timeout
- malformed payload
- unregistered envelope type
- service disabled or not approved
- budget exceeded
- signer missing
- upstream payment rejected

The agent should see useful routing errors, not transport internals or host
addresses.

## Relation To Chat

Existing agent chat remains separate.

Chat today is a host-side WebSocket proxy:

```text
UI -> host sky10 /rpc/agents/{agent}/chat
host sky10 -> guest sky10 /rpc/agents/{agent}/chat
guest sky10 -> agent runtime
```

OpenClaw and Hermes keep their existing chat bridges. Metered services are
different because the runtime initiates the call inside the guest. The bridge
gives that local call a host-owned return path without letting the guest dial
the host.

## Relation To Skylink

Skylink is not part of this sandbox bridge path. It remains the cross-device
transport for services and agents on different machines. Sandbox bridge is
local host/guest VM plumbing.
