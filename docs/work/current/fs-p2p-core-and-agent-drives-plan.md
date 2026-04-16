---
created: 2026-04-14
updated: 2026-04-15
model: gpt-5.4
---

# FS P2P Core And Agent Drives Plan

## Goal

Build one `sky10` file-sync engine that works correctly with no S3 at all,
while letting S3 remain an optional durability, bootstrap, and fetch layer.

The first concrete product built on top of that engine is an `Agents`
drive:

- one shared drive named `Agents`
- one folder per agent
- durable personality/state files such as `soul.md`, `memory.md`, and
  `sky10.md`

`sky10.md` is intended to capture enough runtime and setup information to
recreate the agent on another machine with nearly the same identity and
working state. That includes the agent runtime (`hermes`, `openclaw`,
`claude code`), model, setup assumptions, and other machine-readable or
human-useful replication notes.

## Guiding Rules

- P2P-only mode must be correct, convergent, and restart-safe.
- S3 must improve durability and recovery without changing sync semantics.
- The watched working tree is not the transfer workspace.
- Watchers are hints; periodic scans and bounded anti-entropy are required.
- Agent personality/state files are durable synced content, not special RPC
  metadata disguised as files.

## Milestone Status Snapshot

- `Milestone 0`: complete
- `Milestone 1`: complete
- `Milestone 2`: complete
- `Milestone 3`: contract/design complete, product wiring still pending
- `Milestone 4`: complete
- `Milestone 5`: complete
- `Milestone 6`: complete
- `Milestone 7`: in progress, mostly on observability rather than the full
  reliability matrix

Current execution rule:

- treat `Milestone 7` as the main engineering focus now that optional-S3
  recovery and source health are complete
- keep `Milestone 3` as design-only until the peer-correct FS core is more
  complete

## Existing Work To Leverage

This plan should explicitly build on two recent bodies of work instead of
re-solving their core reliability problems inside `pkg/fs`.

### KV Hardening Lessons To Reuse

From
[`../past/2026/04/07-KV-CRDT-Reliability-Hardening.md`](../past/2026/04/07-KV-CRDT-Reliability-Hardening.md):

- causal metadata should beat wall-clock ordering whenever possible
- delete intent should be replicated explicitly through tombstones
- peer sync should be summary-first anti-entropy, not blind full-state replay
- periodic bounded anti-entropy should heal missed pushes and reconnect gaps
- sync health should be visible and loud when namespaces drift or stall
- protocol registration and startup ordering matter for fresh joins

FS is larger and more complex than KV, but the underlying private-network
correctness problems are the same. We should adapt these patterns for file
metadata and transfer planning.

### Mailbox Layering Lessons To Reuse

From [`../past/2026/04/11-Mailbox.md`](../past/2026/04/11-Mailbox.md):

- one product model can sit above multiple backends
- persist first, deliver second
- keep the fast path and durable fallback path separate
- keep control state separate from larger payload bytes
- make lifecycle and stuck-state behavior observable

FS should reuse those layering ideas without literally modeling bulk file
transfer as mailbox items. The mailbox package is a control-plane system; FS
should borrow its architecture, not its object model.

## Early Product Shape

Initial target layout for the `Agents` drive:

```text
Agents/
  <agent-name-or-id>/
    soul.md
    memory.md
    sky10.md
    notes/
    attachments/
```

Initial intent for `sky10.md`:

- agent name and stable ID
- runtime family (`hermes`, `openclaw`, `claude code`, other)
- model and model provider
- prompt/bootstrap details needed to recreate behavior
- important local paths or repo assumptions
- tool or connector requirements
- last-known machine/setup notes relevant to migration
- fields that are user-authored vs daemon-authored

## Explicitly Out Of Scope For This Plan

- stranger-to-stranger sharing outside trusted private networks
- public or semi-public shared work drives for open agent collaboration
- payment, reputation, or external discovery for agent folders
- final UX for permissions and agent-scoped access control

