---
created: 2026-04-19
model: gpt-5.4
---

# Device Subsystem Follow-Up

The `pkg/device` foundation refactor is complete. See
[`docs/work/past/2026/04/19-Device-Subsystem-Foundation.md`](../past/2026/04/19-Device-Subsystem-Foundation.md)
for the landed Milestone 0-2 work.

This doc tracks the remaining device-subsystem work that did not land as part
of the initial refactor.

## Current Baseline

Already landed:

- `pkg/device` owns current registry-backed device snapshots
- `pkg/device` owns local metadata collection
- `pkg/device` owns current metadata composition from registry + local fallback
  + link presence
- `identity.deviceList` remains the compatibility surface for the current UI
- current-device `platform`, `ip`, and coarse `location` work again in no-S3 /
  P2P mode

Still not done:

- device history is not modeled yet
- there is no append-only observation log
- there are no normalized device change events
- `identity.deviceList` is still the main device-facing RPC
- the UI still reads a current summary, not a timeline
- agent-aware device views are still only implicit composition

## Remaining Milestones

### 1. Device Observations And Event Normalization

Goal: stop treating the device record as a single mutable latest snapshot.

- [ ] Define `Observation`, `CurrentState`, and `ChangeEvent` types in
      `pkg/device`
- [ ] Add append-only observation storage keyed by device
- [ ] Record `source` and `observed_at` on every observation
- [ ] Normalize meaningful events such as:
      - `ip_changed`
      - `location_changed`
      - `system_name_changed`
      - `os_changed`
      - `version_changed`
      - `online`
      - `offline`
- [ ] Deduplicate repeated observations that carry no real state change
- [ ] Project observations into a fast current-state view

### 2. Feed Observations From Real Boundaries

Goal: collect device observations from the places the system already learns
about device state.

- [ ] Capture local observations on daemon start and periodic refresh
- [ ] Capture link-presence-driven observations from `pkg/link`
- [ ] Update current state when peer connectivity changes
- [ ] Keep geo-IP lookup sparse and non-fatal
- [ ] Use narrow interfaces so every package does not become a device package

### 3. Add First-Class Device RPC Surfaces

Goal: stop hiding device behavior entirely behind `identity.deviceList`.

- [ ] Add `device.list` for current summaries
- [ ] Add `device.get` for richer per-device current state
- [ ] Add `device.history` for paginated observations and/or change events
- [ ] Keep `identity.deviceList` as a compatibility adapter during migration
- [ ] Document source precedence and missing-field behavior in responses

### 4. Rework Devices UI Around Current State And History

Goal: let the Devices tab answer both "what is this device now?" and "what
changed?".

- [ ] Keep the card/list surface focused on current summary
- [ ] Show alias separately from observed system name where relevant
- [ ] Show platform/OS, version, current IP, coarse location, online state,
      and last seen from projected current state
- [ ] Add a per-device detail view or drawer for history
- [ ] Prefer meaningful diffs over repeated raw snapshots

### 5. Add Agent-Aware Device Read Models

Goal: show which agents are hosted on a device without making `pkg/device`
depend on `pkg/agent`.

- [ ] Keep `pkg/agent` owning registration and lifecycle
- [ ] Continue to attach `DeviceID` and `DeviceName` to agents
- [ ] Add composition-layer views that join device current state with agents
      hosted on that device
- [ ] Decide whether device detail should show current hosted agents, recent
      agent lifecycle events, or both

### 6. Cleanup And Historical Package Removal

Goal: finish the package-boundary cleanup after the new subsystem is live.

- [ ] Remove remaining compatibility shims from `pkg/fs`
- [ ] Remove obsolete command-layer metadata glue
- [ ] Move any remaining device-specific tests under `pkg/device`
- [ ] Audit remaining device-related call paths for Windows-hostile
      assumptions
- [ ] Update docs once the final RPC/UI surfaces are in place

## Recommended Order

1. Observations and event normalization
2. Feed observations from real boundaries
3. Device RPC surfaces
4. Devices UI history/detail work
5. Agent-aware read models
6. Cleanup

## Guardrails

- Keep `pkg/id` as the owner of membership, roles, trust, and join semantics
- Keep `pkg/device` free of `pkg/agent` imports
- Treat `location` as coarse public-IP geolocation, not exact device location
- Treat `last_seen` as derived from observations, not as an independent
  historical source of truth
- Preserve Windows-safe path and platform behavior as the subsystem grows
