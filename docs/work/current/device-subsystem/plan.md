---
created: 2026-04-18
updated: 2026-04-18
model: gpt-5.4
---

# Device Subsystem And History Plan

## Goal

Make devices a first-class subsystem in `sky10` rather than a historical mix
of `pkg/fs`, `pkg/id`, `pkg/link`, and command-layer glue.

The end state should support:

- a clear `pkg/device` ownership boundary
- stable current device summaries for UI and RPC
- append-only device observations and normalized change events
- richer device UX around host identity, IP, coarse location, OS, version,
  presence, and hosted agents
- correct composition with identity membership and agent placement without
  creating package cycles

## Status Snapshot

- `Milestone 0`: complete
- `Milestone 1`: pending
- `Milestone 2`: pending
- `Milestone 3`: pending
- `Milestone 4`: pending
- `Milestone 5`: pending
- `Milestone 6`: pending
- `Milestone 7`: pending
- `Milestone 8`: pending

## Reference Docs

- [`milestone-0-contract.md`](./milestone-0-contract.md)
- [`current-state-report.md`](./current-state-report.md)

## Why This Exists

Today device behavior is real product surface area:

- devices have a dedicated UI tab
- devices are core to private-network membership
- devices are the hosting unit for agents
- devices surface in link presence, routing, and sync behavior

But the implementation does not reflect that importance:

- S3-backed device registry code lives in `pkg/fs`
- identity RPC manually merges device metadata in `commands/identity_rpc.go`
- link presence carries partial device state
- agent registration takes injected `DeviceID` and `DeviceName` but there is
  no central device subsystem owning that data

That layout reflects feature history, not domain design.

## Target Domain Boundaries

The intended package responsibilities should be:

- `pkg/id`
  Identity keys, manifests, trusted membership, device roles, join/approve
  semantics, "which device keys belong to this identity".
- `pkg/device`
  Device profile, local metadata collection, registry/repository logic,
  current-state projection, observations, change history, and device-focused
  RPC/service composition.
- `pkg/link`
  Transport presence, peer discovery, peer addressing, and connectivity
  signals that feed device observations.
- `pkg/agent`
  Agent identity, lifecycle, registration, and routing using `DeviceID` as
  placement metadata.
- `pkg/fs`
  Storage and sync only. It should not remain the canonical home of device
  metadata.

Dependency direction should stay one-way:

- `agent -> device`
- not `device -> agent`

In domain terms, agents run on devices. In package terms, `pkg/device`
should not import `pkg/agent`.

## Design Principles

- extract ownership before redesigning behavior
- preserve current RPC/UI behavior while moving code
- treat coarse IP geolocation as optional enrichment, not truth
- treat `last seen` as derived from observations, not as an independent
  primary record
- keep alias and observed system name separate
- support Windows-ready metadata collection and paths
- let command/RPC layers compose across subsystems instead of forcing one
  core package to know everything

## Current State To Preserve During Extraction

The first refactor should preserve the existing device list semantics:

- manifest-backed device membership from `pkg/id`
- current metadata enrichment from registry and link presence
- `identity.deviceList` response shape used by the web UI
- current agent registration model where each local agent is associated with a
  `DeviceID` and `DeviceName`

Known current gaps that the later milestones should address:

- no-S3 / P2P path can lose local `platform` and `location`
- `location` is only coarse public-IP geolocation from `ip-api.com`
- device metadata is mostly a mutable latest snapshot, not history
- package ownership is split across unrelated domains

## Milestone 0: Lock The Contract

Goal: define the device subsystem boundary and product contract before moving
files.

Status: complete

Checklist:

- [x] Define the canonical device concepts: profile, current state,
      observation, and change event.
- [x] Define which fields are stable vs derived vs best-effort:
      alias, system name, OS, version, public IP, coarse location,
      multiaddrs, online state, and last seen.
- [x] Define source precedence between local observation, registry snapshot,
      link presence, and user-authored alias.
- [x] Define privacy/retention expectations for IP and location history.
- [x] Define how hosted agents appear in device-facing read models without
      making `pkg/device` depend on `pkg/agent`.

