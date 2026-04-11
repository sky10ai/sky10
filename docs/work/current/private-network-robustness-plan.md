---
created: 2026-04-11
model: gpt-5
---

# Private Network Robustness Plan

## Goal

Make the private network behave predictably across NATs, restarts, relay
outages, and partial discovery failures without requiring S3 or a permanent
central coordinator.

The useful lesson from Tailscale/headscale is structural, not literal:

- keep coordination, direct transport, fallback delivery, and observability
  as separate concerns
- keep peer-to-peer paths primary when they work
- make degraded paths explicit and inspectable instead of hidden in retries
- add an always-on coordinator only after the cheaper decentralized pieces
  are clearly insufficient

## Current Baseline

As of 2026-04-11, sky10 already has the right foundation:

- direct transport via libp2p with hole punching, AutoNAT, and relay service
  in [`pkg/link/node.go`](../../../pkg/link/node.go)
- layered private-network discovery via DHT and Nostr in
  [`pkg/link/discovery.go`](../../../pkg/link/discovery.go) and
  [`pkg/link/nostr.go`](../../../pkg/link/nostr.go)
- public STUN probing and transport preference in
  [`pkg/link/netcheck.go`](../../../pkg/link/netcheck.go) and
  [`pkg/link/dial_strategy.go`](../../../pkg/link/dial_strategy.go)
- republishing on observed address and reachability changes in
  [`commands/serve.go`](../../../commands/serve.go)
- durable mailbox, Nostr relay handoff, and public queue support in
  [`pkg/agent/router.go`](../../../pkg/agent/router.go) and
  [`pkg/agent/mailbox/relay_dropbox.go`](../../../pkg/agent/mailbox/relay_dropbox.go)

The remaining gap is not "basic fallback exists." The gap is that these pieces
still behave like features, not one coherent network system.

## What Robust Means

The private network should satisfy all of these:

- existing direct sessions survive temporary discovery/control-plane trouble
- a failed direct connect degrades into a known fallback path quickly
- both-peer restarts converge without waiting on long periodic ticks
- offline-target messaging becomes queued delivery, not a lost opportunity
- operators can tell why a path is degraded from one RPC/UI surface
- no single optional dependency such as S3 or one Nostr relay is required for
  basic private-network operation

## Failure Contract

| Scenario | Expected behavior | Current state | Gap |
| --- | --- | --- | --- |
| UDP blocked on one side | Prefer TCP or fallback delivery quickly | STUN influences QUIC vs TCP order | No unified path scorer or user-facing reason |
| Symmetric NAT / no direct route | Queue or relay supported traffic; show degraded direct path | Mailbox + Nostr handoff exists for agent traffic | No generic network health story or explicit policy split |
| Both peers restart | Republish and reconnect within seconds once link is up | Startup refresh and address-change republish exist | Retry logic still leans on ad hoc calls and a 2 minute sweep |
| One device offline for a while | Durable delivery on reconnect with receipts | Mailbox exists | Status/UI do not yet make this operationally clear |
| DHT slow or stale | Use last-good records and alternate sources | Resolver chooses among local, DHT, Nostr | Source health and freshness are not surfaced |
| Nostr relay degraded | Use other relays, keep direct traffic working | Multi-relay publish/query exists | Relay ranking, health, and subscription behavior are weak |
| No S3 configured | Private network still works | Already true | Need docs and health output to treat this as normal, not degraded |

## Milestones

### M1. Unify Health And Operator Signals

Build one network-health model and expose it everywhere.

Add:

- a `NetworkHealth` snapshot in `pkg/link` that combines netcheck,
  reachability, observed addrs, last publish, last direct dial success,
  last relay handoff success, and resolver source freshness
- richer `skylink.status` output in [`pkg/link/rpc.go`](../../../pkg/link/rpc.go)
- CLI and web surfaces that show preferred transport, degraded reason, and
  last successful convergence event in
  [`commands/link.go`](../../../commands/link.go) and
  [`web/src/pages/Network.tsx`](../../../web/src/pages/Network.tsx)

Done when:

- one RPC call can answer "why is this peer using TCP, relay, or mailbox?"
- the web Network page shows direct health, fallback health, and recent events
- logs record state transitions instead of only low-level failures

### M2. Replace Ad Hoc Refresh With A Convergence Controller

