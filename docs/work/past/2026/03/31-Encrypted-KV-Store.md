---
created: 2026-03-30
model: claude-opus-4-6
---

# Encrypted Key-Value Store

Built `pkg/kv/` as a sibling to `pkg/fs/`. Same S3 bucket, same
encryption, same snapshot exchange protocol, but KV semantics instead
of file semantics. No filesystem involvement — state lives in memory
+ local JSONL log.

## Design

- **LWW (Last Writer Wins)** CRDT for conflict resolution
- **Values inline** — no chunking, max 4KB per value
- **No outbox** — Set/Delete write directly to local log + poke uploader
- **No reconciler** — no disk state to reconcile
- **Same namespace key infra** — reuses `keys/namespaces/` in S3

### S3 Layout

```
kv/{nsID}/snapshots/{deviceID}/latest.enc
kv/{nsID}/snapshots/{deviceID}/{timestamp}.enc
```

## Files

| File | Lines | Purpose |
|------|-------|---------|
| `entry.go` | ~80 | Entry type (set/delete), clockTuple, LWW beats() |
| `snapshot.go` | ~200 | In-memory snapshot, LWW merge, marshal/unmarshal |
| `local.go` | ~250 | JSONL-backed local ops log with cached snapshot |
| `s3paths.go` | ~20 | S3 key helpers for kv/ prefix |
| `crypto.go` | ~100 | Encrypt/decrypt, namespace key resolution |
| `baseline.go` | ~80 | Per-device baseline tracking for diffing |
| `uploader.go` | ~120 | Snapshot serialize → encrypt → upload |
| `poller.go` | ~200 | Download remote snapshots → baseline diff → merge |
| `store.go` | ~300 | Public API: New, Set, Get, Delete, List, GetAll, Run |
| `rpc.go` | ~200 | RPC handler: skykv.set/get/delete/list/getAll/sync/status |

## RPC Methods

- `skykv.set` — set key/value pair
- `skykv.get` — get value by key
- `skykv.delete` — delete key
- `skykv.list` — list keys (optional prefix filter)
- `skykv.getAll` — get all entries as key/value map
- `skykv.sync` — trigger sync cycle
- `skykv.status` — namespace, device ID, key count

## CLI Commands

- `sky10 kv set <key> <value>`
- `sky10 kv get <key>`
- `sky10 kv delete <key>`
- `sky10 kv list [prefix]`
- `sky10 kv sync`
- `sky10 kv status`

## Key Decisions

- **Independent namespace keys with `kv:` prefix.** Initially shared keys
  with fs, but this created a coupling problem — changed to `kv:` prefix
  so KV and FS namespaces are independently encrypted.
- **Upload debounce at 750ms.** Reduced from higher value for better
  real-time sync responsiveness.
- **Sync notification after S3 upload**, not after local append — fixed
  so remote devices only get notified when data is actually available.
- **Device ID format must match fs.** Caught in testing — both must use
  the same short-ID derivation or cross-module lookups break.

## Tests

52 tests total:
- Unit tests for entry, snapshot, local log, s3paths, namespace keys
- Device ID format consistency test (regression)
- MinIO integration tests for two-device sync round-trip

## Commits

- [`38f9771`](https://github.com/sky10ai/sky10/commit/38f9771) feat(kv): implement encrypted key-value store (v1)
- [`5495ebe`](https://github.com/sky10ai/sky10/commit/5495ebe) fix(kv): share namespace keys with fs
- [`1cb965b`](https://github.com/sky10ai/sky10/commit/1cb965b) fix(kv): independent namespace keys with kv: prefix
- [`d0fb397`](https://github.com/sky10ai/sky10/commit/d0fb397) test(fs,kv): add namespace key prefix and cross-module tests
- [`051a013`](https://github.com/sky10ai/sky10/commit/051a013) fix(kv): use same device ID format as fs
- [`1c98c91`](https://github.com/sky10ai/sky10/commit/1c98c91) test(kv): add regression test for device ID format match
- [`1f43422`](https://github.com/sky10ai/sky10/commit/1f43422) test(kv): add MinIO integration tests for two-device sync
- [`dfaabd8`](https://github.com/sky10ai/sky10/commit/dfaabd8) fix(kv): fire sync notification after S3 upload
- [`20e640f`](https://github.com/sky10ai/sky10/commit/20e640f) chore(kv): extract upload debounce as constant, reduce to 750ms
