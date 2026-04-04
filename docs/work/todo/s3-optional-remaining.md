# S3 Optional — Remaining Work

Phases 1-2 complete (see `past/2026/04/03-S3-Optional.md`).

## Phase 3: S3 as optional storage backend

**Milestone: `sky10 storage add s3` attaches S3 to a running P2P-only daemon.**

- [ ] `sky10 storage add s3 --bucket X --endpoint Y` command
- [ ] Daemon hot-reloads config when storage is added
- [ ] KV starts uploading snapshots to S3 (alongside P2P sync)
- [ ] KV poller starts polling S3 for offline catch-up
- [ ] Device registration written to S3
- [ ] Drives (skyfs) can now be created — they require a storage backend
- [ ] Existing `sky10 fs init` preserved for S3-first setup

## Phase 4: Content-addressed pinning

**Milestone: blobs survive producer going offline.**

- [ ] Agents produce artifacts → content-addressed blobs
- [ ] S3 keeps blobs alive when producing agent is offline
- [ ] IPFS integration as alternative pinning backend
- [ ] `sky10 pin <cid>` → pin to configured backend
- [ ] Without a pinning backend, file sync is live-only (P2P streaming)

## Cleanup

- [ ] Remove old `pkg/fs/invite.go` functions (still used by internal fs tests)
- [ ] Update `pkg/fs` tests to use `pkg/join` instead of local invite functions