Those will likely become later drives or namespaces for shared work output,
handoff folders, and collaboration outside the private network.

## Milestone 0: Model And Invariants

Goal: lock down one FS model before implementation branches.

Reference draft:

- [`fs-sync-model-and-invariants.md`](./fs-sync-model-and-invariants.md)

Checklist:

- [x] Write the end-state sync model for `p2p-only` and `p2p+s3`.
- [x] Explicitly define which KV-hardening lessons carry over unchanged into
      FS and which need FS-specific adaptation.
- [x] Explicitly define which mailbox layering lessons apply to FS and where
      mailbox semantics should stop.
- [x] Define which local data is durable truth vs derived cache.
- [x] Define publish rules: when a local or remote file becomes visible in the
      working tree.
- [x] Define delete semantics for long-offline peers.
- [x] Define conflict semantics for modify/modify and modify/delete.
- [x] Define how peer state, local state, and optional S3 state relate.
- [x] Define the `Agents` drive as a first-class consumer of the FS engine.
- [x] Define the initial file contract for `soul.md`, `memory.md`, and
      `sky10.md`.

Done when:

- [x] We can explain the same sync outcome with S3 disabled or enabled.
- [x] We can explain why the FS design does not regress back to baseline-only
      delete detection or clock-first merge authority.
- [x] We can explain what data is needed to reconstruct an agent folder on a
      new machine.

## Milestone 1: Hidden Transfer Workspace

Goal: separate transfer mechanics from the watched working tree.

Status: complete

Reference draft:

- [`fs-hidden-transfer-workspace.md`](./fs-hidden-transfer-workspace.md)

Checklist:

- [x] Create a per-drive hidden transfer area outside the watched root.
- [x] Add `staging/` for in-progress downloads and promotions.
- [x] Add `objects/` for verified retained content or chunk data.
- [x] Add `sessions/` or equivalent resumable transfer metadata.
- [x] Keep transfer control state separate from bulk payload storage, following
      the mailbox layering pattern.
- [x] Update remote download paths to assemble and verify in hidden storage
      first.
- [x] Publish into the working tree only via atomic rename or equivalent
      promotion.
- [x] Apply the "persist first, publish second" rule to remote file materialization.
- [x] Ensure watcher logic ignores all internal staging/object paths.
- [x] Define crash recovery and cleanup for stale transfer state.

Done when:

- [x] Remote downloads never create partial visible files.
- [x] Watcher events from our own transfer workspace are no longer part of
      normal sync behavior.
- [x] Interrupted downloads leave recoverable state instead of ambiguous files.

Likely repo touchpoints:

- `pkg/fs/reconciler.go` for staged download, verify, and publish flow
- `pkg/fs/rpc_http.go` for browser uploads that currently write straight into
  the watched tree
- `pkg/fs/outbox_worker.go` for publish ordering and blob readiness
- `pkg/fs/daemon_v2_5.go` for per-drive startup and workspace wiring

## Milestone 2: Local Detection Hardening

Goal: make local change detection reliable without trusting watcher events
alone.

Status: complete

Checklist:

- [x] Add periodic full scan alongside the watcher.
- [x] Add a periodic reconcile tick so missed pokes self-heal.
- [x] Route watcher-detected and scan-detected changes through one mutation
      path.
- [x] Add stable-write handling so large local writes are not uploaded too
      early.
- [x] Define scan cadence, jitter, and backoff behavior.
- [x] Add tests for missed watcher events and long-running local writes.

Done when:

- [x] Missed watcher events no longer require restart to recover.
- [x] Local write churn does not produce unstable sync state.

Likely repo touchpoints:

- `pkg/fs/watcher_handler.go` for watcher ingestion
- `pkg/fs/daemon_v2_5.go` for scheduled scan and reconcile cadence
- `pkg/fs/snapshot_poller.go` for removing over-reliance on baseline-only
  remote healing
