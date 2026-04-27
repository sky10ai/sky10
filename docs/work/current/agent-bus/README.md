---
created: 2026-04-26
updated: 2026-04-26
model: claude-opus-4-7
---

# Agent Bus

A typed-envelope message bus that carries every cross-trust-boundary
operation in sky10 — agent ↔ host, host ↔ host across devices, runtime
↔ runtime — replacing the temptation to expose a generic RPC surface
to anything outside the host trust zone.

## Goal

Give sandboxed agents and other untrusted runtimes the operations they
need (read messages, send messages, query wallet, sign x402 payments,
fetch scoped tokens) **without** ever exposing a generic, reachable
RPC surface that can be coaxed into doing more than its menu suggests.
The bus is the single, narrow conduit; every operation rides on it as
a typed envelope; each envelope type is its own audited capability
unit.

## Why this, not RPC

We tried direct guest → host RPC. It was simple. It also collapsed
sandbox isolation — once an agent could reach `/tmp/sky10/sky10.sock`,
"sandbox" was theatre, because the agent could call `sandbox.list`,
`agent.list`, `skylink.call` to other peers, and so on. We pulled
back. See [`docs/learned/sandbox-rpc-isolation.md`](../../../learned/sandbox-rpc-isolation.md).

We considered an allowlist + per-agent policy + scope injection on top
of the existing RPC. That works as a defense, but the *default frame*
when you write a method is "my caller is authenticated, my args are
typed, I can trust them." Skepticism has to be added; it leaks out.

The bus inverts the default. Envelope handlers receive a raw,
identity-stamped, opaque payload. Their first move is parsing — they
*see* the payload crossed a boundary, the way an HTTP handler sees a
request. The architecture and the developer's brain align on
"untrusted by default."

This is not a categorical primitive-level security win — a bug in the
bus is still bad. It's a frame win, and we lock the frame in
structurally so it doesn't drift back to RPC-shaped trust.

## Non-goals

- Bulk file transfer. Drive sync (`pkg/fs`) stays separate. The bus
  is a control plane for modest payloads, not a file pipe.
- Replacing the daemon's local unix-socket RPC for *host-side*
  consumers (CLI, Web UI, host tools). Those continue to use the
  existing RPC surface. The bus is specifically for the cross-trust-
  boundary cases.
- A new wire protocol. We reuse `pkg/agent/chat_websocket.go` +
  `message_hub.go` + `router.go` and generalize them. No new
  framing, no new transport.

## Scope

| Area | In scope |
|---|---|
| Envelope spec | type, identity (bus-stamped), request_id, ts, nonce, payload |
| Transport | reuse chat_websocket (host-local) and skylink (cross-device) |
| Identity injection | bus stamps `agent_id` and `device_id` from authenticated channel; payload cannot lie |
| Replay protection | nonce window + ts skew tolerance |
| Backpressure | per-type queues, per-agent rate limits, drop policy, `bus.busy` signal |
| Handler discipline | six rules locking in secure-by-default frame; arch-test enforced |
| Sync-on-async | request_id correlation pattern for envelopes that need a synchronous answer (x402, wallet.transfer) |
| Initial envelope types | chat, messaging, wallet, secrets (narrow), x402, home |
| Migration | chat_websocket → bus_websocket with backward-compat shim for one release |

## Status

- 2026-04-26 — plan drafted.

## Documents

- [Architecture](architecture.md) — transport, envelope spec, identity, sync-on-async, replay, where drive sync stays separate
- [Handler discipline](handler-discipline.md) — the six rules and why each is structural, not stylistic
- [Envelope types](envelope-types.md) — initial registry: chat, messaging, wallet, secrets, x402, home
- [Migration](migration.md) — how the existing chat websocket evolves to the generalized bus
- [Implementation plan](implementation-plan.md) — milestones in dependency order

## Related

- [`docs/learned/sandbox-rpc-isolation.md`](../../../learned/sandbox-rpc-isolation.md)
  — the historical decision to abandon direct guest → host RPC.
- [`docs/work/current/x402/`](../x402/) — the x402 plan, now scoped as
  a thin envelope-type layer on top of the bus.

## Open questions

1. **Backward-compat window** — keep the legacy chat_websocket shape
   working for one release after migration, or cut over hard?
2. **Per-agent quota defaults** — what are sensible defaults for
   tokens/sec across the initial envelope types?
3. **Audit log location** — append to existing daemon logs, or
   dedicated `bus.jsonl` under `os.UserConfigDir()`?
4. **Cross-device identity** — for envelopes that traverse skylink
   between devices, do we re-stamp `device_id` at each hop, or carry
   the originating device through?
5. **Drop policy** — oldest-first when a queue fills, or newest-first?
   Different envelope types may want different defaults.
