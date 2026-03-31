# Plan: pkg/link/ — Skylink P2P Agent Communication

## Context

Build `pkg/link/` as the P2P communication layer for sky10 agents. Unlike
`pkg/fs/` and `pkg/kv/` (S3-based state sync), skylink is **direct peer-to-peer
communication** — no S3 in the data path. Agents connect to each other, call
capabilities, subscribe to channels, and stream data.

Skylink also provides the **real-time backbone** for existing FS/KV sync. When
peers are connected, updates push instantly via skylink instead of waiting for
S3 polling (30s). S3 remains the durable store and offline catch-up mechanism.

The stack:
- **libp2p** — all communication: direct, hole punching (DCUtR), circuit relay v2
- **GossipSub** — pub/sub for channels, real-time notifications, group messaging
- **IPNS** — persistent agent profiles (capabilities, endpoints) via DHT
- **Nostr** — discovery fallback only (helps two nodes find each other when DHT fails)

## Design Decisions

- **Private by default.** A Dropbox replacement user doesn't care about P2P
  networking. Default mode connects only to own devices (same S3 bucket). No
  public DHT, no IPNS, no relay service, no Nostr. Invisible to the network.
  Public network features are opt-in.
- **libp2p does the heavy lifting.** Encrypted connections (Noise), NAT traversal,
  relay, DHT, stream multiplexing — all built in.
- **IPNS for identity (network mode).** Each agent publishes a signed record to
  DHT. Schema is extensible.
- **Nostr is a dumb pipe (network mode).** Publishes multiaddrs for discovery.
  Zero application traffic.
- **No S3, no Tor.** S3 is for sync (FS/KV). Tor deferred.
- **Identity bridging.** sky10 Ed25519 keys map directly to libp2p peer IDs.

## Operating Modes

### Private Mode (default)

For the Dropbox replacement user. Sync your files, nothing else.

```
- Connect ONLY to devices in the same S3 bucket
- No public DHT participation
- No IPNS publishing (invisible to the network)
- No relay service (don't relay for strangers)
- No Nostr
- Discovery: same-bucket only (devices/ registry)
- GossipSub: only between own devices
- Sync notifications: work (own devices get instant updates)
```

### Network Mode (opt-in)

For agent-to-agent communication, collaboration, marketplace.

```
- Join public IPFS DHT
- Publish IPNS record (agent profile, capabilities)
- Accept connections from authorized peers (allowlist)
- Act as circuit relay for the network
- Nostr discovery enabled
- Full capability registry exposed to authorized peers
```

Enabled via config: `sky10 link network enable` or config file.

## Security

### CRITICAL: Private/Network Isolation

Private sync (FS/KV — family photos, journals, personal files) and network
communication (channels, agent comms) use **completely separate code paths**.
A bug, exploit, or misconfiguration in the network layer cannot leak private
sync data because private sync never enters the network layer's infrastructure.

```
Node (single libp2p host)
|
+-- Own-device layer (PRIVATE)
|   +-- Auto-discovered from S3 devices/ registry
|   +-- Direct point-to-point streams for sync notifications
|   +-- NEVER touches GossipSub
|   +-- ConnectionGater ensures only own devices
|
+-- Network layer (OPT-IN, isolated)
    +-- GossipSub for channels only
    +-- Hashed topic names (opaque, no human-readable names)
    +-- Encrypted content (channel keys)
    +-- Authorized external peers only
```

**Private sync notifications** use `NotifyOwn()` — iterates connected own-device
peers and sends a poke via direct libp2p stream. One-to-one, not broadcast. No
GossipSub involvement. No topic subscription. Nothing for a network peer to
observe.

**Network channels** use `Publish()` — GossipSub with encrypted content and
hashed topic names. Explicitly shared, network-facing things only.

```go
// PRIVATE: direct stream to each own device. Never touches GossipSub.
func (n *Node) NotifyOwn(ctx context.Context, topic string) error {
    for _, peer := range n.ownDevices() {
        go n.sendPoke(ctx, peer, topic)
    }
    return nil
}

// NETWORK: GossipSub publish. Only for channels. Never carries sync data.
func (n *Node) Publish(ctx context.Context, topic string, data []byte) error {
    return n.ps.Publish(topic, data)
}
```

Two separate methods. Two separate code paths. `NotifyOwn` doesn't import or
call anything GossipSub-related. Private sync topics (`fs:Photos`, `kv:default`)
never enter the pub/sub system.

**What a network peer can see (even if fully connected):**

