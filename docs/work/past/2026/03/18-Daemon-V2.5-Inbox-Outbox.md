---
created: 2026-03-18
model: claude-opus-4-6
---

# Daemon V2.5: Inbox/Outbox Sync

Replaced manifest-based sync with two persistent JSONL queues. The
filesystem IS the state. No manifest needed for file listing.

## Architecture

```
LOCAL CHANGE:
  watcher → outbox (append) → outbox worker → S3

REMOTE CHANGE:
  poller → inbox (append) → inbox worker → filesystem

CIRRUS UI:
  file browser → reads filesystem directly (not manifest/RPC)
  activity view → reads inbox + outbox (pending/completed)
  sync status icon → outbox empty = synced
```

## Files Created

### Go (pkg/fs/)
- `synclog.go` + tests — Generic `SyncLog[T]` for append-only JSONL
- `drivestate.go` + tests — Minimal state: path → {checksum, namespace}
- `outbox_worker.go` + tests — Drains local changes to S3
- `inbox_worker.go` + tests — Applies remote changes locally
- `watcher_handler.go` + tests — Watcher events → outbox + state
- `poller_v2.go` + tests — Remote ops → inbox
- `daemon_v2_5.go` + tests — Wires everything together

### Swift (cirrus/macos/)
- `Views/Browser/ActivityView.swift` — Main activity view (default)
- Sync status overlay in FileTreeView and FileColumnView
- `skyfs.syncActivity` RPC endpoint for pending operations
- Filesystem-direct file scanning (no RPC for file list)

## Decisions
- Inbox/outbox are JSONL files, not in-memory channels — crash recovery
- State file is minimal (checksum + namespace), not for UI
- SyncOnce method for one-shot sync (CLI use case)
- Activity view is new default view mode in Cirrus
- Local filesystem scan replaces RPC file listing
- Devices view: loading state instead of timer polling

## Milestones Completed
- M1: Inbox/Outbox types and persistence
- M2: State file
- M3: Outbox worker
- M4: Inbox worker
- M5: Watcher → outbox
- M6: Poller → inbox
- M7: Daemon V2.5 wiring
- M8: Cirrus Activity View
- M9: File browser reads filesystem directly
- M10: Fix Devices View
- M11: V2.5 daemon tests (7 new tests)
