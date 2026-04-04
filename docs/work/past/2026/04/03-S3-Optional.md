---
created: 2026-04-03
model: claude-opus-4-6
---

# Making S3 Optional — P2P-First Architecture

S3 was a hard requirement for sky10. Every operation — identity sync,
device registration, KV sync, join flow — went through S3. This work
makes S3 opt-in. The daemon starts with nothing but a keypair and syncs
everything over libp2p + Nostr.

## Why

S3 as a requirement created several problems:

- **Onboarding friction**: new users needed an S3 bucket before they
  could do anything. Most people don't have one.
- **Single point of failure**: if S3 is down or credentials expire,
  the daemon is dead.
- **Architecture smell**: S3 was doing double duty as a database
  (device registry, invite mailbox, key store) and as blob storage.
  Only blob storage actually needs S3.
- **P2P was underutilized**: libp2p was running but only used for
  sync-notify pokes. Actual data still went through S3.

## What Changed

### Phase 1: S3-free daemon startup (d4b7366)

Core insight: pass `nil` for the backend and guard everything.

- `config.Load()` returns empty config when no `config.json` — not an
  error. Added `HasStorage()` to gate S3 code paths.
- `SyncIdentity(nil)` generates/loads identity from disk only.
- `serve.go` guards device registration, credential check, multiaddr
  publish behind `backend != nil`.
- KV namespace keys resolve from local cache or generate fresh. No S3
  round-trip needed.
- KV uploader/poller skipped entirely when no backend.

New KV P2P sync (`/sky10/kv-sync/1.0.0`):
- On every KV write → serialize snapshot → encrypt → push to all
  connected peers over libp2p stream.
- On receive → decrypt → diff against baseline → LWW merge. Same
  merge logic as the S3 poller, just different transport.
- When S3 IS configured, P2P push runs alongside S3 upload for faster
  convergence.

Nostr wired as primary discovery (private mode):
- Multiaddrs published to Nostr relays on startup.
- `AutoConnect` queries Nostr to find own devices.
- Resolver tries: S3 registry → Nostr → DHT.

### Phase 2: P2P join (9b453ee)

The join flow no longer needs S3 credentials in the invite code.

New invite format: `sky10p2p_<base64({address, nostr_relays, invite_id})>`

Flow:
1. Inviter runs `sky10 invite` → daemon generates P2P invite code
2. Joiner runs `sky10 join sky10p2p_...`
3. Joiner finds inviter via Nostr, connects over libp2p
4. Inviter auto-approves (invite code = authorization)
5. Inviter sends: identity private key (wrapped for joiner's device
   key) + signed manifest + namespace keys (wrapped for shared identity)
6. Joiner adopts identity, caches namespace keys, done

Identity model: the inviter's identity wins. Joiner discards its own.
Two devices that independently `sky10 serve` are two separate identities
until one joins the other.

### Refactor: pkg/join (1bf76d6)

Invite/join was scattered across `pkg/fs/invite.go`, `pkg/link/join.go`,
and `commands/fs_daemon.go`. This was wrong — invite/join are
identity-level operations, not filesystem operations.

Consolidated into `pkg/join/` with three files:
- `invite.go` — encode/decode both P2P and S3 invite formats, S3 mailbox ops
- `handler.go` — libp2p stream handler for incoming join requests
- `request.go` — client side of the P2P join handshake

Commands moved from `sky10 fs invite` / `sky10 fs join` to top-level
`sky10 invite` / `sky10 join`.

## Design Decisions

**Nostr over DHT for private mode discovery.** DHT needs bootstrap
nodes — in private mode with 2-3 own devices, the DHT is empty and
useless. Nostr relays (Damus, nos.lol) have high uptime and work
through NAT. DHT reserved for future Network mode.

**Namespace key generation without S3.** First device generates the
key and caches locally. On P2P join, the inviter wraps and sends it.
On S3 join, keys are exchanged via the S3 mailbox as before.

**Auto-approve.** The invite code itself is the authorization. No
manual approval step. Same principle as the S3 flow where possession
of the invite code (which contains S3 credentials) is sufficient.

**KV sync is push, not poll.** In P2P mode there's nothing to poll.
Every write pushes the encrypted snapshot to all connected peers.
Rate-limited to 1 push/peer/second.

## Remaining Work

**Phase 3: `sky10 storage add s3`** — attach S3 to a running P2P-only
daemon at runtime. KV starts uploading to S3 for offline catch-up.
Drives (skyfs) can be created once a storage backend exists.

**Phase 4: Content-addressed pinning** — S3/IPFS keeps blobs alive
when the producing peer goes offline.

## Commits

| SHA | Message |
|-----|---------|
| d4b7366 | feat: make S3 optional — daemon starts and syncs over P2P |
| 45c6750 | docs(work): update S3-optional plan with milestones and phase 1 status |
| 9b453ee | feat(link): complete P2P join flow with auto-approve and key exchange |
| 1bf76d6 | refactor: move invite/join to pkg/join, commands to top-level |

## Files Created

- `pkg/join/invite.go` — invite encode/decode + S3 mailbox ops
- `pkg/join/handler.go` — P2P join stream handler
- `pkg/join/request.go` — P2P join client
- `pkg/kv/p2p.go` — KV snapshot sync over libp2p
- `pkg/link/helpers.go` — HostMultiaddrs, NostrSecretKey
- `commands/invite.go` — top-level invite command
- `commands/join.go` — top-level join command

## Files Modified

- `commands/serve.go` — nil backend flow, Nostr + KV P2P wiring
- `commands/fs_daemon.go` — makeBackend returns nil, removed join code
- `commands/fs.go` — removed invite/join subcommands
- `pkg/config/config.go` — HasStorage(), Relays(), NostrRelays
- `pkg/id/sync.go` — localIdentity() for nil backend
- `pkg/kv/store.go` — nil backend, SetP2PSync, pokeSync
- `pkg/kv/crypto.go` — local-only namespace key resolution
- `pkg/kv/poller.go` — extracted diffAndMerge as shared function
- `pkg/link/discovery.go` — WithNostr, Nostr in AutoConnect/Resolver
- `pkg/fs/rpc_invite.go` — calls into pkg/join
