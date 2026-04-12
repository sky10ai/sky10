---
created: 2026-04-11
updated: 2026-04-12
model: gpt-5-codex
---

# Private Network Robustness

This work translated the useful Tailscale/headscale lessons into `sky10`
without cloning their architecture. The goal was to make the private network
converge predictably across NATs, restarts, relay trouble, and partial
coordinator failure while keeping peer-to-peer transport primary and keeping
S3 optional.

This started as the active plan in `docs/work/current/private-network-robustness-plan.md`.
It now moves to `past/` because the main architectural slices have been
implemented. The remaining work is narrower: relay productization and
real-world validation, not another open-ended network redesign.

## Why

Before this series, `sky10` had the right pieces but not one coherent system:

- libp2p direct transport, hole punching, DHT/Nostr discovery, and mailbox
  fallback all existed
- path selection and reconnect logic were still spread across ad hoc refreshes
  and retries
- operators could not easily tell why a peer was using QUIC, TCP, relay, or
  mailbox fallback
- Nostr was present, but coordination still leaned too much on query/publish
  timing
- live relay existed only as libp2p capability, not as an explicit managed
  tier with health, cache, or stable selection

The Tailscale/headscale re-study reinforced three structural points:

- control plane, direct transport, live relay, and async delivery should be
  separate layers
- bad-network reliability needs a first-class live relay tier, not just async
  mailbox fallback
- degraded behavior has to be visible and inspectable instead of hidden in
  retries

The study itself is captured in
[`docs/learned/tailscale-headscale-robustness.md`](../../../../learned/tailscale-headscale-robustness.md).

## What Changed

### 1. Health and operator signals were unified

- `pkg/link` now exposes one network-health model and `skylink.status` reports
  direct transport, relay state, mailbox state, coordination state, recent
  events, and netcheck output
- the CLI and Network page now surface preferred transport, degraded reason,
  mailbox handoff state, Nostr relay health, and live relay health
- this made "why is this peer degraded?" answerable from one place instead of
  by reading logs

### 2. Private-network repair moved into a convergence controller

- startup, membership changes, address changes, reachability changes, peer
  disconnects, mailbox handoff events, and retry conditions now funnel through
  a single convergence manager
- the 2 minute sweep remains only as a safety net
- this replaced a scattered mix of refresh calls and retry loops with one
  repair path

### 3. Path selection became stateful instead of stateless

- public STUN probing was added and then promoted from pure diagnostics into
  transport preference
- resolver path memory now remembers last successful transports and recent
  address/transport failures
- explicit transport classes now separate `direct_quic`, `direct_tcp`, and
  `libp2p_relay`
- reconnects now prefer the last known good path instead of rediscovering from
  zero each time

### 4. Delivery policy was made explicit

- mailbox was kept as a durability layer above transport, not redefined as
  "just another transport"
- RPC surfaces now distinguish live delivery, queued delivery, relay handoff,
  and failure
- operator-facing status and UI now expose mailbox backlog, handoff, and
  failure state
- this aligned the network work with the mailbox design in
  [Mailbox](11-Mailbox.md)

### 5. Nostr was upgraded from fallback queries to active coordination

- relay health tracking, ranking, publish quorum tracking, and cached
  last-good discovery were added
- long-lived subscriptions were added for membership, presence, mailbox
  receipts, queue claims, handoff state, and public queue offers
- adaptive polling now acts as a safety net when subscriptions degrade instead
  of being the default steady-state mechanism
- this made Nostr coordination act more like a real control-plane substrate and
  less like best-effort polling

### 6. A first-class live relay tier was started

- managed live relays can now be configured from config or CLI
- live relay bootstrap state is cached on disk and survives restart
- network-mode nodes now enable relay client plus static autorelay bootstrapping
- live relay health is surfaced in status and UI
- a sticky home-relay preference and anti-flap hold-down window now keep
  relayed address publication stable instead of bouncing across active relays

## Associated Work

This series depended on and tied together several adjacent efforts:

- [Private Network Discovery Hardening](06-Private-Network-Discovery-Hardening.md)
  established durable membership plus per-device presence
- [Invite & Join Bootstrap Hardening](07-Invite-Join-Bootstrap-Hardening.md)
  cleaned up bootstrap and join correctness
- [KV CRDT Reliability Hardening](07-KV-CRDT-Reliability-Hardening.md)
  tightened state convergence after reconnect
- [Multi-Instance E2E Foundation](07-Multi-Instance-E2E-Foundation.md)
  provided the real multi-daemon harness this work needed
- [Mailbox](11-Mailbox.md) defined the durability boundary that kept async
  delivery separate from live transport

## Testing And Validation Work

This branch added or expanded several test layers:

- package tests for STUN netcheck, dial strategy, convergence management, relay
  bootstrap and relay health, Nostr coordination, and mailbox delivery state
- live multi-node tests for resolver behavior and relay bootstrap
- multi-daemon process tests with a local Nostr relay for mailbox relay
  delivery, public queue propagation, and queue-claim races
- GitHub Actions was corrected to run `go test ./integration -tags integration`
  instead of a root-package no-op, and CI now runs on branch pushes instead of
  only `main`

## User-Facing Outcomes

- network surfaces can now explain transport, mailbox, and coordination
  degradation instead of collapsing everything into a generic failure
- reconnect behavior is faster and more event-driven
- async work survives disconnection and handoff state is visible
- Nostr coordination is now push-first and health-aware
- restart can recover the last managed live relay set
- relayed paths are now treated as a real degraded-but-live transport class

## Remaining Work

The remaining work is specific, not architectural sprawl:

- prove live relay transport end-to-end on bad NATs and networks where direct
  paths fail consistently
- decide whether stable operator-managed relay nodes are enough or whether
  `skyrelay` should exist as a dedicated service with clearer bootstrap and
  invite support
- run the real-device validation passes from [Mailbox](11-Mailbox.md):
  offline delivery, reconnect drain, relay handoff, long-lived lifecycle, and
  mixed multi-device operator flows
- gather real-world evidence before any `skycoord` work; an optional
  coordinator only makes sense if decentralized convergence still fails after
  the earlier milestones

## Why This Moved To Past

- M1 through M5 were implemented as code, not left as planning
- M6 is no longer vague future work; the branch now has managed live relay
  bootstrap, health, and sticky selection
- the remaining work is now follow-on validation and productization, not one
  large active planning document
