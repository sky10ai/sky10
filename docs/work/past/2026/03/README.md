# March 2026

| Date | Title | Summary |
|------|-------|---------|
| 24 | [Compact Gap & Watcher Fix](24-Compact-Gap-And-Watcher-Fix.md) | Post-compaction poller bootstrap gap (documented + tests), watcher crash on dangling symlinks (fixed), fsnotify kqueue `os.Stat` root cause |
| 21 | [Directory Operations](21-DeleteDir-First-Class-Op.md) | First-class `delete_dir` + `create_dir` ops — CRDT with tombstone semantics, empty dir sync, Cirrus UI, bugfixes — v0.12.0–v0.13.3 |
| 21 | [Upload Stall Detection](21-Upload-Stall-Detection.md) | Read-gap stall detection in transfer.Reader — detects dead TCP connections during uploads by monitoring consumer Read() gaps, cancels via context |
| 20 | [Transfer Module & Reliability](20-Transfer-Module-And-Reliability.md) | pkg/transfer for streaming progress, S3 timeout fix, RPC deadlock fix, poller recovery — v0.10.3, v0.10.4, v0.11.0 |
| 20 | [Checksum Fix & Cirrus Stability](20-Checksum-Fix-And-Cirrus-Stability.md) | Fix cross-device echo loop (checksum scheme mismatch), syncStatus flood, reset/compact UI — v0.10.1, v0.10.2 |
| 19 | [V3: Local Ops Log + CRDT](19-V3-Local-Ops-Log-CRDT.md) | LocalOpsLog replaces DriveState + InboxWorker, Reconciler, CRDT snapshot, local compaction — v0.10.0 |
| 18 | [Daemon V2.5: Inbox/Outbox](18-Daemon-V2.5-Inbox-Outbox.md) | Persistent JSONL queues, filesystem-direct browser, activity view, sync status overlay, 7 new daemon tests |
| 18 | [Daemon V2: Channel Architecture](18-Daemon-V2-Channel-Architecture.md) | Four goroutines, three channels. S3 isolated from UI. Push events. Stable device ID. Local-first manifest updates. |
| 17 | [Three-Way Sync & Cirrus Overhaul](17-Three-Way-Sync-And-Cirrus-Overhaul.md) | Three-way diff with persistent manifest, daemon stability fixes, Finder-like browser, 12 releases |
| 15 | [SkyKey](15-Summary.md) | Crypto primitive package: Bech32m addresses, Seal/Open, Sign/Verify, unified sky10 CLI — 219 tests |
| 14 | [SkyFS V1→V3 + cirrus](14-Summary.md) | Full stack: encrypted storage, multi-device sync, continuous sync daemon, schema versioning, SwiftUI desktop app — 131 tests |
