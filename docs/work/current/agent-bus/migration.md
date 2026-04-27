---
created: 2026-04-26
updated: 2026-04-26
model: claude-opus-4-7
---

# Migration: chat_websocket → Agent Bus

We do not build a second transport. We generalize the existing one.
`pkg/agent/chat_websocket.go` carries chat-shaped messages today; we
extend it to carry envelopes, treat existing chat messages as a
specific envelope type family, and add new types incrementally.

## Today

- `pkg/agent/chat_websocket.go` — websocket server for OpenClaw and
  similar runtimes
- `pkg/agent/types.go` defines a `Message` shape with `Type` ∈
  `{"text", "tool_call", "diff", "permission", "done"}`
- `pkg/agent/router.go` — local + cross-device routing
- `pkg/agent/message_hub.go` — fan-out to subscribers

These run on every sky10 daemon, host or guest. The wire format is
a JSON message that already includes `From`, `To`, `DeviceID`,
`Type`, `Content`, `SessionID`, `Timestamp`.

## Target

- `pkg/bus/` — new package containing transport adapter, dispatcher,
  registry, audit log, quotas, replay protection
- `pkg/bus/envelopes/` — one file per envelope type
- `pkg/agent/chat_websocket.go` becomes a thin shim that delegates to
  `pkg/bus` for envelope handling, keeping its existing API for
  upgrade hooks and connection lifecycle
- `pkg/agent/types.go` — `Message` becomes a thin alias / synonym for
  the chat-family envelopes; existing fields map cleanly:
  - `Message.Type` → `Envelope.Type` (with `chat.` prefix)
  - `Message.From` → `Envelope.AgentID` (re-derived; was caller-supplied)
  - `Message.DeviceID` → `Envelope.DeviceID`
  - `Message.Content` → `Envelope.Payload`
  - `Message.Timestamp` → `Envelope.Ts`

## Wire compatibility

For one release after the bus lands, the websocket accepts both:

1. **Legacy chat shape** — existing `Message{Type:"text", ...}` JSON.
   The bus normalizes it: `Type:"text"` → envelope `chat.send`;
   `From` is dropped and re-stamped from authenticated channel; etc.
2. **New envelope shape** — explicit `{type, agent_id, payload, ...}`.

Outbound (host → agent) follows the same rule: legacy clients receive
legacy-shaped JSON; new clients negotiate envelope shape via a
connection upgrade hint (e.g. a header on the websocket handshake or
an opening `bus.hello` envelope).

After one release, the legacy shape is removed. Connections that
haven't upgraded fail loudly so the operator notices.

## Migration order

This is the order in which envelope types come online. Each step is
independently shippable.

1. **Bus core lands.** `pkg/bus/` exists. `chat_websocket.go`
   delegates to it. Six chat envelope types are registered. Wire
   compatibility shim is in place. No new capabilities yet — just
   the new pipe carrying old traffic.
2. **Backpressure + audit log.** Per-type queues, per-agent rate
   limits, audit log file. Validated under load with chat traffic
   alone.
3. **Wallet envelopes.** `wallet.balance_subscribe`, `wallet.transfer`.
   First non-chat envelopes; first sync-on-async use case.
4. **Messaging envelopes.** `messaging.search`, `messaging.send`,
   `messaging.message_arrived`. Hooked into `pkg/messaging/broker`
   with `requester = agent_id`. Drive-sync wiring for inbound
   message bodies.
5. **Secrets (narrow) envelopes.** `secrets.issue_scoped_token`,
   `secrets.list_authorizations`. No raw-secret read API.
6. **x402 envelopes.** `x402.list_services`, `x402.service_call`,
   `x402.budget_status`, `x402.changes`. Catalog and refresh logic
   from the x402 plan attaches here.
7. **Sandbox enablement.** Stop registering wallet, messaging,
   secrets, x402 RPC handlers in *guest* sky10 instances. Sandboxed
   agents now reach these capabilities exclusively via the bus,
   which proxies (with policy) back to host. Legacy RPC continues
   to work for *host-side* consumers (CLI, Web UI, host tools).
8. **Home envelopes.** Migrate `home.*` RPC methods to the bus.
   Mechanical; no new behavior.

## Backward compatibility for sandboxed agents

During steps 3–6, sandboxed agents that haven't been updated to use
bus-envelope semantics continue to call wallet/messaging/secrets/x402
RPC against the *guest* daemon. Today those calls succeed only
incidentally (broker is empty, wallet handler is unregistered, etc.).
We can either:

- **Hard-fail those calls now** so misuse is loud. The agent's update
  to use bus semantics is the fix.
- **Soft-fail with a warning** that points at the bus migration.

Recommend hard-fail with a clear error message; the affected runtimes
are all in this repo and we control the upgrade.

## Risks during migration

| Risk | Mitigation |
|---|---|
| Legacy clients break when shim is removed | Hold the shim for at least one full release; surface a daemon-startup warning when a legacy connection is observed |
| Sync-on-async correlation has a subtle bug | Land sync support first (chat permission already uses request/response semantics, repurpose its testing) |
| Drive-sync triggers don't arrive before the agent reads | Test the end-to-end flow for `messaging.message_arrived` carefully; allow agent's read to block briefly on drive sync convergence |
| Audit log explodes | Bound size with rotation policy from day one |
| Backpressure starves chat | Per-type quotas; chat envelopes get priority in the dispatcher's scheduler |
