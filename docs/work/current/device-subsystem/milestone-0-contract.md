---
created: 2026-04-18
updated: 2026-04-18
model: gpt-5.4
---

# Device Subsystem Milestone 0 Contract

## Purpose

This document locks the design contract for the `sky10` device subsystem before
code extraction starts.

The point of Milestone 0 is not to move files. The point is to decide what
`pkg/device` will own, what it will not own, and what data model the later
refactor is actually implementing.

## Core Decision

Devices are a first-class subsystem in `sky10`.

That is true because devices are:

- the membership unit of the private network
- the runtime host for agents
- the thing that publishes and receives link presence
- the thing the user sees and manages in the Devices UI

The current placement under `pkg/fs` is historical, not architectural.

## Package Boundaries

### `pkg/id`

Owns identity and membership:

- identity keys and bundles
- manifest membership and signatures
- device roles and trust semantics
- join and approve flows
- the answer to "which device keys belong to this identity?"

### `pkg/device`

Owns device state:

- device profile
- local metadata collection
- current-state projection
- observation and change-event models
- device registry/repository logic
- device-focused service composition and future device RPC surfaces

### `pkg/link`

Owns transport and discovery signals:

- peer discovery
- connectivity
- live presence records
- multiaddrs and peer IDs

`pkg/link` is a producer of device observations. It is not the owner of the
device subsystem.

### `pkg/agent`

Owns agent lifecycle and routing:

- agent registration
- agent identity
- device placement via `DeviceID`
- local and cross-device routing

`pkg/agent` may depend on device identity strings. `pkg/device` must not depend
on `pkg/agent`.

### `pkg/fs`

Owns storage and sync only.

It must stop being the canonical owner of device metadata.

## Dependency Direction

The allowed direction is:

- `agent -> device`

The forbidden direction is:

- `device -> agent`

Reason:

- the domain relationship is "a device may host many agents"
- the package relationship should still avoid cycles
- device state must remain valid even when no agents are running

Agent-aware device views should be built in a composition layer, not by making
`pkg/device` import `pkg/agent`.

## Canonical Device Concepts

### Profile

Stable identity and user-managed fields for a device.

Owned fields:

- `device_id`
- `device_pubkey`
- `identity`
- `alias`
- persistent local preferences or labels

Not owned by the profile:

- membership role
- live presence
- last seen
- current IP or location

### Observation

An append-only raw fact set observed at a specific time from a specific source.

Required fields:

- `device_id` or `device_pubkey`
- `observed_at`
- `source`

Optional observed fields:

- `system_name`
- `os_name`
- `os_version`
- `platform`
- `app_version`
- `public_ip`
- `location`
- `peer_id`
- `multiaddrs`
- `presence_published_at`

Observations are sparse by design. A source should only write the fields it
actually observed.

### CurrentState

The materialized latest known state used by the Devices UI and summary RPCs.

Current state is derived from profile plus observations plus identity
membership. It is not the durable source of truth for everything.

Fields include:

- `device_id`
- `device_pubkey`
- `alias`
- `system_name`
- `display_name`
- `os_name`
- `os_version`
- `platform`
- `app_version`
- `public_ip`
- `location`
- `peer_id`
- `multiaddrs`
- `last_seen`
- `online`
- `role`

### ChangeEvent

A normalized human- and UI-friendly event derived from one or more
observations.

Expected event kinds:

- `system_name_changed`
- `os_changed`
- `version_changed`
- `ip_changed`
- `location_changed`
- `multiaddrs_changed`
- `online`
- `offline`

Events should represent meaningful state transitions, not every raw write.

## Field Ownership

### Stable And Authoritative

These should not come from link presence or geo-IP enrichment:

- `device_id`
- `device_pubkey`
- identity membership
- membership role

Source of truth:

- `pkg/id`

### User-Managed

- `alias`

Source of truth:

- device profile / user action

### Observed Host Metadata

- `system_name`
- `os_name`
- `os_version`
- `platform`
- `app_version`

Preferred source of truth:

- local self-observation collected on the device itself

### Observed Network Metadata

- `public_ip`
- `location`
- `peer_id`
- `multiaddrs`

Preferred source of truth:

- local observation for `public_ip` and `location`
- link presence for `peer_id` and `multiaddrs`

### Derived

- `display_name`
- `last_seen`
- `online`

Rules:

- `display_name` = alias if present, otherwise observed system name,
  otherwise membership name
- `last_seen` is derived from the latest accepted observation timestamp
- `online` is derived from live presence/connectivity signals, not stored as a
  primary field in raw registry data

## Source Precedence

Precedence is field-specific, not one giant total ordering.

### Name Fields

For user-facing display:

1. profile alias
2. latest observed system name
3. membership manifest name

### OS And Version Fields

1. latest local self-observation
2. durable registry snapshot produced by local self-observation
3. link presence only for fields that presence actually carries

Link presence should not invent or overwrite OS fields it does not own.

### Network Reachability Fields

1. live link presence for `peer_id`, `multiaddrs`, and online state
2. latest stored observation for last known values when live presence is absent

### Public IP And Location

1. latest successful local self-observation
2. previously stored value if the newest observation omitted those fields

An empty or failed location lookup must not erase a previously known value
unless we intentionally model that as an explicit removal event later.

## Privacy And Retention

`location` in this subsystem means coarse public-IP geolocation.

It does not mean:

- GPS
- exact physical location
- Wi-Fi triangulation
- user-entered location

Expected shape:

- city
- region
- country

Policy for v1:

- store only coarse public-IP geolocation
- treat location enrichment as optional and best-effort
- do not block registration or presence on lookup failure
- retain current state while the device exists
- retain raw observations for 30 days by default
- retain normalized change events for 180 days by default

The retention windows are design defaults and may later become configurable,
but the subsystem should be designed with expiry in mind rather than assuming
infinite raw history.

## Agent Relationship

The device subsystem should know that devices can host agents, but it should
not own agent internals.

Allowed:

- device-facing read models that include `hosted_agent_count`
- composition-layer responses that join device current state with agent list
- `pkg/agent` consuming `DeviceID` and `DeviceName`

Not allowed:

- `pkg/device` importing `pkg/agent` types
- `pkg/device` owning agent registration or lifecycle
- device history being the source of truth for agent state

If the UI needs "device details with hosted agents", that should be assembled
above the package boundary.

## Compatibility Requirements

Milestone 1 and Milestone 2 must preserve these current contracts while the
subsystem is extracted:

- `identity.deviceList` remains usable by the current web UI
- membership ordering and roles continue to come from `pkg/id`
- agents continue to register against a local `DeviceID` and `DeviceName`

## Out Of Scope For Milestone 0

- moving files
- adding new RPC namespaces
- building the history UI
- changing transport formats
- changing membership semantics

Milestone 0 is complete when the package boundaries and data contract are clear
enough that Milestone 1 can be executed without inventing the model mid-refactor.
