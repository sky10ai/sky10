---
created: 2026-04-19
model: gpt-5.4
---

# Device Subsystem Foundation

This entry covers the `pkg/device` foundation work that landed on `main`
through:

- `47046d2` (`docs(work): add device subsystem plan`)
- `0015375` (`docs(work): complete device milestone 0`)
- `1766cb1` (`refactor(device): extract current device snapshot ownership`)
- `d29a9bd` (`refactor(device): move metadata composition into service`)

This was the completed foundation slice of the broader device-subsystem plan.
It established `pkg/device` as the owner of current device state and metadata
composition without yet introducing device history, change events, or new
device RPC surfaces.

The remaining follow-up is now tracked in
[`docs/work/todo/device-subsystem-followup.md`](../../../todo/device-subsystem-followup.md).

## Why This Work Started

Devices were already a real product concept in `sky10`:

- there is a Devices tab in the web UI
- devices are the membership unit of the private network
- devices host agents
- devices surface in link presence, routing, and sync behavior

But the code still reflected the older S3-backed origin of the feature:

- registry and local metadata lived in `pkg/fs`
- `identity.deviceList` depended on command-layer merge logic
- no-S3 / P2P mode could lose the current device's `platform`, `ip`, and
  `location`

The goal of this slice was not to redesign the product model yet. The goal was
to move ownership into the right package boundary and make the current behavior
coherent.

## What Landed On Main

### 1. Milestone 0 locked the package and data contract

The first two docs commits established the design boundary for the refactor:

- `pkg/id` keeps identity, membership, roles, trust, and join/approve semantics
- `pkg/device` owns device state, local metadata collection, registry logic,
  current-state composition, and future history/event work
- `pkg/link` feeds device observations and presence signals
- `pkg/agent` continues to use `DeviceID` and `DeviceName`, but `pkg/device`
  does not import `pkg/agent`

That contract also made the current field model explicit:

- `platform`, `ip`, `location`, `version`, `last_seen`, and `multiaddrs` are
  best-effort current-state fields
- `location` means coarse public-IP geolocation, not GPS or device-native
  location
- `last_seen` is derived metadata, not a durable historical model yet

### 2. Milestone 1 extracted current device snapshot ownership

`1766cb1` moved the current device snapshot and registry logic out of `pkg/fs`
and into `pkg/device`.

Main outcomes:

- [`pkg/device/info.go`](../../../../pkg/device/info.go) now owns the current
  `Info` model for registry-backed device snapshots
- [`pkg/device/registry.go`](../../../../pkg/device/registry.go) now owns
  registry read/write/update logic
- [`pkg/device/local.go`](../../../../pkg/device/local.go) now owns local
  metadata collection:
  - device name
  - platform normalization
  - coarse public-IP geolocation
- `pkg/fs` was reduced to compatibility shims instead of remaining the
  canonical owner of device metadata

This preserved the existing `identity.deviceList` surface while moving the
underlying ownership into the correct domain package.

### 3. Milestone 2 moved metadata composition into `pkg/device`

`d29a9bd` completed the next structural step by moving the metadata merge logic
out of `commands/identity_rpc.go` and into
[`pkg/device/service.go`](../../../../pkg/device/service.go).

Main outcomes:

- `pkg/device` now composes current metadata from:
  - registry state
  - local current-device fallback
  - live link presence
- `commands/identity_rpc.go` now wires the device service instead of
  implementing device-state semantics directly
- `identity.deviceList` stayed stable as the compatibility surface
- the no-S3 / P2P regression for current-device `platform`, `ip`, and
  `location` was fixed
- the service avoids unnecessary fresh geo-IP lookup when registry data is
  already present for the current device

This means the current device once again shows coarse location and platform in
P2P-only mode, while remote devices still get only the fields that are
actually available from registry data and/or presence.

## Tests That Landed With This Slice

The refactor was anchored with characterization and package-level tests rather
than relying on the UI alone.

Key coverage added or preserved:

- [`pkg/device/registry_test.go`](../../../../pkg/device/registry_test.go)
  for registry ownership and storage behavior
- [`pkg/device/local_test.go`](../../../../pkg/device/local_test.go)
  for local metadata collection and Windows-safe platform normalization
- [`pkg/device/service_test.go`](../../../../pkg/device/service_test.go)
  for:
  - current-device fallback without a backend
  - sparse registry merge behavior
  - avoiding unnecessary geo-IP lookup
  - connected-private-peer presence merge behavior
- [`pkg/fs/rpc_invite_test.go`](../../../../pkg/fs/rpc_invite_test.go)
  to pin the legacy `skyfs.*` invite/device-list behavior during the ongoing
  package-boundary cleanup

## What This Did Not Try To Finish

This work did not add the full device-history product model.

It did not yet introduce:

- append-only observations
- normalized change events
- projected `CurrentState` vs historical records
- dedicated `device.*` RPC surfaces
- device detail/history UI
- agent-aware device read models

Those remaining pieces are tracked in
[`docs/work/todo/device-subsystem-followup.md`](../../../todo/device-subsystem-followup.md).

## Outcome

After these commits:

- `pkg/device` became the real home of current device metadata
- `pkg/fs` stopped being the canonical owner of device state
- `commands` stopped hand-merging device metadata
- `identity.deviceList` kept working for the existing UI
- current-device `platform`, `ip`, and coarse `location` work again in
  no-S3 / P2P mode

That is the foundation the later device-history and device-UI work should now
build on, instead of layering more behavior onto `pkg/fs` and command glue.