| Data | Visible? |
|------|----------|
| Private sync topics (fs:Photos, kv:default) | **No** — direct streams, never GossipSub |
| Sync notification content | **No** — point-to-point to own devices only |
| Which own devices you have | Partial — can see connected peer IDs, not what's syncing |
| Network channel topic names | Hashed only — opaque, no human-readable names |
| Network channel message content | **No** — encrypted with channel key |
| Number of channel subscriptions | Yes — GossipSub count visible to connected peers |

### Authentication

- **Transport:** libp2p Noise protocol on every connection. Mutual authentication
  — both sides cryptographically prove they hold their private key. You always
  know who you're talking to. Relay nodes forward opaque ciphertext.
- **Application:** Channel messages encrypted with channel key (AES-256-GCM).
  Only members who hold the key can decrypt.

### Authorization

- **Connection gating:** In private mode, the node runs a libp2p `ConnectionGater`
  that rejects connections from any peer not in the same-bucket device registry.
  In network mode, maintains an allowlist of authorized peer addresses.
- **Capability access:** Handlers can check `req.PeerID` / `req.Address` and
  reject unauthorized callers. Built-in capabilities (ping, info) are open by
  default. Custom capabilities can restrict to specific peers.
- **Channel membership:** Controlled via key wrapping. No key = can't decrypt =
  can't read, even if you receive the GossipSub messages.

### Privacy

- **Private mode:** Zero network visibility. No DHT, no IPNS, no Nostr. Only
  connects to known devices via direct IP from S3 registry. Indistinguishable
  from a regular TCP connection to an observer. Sync notifications are direct
  streams — no pub/sub metadata to leak.
