# Making S3 Optional

## Goal

S3 becomes an opt-in persistence layer, not a requirement. The daemon
starts and operates fully over P2P (libp2p + Nostr discovery). S3 is
added when you want durable file storage or content pinning.

## Identity Model

Each device generates its own identity on first `sky10 serve`. Two
independent devices are two separate identities — they don't discover
or sync with each other.

**Join is the trust ceremony.** The inviter's identity wins. The joiner
adopts it, discards its own. After join, both devices share one
`sky10q...` address and can sync KV, discover each other, etc.

## Discovery

Nostr relays are the primary discovery layer (private mode). Reasons:

- DHT needs bootstrap nodes — private mode has none
- Both devices behind NAT can publish/query the same relay
- Single HTTP request vs iterative DHT lookups on a sparse table
- Damus/nos.lol have high uptime; 2-node DHT is fragile

DHT is reserved for Network mode (many public peers).

Resolution order: S3 device registry (if configured) → Nostr → DHT (network mode).

## Phases and Milestones

### Phase 1: S3-free daemon startup ✅

**Milestone: `sky10 serve` runs with zero config, KV syncs between peers.**

Completed:

1. `config.Load()` returns empty config when no `config.json` (not an error)
2. `Config.HasStorage()` gates all S3 code paths
3. `SyncIdentity(nil backend)` generates/loads identity locally
4. `serve.go` guards device registration, credential check, multiaddr
   publish behind `backend != nil`
5. KV namespace keys resolved from local cache (generate if missing)
6. KV uploader/poller skipped when no backend
7. New `/sky10/kv-sync/1.0.0` protocol — encrypted snapshot push over
   libp2p streams on every KV change
8. Nostr discovery wired into `Resolver` and `AutoConnect`
9. Multiaddrs published to Nostr relays on startup
10. `NostrSecretKey` derived deterministically from device key

Files changed:
- `commands/serve.go` — nil backend flow, Nostr wiring, KV P2P sync
- `commands/fs_daemon.go` — `makeBackend` returns nil, P2P join command
- `pkg/config/config.go` — `HasStorage()`, `Relays()`, `NostrRelays`
- `pkg/id/sync.go` — `localIdentity()` for nil backend
- `pkg/kv/store.go` — nil backend support, `SetP2PSync`, `pokeSync`
- `pkg/kv/p2p.go` — P2P snapshot sync protocol (new)
- `pkg/kv/crypto.go` — `getOrCreateNamespaceKeyLocal`
- `pkg/kv/poller.go` — extracted `diffAndMerge` as shared function
- `pkg/link/discovery.go` — `WithNostr`, Nostr in `AutoConnect`
- `pkg/link/helpers.go` — `HostMultiaddrs`, `NostrSecretKey` (new)
- `pkg/link/join.go` — P2P invite/join protocol (new)

### Phase 2: P2P join (end-to-end)

**Milestone: second device joins first device with zero S3, full KV sync working.**

Protocol is implemented, needs end-to-end wiring:

1. `sky10 invite` command — generates `sky10p2p_...` code from running daemon
2. `sky10 join sky10p2p_...` — finds inviter via Nostr, connects over libp2p
3. Inviter presents join request to user (RPC notification or auto-approve)
4. On approval: inviter sends identity key + manifest + namespace keys
5. Joiner adopts identity, saves bundle, starts syncing KV immediately
6. Inviter updates its own manifest (adds joiner's device key)
7. Test: two fresh devices, no S3, join + KV round-trip

### Phase 3: S3 as optional storage backend

**Milestone: `sky10 storage add s3` attaches S3 to a running P2P-only daemon.**

1. New `sky10 storage add s3 --bucket X --endpoint Y` command
2. Writes S3 config to `config.json`, daemon picks it up
3. KV starts uploading snapshots to S3 (in addition to P2P sync)
4. KV poller starts polling S3 for offline catch-up
5. Device registration written to S3
6. Drives (skyfs) can now be created — they require a storage backend
7. Existing `sky10 fs init` preserved for S3-first setup

### Phase 4: Content-addressed pinning

**Milestone: blobs survive producer going offline.**

1. Agents produce artifacts → content-addressed blobs
2. S3 keeps blobs alive when producing agent is offline
3. IPFS integration as alternative pinning backend
4. `sky10 pin <cid>` → pin to configured backend
5. Without a pinning backend, file sync is live-only (P2P streaming)

## UI Changes

- Drives/storage moves below core features (KV, Devices, Network)
- Settings shows storage backends (none, S3, IPFS) as optional config
- Onboarding no longer requires S3 — just start the daemon

## Config

Current (with S3):
```json
{
  "bucket": "my-bucket",
  "region": "us-east-1",
  "endpoint": "https://s3.example.com"
}
```

New (P2P-only, no config.json needed):
```
~/.sky10/
  keys/          # identity + device keys (auto-generated)
```

With optional storage added later:
```json
{
  "bucket": "my-bucket",
  "endpoint": "https://s3.example.com",
  "nostr_relays": ["wss://relay.damus.io", "wss://nos.lol"]
}
```
