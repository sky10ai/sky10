---
created: 2026-04-04
model: claude-opus-4-6
---

# Making S3 Optional — Complete

S3 was a hard requirement for sky10. Every operation — identity sync,
device registration, KV sync, join flow — went through S3. This work
makes S3 opt-in. The daemon starts with nothing but a keypair and syncs
everything over libp2p + Nostr. S3 becomes durable storage you can
attach later, not a prerequisite.

This doc covers the full effort from initial architecture through
production debugging across two physical machines (macOS + Linux).

## Why

- **Onboarding friction**: new users needed an S3 bucket before they
  could do anything. Most people don't have one.
- **Single point of failure**: if S3 is down or credentials expire,
  the daemon is dead.
- **Architecture smell**: S3 was doing double duty as a database
  (device registry, invite mailbox, key store) and as blob storage.
  Only blob storage actually needs S3.
- **P2P was underutilized**: libp2p was running but only used for
  sync-notify pokes. Actual data still went through S3.

## Architecture: Before and After

**Before:** Every device needed `~/.sky10/config.json` with S3
credentials. Identity synced via S3. Devices discovered each other via
S3 device registry. KV synced via S3 snapshots. Join flow exchanged
credentials through S3 mailbox.

**After:** Zero config. `sky10 serve` generates a keypair and starts.
Three discovery layers find peers: S3 registry (if configured), Nostr
relays, and DHT FindPeer from manifest device keys. KV syncs over
encrypted libp2p streams. Join works peer-to-peer. S3 is an optional
persistence layer for offline catch-up and file storage.

## Phase 1: S3-Free Daemon Startup

Core insight: pass `nil` for the backend and guard everything.

### Config (pkg/config)

- `Load()` returns empty config when no `config.json` — not an error
- `HasStorage()` gates S3 code paths
- `Relays()` returns configured Nostr relays (or defaults)
- Default relays: `wss://relay.damus.io`, `wss://nos.lol`

### Identity (pkg/id)

- `SyncIdentity(nil)` generates/loads identity from disk only via
  `localIdentity()` — no S3 round-trip
- Identity key stored at `~/.sky10/keys/identity.json`
- Device key stored at `~/.sky10/keys/device.json`
- Manifest stored at `~/.sky10/keys/manifest.json`

### KV Store (pkg/kv)

- `New()` accepts nil backend — uploader/poller not created
- `Run()` with nil backend: resolves keys locally, blocks on context
- Namespace keys generated locally and cached in
  `~/.sky10/keys/ns-<namespace>-<deviceID>.key`
- `pokeSync()` triggers P2P push alongside S3 upload (if configured)
- `SetP2PSync()` attaches the P2P sync handler

### Serve Command (commands/serve.go)

- `makeBackend()` returns nil when no config
- All S3 calls guarded: device registration, credential check,
  multiaddr publish, drive startup, auto-approve
- Logs "starting in P2P-only mode" when no backend

## Phase 2: P2P Join

The join flow no longer needs S3 credentials in the invite code.

### Invite Format

`sky10p2p_<base64({address, nostr_relays, invite_id})>`

### Flow

1. Inviter runs `sky10 invite` → daemon generates P2P invite code
2. Joiner runs `sky10 join sky10p2p_...`
3. Joiner finds inviter via Nostr, connects over libp2p
4. Inviter auto-approves (invite code = authorization)
5. Inviter sends: identity private key (wrapped for joiner's device
   key) + signed manifest + namespace keys
6. Joiner adopts inviter's identity, caches namespace keys, done

Identity model: the inviter's identity wins. Joiner discards its own.

### Package: pkg/join

Consolidated from scattered code across `pkg/fs/invite.go`,
`pkg/link/join.go`, and `commands/fs_daemon.go`.

- `invite.go` — encode/decode both P2P and S3 invite formats
- `handler.go` — libp2p stream handler, auto-approve, NSKeyProvider
- `request.go` — client side of P2P join handshake

Commands moved to top-level: `sky10 invite`, `sky10 join`.

## Phase 3: KV P2P Sync — The Debugging Saga

The KV P2P sync protocol (`/sky10/kv-sync/1.0.0`) was the most
complex piece. It worked in integration tests but failed across real
machines for a cascade of reasons, each masking the next.

