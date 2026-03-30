---
created: 2026-03-30
model: claude-opus-4-6
status: planned
---

# pkg/kv/ — Encrypted Key-Value Store

Build `pkg/kv/` as a sibling to `pkg/fs/`. Same S3 bucket, same encryption,
same snapshot exchange protocol, but KV semantics instead of file semantics.
No filesystem involvement — state lives in memory + local JSONL log.

## Design Decisions

- **Copy, don't extract.** Duplicate relevant patterns from fs/opslog into kv/.
  Don't touch working fs code. Generalize later.
- **LWW only for v1.** Counters, OR-Sets deferred.
- **Values inline.** No chunking/packing. Max 4KB per value in v1.
- **No outbox.** Set/Delete write directly to local log + poke uploader.
- **No reconciler.** No disk state to reconcile.
- **Same namespace key infra.** Reuse keys/namespaces/ in S3.

## S3 Layout

```
kv/{nsID}/snapshots/{deviceID}/latest.enc
kv/{nsID}/snapshots/{deviceID}/{timestamp}.enc
```

## Phase 1: Core Data Structures

### `pkg/kv/entry.go` (~80 lines)
- `EntryType`: `set`, `delete`
- `Entry`: Key, Value ([]byte), Device, Timestamp, Seq, Namespace
- `clockTuple` + `beats()` — copied from fs/opslog

### `pkg/kv/snapshot.go` (~200 lines)
- `ValueInfo`: Value, Modified, Device, Seq, Namespace
- `Snapshot`: `entries map[string]ValueInfo`, `deleted map[string]bool`
- Methods: Lookup, Keys, KeysWithPrefix, Len, Entries, DeletedKeys
- `buildSnapshot(base, entries)` — simplified LWW merge (no dirs, no symlinks)
- `MarshalSnapshot` / `UnmarshalSnapshot`

### `pkg/kv/local.go` (~250 lines)
- `LocalLog`: JSONL-backed local ops log with cached snapshot
- Append, AppendLocal, Snapshot, Lookup, Compact, InvalidateCache
- Pattern from fs/opslog/local.go adapted for KV Entry type

### `pkg/kv/s3paths.go` (~20 lines)
- `snapshotLatestKey(nsID, deviceID)` → `kv/{nsID}/snapshots/{deviceID}/latest.enc`
- `snapshotHistoryKey(nsID, deviceID, ts)`

## Phase 2: Sync Engine

### `pkg/kv/uploader.go` (~120 lines)
- Same pattern as fs/snapshot_uploader.go
- Serialize snapshot → encrypt → upload to kv/ prefix
- Poke/Run/Upload

### `pkg/kv/poller.go` (~200 lines)
- Same pattern as fs/snapshot_poller.go
- Download remote snapshots → baseline diff → merge
- No conflict copies, no directory handling
- `diffAndMerge`: additions/modifications/deletions only

### `pkg/kv/baseline.go` (~80 lines)
- Same as fs/baseline.go but with KV Snapshot type

## Phase 3: Store API

### `pkg/kv/store.go` (~300 lines)
- `Store` struct: backend, identity, localLog, uploader, poller, baselines
- `New(backend, identity, config, logger) (*Store, error)`
- `Set(ctx, key, value)` — append to local log, poke uploader
- `Get(key) ([]byte, bool)` — read from local snapshot
- `Delete(ctx, key)` — append delete, poke uploader
- `List(prefix) []string`
- `GetAll(prefix) map[string][]byte`
- `Run(ctx) error` — resolve nsKey, poll once, start uploader + poller
- `SyncOnce(ctx)` — poll + upload
- `Close()`

### `pkg/kv/crypto.go` (~100 lines)
- Encrypt/Decrypt re-exports from pkg/key
- `getOrCreateNamespaceKey` — duplicated from skyfs.go:537-616
- `deriveNSID` / `resolveNSID` — copied from fs/nsid.go

## Phase 4: CLI + RPC

### `pkg/kv/rpc.go` (~200 lines)
- `KVHandler` with Dispatch method for `skykv.*` methods
- `skykv.set`, `skykv.get`, `skykv.delete`, `skykv.list`,
  `skykv.getAll`, `skykv.sync`, `skykv.status`

### `commands/kv.go` (~200 lines)
- `sky10 kv set <key> <value>`
- `sky10 kv get <key>`
- `sky10 kv delete <key>`
- `sky10 kv list [prefix]`
- `sky10 kv sync`
- `sky10 kv status`

### Integration
- Register KV handler in RPCServer via command layer (don't touch pkg/fs/rpc.go)
- Add `root.AddCommand(commands.KvCmd())` in main.go

## NOT in v1

- Watch/Subscribe (hook point: onChange callback on Poller)
- Large value externalization (>4KB → S3 blob)
- Counter/OR-Set CRDT types
- Per-key encryption
- SQLite VFS layer

## Execution Order

```
Phase 1 → Phase 2 → Phase 3 → Phase 4
```

Each phase compiles, tests pass, independently verifiable.

## Verification

- Unit tests for each file
- Integration test: two-device Set/Get/Delete round-trip (in-memory backend)
- `go test ./pkg/kv/... -count=1`
- Manual: `sky10 kv set foo bar` + `sky10 kv get foo` via RPC
