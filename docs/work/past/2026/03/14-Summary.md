---
created: 2026-03-14
model: claude-opus-4-6
---

# March 14 — SkyFS V1→V3 + Schema Versioning + cirrus macOS

## SkyFS V1: Initial Implementation

Encrypted file storage library and CLI. Single-user, AES-256-GCM, S3-compatible.

### What Was Built
- Go module `github.com/sky10/sky10` with packages: `skyfs/`, `skyadapter/`, `cmd/skyfs/`, `internal/config/`
- `skyadapter.Backend` interface with streaming I/O; S3 backend + in-memory test backend
- AES-256-GCM encryption, Ed25519 identity, ephemeral ECDH key wrapping (Ed25519→X25519)
- Three-layer key hierarchy: user → namespace → file (HKDF derivation)
- Streaming chunker (4MB max, fixed-size), content-addressed blob storage with dedup
- Encrypted manifest for file tree → chunk mappings
- CLI: `skyfs init|put|get|ls|rm|info`
- Deterministic builds via Makefile (`CGO_ENABLED=0`, `-trimpath`, `-buildvcs=false`)

### Key Decisions
- Fixed-size chunking over FastCDC — correct for all ops, CDC deferred to v2
- Ed25519→X25519 via SHA-256 of seed
- Single manifest file (single-writer assumption)
- S3_* env vars — S3-compatible-first, not AWS-first

### Tests: 55

---

## SkyFS V2: Multi-Device Sync

Concurrent writes, conflict detection, local sync state, performance optimizations.

### What Was Built
- **FastCDC** chunking (jotfs/fastcdc-go) — edits only affect nearby chunks
- **Multi-party key wrapping** — WrapKey takes only public key (filippo.io/edwards25519 for birational map)
- **Ops log** — append-only replacing single-writer manifest; deterministic sort by (timestamp, device, seq); conflict detection via prev_checksum
- **v1 backward compat** — loadLatestSnapshot falls back to v1 manifest
- **Snapshot compaction** — configurable retention, idempotent, `skyfs compact`
- **Pack files** — ~16MB packs, PackIndex for chunk→pack mapping, `GetRange` on Backend interface
- **Local SQLite index** (modernc.org/sqlite) — remote_files, chunks, sync_state tables
- **Key rotation + access control** — GrantAccess, RevokeAccess, RotateNamespaceKey, per-identity key files
- **Blob GC** — walk blobs vs manifest, dry-run mode, `skyfs gc`

### Key Decisions
- FastCDC library reuses internal buffer — must copy data out
- SHA-512 + clamp for Ed25519→X25519 private key conversion
- Chunks encrypted individually before packing (no pack-level encryption)
- Key rotation re-encrypts chunk data (HKDF-derived file keys change with namespace key)

### Tests: 96

---

## SkyFS V3: Continuous Sync

Real-time sync daemon: watch local directory, auto-sync, poll for remote changes.

### What Was Built
- **Sync engine** — bidirectional diff (local vs remote), ScanDirectory with streaming checksums, synced-file tracking
- **File watcher** — fsnotify, 500ms debounce, auto-watch new subdirs, skip dotfiles
- **Remote poller** — polls S3 ops/ for new operations, incremental via last_op_timestamp
- **Daemon mode** — watcher + poller + sync engine; 2s batch window; graceful shutdown
- **Selective sync** — filter by namespace/prefix, exclude patterns
- **Compression** — zstd per-chunk before encryption (klauspost/compress); incompressible format detection; header byte for backward compat
- **Versioning** — ListVersions, RestoreVersion, ListSnapshots from ops log
- **Progress tracking** — ProgressReader/ProgressWriter with callbacks
- **Ignore patterns** — .skyfsignore with gitignore-style syntax, default ignores, negation support
- CLI: `skyfs sync|versions|snapshots`

