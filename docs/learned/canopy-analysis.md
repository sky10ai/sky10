# Canopy Architecture Analysis

Deep analysis of [Canopy](https://github.com/kwalus/Canopy) (v0.4.83), a
local-first encrypted P2P collaboration tool written in Python.

## 1. Architecture

### High-Level Structure

Each Canopy instance is a self-contained node. There is no central server.
The architecture is a Flask web app + async WebSocket mesh running on a
background thread.

```
canopy/
  api/          # REST API routes (Flask blueprint, 100+ endpoints)
  core/         # Business logic: channels, messaging, tasks, feed, events
  network/      # P2P layer: identity, discovery, connection, routing
  security/     # Encryption, API keys, trust, file access
  mcp/          # MCP server (stdio, for Claude/Cursor integration)
  ui/           # Flask templates + static JS (web UI)
```

### Component Wiring

`app.py` is the god-object. `create_app()` instantiates Flask, creates a
`DatabaseManager`, `P2PNetworkManager`, `ChannelManager`, `MessageManager`,
`FeedManager`, `TaskManager`, `FileManager`, `ProfileManager`, etc., then
wires ~30 callbacks between them. The P2P network manager runs its own
asyncio event loop on a dedicated `threading.Thread`.

### Transport: WebSocket Mesh

- **Transport**: Plain WebSocket (`websockets` library), with optional TLS
  (self-signed certs). Default ports: 7770 (HTTP/UI), 7771 (mesh WS).
- **No WebRTC, no libp2p, no QUIC**. Pure WebSocket connections, either
  direct or through relay.
- **Message framing**: JSON over WebSocket text frames. Each P2P message is a
  `P2PMessage` dataclass serialized to JSON with fields: `id`, `type`,
  `from_peer`, `to_peer`, `timestamp`, `ttl`, `signature`,
  `encrypted_payload`, `payload`.
- **Compression**: Per-message deflate enabled at WebSocket level.
- **Max frame size**: 20MB (to allow inline file transfer within messages).

### Relay and Brokering

When peers can't connect directly (NAT, different networks), a mutually
trusted third peer can relay traffic. Three relay message types:
`BROKER_REQUEST`, `BROKER_INTRO`, `RELAY_OFFER`. The relay policy is
configurable: `off`, `broker_only` (default), `full_relay`. The relay
peer forwards ciphertext -- it cannot read E2E-encrypted content.

## 2. Encryption

### Cryptographic Primitives

- **Signing**: Ed25519 (identity verification, message signatures)
- **Key exchange**: X25519 (ECDH for deriving shared secrets)
- **Symmetric cipher**: ChaCha20-Poly1305 (AEAD) everywhere
- **KDF**: HKDF-SHA256 with domain-specific info strings
- **Library**: Python `cryptography` (pyca/cryptography)
- **Key encoding**: Base58 for serialization/display

### Three Layers of Encryption

**Layer 1 -- Transport encryption (peer-to-peer):**
Each peer derives a shared secret via X25519 ECDH + HKDF. The HKDF uses
`salt=None, info=b'canopy-p2p-encryption'`. Messages are encrypted with
ChaCha20-Poly1305 using this shared key, then base64-encoded as
`encrypted_payload` in the P2P message envelope. Every message is also
Ed25519-signed.

**Layer 2 -- Encryption at rest (local database):**
`DataEncryptor` derives a 256-bit key from the local Ed25519 private key
via HKDF (`salt=b'canopy-data-at-rest-v1', info=b'canopy-local-storage-encryption'`).
Stored values are `ENC:1:<nonce_hex>:<ciphertext_hex>`. This means only
the local instance can decrypt its own DB. If someone copies the SQLite
file, they get ciphertext.

**Layer 3 -- Recipient-based encryption (private channels / DMs):**
`RecipientEncryptor` generates a random Content Encryption Key (CEK),
encrypts the content with ChaCha20-Poly1305, then wraps the CEK for each
recipient using an ephemeral X25519 keypair + HKDF
(`info=b'canopy-post-key-wrap'`). Format:
`RENC:1:<nonce_hex>:<ciphertext_hex>` with wrapped keys stored separately.

### DM E2E

DMs use a capability negotiation (`dm_e2e_v1`). When both peers support it,
messages are encrypted to the destination peer's X25519 public key before
transmission. Security state per thread is tracked and surfaced in the UI:
`peer_e2e_v1`, `local_only`, `mixed`, `legacy_plaintext`.

### Channel E2E

Private/confidential channels use a symmetric channel key distributed to
members. The channel key is wrapped per-member using X25519 ECDH. Functions:
`encrypt_with_channel_key()`, `decrypt_with_channel_key()`,
`encrypt_key_for_peer()`, `decrypt_key_from_peer()`.

### Key Management

- Keys are generated on first launch and stored as JSON files
  (`peer_identity.json`, `known_peers.json`) in the device data directory.
- Private keys stored unencrypted on disk (file permissions: 0o600).
- No key rotation protocol exists yet.
- No forward secrecy (static X25519 keys, no ratcheting).
- User-level keys stored in `user_keys` table (Ed25519 + X25519 per user).

## 3. Storage

### Database

- **Engine**: SQLite with WAL mode.
- **Connection pooling**: Thread-local connections with a background WAL
  checkpoint thread (5-minute interval).
- **Schema**: Single flat schema with ~20+ tables defined in one
  `executescript()` call. Tables include: `users`, `user_keys`, `api_keys`,
  `messages`, `trust_scores`, `delete_signals`, `peers`, `feed_posts`,
  `channel_messages`, `channels`, `channel_members`, `agent_inbox`,
  `workspace_events`, `content_contexts`, etc.
- **Migration**: Additive `ALTER TABLE ADD COLUMN` migrations with
  try/except around each (no formal migration framework).

### Local-First Sync

There is no CRDT or vector-clock-based sync. Instead:

1. **On connect**: Peers exchange profile cards, channel announcements,
   and run a "catch-up" protocol.
2. **Catch-up**: Each peer sends its latest timestamps per channel. The
   other peer responds with messages newer than those timestamps. There
   is an optional Merkle-assisted digest optimization
   (`sync_digest_v1` capability) but it's disabled by default.
3. **Live sync**: Real-time messages are broadcast as P2P messages of
   type `CHANNEL_MESSAGE`, `FEED_POST`, `DIRECT_MESSAGE`, etc.
4. **Store-and-forward**: Messages for offline peers are queued in memory
   (max 500 per peer) and delivered on reconnect.
5. **Conflict resolution**: Last-write-wins based on timestamps. No
   conflict detection or resolution beyond that.

### Data Layout

```
data/devices/<device_id>/
  canopy.db              # SQLite database
  peer_identity.json     # Ed25519 + X25519 keypairs
  known_peers.json       # Persisted peer identities + endpoints
  secret_key.json        # Flask secret key
  files/                 # Uploaded file storage
  tls/                   # Self-signed TLS certs (if enabled)
```

Device IDs are stable per-machine (derived from hostname + MAC address).

## 4. Agent Integration

### Agent API Surface

Agents interact through two interfaces:

1. **REST API** (`/api/v1/...`): 100+ endpoints. Authenticated via
   `X-API-Key` header. Scoped permissions per key.
2. **MCP Server** (stdio): `CanopyMCPServer` exposes ~40 tools via the
   Model Context Protocol. Used by Claude Desktop, Cursor, etc.

### Agent Lifecycle

- Agent accounts are created as regular users with `account_type='agent'`.
- Agents get API keys with permission scopes (read, write, admin, etc.).
- Agents are `@mentionable` in channels just like humans.

### Key Agent Endpoints

- `GET /api/v1/agents/me/inbox` -- Pull-based inbox with pending mentions,
  tasks, DMs, replies. Rate-limited and configurable per agent.
- `GET /api/v1/agents/me/heartbeat` -- Lightweight polling with workload
  hints (`needs_action`, active counts, oldest pending age).
- `GET /api/v1/agents/me/catchup` -- Full digest of recent activity.
- `GET /api/v1/agents/me/events` -- Low-noise workspace event stream for
  agent runtimes (journal-based, cursor-tracked).

### Agent Inbox

Each agent has an `agent_inbox` table with items triggered by mentions,
DMs, replies, channel additions, etc. Items have statuses:
`pending` -> `seen` -> `completed`/`skipped`/`expired`. Rate limiting
per sender, per channel, with burst caps and hourly limits. Agent-specific
defaults are more permissive than human defaults.

### Mention Claim Locks

To prevent multi-agent pile-on: before responding to a mention, an agent
claims it via `POST /api/v1/mentions/claim`. Other agents see it as claimed
and skip. This is a simple DB-level advisory lock.

### Agent Directives

Persistent runtime instructions stored in the user record
(`agent_directives` column). Hash-based tamper detection.

### MCP Tools

~40 tools including: `canopy_send_message`, `canopy_get_messages`,
`canopy_get_mentions`, `canopy_get_inbox`, `canopy_get_catchup`,
`canopy_list_objectives`, `canopy_create_task`, `canopy_post_channel_message`,
`canopy_search`, etc. Each checks API key permissions before executing.

## 5. Message Protocol

### P2P Message Types (routing.py)

30+ message types organized by function:
- **Communication**: `DIRECT_MESSAGE`, `BROADCAST`, `CHANNEL_MESSAGE`
- **Channel sync**: `CHANNEL_ANNOUNCE`, `CHANNEL_JOIN`, `CHANNEL_SYNC`
- **Catch-up**: `CHANNEL_CATCHUP_REQUEST`, `CHANNEL_CATCHUP_RESPONSE`
- **Profile**: `PROFILE_SYNC`, `PROFILE_UPDATE`
- **Membership**: `MEMBER_SYNC`, `PRIVATE_CHANNEL_INVITE`,
  `CHANNEL_KEY_DISTRIBUTION`, `CHANNEL_KEY_REQUEST`
- **System**: `PEER_ANNOUNCEMENT`, `DELETE_SIGNAL`, `TRUST_UPDATE`
- **Feed**: `FEED_POST`, `INTERACTION`
- **Relay**: `BROKER_REQUEST`, `BROKER_INTRO`, `RELAY_OFFER`
- **Large files**: `LARGE_ATTACHMENT_REQUEST`, `LARGE_ATTACHMENT_CHUNK`
- **Identity**: `PRINCIPAL_ANNOUNCE`, `BOOTSTRAP_GRANT_SYNC`

### Channel Model

Channels have types: `public`, `private`, `dm`, `group_dm`, `general`.
Privacy modes: `open`, `private`, `confidential`. Crypto modes:
`legacy_plaintext`, `e2e_v1`. Channels have lifecycle TTLs and can be
archived.

### Structured Work Objects

Inline block syntax in messages: `[task]...[/task]`, `[objective]...[/objective]`,
`[request]...[/request]`, `[handoff]...[/handoff]`, `[signal]...[/signal]`,
`[circle]...[/circle]`, `[poll]...[/poll]`. Parsed and materialized into
their own DB tables.

### Workspace Event Journal

`workspace_events` table with auto-incrementing `seq` column as cursor.
Event types: `dm.message.created`, `channel.message.created`,
`mention.created`, `inbox.item.created`, etc. Used for incremental UI
updates and agent event feeds. Pruned at 50K rows / 30 days.

## 6. Identity

### Peer Identity

- Each instance generates an Ed25519 + X25519 keypair on first launch.
- Peer ID = base58(SHA-256(Ed25519 public key))[:16] (truncated for
  readability).
- Stored in `peer_identity.json`.
- Verified during handshake: the claimed peer_id must match the hash of
  the presented public key.

### User Identity

- Users are local records with `id`, `username`, `password_hash`.
- Separate from peer identity. Multiple users can exist on one instance.
- Users synced across peers via `PROFILE_SYNC` / `PROFILE_UPDATE` messages.
- `origin_peer` field tracks which peer a user was created on.
- Agent accounts distinguished by `account_type='agent'`.

### Identity Portability (Phase 1, Feature-Flagged)

Bootstrap grant system for moving identity between instances. Still
early/experimental: `PRINCIPAL_ANNOUNCE`, `BOOTSTRAP_GRANT_SYNC`,
`BOOTSTRAP_GRANT_REVOKE` message types exist but gated behind
`identity_portability_enabled` flag.

### Invite Codes

Format: `canopy:<base64url-encoded JSON>` containing:
```json
{
  "v": 1,
  "pid": "<peer_id>",
  "epk": "<ed25519_public_key_base58>",
  "xpk": "<x25519_public_key_base58>",
  "ep": ["ws://<ip>:<port>"]
}
```
These are exchanged out-of-band (copy/paste). The invite encodes both
identity (public keys) and connectivity (endpoints).

## 7. Notable Patterns Worth Borrowing

1. **Invite codes as identity + connectivity bundle**: Clean design.
   One code carries everything needed to verify and connect to a peer.
   No separate key exchange step.

2. **Layered encryption with domain-specific HKDF info strings**: Each
   encryption context gets its own HKDF derivation
   (`canopy-p2p-encryption`, `canopy-data-at-rest-v1`,
   `canopy-post-key-wrap`). Prevents key reuse across contexts.

3. **Agent inbox with rate limiting**: The pull-based inbox with per-sender
   and per-channel rate limits is a practical solution for multi-agent
   coordination. The mention claim lock prevents pile-on.

4. **Workspace event journal**: An append-only event spine with
   auto-incrementing sequence numbers that consumers track via cursors.
   Clean separation between event production and consumption.

5. **Capability negotiation in handshake**: Peers advertise capabilities
   during connection handshake. Features like `dm_e2e_v1`,
   `e2e_channel_v1`, `sync_digest_v1` are only used when both sides
   support them. Good for gradual rollout.

6. **Structured blocks in message content**: `[task]...[/task]` syntax
   that gets parsed and materialized into DB tables. Simple, extensible,
   and human-readable in raw form.

7. **Device-specific data directories**: Using `data/devices/<device_id>/`
   prevents conflicts when the project directory is cloud-synced.

## 8. Weaknesses and Gaps

### Security Concerns

1. **No forward secrecy**: Static X25519 keys with no ratcheting. If a
   private key is compromised, all past and future messages are readable.
   Compare: Signal Protocol uses Double Ratchet for per-message keys.

2. **Private keys stored unencrypted**: `peer_identity.json` contains raw
   private key bytes (base58-encoded). Only protected by file permissions.
   No passphrase, no HSM, no OS keychain integration.

3. **TLS certificate validation disabled**: `create_client_ssl_context()`
   sets `check_hostname=False, verify_mode=CERT_NONE`. While understandable
   for self-signed certs in a mesh, it means a MITM between peers could
   intercept the WebSocket connection. The application-layer encryption
   mitigates this, but it is still a transport weakness.

4. **Timestamp-based conflict resolution**: Last-write-wins with no vector
   clocks. Clock skew between peers can cause message ordering issues or
   silent data loss.

5. **No authenticated associated data (AAD)**: ChaCha20-Poly1305 is used
   with `None` as associated data in most encrypt/decrypt calls. Adding
   the message ID or sender ID as AAD would bind ciphertext to context.

### Architectural Concerns

6. **God-object app.py**: All component wiring happens in one massive
   `create_app()` function. The network manager has ~30 callback hooks.
   This makes the system hard to test and reason about.

7. **No formal migration system**: Schema changes are additive ALTER TABLE
   statements wrapped in try/except. No version tracking, no down-migration,
   no migration ordering.

8. **SQLite scalability ceiling**: Single-writer constraint, no replication,
   no sharding. Fine for personal use, questionable for teams.

9. **In-memory store-and-forward**: Offline message queue is in RAM (max
   500 per peer). Server restart loses queued messages. No persistent
   outbox.

10. **No CRDT / causal ordering**: Sync is timestamp-based catch-up. No
    Merkle DAG, no Lamport clocks, no causal consistency guarantees.
    Messages can be silently missed if catch-up timestamps are wrong.

11. **Monolithic Python**: The entire system (web server, P2P mesh, crypto,
    storage) runs in one Python process with thread-local SQLite connections
    and an asyncio event loop on a background thread. GIL contention, no
    horizontal scaling.

12. **Web UI tightly coupled**: The UI is server-rendered Flask templates
    with inline JavaScript. No API-first design -- many UI routes directly
    access the database.

### Missing Features

13. **No group key agreement**: Private channels use a single symmetric key
    distributed by the creator. No group key agreement protocol. Adding
    members requires the creator (or a key-holder) to wrap and distribute.

14. **No key rotation**: No mechanism to rotate channel keys or peer keys.
    Compromised keys require manual intervention.

15. **No offline-first mobile**: The web UI requires the local server to be
    running. No service worker, no IndexedDB caching, no offline capability
    in the client.
