---
created: 2026-04-26
updated: 2026-04-26
model: claude-opus-4-7
---

# Agent Bus Implementation Plan

Milestones in dependency order. Each is independently shippable.
Later milestones depend on earlier contracts; if a milestone has to
slip, later ones slip with it rather than racing ahead with shims.

## Pre-flight

- [ ] Confirm answers to the open questions in the
  [README](README.md#open-questions): backward-compat window, default
  quotas, audit log location, cross-device identity hop behavior,
  drop policy.
- [ ] Decide whether the bus package lives at `pkg/bus/` or `pkg/agent/bus/`.
  Recommendation: `pkg/bus/` — it is broader than `agent`.

## M1 — bus core

`pkg/bus/` package with envelope, dispatcher, registry, transport
adapter. No new capabilities yet — chat envelopes delegate from
existing `chat_websocket.go`.

Files:

- `pkg/bus/doc.go`
- `pkg/bus/envelope.go` — `Envelope` struct, `TypeSpec`, `Direction`
- `pkg/bus/dispatcher.go` — dumb switch on type, calls registered handler
- `pkg/bus/registry.go` — `Register(TypeSpec)`; init-time validation of required fields
- `pkg/bus/transport.go` — adapter that consumes from existing
  `chat_websocket.go` accept loop and emits to envelope-aware handlers
- `pkg/bus/identity.go` — extract `agent_id` and `device_id` from authenticated
  channel; strip and ignore any payload-supplied identity
- `pkg/bus/audit.go` — structured per-envelope log
- `pkg/bus/replay.go` — bounded LRU for nonce dedup; ts skew check
- `pkg/bus/envelopes/chat_*.go` — six chat envelope types (send,
  receive, tool_call, tool_result, permission, diff)
- `pkg/bus/envelopes/arch_test.go` — enforces rule 5 (validation
  must precede business logic)
- Tests: dispatcher routing, identity injection, replay rejection,
  audit log shape

Exit criteria: existing chat traffic flows through the bus end-to-end
in a test harness that exercises an OpenClaw runtime against a host
daemon. No new external behavior; legacy wire format still accepted.

## M2 — backpressure + quotas

`pkg/bus/quota.go` and per-type queues.

Files:

- `pkg/bus/quota.go` — token-bucket per `(agent_id, type)`
- `pkg/bus/queue.go` — bounded queue per type with configurable drop policy
- `pkg/bus/envelopes/bus_busy.go` — the `bus.busy` envelope sent when a queue is full
- Tests: rate-limit accept/reject, queue overflow, drop ordering, `bus.busy` emission

Exit criteria: under simulated load (1k envelopes/sec mixed types),
chat envelopes maintain p99 latency below a target while abusive
agents hit their per-type rate limits and see `bus.busy` responses.

## M3 — wallet envelopes

First non-chat envelope family; first sync-on-async case in
production.

Files:

- `pkg/bus/envelopes/wallet_balance_subscribe.go`
- `pkg/bus/envelopes/wallet_balance_update.go`
- `pkg/bus/envelopes/wallet_transfer.go`
- `pkg/bus/envelopes/wallet_transfer_signed.go`
- `pkg/bus/envelopes/wallet_history.go`
- Hooks into existing `pkg/wallet`
- Tests: subscribe + push fan-out, transfer with sync correlation,
  scope (caller can only see/move their own subwallet)

Exit criteria: an OpenClaw agent in a sandbox can call
`wallet.balance` over the bus and receive the host's wallet balance.
The guest daemon's wallet RPC handler is **not** in the path.

## M4 — messaging envelopes

Hooks into the existing host-side `pkg/messaging/broker`.

Files:

- `pkg/bus/envelopes/messaging_search.go`
- `pkg/bus/envelopes/messaging_send.go`
- `pkg/bus/envelopes/messaging_list_connections.go`
- `pkg/bus/envelopes/messaging_message_arrived.go`
- `pkg/bus/envelopes/messaging_message_sent.go`
- Drive-sync trigger: when a new inbound message lands at the broker,
  write the body to the agent's drive at a stable path, send a
  `messaging.message_arrived` envelope referencing that path
- Tests: scope (caller can only see their authorized connections),
  inbound flow with drive-sync handoff, outbox delivery confirmation

Exit criteria: an agent inside a Lima VM reads a Slack message
end-to-end. The path is bus → host broker → host fetches → drive
sync to guest → agent reads from local drive. The Slack bot token
**never** crosses into the guest.

## M5 — secrets envelopes (narrow)

Files:

- `pkg/bus/envelopes/secrets_issue_scoped_token.go`
- `pkg/bus/envelopes/secrets_token_issued.go`
- `pkg/bus/envelopes/secrets_list_authorizations.go`
- Hooks into `pkg/secrets` for token issuance
- Tests: scoped token expiry, scope rejection, no raw-secret leakage

Exit criteria: agent can request a time-limited scoped token for an
authorized upstream service and use it directly against that service.
The agent never sees the raw secret. Adding `secrets.get` to the bus
is explicitly forbidden by the registry's compile-time review of
this PR's diff.

## M6 — x402 envelopes

This is where the [x402 plan](../x402/) attaches. The catalog,
discovery, refresh, and budget logic lives in `pkg/x402` on the host;
the bus envelopes are the agent-facing surface.

Files:

- `pkg/bus/envelopes/x402_list_services.go`
- `pkg/bus/envelopes/x402_service_call.go`
- `pkg/bus/envelopes/x402_budget_status.go`
- `pkg/bus/envelopes/x402_changes.go`
- Hooks into `pkg/x402`
- Tests: scope by approval, budget enforcement, sync-on-async for
  service_call, idempotency on payment-binding nonce

Exit criteria: an agent in a Lima VM calls Deepgram (or a sandbox
x402 service) end-to-end via `x402.service_call`. The wallet stays
on host. The agent never sees the 402 challenge.

## M7 — sandbox enablement (cutover)

The point at which sandboxed agents stop incidentally relying on
guest-side RPC handlers.

- Guest daemon stops registering `wallet.*`, `messaging.*`,
  `secrets.*`, `x402.*` RPC handlers
- Guest daemon's RPC server returns a clear "use the agent bus"
  error if any of those methods are called locally
- Documentation update: agent-runtime authors must use envelopes
- Telemetry: count any legacy RPC attempts during a soak window
  before declaring the cutover stable

Exit criteria: zero legacy RPC attempts observed across a one-week
soak in development. Cutover ships in the next release.

## M8 — home envelopes

Mechanical migration of `pkg/home/rpc.go` methods to envelope types.
No new behavior. Sets the precedent that all *cross-trust-boundary*
RPC migrates to envelopes; host-side-only RPC stays on the existing
unix socket.

## M9 — backward-compat shim removal

After at least one full release of the bus running with the legacy
chat shape shim, remove the shim. New agents must use envelope
shape; legacy connections fail loudly.

## Out of scope for this plan

- Replacing host-side unix-socket RPC for CLI / Web UI / host tools.
  Those are inside the trust boundary and don't need envelope
  discipline.
- Building a third-party SDK for the bus protocol. We control all
  current consumers; an SDK becomes worth it once external runtimes
  want to integrate.
- A graphical bus inspector / debugger. Audit log + log analysis
  cover M1–M9 needs.
