---
created: 2026-04-06
updated: 2026-04-06
model: gpt-5.4
---

# Private Network Discovery Refactor — Engineering Plan

## Assumptions

- Existing discovery state can be discarded.
- There is no migration or compatibility requirement for old identity-scoped
  discovery records.
- The refactor may keep the current identity/device keys if useful, but all
  current local discovery caches and manifest authority can be replaced.
- LAN success during development is not evidence of correctness and must not
  shape protocol decisions.

## Workstreams

### 1. Design Freeze

**Goal**

Lock the private-network discovery model before touching code.

**Scope**

- finalize terminology: `private network`
- finalize invariants from [README.md](./README.md)
- finalize record families:
  - membership
  - presence
  - observations later
- finalize membership v1 format:
  - full signed membership record
  - explicit removals
  - serialized membership edits in v1
- decide presence freshness fields and republish cadence targets

**Deliverables**

- this plan directory
- concrete wire-level record contract in [record-spec.md](./record-spec.md)
- explicit approval on invariants and record model

**Acceptance**

- all future implementation work can be judged against a fixed set of
  invariants
- there is no ambiguity about DHT primary vs Nostr fallback

### 2. Surface Cleanup

**Goal**

Remove the current discovery assumptions that are structurally wrong for
multi-device private networks.

**Scope**

- remove the identity-scoped DHT presence model
- remove any remaining assumption that one identity maps to one live peer
- demote local manifest/cache from source of truth to cache only
- identify and delete dead paths that would compete with the new model

**Expected Code Areas**

- `pkg/link/record.go`
- `pkg/link/discovery.go`
- `commands/serve.go`
- any RPC/debug surfaces that expose the old identity-scoped discovery view

**Acceptance**

- there is no code path where one stale local file defines private-network
  membership
- there is no code path where one identity-scoped presence record stands in
  for an entire private network

### 3. DHT Membership And Presence

**Goal**

Establish a correct DHT-native record model for multi-device private
networks.

**Scope**

- add a proper custom DHT namespace/validator for sky10 private-network
  records
- implement one deterministic DHT key for private-network membership
- implement one deterministic DHT key per device for private-network presence
- verify signatures at read and write boundaries

**Membership Requirements**

- signed by the shared identity key
- authoritative device set
- includes explicit removals
- versioned or monotonic-timestamped
- v1 uses serialized full-record updates, not concurrent multi-writer merge

**Presence Requirements**

- signed by the device key
- includes peer ID, multiaddrs, observed time, expiry
- keyed by identity + device public key

**Acceptance**

- 3+ devices in the same private network can publish without clobbering each
  other
- DHT writes and reads succeed for both membership and presence
- stale or malformed records are rejected

### 4. Startup Rebuild Logic

**Goal**

Make daemon startup reconstruct the private network from global state.

**Scope**

- fetch membership from DHT first
- fall back to Nostr only when DHT data is unavailable or stale
- derive the expected device presence keys from verified membership
- fetch and verify presence records
- dial all fresh peers
- rewrite local cache from verified global state

**Expected Code Areas**

- `commands/serve.go`
- `pkg/link/discovery.go`
- `pkg/id/*` cache handling
- any local cache read/write helpers

**Acceptance**

- delete local discovery cache on one device, restart it, and it rejoins the
  private network without re-invite
- restart all devices independently and they reconstruct from global state
- startup behavior does not require LAN visibility

### 5. Presence Heartbeats And Expiry

**Goal**

Keep reachability fresh so the global Schelling point reflects current
network reality.

**Scope**

- publish presence on startup
- republish presence periodically
- republish on network/address changes
- expire stale presence quickly enough to avoid poisoning reconnect logic
- define tie-breaking rules for competing presence writes from the same device

**Acceptance**

- NAT/port/IP drift heals automatically within the heartbeat window
- stale presence does not strand reconnect logic for long
- private-network reconnect works after long offline periods

### 6. Nostr Fallback

**Goal**