- `pkg/fs/rpc_handler.go` and `web/src/pages/Drives.tsx` for surfacing scan
  and reconcile state

## Milestone 3: Agents Drive V1

Goal: ship the first durable personality-sharing drive on the current engine
while deeper FS work continues.

Status: design complete, implementation pending

Reference draft:

- [`agents-drive-contract.md`](./agents-drive-contract.md)

Contract status:

- The folder contract and recreation model are defined here.
- Product and runtime wiring remain future implementation work.

Checklist:

- [x] Define how an `Agents` drive is created and discovered.
- [x] Decide whether `Agents` is user-created, daemon-suggested, or
      auto-provisioned on first agent setup.
- [x] Define the per-agent folder naming convention.
- [x] Define which files are seeded automatically for a new agent folder.
- [x] Create initial `sky10.md` template content and field ownership rules.
- [x] Decide what is human-edited vs runtime-updated vs daemon-generated.
- [x] Define how an agent folder maps to a local agent runtime instance.
- [x] Define how another machine should interpret and recreate an agent from
      `sky10.md`.
- [x] Add docs for expected folder examples and replication intent.
- [x] Ensure the `Agents` drive uses the same durable FS semantics as ordinary
      synced content rather than special-casing agent personality files.

Implementation follow-up checklist:

- [ ] Create/provision an `Agents` drive through normal drive flows.
- [ ] Seed a new agent folder with `soul.md`, `memory.md`, `sky10.md`,
      `notes/`, and `attachments/`.
- [ ] Wire runtime-side agent/profile creation to the `Agents` drive contract.
- [ ] Add UI affordances for recognizing and opening the `Agents` drive.
- [ ] Add tests for initial folder seeding and cross-machine recreation inputs.

Done when:

- [x] A user can point to one agent folder and understand how that folder is
      meant to recreate the agent elsewhere.
- [x] `sky10.md` is specific enough to be operationally useful, not just a
      prose note.
- [ ] A user can create and sync an actual `Agents` drive in the product.

Likely repo touchpoints:

- `pkg/fs/drive.go` for drive creation/discovery rules
- `pkg/fs/rpc_drives.go` for create/list/start semantics and drive metadata
- `commands/fs.go` for CLI drive creation and management flows
- `web/src/lib/rpc.ts` for drive RPC additions consumed by the UI
- `web/src/pages/Drives.tsx` for first-class `Agents` drive presentation
- `web/src/pages/FileBrowser.tsx` for navigating seeded agent folders
- `web/src/pages/Agents.tsx` for linking runtime agents to folder-backed state
- `pkg/agent/rpc.go` and `pkg/agent/router.go` for any future runtime/folder
  correlation

Initial `sky10.md` contract to define during this milestone:

- stable agent ID, display name, and owning device ID
- runtime family and runtime version
- model, provider, and important inference settings
- bootstrap instructions or prompt references needed to recreate behavior
- expected working directories, repo assumptions, and tool requirements
- which fields are human-authored, runtime-authored, or daemon-authored
- last migration/export note that helps another machine recreate the agent

## Milestone 4: Peer Metadata Engine

Goal: make non-S3 sync first-class through durable peer-native metadata
exchange.

Status: complete

Checklist:

- [x] Add a durable per-drive metadata DB for local state and remote-per-peer
      state.
- [x] Persist explicit tombstones instead of relying on absence.
- [x] Add stronger conflict metadata than timestamp-first LWW, reusing the
      causal direction established in KV hardening.
- [x] Add an FS libp2p metadata protocol for full sync on first contact.
- [x] Reuse the summary-first anti-entropy pattern proven in `pkg/kv/p2p.go`
      instead of inventing a fresh snapshot-broadcast loop.
