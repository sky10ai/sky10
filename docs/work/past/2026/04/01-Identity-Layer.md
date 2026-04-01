---
created: 2026-04-01
model: claude-opus-4-6
---

# Identity Layer — Separate Identity from Device Keys

Introduced `pkg/id/` to decouple user identity from device transport.
Previously every device had its own `sky10q...` address — the world saw
N addresses for one person. Now one identity key is shared across all
devices, and each device has a separate transport key for libp2p.

## Why

The single-key-per-device model was a dead end for three reasons:

1. **External identity fragmentation.** Channels, IPNS records, contacts,
   and the future agent marketplace all need to address "you" — not
   "your laptop" vs "your phone." With N addresses per person, every
   system that references identity needs to handle N-way aliasing.

2. **Encryption key distribution.** Namespace keys are wrapped with the
   identity public key. If each device has a different key, adding a
   device means re-wrapping every namespace key for the new device's
   pubkey. With a shared identity key, any device can unwrap immediately
   — zero key redistribution cost.

3. **Foundation for delegation.** Agents need their own identities with
   scoped permissions. That requires a clear concept of "identity" vs
   "device" — so an agent's identity can be delegated from a human's
   identity. Without this split, there's no clean way to express
   "identity A authorized identity B."

This work is the structural prerequisite for: multi-device real identity,
agent delegation, channel membership that survives device changes, and
IPNS publishing under a stable address.

## What shipped (v0.28.0)

### pkg/id/ — 3 source files, ~460 lines

- **bundle.go** — `Bundle` type: holds identity key + device key + signed
  manifest. `Address()` returns identity. `DeviceAddress()` returns device.
  `IsDeviceAuthorized()` checks manifest.

- **manifest.go** — `DeviceManifest`: signed list of authorized device
  public keys. `AddDevice`, `RemoveDevice`, `HasDevice`, `Sign`, `Verify`.
  Signing payload is canonical (sorted by pubkey) for determinism.

- **store.go** — `Store`: disk persistence at `~/.sky10/keys/`. `Generate`
  creates fresh identity + device + manifest. `Migrate` converts legacy
  `key.json` (identity = old key, new device key generated). `Load`
  auto-migrates if needed.

- **rpc.go** — `identity.show` and `identity.devices` RPC methods.

### Downstream changes

- **pkg/link/node.go** — `New()` accepts `*id.Bundle`. Peer ID derived
  from device key, `Address()` from identity key. Added `NewFromKey()`
  convenience for tests.

- **pkg/link/identity.go** — Removed `PeerIDFromAddress()` and
  `AddressFromPeerID()` — identity and peer ID are no longer 1:1.

- **pkg/link/record.go** — DHT records keyed by identity address (was
  peer ID). `AgentRecord.PeerID` → `DevicePeerID`.

- **pkg/link/discovery.go** — `AutoConnect` skips self by peer ID (not
  address, since all own devices share identity address). Resolver no
  longer calls `PeerIDFromAddress`.

- **commands/serve.go** — Loads `id.Bundle`, passes identity key to fs/kv,
  full bundle to link node.

- **commands/fs_daemon.go** — `fs init` and `fs join` use `id.Store`.

- **Web UI** — Settings page shows identity address, device count, and
  authorized devices manifest. Network page matches peers by peer ID
  from multiaddrs.

### Tests — 48 tests across 4 files

- `bundle_test.go` — Address determinism, two-device same-identity,
  table-driven validation rejection (4 cases).
- `manifest_test.go` — Deterministic payload ordering, table-driven
  tampering detection (6 cases), JSON round-trip, edge cases.
- `store_test.go` — Generate, save/load, migration, auto-migration.
- `integration_test.go` — MinIO: two devices sharing a bucket with
  shared identity, manifest publish/load via S3, device-added-after-
  initial-sync flow.

## Migration path

Existing users: on first daemon start, `id.Store.Load()` detects legacy
`key.json`, calls `Migrate()`. The old key becomes the identity key
(preserving the user's `sky10q...` address). A new device key is
generated. Legacy `key.json` is preserved (not deleted).

## Disk layout

```
~/.sky10/keys/
├── identity.json    # Identity key — "you" (same on all devices)
├── device.json      # Device key — unique per device
├── manifest.json    # Signed device manifest
└── key.json         # Legacy (preserved after migration)
```

## Not yet implemented

- Identity key transfer during `fs join` (manual copy for now)
- Delegation (identity A authorizes identity B with scoped permissions)
- Passkeys / Secure Enclave storage
- Namespace key hierarchy (per-namespace scoping)
- Key rotation / revocation
- Recovery (social, hardware, time-lock)