### Protocol Design

- On every KV write: serialize full snapshot → encrypt with namespace
  AES-256-GCM key → length-prefixed push to all connected peers over
  libp2p streams
- On receive: decrypt → diff against per-peer baseline → LWW merge
  (same logic as S3 poller)
- When S3 IS configured, P2P push runs alongside S3 upload for faster
  convergence

### Bug 1: Nostr Custom Tag Not Indexed (v0.31.1)

**Symptom:** Devices couldn't find each other at all.

**Root cause:** Published multiaddrs to Nostr using a custom `sky10`
tag. Public relays (Damus, nos.lol) don't index custom tags — only
standard NIP-78 tags.

**Fix:** Changed to use the `d` tag: `"sky10:" + address`. All relays
index `d` tags for replaceable events (kind 30078). ae0285b

### Bug 2: JSON Encoding of Encrypted Snapshots (v0.31.2)

**Symptom:** P2P push "succeeded" (no errors), but receiver silently
failed to decode the snapshot.

**Root cause:** `fmt.Sprintf("%q", encrypted)` produces Go string
escaping (`"\x00\x01..."`), not valid JSON. The receiver's
`json.Unmarshal` returned an error that was logged but not obvious in
the noise.

**Fix:** `json.Marshal(encrypted)` produces proper base64-encoded
JSON. Same bug existed in the join identity key encoding. 3405ba2

**Regression test:** `TestP2PSyncMsgEncoding` — full encode/decode
roundtrip of the wire format. c386ff7

### Bug 3: Context Cancellation Killing Pushes (v0.31.8)

