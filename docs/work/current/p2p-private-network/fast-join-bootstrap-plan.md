---
created: 2026-04-07
updated: 2026-04-07
model: gpt-5.4
---

# Fast Join Bootstrap Plan

## Problem

Fresh private-network joins eventually work, but the user-facing
experience is still too slow and too flaky.

Observed on `2026-04-07` during a full two-device reset:

- Mac and Linux both started from clean state
- `identity.join` over RPC could time out after about `20s`
- Linux logged:
  - `resolving inviter: could not resolve membership`
  - later `could not resolve any live peers`
- despite that, the join eventually completed in the background
- only after additional delay did both daemons reach:
  - `device_count: 2`
  - `peer_count: 1`
  - `skykv.status.sync_state: ok`

This means the current bootstrap path is relying too much on eventual
global discovery convergence during the join itself.

That is the wrong dependency.

## Current Design

Today a P2P invite includes:

- private-network identity address
- a small Nostr relay list
- random invite ID

But the join flow still does this:

1. decode invite
2. construct a resolver with `WithNostr(invite.NostrRelays)`
3. call `resolver.Resolve(invite.Address)`
4. only after that succeeds, open the libp2p join stream

So Nostr is currently used only as one possible discovery backend for
resolving the inviter's live libp2p presence.

It is **not** being used as the invite-session transport itself.

That is why the current flow can still take tens of seconds even though
the invite already contains relay URLs.

## Design Goal

Make fresh private-network join feel immediate and deterministic.

Target properties:

- scanning or pasting an invite should usually complete within a few
  seconds, not tens of seconds
- initial join must not depend on DHT/provider propagation
- initial join must not depend on LAN reachability
- DHT remains the primary global Schelling point for reconnect after the
  private network already exists
- Nostr remains available as reconnect fallback, but is promoted to
  first-class bootstrap transport for the initial invite session

## Core Principle

`Global discovery is for reconnects. Invite bootstrap should be direct.`

The first join should use information already in the invite plus a
small invite-session mailbox on the invite's Nostr relays.

Once the two devices are linked, steady-state reconnect goes back to:

- DHT primary
- Nostr fallback

## Proposed Architecture

### Invite Payload

Expand the P2P invite to include direct bootstrap hints:

- private-network identity address
- invite session ID
- `1-2` Nostr relays
- inviter peer ID
- `1-2` best current inviter multiaddrs
- issued-at timestamp
- optional signature over the bootstrap payload

This is still practical for:

- pasted strings
- QR codes

The payload is larger than today, but still comfortably within the
range of opaque invite strings.

### Bootstrap Transport

Use the invite's Nostr relays as the control plane for the first join.

Flow:

1. inviter creates invite
2. inviter subscribes on the invite relays for that invite session
3. joiner decodes invite
4. joiner tries direct libp2p dial using invite `peer_id +
   multiaddrs`
5. in parallel, joiner publishes an encrypted join request to the invite
   session on Nostr
6. inviter receives the join request over Nostr and responds over the
   same invite session
7. response includes:
   - approval
   - manifest
   - namespace keys
   - inviter current presence
8. both sides immediately seed each other into their peerstores and
   attempt direct libp2p connect
9. both sides immediately publish updated membership/presence to DHT and
   Nostr for future reconnects

### Why This Is Better

This removes the slow dependency on global propagation from the
user-facing pairing moment.

The devices do not need to wait for:

- DHT provider propagation
- DHT lookup convergence
- Nostr-based global presence lookup

They already know where to meet because the invite gives them:

- the relays for the invite session
- the inviter's direct dial hints

## Protocol Shape

### Invite

Conceptual shape:

```json
{
  "a": "sky10q...",
  "i": "a73772b0bf0a82031a0a6fcf5f8c755c",
  "r": ["wss://relay.damus.io", "wss://nos.lol"],
  "p": "12D3KooW...",
  "m": [
    "/ip4/167.248.84.118/udp/57648/quic-v1/p2p/12D3KooW..."
  ],
  "t": "2026-04-07T08:40:00Z"
}
```

Notes:

- `r` should be short
- `m` should include only the best public addrs
- local-only/private-only addrs do not belong in the invite unless they
  are clearly useful

### Join Request Over Nostr

Fields:

- invite session ID
- joiner device pubkey
- joiner device name
- joiner current peer ID
- joiner current multiaddrs
- timestamp
- signature

### Join Response Over Nostr

Fields:

- approved / denied
- signed private-network membership
- wrapped namespace keys
- inviter current peer ID
- inviter current multiaddrs
- timestamp
- signature

These are control-plane messages only. Bulk sync and normal traffic
still belong on libp2p.

## Join Execution Strategy

Run these in parallel:

- direct libp2p dial using invite-carried peer info
- Nostr invite-session exchange
- background DHT publication

Preferred order:

1. direct dial if invite hints are fresh and valid
2. Nostr invite-session mailbox
3. DHT/global resolver only as fallback during bootstrap

This makes first join fast without weakening steady-state architecture.

## Post-Join Behavior

Immediately after approval:

- inviter saves updated membership
- inviter publishes membership and presence right away
- joiner saves joined bundle and namespace keys before restart
- joiner publishes presence right away after restart
- both sides run an eager connect loop for a short bounded window

Do not rely only on the normal background heartbeat.

Suggested behavior:

- `3-5` rapid publish/connect retries over the first `10-15s`
- then fall back to the normal background cadence

## UX Contract

Expected states:

- `joining`
- `joined, connecting`
- `connected`

The current issue is that the join path can appear to hang and then only
eventually become healthy. The daemon should surface bootstrap phase
explicitly instead of masking it behind a single resolver timeout.

## Milestones

### Milestone 1: Invite Payload Upgrade

- add inviter peer ID to invite
- add best current multiaddrs to invite
- trim relay list to the best `1-2`
- add issued-at timestamp
- update invite QR/string tests

Acceptance:

- invite remains practical to paste and scan
- direct dial hints are present in every new invite

### Milestone 2: Nostr Invite Session

- add invite-session request/response records on Nostr
- inviter subscribes immediately after invite creation
- joiner publishes join request immediately after decoding invite
- inviter replies with approval/manifest/keys over Nostr

Acceptance:

- join no longer requires `resolver.Resolve(...)` as the first step
- a fresh join can complete even when DHT/provider propagation is slow

### Milestone 3: Direct Dial First

- seed peerstore from invite hints
- attempt direct libp2p connect before global resolver fallback
- seed peerstore again from join response presence fields

Acceptance:

- when peer hints are usable, join reaches a live peer without waiting
  for DHT/Nostr discovery convergence

### Milestone 4: Aggressive Post-Join Publish And Connect

- immediate publish after approval
- immediate publish after joiner restart
- short bounded eager reconnect loop
- clear bootstrap state surfaced in RPC/UI

Acceptance:

- `identity.join` is followed quickly by `peer_count: 1`
- `skykv.status` reaches `ok` within a few seconds in normal cases

### Milestone 5: Regression Coverage

Add end-to-end tests for:

- fresh wipe on both devices
- invite over RPC
- join over RPC
- both sides reach `device_count: 2`
- both sides reach `peer_count: 1`
- KV set/delete round-trip succeeds within a bounded window
- delayed DHT propagation does not break initial join
- bootstrap still succeeds if direct dial hints fail but Nostr invite
  session works

Acceptance:

- the `2026-04-07` slow-join scenario is covered explicitly

## Non-Goals

- replacing libp2p as the steady-state data plane
- sending bulk file payloads over Nostr
- making Nostr the permanent primary reconnect substrate

This plan is only about making first join fast and reliable.

## Risks

### Relay Availability

Invite-session bootstrap depends on the selected relay(s) being usable.

Mitigation:

- use `2` relays, not `1`
- keep direct dial hints in the invite too

### Invite Bloat

More fields make invites larger.

Mitigation:

- keep field names compact in the encoded payload
- include only the best `1-2` multiaddrs
- include only the best `1-2` relays

### Dual-Path Complexity

We will have:

- bootstrap path
- steady-state reconnect path

Mitigation:

- keep the rule simple:
  - bootstrap uses invite-session transport
  - reconnect uses global discovery

## Recommended Next Step

Implement Milestones 1 and 2 together.

Just adding more addresses to the invite helps, but does not fully solve
the root problem if the join still waits on global resolver state.

The meaningful fix is:

- invite carries direct hints
- join uses Nostr invite-session bootstrap immediately
- global discovery is demoted to fallback during the initial join
