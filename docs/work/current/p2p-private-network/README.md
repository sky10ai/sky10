---
created: 2026-04-06
updated: 2026-04-06
model: gpt-5.4
---

# Private Network Discovery Refactor

## Goal

Refactor sky10 private-network discovery so that once a device joins a
private network, ordinary restarts, updates, IP changes, and long offline
periods do not require re-invite or manual recovery.

This refactor treats the current state as disposable. There is no migration
requirement. Existing local discovery state can be blasted if that makes the
design simpler and more correct.

## Terminology

- **sky10 network**: the broader public network and discovery substrates
  (DHT primary, Nostr fallback).
- **private network**: one user's durable set of linked devices under one
  shared identity.

Use `private network` consistently in code, docs, and product language.

## Non-Negotiable Invariants

- A private network can contain many devices.
- Joining is one-time bootstrap, not recurring maintenance.
- Membership and reachability are separate concerns.
- Membership is durable and globally recoverable.
- Presence is per-device, ephemeral, and continuously refreshed.
- Local disk may be stale or missing; private-network state must still be
  reconstructible.
- All correctness assumes devices may be isolated across the world.
- No design may rely on LAN reachability, same-subnet behavior, mDNS, local
  broadcast, or private IP stability.
- DHT is the primary global Schelling point.
- Nostr is the fallback Schelling point, using the same record semantics.
- Normal restart/update must not require repair.
- Repair flow exists only for exceptional recovery from catastrophic local
  divergence or state loss.

## Mental Model

- **membership** is the durable guest list for the private network:
  which device public keys are allowed to belong.
- **presence** is each device's current address card:
  where that device can be reached right now.
- Membership changes rarely.
- Presence changes often.
- Each device writes only its own presence record.
- Membership v1 uses a single full signed membership record, and membership
  edits are serialized rather than treated as concurrent multi-writer updates.

## Record Model

### Private network membership

One durable signed record that answers:

> Which device public keys are authorized members of this private network?

Properties:

- signed by the shared identity key
- authoritative for membership
- versioned or monotonic-timestamped
- includes removals explicitly (`revoked` set or tombstones)
- v1 is a serialized full-record update model, not a concurrent multi-writer
  merge model
- DHT primary discovers this record through identity-scoped provider ads
- the record itself is fetched from discovered peers over skylink
- Nostr mirrors the same signed payload as fallback

### Private network presence

One device-scoped discovery path per device that answers:

> Where is this device reachable right now?

Properties:

- keyed by identity + device public key
- DHT primary uses per-device provider ads plus peer routing (`FindPeer`)
- the expected peer ID is derived from the device public key
- Nostr fallback uses a signed device presence record with multiaddrs and TTL
- no cross-device clobbering because each device advertises only its own key

### Private network observations

Deferred. Observation records are advisory hints from one device about another
device's recent reachability. They are useful for larger private networks but
are not required for correctness in the first version.

## Runtime Model

### Startup

1. Load local shared identity key and local device key.
2. Discover private-network membership providers from DHT.
3. Fetch signed membership from discovered peers over skylink.
4. Fall back to Nostr if DHT membership discovery or fetch fails.
5. Verify membership with the shared identity key.
6. Resolve per-device presence from DHT provider ads and `FindPeer`.
7. Fall back to Nostr presence when DHT reachability is missing or stale.
8. Dial all fresh peers.
9. Rewrite local cache from verified global state.

### Runtime

- Advertise membership and own presence to DHT on startup.
- Republish DHT provider ads periodically.
- Mirror membership and presence to Nostr.
- Republish on address/reachability changes.
- Refresh membership and peer presence periodically.
- Expire stale presence automatically.

## Local State Policy

Local files are cache only. A stale or missing local file must not be able to
partition a private network by itself.

## Repair Policy

Repair is an exceptional recovery path for catastrophic divergence, not a
normal workflow. It must preserve the existing private network, not create a
new one, and it must not rely on LAN assumptions.

## Deliverables

- New discovery model centered on `private network membership` and
  `private network presence`.
- Concrete record contract in [record-spec.md](./record-spec.md).
- KV CRDT direction and hardening plan in [kv-crdt-plan.md](./kv-crdt-plan.md).
- DHT-first implementation with provider ads, peer routing, and Nostr fallback.
- Startup and runtime self-healing behavior.
- Explicit repair flow for catastrophic local divergence.

See [engineering-plan.md](./engineering-plan.md) for milestones,
acceptance criteria, and test coverage.