- [x] Add delta or summary-based anti-entropy for reconnects.
- [x] Add periodic bounded anti-entropy even without new writes.
- [x] Persist peer sync state across restart.
- [x] Make protocol registration/startup ordering deterministic so fresh joins
      do not miss FS metadata sync.
- [x] Add tests for long-offline catch-up and delete propagation without S3.
- [x] Add tests for delete propagation without S3.
- [x] Add reconnect-triggered anti-entropy without requiring a manual push.

Done when:

- [x] Two peers can converge correctly after long offline periods with no S3.
- [x] Delete intent survives reconnects without depending on a lucky baseline.
- [x] Fresh private-network join can start FS metadata sync without requiring a
      second reconnect or restart.

Likely repo touchpoints:

- `pkg/fs/opslog/opslog.go` for current file metadata and conflict behavior
- `pkg/fs/snapshot_poller.go` for replacing baseline-diff assumptions
- `pkg/fs/daemon_v2_5.go` for peer sync loop startup and anti-entropy cadence
- `pkg/kv/p2p.go` as the model for summary-first anti-entropy and startup
  ordering
- `pkg/fs/rpc_drives.go` and `pkg/fs/rpc_handler.go` for drive sync/debug
  surfaces

## Milestone 5: Unified Pull Planner

Goal: fetch data from the best available source instead of hardwiring one
remote path.

Status: complete

Checklist:

- [x] Build one planner that can fetch from local cache, local file reuse,
      peers, and S3.
- [x] Existing local content reuse reduces unnecessary downloads.
- [x] Prefer local cache before peer or S3 fetch when safe.
- [x] Prefer local file reuse before peer or S3 fetch when safe.
- [x] Add bounded concurrency and backpressure for file and chunk pulls.
- [x] Add verified block or chunk fetch with retry and source fallback.
- [x] Ensure peers can serve verified data from hidden local storage.
- [x] Keep peer transfer as the fast/live path and S3 as the durable/bootstrap
      path without changing file semantics.
- [x] Ensure S3 slots in as an optional source, not a different engine.
- [x] Surface read-source activity and last-source selection at drive/activity
      level.
- [x] Surface per-file transfer progress and active source selection.
- [x] Add source-health scoring, retry backoff, and degraded-source policy.

Done when:

- [x] The same file can be satisfied from peers in P2P-only mode and from S3
      when peers are absent, with no semantic change.
- [x] Existing local content reuse reduces unnecessary downloads.
- [x] Existing local cache reuse reduces unnecessary downloads.

Likely repo touchpoints:

- `pkg/fs/reconciler.go` for download planning and publish
- `pkg/fs/chunk.go` for reuse of current chunking behavior
- `pkg/fs/rpc_files.go` for future non-S3 download semantics
- `pkg/fs/rpc_http.go` for UI/browser-driven uploads and downloads
- `web/src/pages/FileBrowser.tsx` for transfer progress and source visibility

## Milestone 6: S3 As Optional Durability Layer

Goal: preserve and harden the recent S3 durability work without making it the
definition of correctness.

Status: complete

Checklist:

- [x] Keep upload-then-record behavior when S3 is enabled.
- [x] Preserve one FS model above multiple backends, following the mailbox
      layering rule.
- [x] Treat S3 as durable replica, bootstrap source, and optional blob source.
- [x] Ensure peer-native sync works unchanged when S3 is disabled.
- [x] Ensure cold-start or peer-absent recovery can leverage S3 when present.
- [x] Define S3-specific retry, validation, and health surfaces separately
      from peer health.
- [x] Add tests for peer-only, S3-only recovery, and hybrid peer+S3 behavior.

Done when:

- [x] Enabling S3 improves durability and availability but does not alter
      merge semantics.
- [x] Disabling S3 does not downgrade the engine into a second-class mode.

Likely repo touchpoints:

- `pkg/fs/outbox_worker.go` for upload-then-record guarantees
- `pkg/fs/rpc_handler.go` for RPC gating that currently assumes storage-backed
  operations
