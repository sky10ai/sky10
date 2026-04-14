---
created: 2026-04-14
updated: 2026-04-14
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

Checklist:

- [ ] Write the end-state sync model for `p2p-only` and `p2p+s3`.
- [ ] Define which local data is durable truth vs derived cache.
- [ ] Define publish rules: when a local or remote file becomes visible in the
      working tree.
- [ ] Define delete semantics for long-offline peers.
- [ ] Define conflict semantics for modify/modify and modify/delete.
- [ ] Define how peer state, local state, and optional S3 state relate.
- [ ] Define the `Agents` drive as a first-class consumer of the FS engine.
- [ ] Define the initial file contract for `soul.md`, `memory.md`, and
      `sky10.md`.

Done when:

- [ ] We can explain the same sync outcome with S3 disabled or enabled.
- [ ] We can explain what data is needed to reconstruct an agent folder on a
      new machine.

## Milestone 1: Hidden Transfer Workspace

Goal: separate transfer mechanics from the watched working tree.

Checklist:

- [ ] Create a per-drive hidden transfer area outside the watched root.
- [ ] Add `staging/` for in-progress downloads and promotions.
- [ ] Add `objects/` for verified retained content or chunk data.
- [ ] Add `sessions/` or equivalent resumable transfer metadata.
- [ ] Update remote download paths to assemble and verify in hidden storage
      first.
- [ ] Publish into the working tree only via atomic rename or equivalent
      promotion.
- [ ] Ensure watcher logic ignores all internal staging/object paths.
- [ ] Define crash recovery and cleanup for stale transfer state.

Done when:

- [ ] Remote downloads never create partial visible files.
- [ ] Watcher events from our own transfer workspace are no longer part of
      normal sync behavior.
- [ ] Interrupted downloads leave recoverable state instead of ambiguous files.

## Milestone 2: Local Detection Hardening

Goal: make local change detection reliable without trusting watcher events
alone.

Checklist:

- [ ] Add periodic full scan alongside the watcher.
- [ ] Add a periodic reconcile tick so missed pokes self-heal.
- [ ] Route watcher-detected and scan-detected changes through one mutation
      path.
- [ ] Add stable-write handling so large local writes are not uploaded too
      early.
- [ ] Define scan cadence, jitter, and backoff behavior.
- [ ] Add tests for missed watcher events and long-running local writes.

Done when:

- [ ] Missed watcher events no longer require restart to recover.
- [ ] Local write churn does not produce unstable sync state.

## Milestone 3: Agents Drive V1

Goal: ship the first durable personality-sharing drive on the current engine
while deeper FS work continues.

Checklist:

- [ ] Define how an `Agents` drive is created and discovered.
- [ ] Define the per-agent folder naming convention.
- [ ] Define which files are seeded automatically for a new agent folder.
- [ ] Create initial `sky10.md` template content and field ownership rules.
- [ ] Decide what is human-edited vs runtime-updated vs daemon-generated.
- [ ] Define how an agent folder maps to a local agent runtime instance.
- [ ] Define how another machine should interpret and recreate an agent from
      `sky10.md`.
- [ ] Add docs for expected folder examples and replication intent.

Done when:

- [ ] A user can point to one agent folder and understand how that folder is
      meant to recreate the agent elsewhere.
- [ ] `sky10.md` is specific enough to be operationally useful, not just a
      prose note.

## Milestone 4: Peer Metadata Engine

Goal: make non-S3 sync first-class through durable peer-native metadata
exchange.

Checklist:

- [ ] Add a durable per-drive metadata DB for local state and remote-per-peer
      state.
- [ ] Persist explicit tombstones instead of relying on absence.
- [ ] Add stronger conflict metadata than timestamp-first LWW.
- [ ] Add an FS libp2p metadata protocol for full sync on first contact.
- [ ] Add delta or summary-based anti-entropy for reconnects.
- [ ] Add periodic bounded anti-entropy even without new writes.
- [ ] Persist peer sync state across restart.
- [ ] Add tests for long-offline catch-up and delete propagation without S3.

Done when:

- [ ] Two peers can converge correctly after long offline periods with no S3.
- [ ] Delete intent survives reconnects without depending on a lucky baseline.

## Milestone 5: Unified Pull Planner

Goal: fetch data from the best available source instead of hardwiring one
remote path.

Checklist:

- [ ] Build one planner that can fetch from local cache, local file reuse,
      peers, and S3.
- [ ] Prefer local reuse before peer or S3 fetch when safe.
- [ ] Add bounded concurrency and backpressure for file and chunk pulls.
- [ ] Add verified block or chunk fetch with retry and source fallback.
- [ ] Ensure peers can serve verified data from hidden local storage.
- [ ] Ensure S3 slots in as an optional source, not a different engine.
- [ ] Surface per-file transfer progress and active source selection.

Done when:

- [ ] The same file can be satisfied from peers in P2P-only mode and from S3
      when peers are absent, with no semantic change.
- [ ] Existing local content reuse reduces unnecessary downloads.

## Milestone 6: S3 As Optional Durability Layer

Goal: preserve and harden the recent S3 durability work without making it the
definition of correctness.

Checklist:

- [ ] Keep upload-then-record behavior when S3 is enabled.
- [ ] Treat S3 as durable replica, bootstrap source, and optional blob source.
- [ ] Ensure peer-native sync works unchanged when S3 is disabled.
- [ ] Ensure cold-start or peer-absent recovery can leverage S3 when present.
- [ ] Define S3-specific retry, validation, and health surfaces separately
      from peer health.
- [ ] Add tests for peer-only, S3-only recovery, and hybrid peer+S3 behavior.

Done when:

- [ ] Enabling S3 improves durability and availability but does not alter
      merge semantics.
- [ ] Disabling S3 does not downgrade the engine into a second-class mode.

## Milestone 7: Observability, Conflicts, And Reliability Matrix

Goal: make the system explainable and verifiably reliable.

Checklist:

- [ ] Add per-drive health surfaces for watcher, scan, anti-entropy, peers,
      staging, and S3.
- [ ] Add clear user-visible transfer phases: scanning, uploading,
      downloading, reconciling, retrying, conflict, degraded.
- [ ] Make conflict-copy behavior explicit and testable.
- [ ] Review Windows, case-sensitivity, and path-normalization edge cases.
- [ ] Add end-to-end coverage for P2P-only two-device sync.
- [ ] Add end-to-end coverage for P2P-only offline catch-up.
- [ ] Add end-to-end coverage for P2P-only delete propagation.
- [ ] Add end-to-end coverage for conflict copy behavior.
- [ ] Add end-to-end coverage for restart during download.
- [ ] Add end-to-end coverage for restart during publish.
- [ ] Add end-to-end coverage for peer unavailable, S3 available.
- [ ] Add end-to-end coverage for peer available, S3 unavailable.
- [ ] Add end-to-end coverage for hybrid peer+S3 recovery.

Done when:

- [ ] A user can tell why a drive is behind or degraded without reading raw
      logs.
- [ ] Reliability claims are backed by repeatable tests instead of manual
      confidence.

## Recommended Order

1. Milestone 0
2. Milestone 1
3. Milestone 2
4. Milestone 3
5. Milestone 4
6. Milestone 5
7. Milestone 6
8. Milestone 7

## Notes

- The `Agents` drive is intentionally early because it is the first clear
  consumer of durable synced agent personality/state.
- The hidden transfer workspace is intentionally early because it improves
  correctness regardless of whether the source is a peer or S3.
- Later shared drives for agent work output and cross-network collaboration
  should be planned separately once the core private-network FS model is
  stable.