**Symptom:** KV writes from the RPC handler never reached the peer.
Direct pushes (e.g. from RegisterProtocol's initial broadcast) worked.

**Root cause:** `pokeSync(ctx)` passed the RPC request context to
`go p2p.PushToAll(ctx)`. When the RPC response was sent back to the
caller, the context was cancelled — killing the push goroutine mid-
stream. The goroutine started, opened a libp2p stream, then the
context died before the write completed.

**Fix:** `go p2p.PushToAll(context.Background())`. The push goroutine
is fire-and-forget — it should not be tied to the request lifecycle.
52ca47f

**Regression test:** `TestP2PSyncCancelledContext` — calls Set with a
context that's immediately cancelled, verifies data still reaches the
peer. 1378889

### Bug 4: Rate Limiter Silently Dropping Pushes (v0.31.9)

**Symptom:** First write after connecting synced fine. Subsequent
writes within the same second were lost.

**Root cause:** `RegisterProtocol()` called `PushToAll()` on startup,
which set `lastPush[peer]` timestamps for all connected peers. When
`Set()` triggered `pokeSync()` within the same second, the per-peer
rate limiter (`1 push/peer/second`) silently dropped the push. No log,
no error — just gone.

**Fix:** Removed the rate limiter entirely. The full-snapshot design
means every push is idempotent — receiving the same snapshot twice is a
no-op (baseline diffing handles it). The rate limiter was premature
optimization that caused data loss. f271c86

**Regression test:** `TestP2PSyncRapidSets` — fires 5 Sets with no
delay after an initial push, verifies the final value arrives. 1378889

### Bug 5: Auto-Connect Race on Restart (v0.31.10)

**Symptom:** After both devices restarted, they couldn't find each
other. Manual restart of one fixed it.

**Root cause:** Both devices restart simultaneously. Both publish new
multiaddrs to Nostr, then immediately run `AutoConnect`. But the other
device hasn't published its new addrs yet — the old addrs are stale.
Both find stale entries and fail to connect.

**Fix:** Added a 15-second retry. If `AutoConnect` finds 0 connected
peers, it tries again after 15s — by which time the other device has
published. 28abcfe

### Debugging Infrastructure

Diagnosing these bugs across machines (macOS local, Linux VM remote)
required building observability tooling along the way:

- `skyfs.logs` RPC (97316a3): reads `/tmp/sky10/daemon.log` with
  optional filter, returns last N lines. Essential for remote debugging
  without SSH.
- Progressive log upgrades: P2P sync logs moved from Debug to Info
  (de3614a), then all failure paths upgraded to Warn (2f05fa6).
- Diagnostic nil-check log in pokeSync (649b893): made it obvious when
  P2P sync wasn't wired up.

## Phase 4: Discovery Hardening

### Network Mode by Default (08dbc1d)

Changed libp2p from `Private` mode to `Network` mode. This enables:
- DHT (Kademlia) — peers find each other through the distributed hash
  table
- Relay — NAT traversal via relay nodes
- AutoNAT — automatic NAT detection
- Hole punching — direct connections through NAT

### Nostr Discovery Improvements

- `QueryAll` (limit 10) instead of `Query` (limit 1) — finds all
  devices, not just one (18d5d1b)
- `autoConnectFromNostr` iterates all results, skipping self by peer ID

### DHT FindPeer from Manifest (6e14541)

Since libp2p peer IDs are deterministically derived from device keys
(`PeerIDFromKey` in `pkg/link/identity.go`), devices that share a
manifest can compute each other's peer IDs without any external service.

Added `PeerIDFromPubKey(ed25519.PublicKey)` and
`autoConnectFromManifest()` as Layer 3 in AutoConnect:

1. Read manifest device entries
2. Compute peer ID from each device's public key
3. Skip self and already-connected peers
4. `dht.FindPeer(ctx, peerID)` to get current multiaddrs
5. Connect

**Three discovery layers (in order):**

| Layer | Source | Requirement |
|-------|--------|-------------|
| S3 device registry | `devices/*.json` | S3 backend configured |
| Nostr relays | NIP-78 replaceable events | Internet access |
| DHT FindPeer | Manifest device pubkeys | Both peers on DHT |

If all three fail, devices still connect when they happen to be on the
same LAN (mDNS, which libp2p enables by default in network mode).

## Device IDs

Standardized device ID format across the codebase:

- Format: `D-` + 8 characters derived from device key address
- `Key.ShortID()` returns raw 8 chars (no prefix)
- `Bundle.DeviceID()` returns `"D-" + Device.ShortID()`
- `kv.ShortDeviceID()` returns `"D-" + identity.ShortID()`
- All display and storage uses the `D-` prefix

Commits: 071ac60, ab6003a, 9f836bb, 18766cc

## Web UI

- Getting-started landing page with invite/join flow (535602b, a1d2da5)
- Real-time join detection on invite page (29db636)
- Clipboard copy feedback on invite code (e455f3d)
- Nav reordered: Devices → Agents → Network → KV → Drives → Settings
- Agents placeholder page for future work
- Handles missing version field gracefully (129ecc8)

## Install Script (install.sh)

Curl one-liner for macOS and Linux:

```
curl -fsSL https://raw.githubusercontent.com/sky10ai/sky10/main/install.sh | bash
```

- Detects OS (darwin/linux) and architecture (arm64/amd64)
- Downloads from GitHub releases
- On Linux: creates systemd service (`sky10.service`), enables and
  starts it automatically
- Shows invite/join commands on completion

Commits: ced26fb, 1123eef, 6dd9dbc, 3824a80

## CI: Verify Release (verify-release.yml)

The reproducible build verification workflow was failing on every
v0.31.x release for a simple reason: `checksums.txt` has entries for
all 4 platform binaries, but the workflow only downloaded
`sky10-darwin-arm64`. `shasum -c checksums.txt` failed on the 3 missing
files — even though the actual binary matched perfectly.

**Fix:** Converted to a matrix strategy with 4 parallel jobs (one per
platform). Each job builds its binary from source on the matching
runner (macos-latest for darwin, ubuntu-latest for linux) and compares
against the release asset. ac5290e

## Design Decisions

**Nostr over DHT for primary discovery.** Nostr relays have high
uptime, work through NAT, and respond in <1s. DHT FindPeer can take
10-30s and depends on both peers being well-connected to the DHT. Nostr
is the fast path; DHT is the reliable fallback.

**Full-snapshot push, not delta sync.** Every P2P push sends the entire
encrypted KV snapshot. This is simpler than delta sync, naturally
idempotent (baseline diffing handles duplicates), and the snapshot is
small (KV values are capped at 4KB each). The tradeoff is bandwidth —
acceptable for <1000 keys, will need revisiting at scale.

**No rate limiter.** The rate limiter caused silent data loss (Bug 4).
Full-snapshot idempotency makes it safe to push aggressively. If
bandwidth becomes an issue, the fix is delta sync, not rate limiting.

**Auto-approve joins.** The invite code itself is the authorization.
Possession of the code (which requires physical proximity or a trusted
channel) is sufficient. No manual approval step.

**Namespace key generation without S3.** First device generates the
key and caches locally. On P2P join, the inviter wraps and sends it.
On S3 join, keys are exchanged via the S3 mailbox as before.

**Three-layer discovery.** Each layer has different failure modes: S3
needs credentials and internet, Nostr needs internet and working relays,
DHT needs both peers advertising on the hash table. Having all three
means at least one works in virtually any network configuration.

## Lessons Learned

1. **Bugs cascade.** The P2P sync had 5 bugs stacked on top of each
   other. Each fix only revealed the next. You can't diagnose bug 4
   until bugs 1-3 are fixed.

2. **Silent failures are worse than crashes.** The rate limiter (bug 4)
   and context cancellation (bug 3) produced zero errors. The push
   "worked" — just didn't arrive. Always log at Warn or higher when
   data doesn't reach its destination.

3. **Test on real machines.** Integration tests with two in-process
   libp2p nodes caught encoding bugs but missed context lifecycle and
   timing issues that only manifest across real network boundaries.

4. **Build observability before you need it.** The `skyfs.logs` RPC
   was built mid-debugging and became essential. Without it, diagnosing
   the Linux side required the user to manually check logs.

5. **Deterministic builds are worth the effort.** Using git committer
   timestamps (`git log -1 --format=%cd`) instead of wall-clock time
   makes builds reproducible. The verify-release CI catches any drift.

## All Commits (excluding self-update)

| SHA | Message |
|-----|---------|
| 6e14541 | feat(link): add DHT FindPeer discovery from manifest device keys |
| 1378889 | test(kv): add regression tests for context cancellation and rapid sets |
| cc3e806 | fix(kv): close libp2p node before TempDir cleanup in P2P tests |
| ac5290e | fix(ci): verify all 4 platform binaries in verify-release |
| 44d050b | fix(ci): verify only darwin-arm64 checksum in verify-release |
| 28abcfe | fix(link): retry auto-connect after 15s if no peers found |
| f271c86 | fix(kv): remove P2P push rate limiter |
| 52ca47f | fix(kv): use background context for P2P push goroutine |
| 2f05fa6 | chore(kv): upgrade all P2P push failure logs to WARN |
| 97316a3 | feat(fs): add skyfs.logs RPC — read daemon log with optional filter |
| 649b893 | chore(kv): add diagnostic log when p2pSync is nil in pokeSync |
| 18766cc | fix(kv): revert ShortDeviceID — D- prefix is correct for device IDs |
| de3614a | chore(kv): upgrade P2P sync logs from debug to info |
| 3824a80 | fix: show invite and join commands in install.sh output |
| 6dd9dbc | fix: install.sh sets up systemd on any Linux, not just Debian |
| 1123eef | feat: install.sh sets up systemd service on Debian/Ubuntu |
| 08dbc1d | feat(link): default to network mode — enable DHT, relay, external peers |
| 18d5d1b | fix(link): auto-connect finds all devices, not just the first |
| 9265143 | test: add P2P sync integration and Nostr fallback tests |
| c386ff7 | test(kv): add regression tests for P2P sync encoding |
| 3405ba2 | fix(kv): fix P2P snapshot encoding — use json.Marshal not fmt.Sprintf |
| 129ecc8 | fix(web): handle missing version field in device list |
| e4837fe | feat(fs): show connected P2P peers in device list |
| 29db636 | feat(web): real-time join detection on invite page |
| ae0285b | fix(link): use d tag for Nostr discovery queries |
| 2d0e706 | Bump Cirrus to v0.31.0 |
| 739d0e2 | docs: update README with install script, update release for all platforms |
| ced26fb | feat: add install.sh for macOS and Linux |
| e455f3d | feat(web): show checkmark on invite code copy |
| a1d2da5 | feat(web): add join invite input to getting-started page |
| e815994 | fix(web): update invite page copy — remove "Ethereal Vault" |
| 535602b | feat(web): add getting-started landing page, agents nav, reorder sidebar |
| 2f256ac | feat(web): devices first — move above KV, set as default route |
| 9f836bb | fix(key): ShortID returns raw 8 chars, callers add prefix |
| 885f24a | fix(fs): include IP and location for this device in P2P mode |
| ab6003a | refactor(key): consolidate device ID derivation into Key.ShortID() |
| 4c16083 | fix(fs): guard all S3-dependent RPC methods against nil backend |
| 071ac60 | refactor: change device IDs to D- prefix + 8 chars |
| e817684 | docs(work): add todo for S3-optional phases 3-4 |
| 525521f | docs(work): move S3-optional plan to past, record completed work |
| 1bf76d6 | refactor: move invite/join to pkg/join, commands to top-level |
| 9b453ee | feat(link): complete P2P join flow with auto-approve and key exchange |
| 45c6750 | docs(work): update S3-optional plan with milestones and phase 1 status |
| d4b7366 | feat: make S3 optional — daemon starts and syncs over P2P |
| c82b8ec | fix: reorder nav — drives before settings |
| a849a19 | docs: add S3-optional plan; reorder nav — drives below settings |

## Files Created

| File | Purpose |
|------|---------|
| `pkg/kv/p2p.go` | KV snapshot sync over libp2p streams |
| `pkg/kv/p2p_test.go` | Unit tests: nil backend, encoding, roundtrip, diffAndMerge |
| `pkg/kv/p2p_integration_test.go` | Integration tests: two real libp2p nodes syncing |
| `pkg/join/invite.go` | Invite encode/decode (P2P + S3 formats) |
| `pkg/join/handler.go` | Libp2p stream handler for join requests |
| `pkg/join/request.go` | Client side of P2P join handshake |
| `pkg/join/invite_test.go` | P2P invite encode/decode tests |
| `pkg/join/join_integration_test.go` | Full join handshake integration test |
| `pkg/link/helpers.go` | HostMultiaddrs(), NostrSecretKey() |
| `pkg/link/discovery_test.go` | Nostr fallback discovery tests |
| `pkg/fs/rpc_logs.go` | skyfs.logs RPC for remote log access |
| `commands/invite.go` | Top-level `sky10 invite` command |
| `commands/join.go` | Top-level `sky10 join` (P2P + S3 flows) |
| `install.sh` | Curl installer for macOS + Linux with systemd |
| `web/src/pages/GettingStarted.tsx` | Welcome page with invite/join |
| `web/src/pages/Agents.tsx` | Placeholder for agent UI |

## Files Modified (Key Changes)

| File | Change |
|------|--------|
| `commands/serve.go` | Nil backend flow, Nostr + P2P wiring, network mode, 3-layer discovery |
| `commands/fs.go` | Removed invite/join subcommands |
| `commands/fs_daemon.go` | makeBackend returns nil, removed old join code |
| `pkg/config/config.go` | HasStorage(), Relays(), NostrRelays, default relays |
| `pkg/id/sync.go` | localIdentity() for nil backend |
| `pkg/id/bundle.go` | DeviceID() with D- prefix |
| `pkg/key/key.go` | ShortID() — raw 8 chars from address |
| `pkg/kv/store.go` | Nil backend, SetP2PSync, pokeSync with background ctx |
| `pkg/kv/crypto.go` | Local-only namespace key resolution, CacheKeyLocally |
| `pkg/kv/poller.go` | Extracted diffAndMerge as shared function |
| `pkg/link/discovery.go` | WithNostr, 3-layer AutoConnect, autoConnectFromManifest |
| `pkg/link/identity.go` | PeerIDFromPubKey for manifest-based discovery |
| `pkg/link/nostr.go` | d tag for NIP-78, QueryAll with limit 10 |
| `pkg/fs/rpc_handler.go` | S3-dependent RPCs guarded, skyfs.logs added |
| `pkg/fs/rpc_invite.go` | Delegates to pkg/join, P2P invite when no backend |
| `pkg/fs/device.go` | P2P peer devices in device list |
| `.github/workflows/verify-release.yml` | Matrix strategy for all 4 platforms |

## Remaining Work

See `docs/work/todo/s3-optional-remaining.md`:

- **Phase 5: `sky10 storage add s3`** — attach S3 to a running daemon
- **Phase 6: Content-addressed pinning** — S3/IPFS for offline blobs