Mirror the same semantics to Nostr so private-network discovery survives DHT
problems without inventing a second trust model.

**Scope**

- publish membership and presence with the same identity/device semantics
- use the same signature and freshness rules
- query Nostr only as fallback:
  - missing DHT membership
  - missing or stale DHT presence

**Expected Code Areas**

- `pkg/link/nostr.go`
- `pkg/link/discovery.go`
- any publish scheduling code

**Acceptance**

- DHT outage does not strand the private network
- Nostr fallback requires no special-case trust logic

### 7. Repair Flow

**Goal**

Support catastrophic local divergence without redefining normal operation.

**Scope**

- add an explicit private-network repair workflow
- preserve the existing private network
- do not create a new identity
- do not rely on LAN assumptions

**Suggested V1 Boundary**

- repair eligibility is intentionally deferred as an open design question
- the workflow must preserve the existing private network once that boundary
  is defined

**Acceptance**

- a badly diverged device can be rehydrated from a healthy member without
  creating a new private network
- ordinary restart/update still never requires repair

### 8. Hardening And Debug Surfaces

**Goal**

Make the new model observable and regression-resistant.

**Scope**

- add debug/RPC surfaces for:
  - current private-network membership
  - current presence records
  - freshness/expiry state
  - source used: DHT or Nostr
- add targeted regression tests
- validate behavior with more than two devices

**Acceptance**

- the April 6 failure mode is covered by automated tests
- operators can explain private-network state from runtime debug output

### 9. Later Improvement: Observations

**Goal**

Improve large private-network resilience without making observations a
correctness dependency.

**Scope**

- optional advisory records from one device about another device's recent
  reachability
- use as dial hints only
- never authoritative for membership
- never fresher than valid self-published presence

**Acceptance**

- larger private networks gain additional resilience
- core correctness still works without observations

## Milestone Sequencing

### Milestone A

- Workstream 1: Design Freeze
- Workstream 2: Surface Cleanup

**Exit Criteria**

- old model fully identified and intentionally removed from the design

### Milestone B

- Workstream 3: DHT Membership And Presence
- Workstream 4: Startup Rebuild Logic

**Exit Criteria**

- DHT-only private-network recovery works without relying on local authority

### Milestone C

- Workstream 5: Presence Heartbeats And Expiry
- Workstream 6: Nostr Fallback

**Exit Criteria**

- private network remains durable through address changes and DHT issues

### Milestone D

- Workstream 7: Repair Flow
- Workstream 8: Hardening And Debug Surfaces

**Exit Criteria**

- catastrophic divergence is repairable
- the new model is observable and regression-tested

### Milestone E

- Workstream 9: Observations

**Exit Criteria**

- larger private networks gain resilience beyond direct self-publication

## Outstanding Questions

- Repair eligibility:
  exactly what trust material is sufficient for repair is intentionally
  deferred until Workstream 7.
- Post-v1 membership evolution:
  if serialized full-record membership updates become too restrictive, the
  next step is an op-based or CRDT-like model rather than silently stretching
  the v1 manifest.

## Test Matrix

### Core Cases

- 2-device private network, no LAN assumptions
- 3-device private network, no LAN assumptions
- one device restarts while others stay up
- all devices restart independently
- one device loses local cache entirely
- one device comes back after long offline period
- device multiaddr set changes mid-run
- sequential membership add/remove from different devices

### Failure Cases

- stale membership cache on one device
- stale presence record in DHT
- missing DHT data with Nostr fallback available
- malformed or invalidly signed membership
- malformed or invalidly signed presence
- device removed from membership but still publishing stale presence
- attempted concurrent independent membership edits are treated as out of
  scope for v1 and must not silently redefine the design

### Repair Cases

- device with partially valid trust material
- device with missing local cache but intact identity/device keys
- device that must not create a new private network during repair

## Out Of Scope For Initial Refactor

- KV-backed membership as the source of truth
- observation records as a correctness dependency
- LAN-local discovery as a required path
- preserving old identity-scoped discovery formats
