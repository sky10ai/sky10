---
created: 2026-04-18
updated: 2026-04-18
model: gpt-5.4
---

# Device Subsystem Current State Report

## Summary

The current device implementation is split across `pkg/fs`, `pkg/id`,
`commands`, `pkg/link`, and `pkg/agent`.

That split explains both the architectural confusion and the observed product
gaps:

- devices are treated like a first-class product concept in the UI
- but their state is still owned by historical FS-era code
- current device metadata is assembled in the command layer
- there is no durable device history model

This report captures the starting point that Milestone 0 is designing against.

## Current Ownership In Code

### `pkg/fs`

[`pkg/fs/device.go`](../../../../pkg/fs/device.go) currently owns:

- the S3-backed device registry
- `DeviceInfo`
- `RegisterDevice`
- `ListDevices`
- `UpdateDeviceMultiaddrs`
- `GetDeviceName`
- platform detection
- public-IP geolocation lookup

This is the strongest signal that device state still lives in the wrong domain.

[`pkg/fs/rpc_invite.go`](../../../../pkg/fs/rpc_invite.go) also still contains
legacy `skyfs.deviceList` behavior that used to synthesize local device
metadata directly.

### `pkg/id`

[`pkg/id/rpc.go`](../../../../pkg/id/rpc.go) owns the manifest-backed
`identity.deviceList` response shape.

This file correctly owns:

- membership ordering
- `this_device`
- roles
- manifest-derived names and identifiers

It does not own device-state collection itself; it accepts an external
metadata provider.

### `commands`

[`commands/identity_rpc.go`](../../../../commands/identity_rpc.go) currently
merges device metadata from:

- `skyfs.ListDevices(...)`
- current link presence
- connected private peers

That means the command layer is currently implementing device-state composition.

### `pkg/link`

[`pkg/link/record.go`](../../../../pkg/link/record.go) defines live presence.

Presence currently carries:

- `device_pubkey`
- `peer_id`
- `multiaddrs`
- `published_at`
- `expires_at`
- `version`

Presence does not currently carry:

- system name
- OS/platform
- public IP
- location

So link is only a partial producer of device state.

### `pkg/agent`

[`pkg/agent/types.go`](../../../../pkg/agent/types.go) and
[`pkg/agent/registry.go`](../../../../pkg/agent/registry.go) already model agents
as hosted on a device via:

- `DeviceID`
- `DeviceName`

[`commands/serve.go`](../../../../commands/serve.go) injects those values when the
agent registry is created.

This confirms the correct dependency direction:

- agents know which device they run on
- devices should not have to import agent internals

## Historical Reason Devices Landed Under `pkg/fs`

Devices originally entered the repo as an S3-backed device registry used for
sync/onboarding flows. That is why the current registry, hostname, platform,
and location code all live under `pkg/fs`.

That origin explains the placement.

It does not justify keeping the placement now that devices are also core to:

- identity membership
- link presence
- the Devices UI
- agent hosting and routing

## What `location` Means Today

Current `location` is not device-native location.

[`pkg/fs/device.go`](../../../../pkg/fs/device.go) calls an external service and
stores coarse public-IP geolocation:

- public IP
- city
- region
- country

So today's `location` means:

- approximate public-IP geolocation from `ip-api.com`

It does not mean:

- GPS
- exact physical location
- user-supplied location

That field should therefore be treated as optional network enrichment, not
authoritative device truth.

## Observed Regression

The current UI still expects `platform` and `location`, but the data path
changed.

The old `skyfs.deviceList` path in
[`pkg/fs/rpc_invite.go`](../../../../pkg/fs/rpc_invite.go) explicitly synthesized
local `platform`, `ip`, and `location` for the current device even without S3.

The newer `identity.deviceList` path uses `commands/identity_rpc.go` to merge:

- durable registry values from `skyfs.ListDevices(...)`
- live link presence

That introduced a no-S3 / P2P-only gap:

- if there is no backend, registry metadata is absent
- link presence does not carry platform or location
- the current device can therefore lose those fields

This is a real product bug and also a sign that device-state ownership is
split incorrectly.

## Architectural Findings

### Devices Are Not An FS Subfeature

They are a core domain that crosses:

- identity
- transport
- agents
- UI

`pkg/fs` should not remain their canonical home.

### `pkg/id` Should Keep Membership Ownership

The device subsystem should not absorb:

- identity keys
- manifest signatures
- device role semantics
- join approval rules

Those remain in `pkg/id`.

### `pkg/device` Should Not Import `pkg/agent`

The domain says devices host agents.

The package relationship should still remain one-way:

- `pkg/agent` uses `DeviceID`
- `pkg/device` does not own or import agent internals

Device-plus-agent views should be joined in a composition layer.

### Current Device Data Is Snapshot-Shaped, Not History-Shaped

The current model mostly stores or computes "latest known values":

- platform
- IP
- location
- version
- last seen
- multiaddrs

That is enough for a card.

It is not enough for:

- "what changed?"
- "when did this machine move?"
- "why did the OS/version display change?"
- "which transitions happened while the device was offline?"

That is why Milestone 3 introduces observations and normalized change events.

## Milestone 0 Outcome

Based on the current state above, the Milestone 0 contract makes these design
calls:

- create a first-class `pkg/device`
- keep `pkg/id` as the owner of membership and roles
- treat `pkg/link` as a producer of device observations
- keep agent coupling one-way
- model device history using observations and change events instead of a single
  mutable snapshot

The next milestone is code extraction, not more architecture drift.
