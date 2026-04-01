---
created: 2026-03-30
model: claude-opus-4-6
---

# Skylink — P2P Agent Communication

Built `pkg/link/` — libp2p-based P2P layer for real-time sync
notifications between own devices and encrypted agent communication.
Three milestones shipped (M1-M3), M4 (public network) partial.

## M1: Own-Device Direct Sync (v0.25.0-v0.25.3)

Instant KV sync between own devices via libp2p direct streams.

- `identity.go` — sky10 Ed25519 keys <-> libp2p peer IDs
- `node.go` — libp2p host, Noise encryption, hole punching, private/network modes
- `gater.go` — ConnectionGater allowlist (private mode rejects unknown peers)
- `notify.go` — NotifyOwn/OnSyncNotify via direct streams (never GossipSub)
- `discovery.go` — same-bucket auto-discovery via S3 devices/ registry + multiaddrs
- Wired into daemon: KV notifier fires after S3 upload, auto-connect on startup

Tested live: two devices auto-connect, KV updates propagate in ~2s.

## M2: Protocol + Capabilities

Request/response protocol over libp2p streams.

- `protocol.go` — length-prefixed JSON wire format, Node.Call with deadline propagation
- `handler.go` — capability registry, HandleStream dispatcher, built-in ping
- `skylink.connect`, `skylink.call` RPC + CLI

Tested live: `sky10 link call <address> ping` → `{"pong":true}` between devices.

## M3: Encrypted Channels

Encrypted pub/sub with GossipSub + AES-256 channel keys.

- `pubsub.go` — GossipSub wrapper (network layer only, never carries sync data)
- `channel.go` — CreateChannel, JoinChannel, SendToChannel, InviteToChannel
- Channel keys use pkg/key primitives (GenerateSymmetricKey, WrapKey, Encrypt)
- Non-members receive ciphertext but can't decrypt

Unit tested. Not tested live.

## M4: Public Network (partial, not hardened)

- `record.go` — agent records via DHT PutValue/GetValue (custom key, not real IPNS)
- `nostr.go` — Nostr discovery (publish/query multiaddrs, NIP-78)
- `skylink.resolve`, `skylink.publish` RPC + CLI
- Network mode in node.go — joins public DHT, enables relay + AutoNAT

Not done: real IPNS, authorize/revoke, network mode gater hardening,
channel RPC/CLI, live cross-user testing.

## Security Model

Private sync (FS/KV) and network communication use completely separate
code paths. Sync notifications are direct point-to-point streams, never
GossipSub. A bug in the network layer cannot leak private sync data.

Two encryption layers:
- **Transport (Noise):** E2E on every connection, even through relays
- **Application (channel keys):** AES-256-GCM per channel, wrapped per member

## Files

```
pkg/link/
├── identity.go       (78 lines)
├── node.go           (198 lines)
├── gater.go          (92 lines)
├── notify.go         (54 lines)
├── protocol.go       (121 lines)
├── handler.go        (118 lines)
├── pubsub.go         (135 lines)
├── channel.go        (198 lines)
├── record.go         (99 lines)
├── discovery.go      (158 lines)
├── nostr.go          (98 lines)
├── rpc.go            (175 lines)
commands/link.go      (143 lines)
```

46 tests.

## Commits

- [`9085844`](https://github.com/sky10ai/sky10/commit/9085844) feat(link): add pkg/link with identity bridging, libp2p node, and sync notifications
- [`fda9c1c`](https://github.com/sky10ai/sky10/commit/fda9c1c) feat(link): add request/response protocol and capability registry
- [`fef7ea7`](https://github.com/sky10ai/sky10/commit/fef7ea7) feat(link): add encrypted channels with GossipSub pub/sub
- [`d7baf7c`](https://github.com/sky10ai/sky10/commit/d7baf7c) feat(link): add connection gater, DHT records, discovery, and Nostr
- [`4de8e46`](https://github.com/sky10ai/sky10/commit/4de8e46) feat(link): wire skylink into daemon with RPC, CLI, and KV sync notifications
- [`3b0b1f6`](https://github.com/sky10ai/sky10/commit/3b0b1f6) feat(link): auto-discover and connect to own devices via S3 multiaddrs
