# Skylink — P2P Agent Communication

## Status: M1-M3 shipped, M4 partial

## What's Done

### M1: Own-Device Direct Sync ✅ (v0.25.0-v0.25.3)

Instant KV sync between own devices via libp2p direct streams.

- `identity.go` — sky10 Ed25519 keys <-> libp2p peer IDs
- `node.go` — libp2p host, Noise encryption, hole punching, private/network modes
- `gater.go` — ConnectionGater allowlist (private mode rejects unknown peers)
- `notify.go` — `NotifyOwn`/`OnSyncNotify` via direct streams (never GossipSub)
- `discovery.go` — same-bucket auto-discovery via S3 `devices/` registry + multiaddrs
- `rpc.go` — `skylink.status`, `skylink.peers`
- `commands/link.go` — `sky10 link status`, `sky10 link peers`
- `commands/serve.go` — node wired into daemon, KV notifier fires after S3 upload
- KV `SetNotifier` + `Poke()` for real-time sync triggering
- `UpdateDeviceMultiaddrs` — publish libp2p addresses to S3 device registry
- `AutoConnect` — connect to own devices on startup

**Tested live:** two devices auto-connect, KV updates propagate in ~2s.

### M2: Protocol + Capabilities ✅ (v0.25.0)

Request/response protocol over libp2p streams.

- `protocol.go` — length-prefixed JSON wire format, `Node.Call` with deadline propagation
- `handler.go` — capability registry, `HandleStream` dispatcher, built-in `ping`
- `rpc.go` — `skylink.connect`, `skylink.call`
- `commands/link.go` — `sky10 link connect`, `sky10 link call`

**Tested live:** `sky10 link call <address> ping` → `{"pong":true}` between devices.

### M3: Channels ✅ (v0.25.0)

Encrypted pub/sub with GossipSub + AES-256 channel keys.

- `pubsub.go` — GossipSub wrapper (network layer only, never carries sync data)
- `channel.go` — `CreateChannel`, `JoinChannel`, `SendToChannel`, `InviteToChannel`
- Channel keys use `pkg/key` primitives (GenerateSymmetricKey, WrapKey, Encrypt)
- Non-members receive ciphertext but can't decrypt

**Unit tested:** create, send/receive, non-member exclusion, invite. Not tested live.

## What's Partially Done

### M4: Public Network (partial)

Built but not tested or hardened:

- `record.go` — agent records via DHT `PutValue`/`GetValue` (custom key, **not real IPNS**)
- `nostr.go` — Nostr discovery (publish/query multiaddrs, uses NIP-78)
- `rpc.go` — `skylink.resolve`, `skylink.publish`
- `commands/link.go` — `sky10 link resolve`, `sky10 link publish`
- Network mode in `node.go` — joins public DHT, enables relay + AutoNAT

**Not done:**
- Real IPNS publishing (using `boxo/ipns` signed records, not custom DHT keys)
- `skylink.authorize` / `skylink.revoke` RPC methods
- `sky10 link network enable` CLI command
- Network mode gater hardening (currently accepts all authenticated peers)
- Live testing of DHT, Nostr, or network mode
- Channel RPC methods (`skylink.channelCreate/Send/Invite`)
- Channel CLI commands (`sky10 link channel ...`)

## Security Model

### CRITICAL: Private/Network Isolation

Private sync (FS/KV) and network communication (channels, agent comms) use
**completely separate code paths**. A bug in the network layer cannot leak
private sync data.

```
Own-device layer (PRIVATE):
  - Direct point-to-point streams for sync notifications (NotifyOwn)
  - NEVER touches GossipSub
  - ConnectionGater: only same-bucket devices

Network layer (OPT-IN):
  - GossipSub for channels only
  - Hashed topic names
  - Encrypted content (channel keys)
  - Authorized external peers only
```

### Encryption: Two Layers

- **Transport (Noise):** E2E on every connection, even through relays. Automatic.
- **Application (channel keys):** AES-256-GCM per channel, wrapped per member.

### What a network peer can see (even if connected)

| Data | Visible? |
|------|----------|
| FS/KV sync topics | **No** — direct streams, never GossipSub |
| File contents / KV values | **No** — encrypted with namespace keys |
| Sync notification content | **No** — point-to-point to own devices |
| Channel message content | **No** — encrypted with channel key |
| Connected peer IDs | Yes — libp2p connection metadata |
| Channel subscription count | Yes — GossipSub metadata |

### Threat Mitigations

| Threat | Mitigation |
|--------|-----------|
| Private data leaks to network | Structural: separate code paths |
| Unauthorized connections | ConnectionGater allowlist |
| MITM | Noise mutual auth |
| Relay eavesdropping | Noise E2E — relay sees ciphertext only |
| GossipSub spam | Topic validators, peer scoring (libp2p built-in) |
| Key compromise | Revoke device, rotate channel keys |

## What's Next

1. **Real IPNS** — publish signed agent records resolvable by any IPFS node
2. **Channel RPC/CLI** — wire channel operations into daemon
3. **Network mode hardening** — authorize/revoke, audit exposed capabilities
4. **Live cross-user test** — two different users connecting via public DHT

## Tor: Deferred, Not Rejected

Revisit for mobile app (carrier NAT) or corporate/hotel network failures.
Architecture accommodates it as another libp2p transport.

## Dependencies

```
github.com/libp2p/go-libp2p            — host, transports, NAT, relay
github.com/libp2p/go-libp2p-kad-dht    — Kademlia DHT
github.com/libp2p/go-libp2p-pubsub     — GossipSub pub/sub
github.com/ipfs/boxo                   — IPNS records (not yet used)
github.com/nbd-wtf/go-nostr            — Nostr client (discovery only)
```

## File Inventory

```
pkg/link/
├── identity.go       (78 lines)   — sky10 key <-> libp2p peer ID
├── node.go           (198 lines)  — libp2p host, modes, lifecycle
├── gater.go          (92 lines)   — ConnectionGater allowlist
├── notify.go         (54 lines)   — own-device sync via direct streams
├── protocol.go       (121 lines)  — wire format, Call()
├── handler.go        (118 lines)  — capability registry, ping
├── pubsub.go         (135 lines)  — GossipSub wrapper
├── channel.go        (198 lines)  — encrypted channels
├── record.go         (99 lines)   — DHT agent records (not IPNS yet)
├── discovery.go      (158 lines)  — same-bucket + DHT + auto-connect
├── nostr.go          (98 lines)   — Nostr discovery
├── rpc.go            (175 lines)  — skylink.* RPC methods
└── *_test.go         (46 tests)

commands/link.go      (143 lines)  — CLI commands
```
