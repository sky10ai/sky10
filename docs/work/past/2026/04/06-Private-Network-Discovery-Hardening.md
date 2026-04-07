---
created: 2026-04-06
model: gpt-5.4
---

# Private Network Discovery Hardening

Reworked `sky10` private-network discovery so linked devices behave like
one durable private network instead of a pile of local caches and
best-effort peer guesses. This closed the April 6 regression where one
stale manifest could partition the network after restart, cleaned up the
device control-plane boundaries, tightened KV sync to the private
network, and removed several misleading or slow surfaces that made the
system hard to trust.

This work shipped incrementally across `v0.40.0` through `v0.40.4`.

## Why

The old behavior had several structural problems:

- private-network reachability was effectively modeled as one
  identity-scoped peer record, which is wrong once one identity can have
  many devices
- one stale local manifest could make a device "forget" the rest of the
  private network after restart
- join/bootstrap was split across daemon-owned invite state and a
  CLI-only join path, which made the product control plane incoherent
- device enumeration leaked public libp2p peers into private-network
  surfaces
- KV sync fanned out to all connected peers instead of only connected
  private-network peers
- the Devices and Agents pages blocked on slow network resolution and
  unrelated peer fanout

That combination made the private network feel unreliable even when the
underlying cryptographic identity model was sound.

## What Changed

### 1. Private-network discovery now uses membership plus per-device presence

The discovery model was rebuilt around two record families:

- durable private-network membership
- per-device private-network presence

DHT provider records are now the primary global discovery substrate, with
Nostr as fallback. Membership is discovered through identity-scoped
providers and fetched over skylink from discovered peers. Presence is
resolved per device, which avoids the old "one identity record for the
whole swarm" clobbering problem.

This matters because a multi-device private network needs a durable guest
list plus a separate current address card for each device. One identity
does not map to one live peer anymore.

### 2. Local manifest state was demoted from authority to cache

Startup now rebuilds private-network state from globally discoverable
membership and presence instead of trusting one local manifest file as
the source of truth.

That matters because stale local disk should never be enough to partition
an already-linked private network by itself.

### 3. Join/bootstrap moved into daemon RPC

The daemon now owns the join path through `identity.join` instead of
requiring `sky10 join ...` as a separate CLI-only bootstrap step. Invite,
join, device listing, and device removal now live on the same root RPC
surface.

That matters because private-network lifecycle belongs to the daemon. The
product should not depend on a separate escape-hatch CLI path for core
membership changes.

### 4. Private-network device surfaces were separated from public peers

Device enumeration was moved out of `skyfs.*` and corrected so
private-network device lists come from verified membership, not arbitrary
connected libp2p peers.

That matters because operators need to be able to trust that "device"
means "authorized private-network member," not "some public peer on the
network."

### 5. KV sync now follows the private network instead of the raw peer set

KV snapshot push and notification fanout now target only connected
private-network peers. The daemon also triggers a fresh KV push after
private-network reconnect so convergence does not depend on waiting for
the next mutation.

That matters because encrypted KV data should ride the private network,
not every currently connected libp2p peer, and reconnect should actually
cause the CRDT-like state to reconcile.

### 6. Slow private-network surfaces were made non-blocking

The Devices view stopped doing a blocking network-wide resolve in the RPC
path, and agent listing stopped fanning out to irrelevant public peers.

That matters because operator trust collapses when the product feels
stalled for seconds every time someone opens Devices or Agents.

### 7. Debugging and test coverage were expanded

Coverage was added for:

- DHT/provider-based private-network discovery
- late connect and restart rediscovery for KV over the private network
- daemon-owned join behavior
- private-network-only peer fanout for KV and agent enumeration

The DHT-heavy tests were also stabilized for CI by using an isolated
local DHT overlay instead of depending on live public bootstrap timing on
every run.

That matters because the failure mode here was a restart-time distributed
systems bug. If the test suite cannot reproduce restart + rediscovery
reliably, the private-network work is not defensible.

## User-Facing Outcomes

- linked devices can rebuild private-network membership after restart
  without treating one stale local manifest as truth
- private-network join is daemon-owned end to end
- device lists no longer inflate with public DHT/bootstrap peers
- KV sync behaves more predictably after reconnect
- Devices and Agents load faster because they no longer block on the
  wrong network calls
- daemon restart is remotely triggerable through `system.restart`

## Important Releases In This Series

- `v0.40.0`: initial private-network discovery refactor
- `v0.40.1`: fix incorrect device surfaces and stop treating public peers
  as devices
- `v0.40.2`: move join into daemon RPC
- `v0.40.3`: tighten KV sync to private-network peers and reconnect
  pushes
- `v0.40.4`: remove blocking device/agent RPC behavior

## Follow-On Work

The KV reliability follow-on has now been completed and archived in:

- [KV CRDT Reliability Hardening](07-KV-CRDT-Reliability-Hardening.md)

The invite/bootstrap follow-on has also now been archived in:

- [Invite & Join Bootstrap Hardening](07-Invite-Join-Bootstrap-Hardening.md)