- `pkg/fs/rpc_files.go` for S3-backed and non-S3-backed file flows
- `pkg/fs/rpc_drives.go` and `pkg/fs/rpc_logs.go` for health/debug output

## Milestone 7: Observability, Conflicts, And Reliability Matrix

Goal: make the system explainable and verifiably reliable.

Status: in progress

Checklist:

- [x] Add per-drive health surfaces for watcher, scan, anti-entropy, peers,
      staging, and S3.
- [x] Reuse KV-style sync health expectations: readiness, peer count, last
      successful anti-entropy, and loud failure surfaces.
- [x] Add clear user-visible transfer phases: scanning, uploading,
      downloading, reconciling, retrying, conflict, degraded.
- [x] Reuse mailbox-style lifecycle visibility for transfer sessions and stuck
      work: attempted, in-progress, failed, retrying, delivered/published.
- [x] Make conflict-copy behavior explicit and testable.
- [ ] Review Windows, case-sensitivity, and path-normalization edge cases.
- [x] Add end-to-end coverage for P2P-only two-device sync.
- [x] Add end-to-end coverage for P2P-only offline catch-up.
- [x] Add end-to-end coverage for P2P-only delete propagation.
- [x] Add end-to-end coverage for conflict copy behavior.
- [x] Add end-to-end coverage for restart during download.
- [x] Add end-to-end coverage for restart during publish.
- [x] Add end-to-end coverage for peer unavailable, S3 available.
- [x] Add end-to-end coverage for peer available, S3 unavailable.
- [x] Add end-to-end coverage for hybrid peer+S3 recovery.

Done when:

- [x] A user can tell why a drive is behind or degraded without reading raw
      logs.
- [ ] Reliability claims are backed by repeatable tests instead of manual
      confidence.

Likely repo touchpoints:

- `pkg/fs/rpc_handler.go` for per-drive health/status responses
- `pkg/fs/rpc_drives.go` for richer drive state inspection
- `web/src/pages/Drives.tsx` and `web/src/pages/Activity.tsx` for user-visible
  health and transfer phases
- `pkg/fs/rpc_test.go` and `pkg/fs/integration_http_test.go` for RPC and HTTP
  coverage
- new end-to-end sync tests around peer-only and hybrid recovery paths

## Recommended Order

Completed:

1. Milestone 0
2. Milestone 1

Current mainline execution:

3. Milestone 4
4. Milestone 5
5. Milestone 6
6. Milestone 7

Deferred until the FS core is further along:

8. Milestone 3 product wiring

## Notes

- The `Agents` drive is intentionally early because it is the first clear
  consumer of durable synced agent personality/state.
- The hidden transfer workspace is intentionally early because it improves
  correctness regardless of whether the source is a peer or S3.
- Later shared drives for agent work output and cross-network collaboration
  should be planned separately once the core private-network FS model is
  stable.

## Implementation Slices

If we want this plan to land in reviewable checkpoints instead of one large FS
rewrite, the first slices should be:

1. Write the Milestone 0 model doc with explicit FS invariants, using the KV
   and mailbox work as named inputs.
2. Lock down the `Agents` drive contract and `sky10.md` field ownership before
   wiring agent-folder creation into the product.
3. Land the hidden transfer workspace before deeper peer-protocol changes so
   watcher correctness improves immediately for both peer and S3 sources.
4. Move peer metadata anti-entropy and the unified pull planner separately so
   metadata correctness and transfer efficiency can be tested in isolation.

## Current Focus

To stay aligned with this plan, the next coding focus should now be:

1. move to `Milestone 7`: broader reliability surfacing, degraded-state
   explainability, and the remaining matrix work
2. treat follow-on observability as support for reliability claims rather than
   a separate side track
3. keep `Milestone 3` in design/docs mode until the peer-correct core and
   optional-S3 layering are stronger