Done when:

- [x] We can explain exactly what belongs in `pkg/device` vs `pkg/id`.
- [x] We can explain the current-state and history models without reference to
      `pkg/fs`.

Artifacts:

- [`milestone-0-contract.md`](./milestone-0-contract.md)
- [`current-state-report.md`](./current-state-report.md)

## Milestone 1: Extract Current Device Snapshot Ownership

Goal: move current device snapshot logic out of `pkg/fs` into `pkg/device`
without changing behavior.

Scope:

- move current `DeviceInfo`-style types
- move registry load/save/update logic
- move `GetDeviceName`, platform detection, and geo-IP lookup behind a
  device-local collector
- keep the same storage behavior and same device-list output semantics

Checklist:

- [ ] Create `pkg/device` with a narrow current-state model.
- [ ] Move S3-backed device registry code from `pkg/fs` into `pkg/device`.
- [ ] Hide geo-IP lookup behind a device-local metadata collector rather than
      exporting a generic helper.
- [ ] Keep Windows-friendly platform detection in scope for the extraction.
- [ ] Leave compatibility shims only where needed during the transition.

Done when:

- [ ] `pkg/fs` no longer owns canonical device metadata types or collectors.
- [ ] Existing command wiring can fetch current device state via `pkg/device`.

## Milestone 2: Replace Command-Layer Metadata Merging With A Device Service

Goal: stop hand-merging device metadata inside `commands/identity_rpc.go`.

The command layer should wire a device service, not implement the device model.

Checklist:

- [ ] Create a `pkg/device` service that composes registry state, local
      metadata, and link presence.
- [ ] Move current merge behavior out of `commands/identity_rpc.go`.
- [ ] Preserve `identity.deviceList` as a compatibility surface backed by the
      new service.
- [ ] Restore correct local metadata in no-S3 / P2P-only mode.
- [ ] Keep membership ordering and device role logic anchored in `pkg/id`.

Done when:

- [ ] Device metadata composition logic is owned by `pkg/device`.
- [ ] `identity.deviceList` remains stable for the current UI.

## Milestone 3: Introduce Device Observations And Event Normalization

Goal: stop treating the device record as a single mutable blob.

New model:

- `Profile`
  Stable identity and user-managed fields.
- `Observation`
  Raw append-only facts observed at a specific time from a specific source.
- `CurrentState`
  Materialized latest known state for cards and summaries.
- `ChangeEvent`
  Normalized events derived from observations.

Checklist:

- [ ] Define append-only observation storage keyed by device.
- [ ] Record source and timestamp on every observation.
- [ ] Normalize real changes into explicit events such as `ip_changed`,
      `location_changed`, `system_name_changed`, `os_changed`,
      `version_changed`, `online`, and `offline`.
- [ ] Deduplicate repeated observations that carry no real change.
- [ ] Project latest observations into a fast current-state view.

Done when:

- [ ] Device cards can read a stable current-state model.
- [ ] Device history can explain what changed and when.

## Milestone 4: Feed Observations From Existing System Boundaries

Goal: collect device observations from the real places device state changes.

Candidate producers:

- daemon startup and registration
- periodic local metadata refresh
- link presence publish
- remote presence receive
- peer connect/disconnect
- device version change on upgrade/restart
- future explicit local rename or alias updates

Checklist:

- [ ] Capture a local observation on daemon start and periodic refresh.
- [ ] Capture presence-derived observations from `pkg/link`.
- [ ] Update current state when peer connectivity changes.
- [ ] Treat location lookup failures as non-fatal and sparse.
- [ ] Avoid turning every package into a device package; use narrow observer
      or service interfaces at the boundaries.

Done when:

- [ ] New device history entries appear because the system observed something,
      not because one RPC wrote a giant replacement snapshot.

## Milestone 5: Add First-Class Device RPC Surfaces

Goal: make device-facing RPC explicit instead of hiding everything behind
`identity.deviceList`.

Recommended surfaces:

- `device.list`
  Current summary view for the devices page.
- `device.get`
  One device with richer current state.
- `device.history`
  Paginated observations and/or normalized change events.

Compatibility rule:

