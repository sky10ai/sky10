# Making S3 Optional

## Goal

S3 becomes an opt-in persistence layer, not a requirement. The daemon
starts and operates fully over P2P (libp2p + Nostr discovery). S3 is
added when you want durable file storage or content pinning.

## Current S3 Dependencies

| Feature | S3 usage | Can be P2P-only? |
|---------|----------|-------------------|
| Device registry | `devices/*.json` | Yes — Nostr + DHT already publish multiaddrs |
| AutoConnect | reads `devices/` to find peers | Yes — query Nostr instead |
| Identity sync | `identity/` prefix | Yes — exchange over libp2p stream on join |
| Key/manifest | `keys/` prefix | Yes — replicate over P2P on connect |
| KV store | `kv/` prefix, polled | Yes — CRDT over libp2p, ~200 lines |
| Invites/join | `invites/` prefix | Yes — direct libp2p handshake |
| Ops log (sync) | `ops/` prefix, polled | Yes — stream ops over libp2p, store-and-forward |
| File blobs | `blobs/`, `packs/` | **No** — needs a store. S3, IPFS, or local |
| Debug dumps | `debug/` prefix | Yes — stream directly to requester |
| Schema marker | `schema.json` | Unnecessary without S3 |

## What Changes

### Phase 1: S3-free daemon startup
- `sky10 serve` starts without S3 credentials
- P2P node initializes (libp2p + Nostr discovery)
- KV works over P2P only (CRDT sync on connect)
- Device discovery via Nostr (already implemented)
- Join flow: `sky10 join <identity>` connects via Nostr → libp2p handshake → key exchange
- Web UI works (embedded, served on HTTP port)

### Phase 2: S3 as optional storage backend
- `sky10 storage add s3 --bucket X --endpoint Y` attaches S3
- Drives (skyfs) require a storage backend — S3 or local or IPFS
- When S3 is attached, blobs are pinned there for durability
- Ops log can optionally replicate to S3 for offline catch-up
- Without S3, file sync is live-only (P2P streaming between online peers)

### Phase 3: Content-addressed pinning
- Agents produce artifacts → content-addressed blobs
- S3 keeps blobs alive when the producing agent goes offline
- IPFS integration as alternative pinning backend
- `sky10 pin <cid>` → pin to configured backend

## UI Changes

- Drives/storage moves below core features (KV, Devices, Network)
- Settings shows storage backends (none, S3, IPFS) as optional config
- Onboarding no longer requires S3 — just start the daemon

## Config Changes

Current: S3 credentials required at startup, config.json must exist.

New: Minimal config (identity only). Storage backends added post-init.

```
~/.sky10/
  keys/          # identity + device keys (required)
  config.json    # optional: storage backends, nostr relays
```
