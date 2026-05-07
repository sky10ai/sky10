---
created: 2026-04-26
updated: 2026-05-07
model: claude-opus-4-7
---

# Sandbox Comms

Per-intent websocket endpoints that carry cross-trust-boundary
operations between sandboxed agents and the host daemon. For Lima
`user-v2` sandboxes, this must ride a host-owned connection to the
guest; the guest must not open a direct callback to host services
through gateway aliases or loopback shortcuts. The architecture exists
*because of* sandbox isolation; the package lives at
`pkg/sandbox/comms/` to make that dependency direction explicit.

## Why this is in `pkg/sandbox/`, not `pkg/bus/`

A neutral name like "bus" obscured what this is for. The shared
plumbing in `pkg/sandbox/comms/` only exists to give sandboxed
agents a narrow, hardened way to reach host capabilities they
otherwise couldn't reach safely. That's a sandbox concern. Naming
it after sandboxes makes the design constraint visible to anyone
reading the import path.

## Shape

Each capability gets its own websocket endpoint, its own subpackage,
its own envelope handlers. URL path is the dispatcher. There is no
unified bus.

```
pkg/sandbox/comms/                    # shared plumbing (M1)
  conn.go         envelope.go         identity.go
  replay.go       audit.go            quota.go
  endpoint.go

pkg/sandbox/comms/x402/               # x402 capability (M2, this branch)
  endpoint.go     list_services.go
  service_call.go budget_status.go    changes.go

pkg/sandbox/comms/wallet/             # future, not in this branch
pkg/sandbox/comms/messengers/         # future, not in this branch
```

URL paths: `/comms/metered-services/ws`, future `/comms/wallet/ws`,
`/comms/messengers/ws`. These paths describe per-capability envelope
contracts, not permission for the guest to dial host loopback or
gateway aliases directly. Existing chat at `/rpc/agents/{agent}/chat`
is **not touched**.

## Why per-intent and not one bus

We considered a single endpoint that multiplexes envelope types.
Even with strict handler discipline, a single endpoint drifts back
to RPC over time. Adding capability N+1 to an existing bus is a
small, low-friction change that always feels small; six months in
the bus is functionally the host's RPC surface in a different
costume. Per-intent endpoints put friction in the right place: a
new capability requires a new URL, a new subpackage, a new
endpoint registration. That friction is the feature. It forces the
question "is this really its own capability?" to be answered every
time, not just at design review.

See [`docs/learned/sandbox-rpc-isolation.md`](../../../learned/sandbox-rpc-isolation.md)
for the historical lesson behind this.

## Goals

- Sandboxed agents reach **only** the capabilities they need, over a
  host-owned guest connection, through endpoints that physically
  cannot serve other capabilities.
- Identity is bus-stamped at each endpoint from the authenticated
  websocket — payloads cannot lie about who they're from.
- Per-handler discipline rules keep each handler narrow and
  visibly defensive.
- Adding a new capability is a deliberate architectural moment.
- The shared plumbing in `pkg/sandbox/comms/` is small enough to
  audit by hand and stable enough to import without surprises.

## Non-goals (this branch)

- **Wallet capability.** Future branch. Plumbing is generic enough
  to support it; we just don't write the handlers here.
- **Messengers capability.** Future branch. The host-side broker
  in `pkg/messaging/broker` already has the requester-scoping
  primitive needed; the comms layer ships when we add it.
- **Secrets capability.** Not via comms at all. Secrets come into
  the VM via env-var mount at sandbox creation — see
  `pkg/sandbox/openclaw_env.go` and `ResolveOpenClawProviderEnv`.
  Pre-stage at boot, never fetch at runtime.
- **Chat migration.** `/rpc/agents/{agent}/chat` and
  `pkg/agent/chat_websocket.go` stay as-is. Comms is purely
  additive.
- **Replacing host-side unix-socket RPC.** The existing
  daemon RPC continues to serve CLI, Web UI, and host tools.
  Comms is specifically the path for sandboxed-agent → host
  capabilities.

## Scope (this branch)

| Area | In scope |
|---|---|
| `pkg/sandbox/comms/` | shared plumbing: envelope, identity, replay, audit, quota, connection lifecycle, endpoint helper |
| `pkg/sandbox/comms/x402/` | x402 endpoint + envelope handlers (list_services, service_call, budget_status, changes) |
| Discipline | six handler rules; arch-test enforcing validation-first |
| Wire compat | none needed — additive only |
| Cross-device | uses skylink for envelopes targeted at remote daemons; transparent to handlers |

## Status

- 2026-04-26 — plan drafted; reflects per-intent decision and
  narrower branch scope (x402 only).
- 2026-04-27 — M1 (shared transport plumbing) landed in
  `pkg/sandbox/comms/`. Envelope spec, identity injection, replay
  protection, audit log, per-(agent, type) quotas, and connection
  lifecycle all in place with tests. The `arch_test.go` slipped to
  M2 because it has nothing to scan until the first capability
  subpackage exists.
- 2026-04-27 — M2 (x402 capability) handlers landed in
  `pkg/sandbox/comms/x402/` against a `Backend` interface. Three
  envelope types (`list_services`, `service_call`, `budget_status`)
  with full test coverage including a structural test that the
  bus-stamped `AgentID` cannot be smuggled through the payload.
  The arch test scans the x402 subpackage and enforces validation-
  first. Real `pkg/x402` Backend implementation and daemon wiring
  follow under the [x402 plan](../x402/).

## Documents

- [Architecture](architecture.md) — transport plumbing, envelope
  spec, per-intent endpoint pattern, x402 capability shape
- [Handler discipline](handler-discipline.md) — the six rules
- [Implementation plan](implementation-plan.md) — milestones for
  this branch (M1 plumbing, M2 x402); future capabilities listed
  but not specced

## Related

- [`docs/learned/sandbox-rpc-isolation.md`](../../../learned/sandbox-rpc-isolation.md)
  — why we don't expose generic RPC to sandboxed agents and why a
  single bus would drift back to that anyway.
- [`docs/work/current/x402/`](../x402/) — x402 catalog, discovery,
  budget, and host-side logic. The handlers in
  `pkg/sandbox/comms/x402/` delegate to `pkg/x402/`.

## Open questions

1. **Connection auth** — websocket auth token, libp2p-signed
   handshake from inside the VM, or both?
2. **Audit log location** — append to existing daemon logs, or
   dedicated `comms.jsonl` under `os.UserConfigDir()`?
3. **Per-(agent, type) quota defaults** — concrete numbers for
   x402 envelope types?
4. **Cross-device identity hops** — for envelopes that traverse
   skylink between devices, do we re-stamp `device_id` at each
   hop, or carry the originating device through?
5. **Drop policy** when a per-type queue fills — oldest-first
   or newest-first?