### Key Decisions
- Synced-file tracking via in-memory map (resets on restart → full diff)
- 500ms debounce for editor save patterns, 2s batch window for daemon
- Compression header byte: 0x00 = uncompressed, 0x01 = zstd
- zstd level 3 default

### Tests: 124

---

## Schema Versioning

Storage schema v1.0.0 — authoritative reference for skyfs encrypted storage format.

### What Was Built
- Schema spec: SHA3-256 hashing, HKDF-SHA3-256 KDF, AES-256-GCM cipher, ephemeral ECDH X25519 key wrapping
- `SKY\x01` 4-byte blob header (magic bytes + major version)
- `sky10.schema` unencrypted JSON in bucket root
- Semver rules: major = breaking (algorithm change), minor = backward-compatible addition, patch = bug fix
- Validation on bucket open: major version mismatch → error with upgrade/migrate guidance
- Bucket layout: `ops/`, `manifests/`, `blobs/`, `packs/`, `pack-index.enc`, `keys/namespaces/`

---

## cirrus macOS — SwiftUI Desktop App

Menu bar app with file browser, connected to Go backend via JSON-RPC.

### What Was Built
- **Go RPC server** — JSON-RPC 2.0 over Unix socket (`~/.skyfs/skyfs.sock`); methods: list, put, get, remove, info, status, versions, compact, gc; `skyfs serve` command
- **Menu bar app** — MenuBarExtra with sync state icons (SF Symbols cloud variants); Open Browser, Sync Now, Preferences, Quit
- **Daemon manager** — start/stop Go process, auto-restart on crash
- **RPC client** — async/await actor, JSON-RPC 2.0 over Unix socket
- **File browser** — NavigationSplitView: namespace sidebar + sortable file table + detail panel; context menu (Download, Copy Path, Delete); search filtering
- **File operations** — upload (NSOpenPanel), download (NSSavePanel), delete with confirmation
- **Settings** — tabbed: General (sync dir, launch at login, poll interval), Storage (bucket, region, endpoint, test connection), Account (identity, file count, size)

### Key Decisions
- Unix domain socket for IPC — simpler than HTTP, scoped to local machine
- JSON-RPC 2.0 — standard, easy to debug with socat
- Events logged, not pushed (response interleaving issue — deferred to separate event socket)
- Zero third-party Swift deps — all Apple frameworks
- RPCClient as actor for thread safety

### Tests: 131 Go (7 new RPC), SwiftUI views need Xcode

---

## Files Created (All Work on March 14)

```
Go:
  skyfs/skyfs.go, crypto.go, identity.go, keys.go, chunk.go, manifest.go
  skyfs/op.go, compact.go, pack.go, index.go, access.go, gc.go
  skyfs/sync.go, diff.go, scan.go, watcher.go, poller.go, daemon.go
  skyfs/compress.go, version.go, progress.go, ignore.go
  skyfs/*_test.go (all corresponding test files)
  skyfs/rpc.go, rpc_test.go
  skyadapter/adapter.go
  skyadapter/s3/s3.go, s3_test.go
  internal/config/config.go, config_test.go
  cmd/skyfs/main.go
  Makefile, LICENSE, README.md, CLAUDE.md

Swift:
  cirrus/cirrus/App.swift
  cirrus/cirrus/Models/AppState.swift, SyncState.swift, FileNode.swift
  cirrus/cirrus/Services/DaemonManager.swift, RPCClient.swift, SkyClient.swift
  cirrus/cirrus/Views/MenuBar/MenuBarView.swift
  cirrus/cirrus/Views/Browser/BrowserView.swift, SidebarView.swift, FileListView.swift, FileRowView.swift, DetailView.swift
  cirrus/cirrus/Views/Settings/SettingsView.swift

Dependencies added:
  github.com/jotfs/fastcdc-go, filippo.io/edwards25519, modernc.org/sqlite
  github.com/fsnotify/fsnotify, github.com/klauspost/compress/zstd
```
