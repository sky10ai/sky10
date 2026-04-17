---
created: 2026-04-16
model: gpt-5.4
---

# Syncthing-Informed FS P2P Hardening

This entry covers the `study-syncthing-p2p-sync` branch, which was built in
roughly forty commits and used Syncthing as the main external reference for how
`sky10` file sync should behave when S3 is optional instead of foundational.

The point was not to turn `sky10` into Syncthing. The point was to stop letting
non-S3 mode behave like a degraded side path and instead make the core FS
engine converge correctly with peers alone, while still letting S3 remain a
valuable durability and bootstrap layer.

The branch also turned a large amount of earlier architecture work from
`current/` planning docs into real code: hidden transfer workspace, stable
publish barrier, peer metadata sync, peer chunk fetch, source planning,
conflict visibility, Windows path policy, and dedicated daemon integration
coverage.

## Why Syncthing Mattered

The key lesson from Syncthing was not "copy the protocol." It was:

- sync correctness comes from durable per-device metadata and anti-entropy, not
  from lucky pushes
- the visible working tree should not be the transfer workspace
- watcher events are only hints; periodic scan is mandatory
- data transfer should be block-verified and source-planned
- conflict copies, temp-file publish, and deletes need to be first-class
- Windows/path behavior is part of correctness, not just polish

That matched the main concern with `sky10`'s pre-branch FS direction. S3 had
become much stronger, but the non-S3 path still leaned too much on snapshot
polling, baseline diffing, and best-effort behavior. If S3 is optional, the
peer-correct path has to stand on its own.

## What Syncthing Taught Us

### 1. Metadata plane and data plane should be separate

Syncthing keeps file-version knowledge separate from block transfer. That maps
well to `sky10`:

- metadata exchange decides what should exist
- data transfer decides how to get the bytes

This branch applied that split to `pkg/fs/p2p.go`, `pkg/fs/skyfs.go`, and the
reconciler instead of treating "remote sync" as one opaque mechanism.

### 2. Watcher plus periodic scan beats watcher-only optimism

Syncthing explicitly keeps both. This branch made that a hard product rule in
`pkg/fs/daemon_v2_5.go`:

- periodic local scan
- periodic reconcile tick
- stable-write gating before ingesting fresh local changes
- explicit scan cadence, jitter, and backoff

That closed the "missed watcher event until restart" class of bug.

### 3. Stage and verify outside the working tree

Syncthing's temp-file and atomic-replace behavior reinforced that the visible
tree should not also be the transfer workspace.

This branch introduced a per-drive hidden transfer workspace:

- `staging/`
- `objects/`
- `sessions/`

and rewired both remote downloads and browser uploads to use the same
stage-then-publish barrier before any visible file update.

### 4. Anti-entropy matters more than one lucky notification

Syncthing converges because peers reconnect, compare state, and heal. This
branch carried the same discipline into FS:

- summary-first peer metadata sync
- reconnect-triggered FS anti-entropy
- periodic bounded anti-entropy
- durable per-peer sync state on disk

That was heavily influenced by the earlier KV hardening work, but Syncthing
made it clear that FS needed the same reliability posture.

### 5. Pull planning should choose sources intentionally

Syncthing does not just "download remote." It compares local blocks, peer
availability, and transfer order. This branch moved `sky10` toward that model:

- local object cache first
- safe local file reuse second
- peer chunk fetch next
- S3 last, when present

This made S3 an optional source in the planner rather than the thing that
defined FS behavior.

### 6. Windows/path policy is part of sync correctness

Syncthing has to care about case collisions, illegal names, and symlink
behavior. That applies here too. A file sync engine is not correct if it only
works on a Unix-shaped filesystem.

This branch turned that into an explicit path-policy layer with health
surfaces, not a handful of scattered edge checks.

## Where `sky10` Deliberately Diverged From Syncthing

### 1. S3 stayed optional, not forbidden

Syncthing has no central durable backend. `sky10` does. The branch kept that
advantage while refusing to let S3 define correctness.

The target model became:

- peer-correct without S3
- more durable and easier to recover with S3

### 2. We kept content-defined chunking

Syncthing uses fixed-size block metadata. `sky10` already had FastCDC-based
chunking, and nothing in the research justified throwing that away during this
reliability pass.

The more important Syncthing lesson was verified block reuse and source
planning, not the exact chunking algorithm.