Move private-network repair into a single stateful controller instead of
scattered refresh calls.

Add a `link.Manager` or equivalent that reacts to:

- startup
- join completion or manifest change
- local address change
- reachability change
- peer disconnect
- repeated RPC dial failure
- mailbox handoff success or failure
- relay poll receipt that proves a peer is alive

Keep the 2 minute sweep only as a safety net.

Done when:

- both-peer restart convergence is event-driven rather than ticker-driven
- repeated failures trigger bounded, jittered retries instead of bursty loops
- network repair behavior is implemented in one place instead of spread across
  [`commands/serve.go`](../../../commands/serve.go),
  [`pkg/link/discovery.go`](../../../pkg/link/discovery.go), and
  [`pkg/agent/router.go`](../../../pkg/agent/router.go)

### M3. Add Path Memory And Transport Scoring

Right now STUN only reorders addresses. Robust systems also remember what
actually worked.

Add:

- per-peer memory of last successful transport, address family, and discovery
  source
- negative feedback on repeated dial failures so dead addresses stop winning
- explicit path classes: `direct_quic`, `direct_tcp`, `libp2p_relay`,
  `nostr_mailbox`
- freshness and expiry handling for presence records so stale hints age out

Done when:

- reconnects prefer the last known good path before re-exploring everything
- stale public hints do not keep poisoning future dials
- `skylink.resolve` can explain why one candidate beat another

### M4. Make Delivery Policy Explicit

Tailscale feels reliable partly because every traffic class has a clear fallback
story. sky10 should define that explicitly instead of by special case.

Define supported policy per traffic type:

- interactive RPC: direct libp2p only, fast fail, no hidden queueing
- agent/user messages: direct libp2p first, mailbox plus Nostr relay fallback
- control-plane updates: DHT plus Nostr, cached locally, retried aggressively
- bulk sync and snapshots: direct plus store-backed sync only, never Nostr

Implementation targets:

- document the policy in the relevant RPC and router code
- return transport and queueing metadata to callers
- expose mailbox and relay receipt state in status/UI

Done when:

- every networked operation has an explicit online/offline semantic
- users can tell whether a request was sent live, queued, handed off, or failed
- future features do not need to reinvent fallback behavior ad hoc

### M5. Upgrade Nostr From Fallback Queries To Active Coordination

Today Nostr is used, but mostly in a query/publish style. Robust coordination
needs more push and more health awareness.

Add:

- long-lived relay subscriptions for membership, presence, mailbox receipts,
  and queue claims where it improves convergence
- relay health tracking and ranking by success rate and latency
- publish quorum rules so one bad relay does not look like success
- cached last-good relay state so brief relay outages do not erase presence

Done when:

- network updates arrive without waiting for periodic re-query
- relay outages show as degraded coordination, not mysterious silence
- mailbox receipts and queue claims converge faster than the current poll-only
  behavior

### M6. Decide Whether An Optional Coordinator Is Still Needed

Only after M1 through M5 should sky10 consider a headscale-like service.

If needed, `skycoord` should stay control-plane only:

- signed membership and presence watch stream
- relay and STUN configuration distribution
- optional policy distribution for private networks
- no mandatory proxying of healthy peer-to-peer traffic

This should be justified by measured problems such as:

- unacceptable cold-start latency after the earlier milestones
- persistent multi-device convergence issues on bad networks
- a need for centrally managed policy that Nostr plus DHT cannot express cleanly

Done when:

- we have evidence that decentralized coordination is still insufficient
- the service is optional and does not become required for private-network
  traffic that already works peer-to-peer

## Sequencing

The order matters:

1. health and observability
2. convergence controller
3. path memory and scoring
4. explicit delivery policy
5. stronger Nostr coordination
6. optional coordinator decision

Skipping straight to a coordinator would hide the real gaps and bake current
retry ambiguity into a more complicated system.

## Immediate Next Work

The next concrete implementation slice should be M1.

That work is small enough to ship incrementally and high leverage enough to
make every later milestone easier:

- enrich `skylink.status`
- surface netcheck and fallback state in the Network page
- add structured recent-event reporting for publish, connect, relay handoff,
  and mailbox drain

Once that exists, the rest of the plan can be driven by observed failures
instead of guesswork.