- **Network mode:** Peer ID and multiaddrs visible in DHT. IPNS record is
  public (but you control what's in it). GossipSub topic subscriptions visible
  to connected peers (hashed, not human-readable). Message content always encrypted.

### Threat Mitigations

| Threat | Mitigation |
|--------|-----------|
| Private sync data leaks to network | Structural: private sync uses direct streams, never GossipSub |
| Unauthorized connections | ConnectionGater allowlist (private: same-bucket only) |
| Man-in-the-middle | Noise mutual auth (both sides verify keys) |
| Replay attacks | Message IDs + timestamps |
| Key compromise (device) | Revoke device, rotate channel keys |
| Key compromise (channel) | Key rotation, re-wrap for remaining members |
| Metadata leakage | Private mode: nothing leaks. Network: DHT has peer ID + addrs |
| Sync topic name leakage | Private sync never enters GossipSub. Network topics hashed. |
| DoS / resource exhaustion | Rate limiting on incoming streams, connection limits |
| Relay eavesdropping | Noise E2E — relay only sees ciphertext |
| GossipSub spam | Topic validators, message signing, peer scoring (built into libp2p) |

## Encryption: Two Layers

**Transport (libp2p Noise)** — every connection is E2E encrypted, even through
relays. Mutual auth, forward secrecy. Automatic.

**Application (channel keys)** — for group communication and persisted messages.
Channel key (AES-256) wrapped per member. Same pattern as FS/KV namespace keys.

```
Transport:   A ==[Noise]== relay ==[Noise]== B    (relay sees nothing)
Application: message = Encrypt(channelKey, payload)  (persisted, group-readable)
```

## Communication Patterns

1. **Request/response** — one-to-one, synchronous. Opens a libp2p stream, sends
   request, reads response.

2. **Pub/sub (GossipSub)** — topic-based, many-to-many, real-time.
   `go-libp2p-pubsub` (battle-tested — Ethereum 2.0 uses it).

3. **Channels** — persistent encrypted topics. GossipSub for real-time delivery +
   KV for persistence/history. Membership managed via key wrapping.

4. **Sync notifications** — lightweight "pull from S3" nudges sent via direct
   streams to own devices when FS/KV data changes. Never touches GossipSub.
   Turns 30s polling into instant sync.

## Real-Time Sync Notifications

```
Current:   Device A sets key -> upload to S3 -> Device B polls every 30s -> sees change
With link: Device A sets key -> upload to S3 + notify own devices -> Device B polls S3 now
```

v1: notification is just a "pull now" nudge. Existing S3 sync does the data
transfer. Direct data push is a future optimization.

Sync notifications use `NotifyOwn` — direct point-to-point streams to own
devices. **Never GossipSub.** Private sync topics never enter the pub/sub
system and are invisible to network peers.

```go
// KV store, after Set/Delete + uploader poke:
node.NotifyOwn(ctx, "kv:default")  // direct poke to own devices

// FS daemon, after outbox drain:
node.NotifyOwn(ctx, "fs:Photos")   // direct poke to own devices
```

## Channels (building block for apps)

```
Channel "general":
+-- Channel key: AES-256 (same pattern as FS/KV namespace keys)
+-- Key wrapped for each member's pubkey
+-- Real-time: GossipSub -> online members get messages instantly
+-- Persistence: KV entries under link:{channelID} (v2)
+-- Membership: add/remove via key wrapping/rotation
```

## Dependencies

```
github.com/libp2p/go-libp2p            — host, transports, NAT, relay
github.com/libp2p/go-libp2p-kad-dht    — Kademlia DHT
github.com/libp2p/go-libp2p-pubsub     — GossipSub pub/sub
github.com/ipfs/boxo                   — IPNS records
github.com/nbd-wtf/go-nostr            — Nostr client (discovery only)
```

All pure Go. No CGO.

## File Structure

```
pkg/link/
+-- identity.go       — sky10 key <-> libp2p peer ID bridging
+-- node.go           — Node: libp2p host + DHT + lifecycle
+-- gater.go          — ConnectionGater: allowlist, private/network mode
+-- notify.go         — Own-device sync notifications via direct streams
+-- protocol.go       — Wire format: length-prefixed JSON messages
+-- handler.go        — Capability registry (agents register what they can do)
+-- pubsub.go         — GossipSub wrapper: topics, subscribe, publish (network only)
+-- channel.go        — Encrypted channels: membership, keys, persistence
+-- record.go         — IPNS agent records (publish/resolve)
+-- discovery.go      — Layered: same-bucket -> DHT/IPNS -> Nostr
+-- nostr.go          — Nostr discovery (publish/query multiaddrs only)
+-- rpc.go            — skylink.* RPC handler for daemon
+-- *_test.go         — Tests alongside each file

commands/link.go      — CLI: sky10 link {status,peers,resolve,connect,call,...}
commands/serve.go     — Add link node creation + registration
main.go              — Add commands.LinkCmd()
```

---

# Milestones

## M1: Own-Device Direct Sync

**The Dropbox user gets instant sync between their devices.**

Private mode only. No public network. This is the highest-value deliverable —
every existing sky10 user benefits immediately.

### What's built

- `identity.go` (~80 lines) — sky10 key <-> libp2p peer ID bridging
- `node.go` (~250 lines) — libp2p host with Noise encryption, hole punching.
  Private mode: no public DHT, no relay service.
- `gater.go` (~120 lines) — ConnectionGater that only allows peers from the
  same-bucket device registry. Rejects all unknown connections.
- `discovery.go` (~100 lines, partial) — same-bucket discovery only. Reads
  `devices/{id}.json` from S3, extracts IPs, auto-connects on startup.
- `notify.go` (~120 lines) — Own-device sync notifications via direct streams.
  `NotifyOwn`, `OnSyncNotify`, `sendPoke`. No GossipSub.
- `rpc.go` (~150 lines, partial) — `skylink.status`, `skylink.peers`, `skylink.notify`
- `commands/serve.go` — wire link node, sync notifications, subscribe to topics
- `commands/link.go` (~80 lines, partial) — `sky10 link status`, `sky10 link peers`

### Integration with KV/FS

```go
// serve.go: after KV/FS setup
linkNode := link.New(id, link.Config{Mode: link.Private}, logger)
linkNode.SetBackend(backend)  // for same-bucket discovery
server.RegisterHandler(link.NewRPCHandler(linkNode, nil))

// Outbound: KV/FS changes notify own devices via direct streams (not GossipSub)
kvStore.SetNotifier(func(ns string) { linkNode.NotifyOwn(ctx, "kv:"+ns) })

// Inbound: when own device pokes us, trigger immediate S3 poll
linkNode.OnSyncNotify(func(from peer.ID, topic string) {
    if topic == "kv:default" { kvStore.Poke() }
})

go linkNode.Run(ctx)
```

### Config

```go
type Config struct {
    Mode        Mode     // Private (default) or Network
    ListenAddrs []string // default: ["/ip4/0.0.0.0/tcp/0", "/ip4/0.0.0.0/udp/0/quic-v1"]
}

type Mode int
const (
    Private Mode = iota  // own devices only, no public DHT
    Network              // full P2P network participation
)
```

### Tests

- Identity round-trip: sky10 key -> peer ID -> address -> verify match
- Node start/stop lifecycle
- ConnectionGater: allowed peer connects, unknown peer rejected
- Two nodes (same mock bucket): auto-discover, auto-connect
- NotifyOwn: device A notifies, device B's OnSyncNotify fires
- Isolation: NotifyOwn does not use GossipSub (verify no topic subscriptions)

### M1 deliverable

`sky10 serve` starts a private P2P node. Own devices auto-discover and connect.
KV/FS changes push instant "poll now" via direct streams to own devices. 30s
polling becomes sub-second sync. Zero configuration. Zero exposure to public
network. Private sync data structurally isolated from any network layer.

---

## M2: Protocol + Capabilities

**Devices can call capabilities on each other.**

### What's built

- `protocol.go` (~200 lines) — wire format, `WriteMessage`/`ReadMessage`, `Node.Call`
- `handler.go` (~200 lines) — capability registry, `HandleStream`, built-in `ping`/`info`
- `rpc.go` additions — `skylink.call`, `skylink.resolve`
- `commands/link.go` additions — `sky10 link call <address> <method> [json]`

### Wire format

`[4-byte big-endian length][JSON message]`

```go
type Message struct {
    ID     string          `json:"id"`
    Method string          `json:"method,omitempty"`
    Params json.RawMessage `json:"params,omitempty"`
    Result json.RawMessage `json:"result,omitempty"`
    Error  *MessageError   `json:"error,omitempty"`
}
```

### Capability registry

```go
type HandlerFunc func(ctx context.Context, req *PeerRequest) (interface{}, error)

type PeerRequest struct {
    PeerID  peer.ID
    Address string          // sky10q... of caller
    Method  string
    Params  json.RawMessage
}
```

Handlers can check `req.Address` for authorization. Built-in capabilities:
- `ping` — returns `{pong: true}`
- `info` — returns capabilities, address, version

### Tests

- WriteMessage/ReadMessage round-trip
- MaxMessageSize enforcement
- Two-node Call: A registers "echo", B calls it, response verified
- Unknown method -> error response
- ConnectionGater: unauthorized peer can't call capabilities

### M2 deliverable

`sky10 link call <address> ping` works between own devices. Foundation for
any inter-device RPC. FS/KV could register capabilities for direct peer queries.

---

## M3: Channels (Encrypted Pub/Sub)

**Foundation for chat, collaboration, and agent coordination.**

### What's built

- `channel.go` (~250 lines) — encrypted topics with membership and key management

```go
func (n *Node) CreateChannel(ctx, name) (*Channel, error)
func (n *Node) JoinChannel(ctx, id, wrappedKey) (*Channel, error)
func (n *Node) SendToChannel(ctx, channelID, data) error
func (n *Node) InviteToChannel(ctx, channelID, memberAddr) (wrappedKey, error)
func (n *Node) OnChannelMessage(channelID, handler) error
```

- Channel key management uses existing `pkg/key` primitives:
  `GenerateSymmetricKey`, `WrapKey`, `UnwrapKey`, `Encrypt`, `Decrypt`
- Messages encrypted with channel key, published to GossipSub
- Non-members receive ciphertext but can't decrypt
- `rpc.go` additions — `skylink.channelCreate/Send/Invite`
- `commands/link.go` additions — `sky10 link channel {create,send,invite}`

### Tests

- Create channel, verify key generated
- Two members: send message, verify decryption
- Non-member can't decrypt
- Invite third member, verify access
- Multiple channels: messages don't cross

### M3 deliverable

Encrypted group communication between own devices. The building block for
Slack clone, agent collaboration channels.

---

## M4: Public Network (Opt-In)

**Agent-to-agent communication across users.**

This is the milestone that opens skylink beyond own devices. Everything here
is gated behind `Mode: Network`.

### What's built

- `node.go` updates — network mode: join public IPFS DHT, enable relay service,
  enable AutoNAT
- `gater.go` updates — network mode: allowlist of authorized external peers
  (managed via `skylink.authorize` / `skylink.revoke`)
- `record.go` (~200 lines) — IPNS agent records (publish/resolve via DHT)
- `discovery.go` updates — full layered resolution: same-bucket -> DHT/IPNS -> Nostr
- `nostr.go` (~100 lines) — Nostr discovery (publish/query multiaddrs only)
- `rpc.go` additions — `skylink.publish`, `skylink.authorize`, `skylink.revoke`,
  `skylink.resolve` (full, not just same-bucket)
- `commands/link.go` additions — `sky10 link network enable`, `sky10 link publish`,
  `sky10 link authorize <address>`, `sky10 link resolve <address>`

### IPNS agent record

```go
type AgentRecord struct {
    Address      string       `json:"address"`
    PeerID       string       `json:"peer_id"`
    Capabilities []Capability `json:"capabilities"`
    Multiaddrs   []string     `json:"multiaddrs"`
    Version      string       `json:"version"`
    UpdatedAt    time.Time    `json:"updated_at"`
}
```

### Discovery layers (network mode)

```
1. Same bucket (S3 devices/ registry) -- own devices
2. DHT peer routing + IPNS -- any agent
3. Nostr -- fallback when DHT doesn't have the peer
```

### Authorization for external peers

```go
// Authorize a peer to connect
skylink.authorize({address: "sky10q..."})

// Revoke access
skylink.revoke({address: "sky10q..."})

// List authorized peers
skylink.authorized() -> [{address, name, since}]
```

Authorized peers can connect, call capabilities, and join shared channels.
Unauthorized peers are rejected at the connection level by the gater.

### Tests

- Network mode: node joins DHT, publishes IPNS record, other node resolves
- ConnectionGater: authorized external peer connects, unauthorized rejected
- Nostr: publish multiaddrs, query from second node
- Full flow: authorize -> resolve -> connect -> call capability

### M4 deliverable

Full agent-to-agent P2P network. Authorized peers connect across the internet
with hole punching + relay. IPNS profiles discoverable via DHT. Nostr fallback
for edge cases. Every agent acts as relay, growing network capacity.

---

## Execution Order

```
M1 (own-device sync)     — immediate value for all users
  -> M2 (capabilities)   — inter-device RPC
    -> M3 (channels)     — encrypted group comms
      -> M4 (network)    — cross-user agent network
```

Each milestone compiles, tests pass, ships independently.

## Line Count Estimates

| File | Lines |
|------|-------|
| identity.go (+test) | ~200 |
| node.go (+test) | ~400 |
| gater.go (+test) | ~250 |
| notify.go (+test) | ~200 |
| protocol.go (+test) | ~380 |
| handler.go (+test) | ~350 |
| pubsub.go (+test) | ~350 |
| channel.go (+test) | ~430 |
| record.go (+test) | ~350 |
| discovery.go (+test) | ~380 |
| nostr.go (+test) | ~180 |
| rpc.go (+test) | ~400 |
| commands/link.go | ~300 |
| **Total** | **~4,170** |

All files under 500-line limit.

## Critical Files to Modify

- `commands/serve.go:86` — add link node creation, wire sync notifications
- `main.go:27` — add `root.AddCommand(commands.LinkCmd())`
- `go.mod` — add libp2p, kad-dht, pubsub, boxo, go-nostr deps
- `pkg/kv/store.go` — add `SetNotifier` callback + `Poke()` method

## Verification

- `go test ./pkg/link/... -count=1` after each milestone
- M1: two-node sync notification round-trip, ConnectionGater rejects unknown peer
- M2: two-node capability call, unauthorized peer rejected
- M3: channel create, invite, send, decrypt verified
- M4: DHT publish/resolve, Nostr discovery, cross-user connection
- Manual: `sky10 serve` -> `sky10 link status` -> `sky10 link call <addr> ping`
- `make check` (gofmt + go vet) before every push

## What This Enables (Future)

- **Slack clone:** workspaces (buckets) + channels (encrypted GossipSub + KV history)
- **Agent marketplace:** IPNS profiles + capabilities + request/response
- **Instant sync:** sub-second FS/KV updates between connected devices
- **Direct peer transfers:** stream files between devices without S3 round-trip

## Tor: Deferred, Not Rejected

Tor provides 100% NAT traversal (~99% -> ~99.9%). Deferred because libp2p relay
covers ~99% and Tor adds 15MB binary + 10-30s startup. Architecture accommodates
it later as just another libp2p transport.

**Revisit when:** Mobile app (carrier NAT is where Tor earns its keep), or user
reports of connection failures from corporate/hotel/airport networks.

**Option:** Connect to system Tor daemon if available instead of embedding go-libtor.

## NOT in v1

- Voice/video (pion/webrtc)
- Tor onion services (see above)
- SFU for group calls
- Mobile thin client
- Solana registry
- Payments
- Direct data push (v1 nudges peers to poll S3)
- Channel message persistence in KV (v2)