### 3. We did not rebuild the whole system around Syncthing's full index model

This branch added real peer metadata sync and durable per-peer state, but it
did not fully replace every local FS data structure with a Syncthing-shaped
index DB.

That was an intentional scope boundary. The goal was to make the existing
engine peer-correct, observable, and testable without stalling on a much
larger rewrite.

## What We Implemented

### 1. Concrete FS planning artifacts

Before the code work, the branch wrote down the intended model in:

- [`../../current/fs-p2p-core-and-agent-drives-plan.md`](../../current/fs-p2p-core-and-agent-drives-plan.md)
- [`../../current/fs-sync-model-and-invariants.md`](../../current/fs-sync-model-and-invariants.md)
- [`../../current/fs-hidden-transfer-workspace.md`](../../current/fs-hidden-transfer-workspace.md)
- [`../../current/agents-drive-contract.md`](../../current/agents-drive-contract.md)
- [`../../current/fs-windows-normalization-plan.md`](../../current/fs-windows-normalization-plan.md)

Those docs mattered because the branch had to keep one coherent model across:

- P2P-only mode
- P2P plus S3 mode
- agent-drive ambitions
- Windows constraints

### 2. Hidden transfer workspace and publish barrier

The branch added:

- per-drive transfer staging
- startup workspace initialization
- transfer-session persistence
- restart recovery for staged files
- upload and download flows that both use `stage -> verify -> publish`

Main code areas:

- `pkg/fs/transfer_workspace.go`
- `pkg/fs/reconciler.go`
- `pkg/fs/rpc_http.go`
- `pkg/fs/daemon_v2_5.go`

This separated user-visible filesystem state from transfer state and made
restart behavior much more defensible.

### 3. Local detection hardening

The branch closed several local correctness gaps:

- periodic scan and reconcile healing
- scan jitter and backoff policy
- stable-write gating so files still being written are not uploaded too early
- outbox refresh if queued metadata becomes stale before upload

Main code areas:

- `pkg/fs/daemon_v2_5.go`
- `pkg/fs/watcher_handler.go`
- `pkg/fs/outbox_worker.go`

This turned local mutation detection into something closer to "eventually
correct" instead of "works when the watcher path is lucky."

### 4. Peer-native metadata sync

This branch made peer metadata exchange real:

- FS P2P metadata protocol in `pkg/fs/p2p.go`
- tombstone-aware peer snapshot exchange
- periodic anti-entropy
- reconnect-triggered sync
- durable per-peer sync state in `pkg/fs/p2p_state.go`
- long-offline catch-up coverage

It also fixed a deeper correctness issue by preserving tombstones across
compaction and by making merge prefer causal successors before raw timestamp
tiebreaks when `prev_checksum` proves the relationship.

Main code areas:

- `pkg/fs/p2p.go`
- `pkg/fs/p2p_state.go`
- `pkg/fs/opslog/`

This was the part that made non-S3 mode first-class instead of merely
"best-effort until snapshots line up."

### 5. Peer chunk fetch and unified pull planning

The branch taught FS to fetch data from peers without S3 and to plan sources
explicitly:

- local object cache
- peer chunk protocol
- explicit chunk-source planner
- source backoff and degraded-state handling
- local file chunk reuse during reconcile
- bounded chunk and file download concurrency
- per-file progress and active source tracking

Main code areas:

- `pkg/fs/skyfs.go`
- `pkg/fs/object_cache.go`
- `pkg/fs/chunk_source_planner.go`
- `pkg/fs/chunk_reuse.go`
- `pkg/fs/pull_planner.go`
- `pkg/fs/reconciler.go`

This is where Syncthing's "verified blocks plus source planning" lesson showed
up most directly.

### 6. S3 became an optional durability layer in practice

The branch finished the S3-optional direction instead of just describing it:

- FS can run with `backend == nil`
- peer chunks can satisfy downloads without S3
- read-source and source-health surfaces distinguish peer vs S3 behavior
- process integration covers peer-only, S3-only fallback, and hybrid recovery

That means S3 still adds real value, but the engine no longer needs S3 to make
the sync story coherent.

### 7. Health, conflicts, and explainability

This branch made the system much more explainable:

- transfer sessions are surfaced in RPC and UI
- read-source stats show local/peer/S3 usage
- FS sync health reports readiness, peer count, last good sync, and errors
- conflict copies are explicit in activity and drive health
- conflict artifacts no longer get re-queued as ordinary local edits

Main code areas:

- `pkg/fs/sync_health.go`
- `pkg/fs/rpc_sync.go`
- `pkg/fs/rpc_drives.go`
- `web/src/pages/Activity.tsx`
- `web/src/components/drives/DriveCard.tsx`

This followed the same "make degraded states loud" lesson that the earlier KV
and mailbox work had already established.

### 8. Windows path normalization and symlink policy

The branch added a concrete Windows-aware path layer:

- canonical logical path normalization in `pkg/fs/path.go`
- Windows invalid-name and case-collision detection
- path-policy health and activity surfaces
- local watcher/scan blocking for ambiguous case-colliding edits
- real Windows symlink policy with capability gating instead of junction
  fallback

Main code areas:

- `pkg/fs/path.go`
- `pkg/fs/path_policy.go`
- `pkg/fs/symlink_policy*.go`

This turned Windows readiness from "eventual aspiration" into concrete FS
behavior with tests.

## Test Infrastructure And Coverage Added

Two testing improvements mattered as much as the code:

### 1. Dedicated FS integration workers

The branch split heavier FS integration coverage into dedicated jobs:

- `make test-skyfs-p2p-integration`
- `make test-skyfs-daemon-integration`

That kept the default suite lighter while still preserving real daemon and
libp2p coverage.

### 2. Real daemon-level FS coverage

The daemon integration suite now covers:

- P2P-only sync
- offline catch-up
- peer chunk use
- S3 fallback when peers are absent
- hybrid peer-then-S3 recovery
- conflict-copy behavior
- Windows invalid-path handling on a Windows-policy node
- Windows symlink behavior on a Windows-policy node

That matters because most of the branch's claims are about reconnection,
restarts, anti-entropy, and materialization edge cases, not just unit-level
helpers.

## Post-Merge CI Hardening On `main`

One important follow-up happened after the branch landed: the first `main`
builds exposed several FS test failures that local runs had not caught
reliably enough before merge.

The failures were not new product bugs in the core FS engine. They were test
isolation problems caused by the branch's new local object-cache and namespace
metadata behavior:

- some tests still assumed backend reads would always happen, even when chunks
  were already present in the local object cache
- some tests still expected the old namespace-key shape and did not account for
  both `.ns.enc` and `.meta.enc`
- several tests were still sharing global local FS state under the default
  `~/.sky10` test location instead of isolating `SKY10_HOME`

The `main` follow-up fixes did three things:

- updated stale expectations for namespace metadata layout
- explicitly removed cached local blobs in tests that were supposed to verify
  backend fetch behavior
- added reusable test helpers so cache-dependent tests run with isolated local
  FS state instead of leaking through global disk state

That work mattered because it sharpened a real lesson from the branch:

- once local hidden state becomes more capable, tests must control that state
  explicitly
- "passes locally" is not enough for merge; the exact GitHub workflow paths
  need to be treated as the real acceptance bar

## What This Branch Did Not Finish

This branch substantially improved FS correctness, but it did not finish every
related idea.

Still open after this work:

- the `Agents` drive contract is designed, but not yet productized
- some Windows-plan cleanup remains, especially deeper canonical-path
  enforcement and more Windows-only test coverage
- the branch does not rewrite FS into a complete Syncthing-style persistent
  index database

That is acceptable. The important change is that the engine now behaves like a
real peer-correct sync core, not like an S3-first design with a weak fallback.

## Main Result

Before this branch, `sky10` had a stronger S3 story than P2P story.

After this branch:

- peer-only file sync is materially real
- S3 is optional but useful
- transfer state is separated from the visible tree
- deletes, conflicts, reconnects, and restarts are much better defended
- health and degraded states are visible
- Windows/path policy is explicit

That is the main Syncthing takeaway as applied to `sky10`:

P2P file sync gets reliable when metadata anti-entropy, transfer staging,
verified block pulls, and loud failure surfaces are treated as first-class
system design, not as cleanup around a storage backend.

The branch also left one operational lesson that is worth keeping: when sync
behavior starts depending on durable local caches and hidden workspace state,
the test suite and CI discipline need to be upgraded alongside the product
code, not after the merge.