- keep `identity.deviceList` during migration as a compatibility adapter

Checklist:

- [ ] Add device list RPC backed by current-state projection.
- [ ] Add device detail RPC backed by the same subsystem.
- [ ] Add history RPC with pagination and stable ordering.
- [ ] Keep current UI unblocked while new RPCs land.
- [ ] Document source precedence and missing-field behavior in responses.

Done when:

- [ ] device-specific UI does not depend on identity RPC internals.

## Milestone 6: Rework Devices UI Around Current State And History

Goal: give the Devices tab a real current summary and drill-down history view.

UI layers:

- cards/list for current state
- detail view for history and change timeline
- later network-wide event feed if useful

Checklist:

- [ ] Keep the device card focused on current summary.
- [ ] Show alias separately from observed system name where applicable.
- [ ] Show OS/platform, version, current IP, coarse location, online state,
      and last seen from current-state projection.
- [ ] Add a per-device detail view or drawer for history timeline.
- [ ] Surface meaningful diffs rather than raw repeated snapshots.

Done when:

- [ ] A user can answer "what is this device now?" and "what changed?" from
      the UI without reading logs.

## Milestone 7: Add Agent-Aware Device Read Models

Goal: show which agents are hosted on a device without coupling
`pkg/device` to `pkg/agent`.

Rule:

- this is a composition-layer concern, not a core `pkg/device` dependency

Checklist:

- [ ] Keep `pkg/agent` owning agent registration and lifecycle.
- [ ] Continue to attach `DeviceID` and `DeviceName` to agent registration.
- [ ] Add composition-layer read models that join device current state with
      agents hosted on that device.
- [ ] Decide whether device detail views should show current hosted agents,
      recent agent lifecycle events, or both.

Done when:

- [ ] Users can see which agents live on a device without creating a package
      cycle or muddying subsystem ownership.

## Milestone 8: Migration And Cleanup

Goal: remove the historical package placement once the new subsystem is live.

Checklist:

- [ ] Remove compatibility shims from `pkg/fs`.
- [ ] Remove command-layer metadata glue that became obsolete.
- [ ] Move remaining device-specific tests under `pkg/device`.
- [ ] Update docs and references to the new package boundaries.
- [ ] Audit for Windows-hostile assumptions in platform or path handling.

Done when:

- [ ] The codebase structure matches the actual domain model.

## Test Plan

The refactor should add or preserve test coverage in layers.

### Contract And Compatibility Tests

- [ ] Keep `pkg/id` tests proving manifest-backed ordering and device-role
      behavior remain correct.
- [ ] Keep compatibility tests for `identity.deviceList` response shape during
      the migration.
- [ ] Add a regression test for no-S3 / P2P current-device metadata so
      `platform` and `location` do not disappear again.

### `pkg/device` Unit Tests

- [ ] local metadata collector: hostname, OS/platform, version, sparse
      geo-IP enrichment, non-fatal failures
- [ ] registry repository: read/write/update behavior and stable identifiers
- [ ] current-state projection: latest values are selected correctly
- [ ] change normalization: repeated observations dedupe correctly and real
      field transitions emit the right event types
- [ ] source precedence: alias vs observed hostname, registry vs presence,
      partial presence updates not clobbering better data

### Integration Tests

- [ ] command wiring uses `pkg/device` service rather than hand-merging fields
- [ ] link-driven presence updates feed current state and history correctly
- [ ] agent registration still consumes injected `DeviceID` and `DeviceName`
      without importing `pkg/device`
- [ ] device RPC list/detail/history responses remain deterministic and
      paginated correctly

### Platform Tests

- [ ] platform normalization is correct on supported OSes
- [ ] Windows-safe behavior is covered where metadata collection or paths are
      involved

## Recommended Delivery Order

The safest execution order is:

1. Milestone 0
2. Milestone 1
3. Milestone 2
4. Milestone 3
5. Milestone 4
6. Milestone 5
7. Milestone 6
8. Milestone 7
9. Milestone 8

That sequencing keeps structural extraction separate from the larger history
product change and reduces the risk of breaking current device UX during the
package move.
