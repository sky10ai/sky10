---
created: 2026-04-06
updated: 2026-04-06
model: gpt-5.4
---

# Private Network Record Spec (v1)

This document turns the private-network discovery plan into concrete wire
shapes for DHT-primary discovery and Nostr fallback.

## Scope

This spec defines:

- deterministic DHT keys
- membership and presence payloads
- signature and validation rules
- record-selection rules when multiple values exist
- the v1 serialized membership-update rule

This spec does not define:

- repair eligibility
- observation records
- a CRDT or op-log membership model

## Naming

- `identity`
  The shared private-network identity address, for example `sky10q...`.
- `device_pubkey`
  The raw Ed25519 public key of a device, encoded as lowercase hex.
  This is the long cryptographic device identifier used on the wire.
- `peer_id`
  The libp2p peer ID deterministically derived from `device_pubkey`.
- `device_id`
  The short UI identifier, such as `D-ab12cd34`.
  This is not used in DHT keys or record identity because it is only a short
  display string.

## Identifier Policy

- `identity` is the stable private-network identifier.
- `device_pubkey` is the stable per-device cryptographic identifier.
- `device_id` is a short operator-facing label.

For v1 private-network discovery:

- DHT keys use `identity` and `device_pubkey`
- signatures verify against `identity` or `device_pubkey`
- logs and debug surfaces may show `device_id` alongside the long identifier
- `device_id` must never be the sole wire identifier

Why:

- authorization is defined over device public keys, not short IDs
- peer IDs are derived from device public keys, so `device_pubkey` is the
  more fundamental identifier
- short IDs are compact and operator-friendly, but they are not the right
  canonical identity for global discovery records

## DHT Key Layout

The DHT namespace is:

`/sky10-private-network/...`

### Membership Key

One key per private network:

```text
/sky10-private-network/v1/membership/<identity>
```

Example:

```text
/sky10-private-network/v1/membership/sky10qexample...
```

### Presence Key

One key per device inside a private network:

```text
/sky10-private-network/v1/presence/<identity>/<device_pubkey>
```

Example:

```text
/sky10-private-network/v1/presence/sky10qexample.../9f2a...
```

## Membership Record

Membership is the durable guest list for the private network.

### Payload

```json
{
  "schema": "sky10.private-network.membership.v1",
  "identity": "sky10q...",
  "revision": 7,
  "updated_at": "2026-04-06T21:18:00Z",
  "devices": [
    {
      "public_key": "9f2a...",
      "name": "macos.shared",
      "added_at": "2026-04-03T21:08:09Z"
    }
  ],
  "revoked": [
    {
      "public_key": "31cd...",
      "revoked_at": "2026-04-06T20:44:10Z"
    }
  ],
  "signature": "base64-ed25519-signature"
}
```

### Field Rules

- `schema` must equal `sky10.private-network.membership.v1`.
- `identity` must equal the identity suffix in the DHT key.
- `revision` is a monotonically increasing integer for this private network.
- `updated_at` is RFC3339 UTC.
- `devices` contains the currently authorized device keys.
- `revoked` contains explicit removals.
- A device public key must appear in exactly one of:
  - `devices`
  - `revoked`
- `signature` is an Ed25519 signature made by the shared identity key.

### Canonical Signing Rules

The signature covers all fields except `signature`.

For canonicalization:

- `devices` are sorted by `public_key`
- `revoked` are sorted by `public_key`
- the payload is encoded as deterministic JSON

### Validation Rules

Membership is valid only if:

- the DHT key path is valid
- `schema` matches
- `identity` parses as a valid sky10 address
- the identity in the payload matches the identity in the key
- no device appears in both `devices` and `revoked`
- the signature verifies against the identity public key

### Selection Rules

If multiple valid membership records are encountered for the same key:

1. highest `revision` wins
2. if revisions tie, latest `updated_at` wins
3. if still tied, choose the lexicographically largest canonical payload

## Presence Record

Presence is a device's current address card for the private network.

### Payload

```json
{
  "schema": "sky10.private-network.presence.v1",
  "identity": "sky10q...",
  "device_pubkey": "9f2a...",
  "peer_id": "12D3KooW...",
  "multiaddrs": [
    "/ip4/203.0.113.10/udp/41000/quic-v1/p2p/12D3KooW..."
  ],
  "published_at": "2026-04-06T21:18:00Z",
  "expires_at": "2026-04-06T21:28:00Z",
  "version": "v0.39.1",
  "signature": "base64-ed25519-signature"
}
```

### Field Rules

- `schema` must equal `sky10.private-network.presence.v1`.
- `identity` must equal the identity suffix in the DHT key.
- `device_pubkey` must equal the device suffix in the DHT key.
- `peer_id` must deterministically match `device_pubkey`.
- every `multiaddr` must parse successfully and embed the same `peer_id`.
- `published_at` and `expires_at` are RFC3339 UTC.
- `expires_at` must be later than `published_at`.
- `version` is optional and is for debugging/operator visibility only.
- `signature` is an Ed25519 signature made by the device key.

### Canonical Signing Rules

The signature covers all fields except `signature`.

For canonicalization:

- `multiaddrs` are sorted lexicographically
- the payload is encoded as deterministic JSON

### Validation Rules

Presence is valid only if:

- the DHT key path is valid
- `schema` matches
- `identity` parses as a valid sky10 address
- `device_pubkey` is valid lowercase hex and decodes to a valid Ed25519 key
- `peer_id` matches `device_pubkey`
- all `multiaddrs` parse and point at the same `peer_id`
- the signature verifies against `device_pubkey`

Presence is usable only if:

- the device is in the current membership `devices` set
- the device is not in `revoked`
- `expires_at` is still in the future

### Selection Rules

If multiple valid presence records are encountered for the same device:

1. latest `published_at` wins
2. if `published_at` ties, latest `expires_at` wins
3. if still tied, choose the lexicographically largest canonical payload

## Lookup Rules

On startup:

1. fetch membership from the membership key
2. validate and select the winning membership record
3. for each active device in `devices`, derive its presence key
4. fetch presence for each device
5. validate and select the winning presence record for each device
6. ignore any presence for devices not in active membership
7. dial all fresh peers except self
8. rewrite local cache from the winning global records

## Write Rules

### Membership Writes

Membership v1 is intentionally serialized.

The expected write sequence is:

1. read the current winning membership record
2. apply exactly one logical membership change
3. increment `revision`
4. set `updated_at` to now
5. re-sign with the shared identity key
6. publish to DHT
7. mirror to Nostr fallback
8. update local cache only after global publish succeeds

V1 does not attempt to support concurrent independent membership edits from
multiple devices. If that becomes necessary, the next step is an op-based or
CRDT-like model, not an implicit stretch of this manifest design.

### Presence Writes

Each device writes only its own presence key.

The expected write sequence is:

1. gather the current externally useful multiaddrs
2. compute the device's `peer_id`
3. set `published_at` to now
4. set `expires_at` to now plus the chosen TTL
5. sign with the device key
6. publish to DHT
7. mirror to Nostr fallback

Presence is republished:

- on startup
- periodically before expiry
- on material address/reachability changes

## Nostr Mapping

Nostr fallback uses the same logical record identity and the same payload
shapes.

Suggested `d` tags:

- membership:
  `sky10-private-network:v1:membership:<identity>`
- presence:
  `sky10-private-network:v1:presence:<identity>:<device_pubkey>`

Suggested additional tags:

- `["identity", "<identity>"]`
- `["device", "<device_pubkey>"]` for presence

The same validation and selection rules apply after fetching from Nostr.

## Why This Fixes The Existing Failure

The current design stores one identity-scoped DHT record that contains one
peer ID and one multiaddr set for the entire private network. That cannot
represent many devices without clobbering.

This v1 spec fixes that by splitting discovery into:

- one durable membership record per private network
- one presence record per device

That means:

- devices no longer overwrite each other in the DHT
- startup can derive all expected presence keys from membership
- stale local files stop being authoritative by themselves
