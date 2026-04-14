---
created: 2026-04-13
updated: 2026-04-14
model: gpt-5.4
---

# Secrets V1

Built and finished a usable `secrets` layer on top of KV for owner-managed
private-network secrets such as API keys, tokens, DSNs, certs, and small
private artifacts.

This work turned `secrets` from a transport experiment into a real product
surface:

- device-scoped trust model instead of vague agent policy
- explicit sharing scopes: `current`, `trusted`, `explicit`
- trusted vs sandbox device classes
- CLI, RPC, and web UI at `/settings/secrets`
- multi-device reconciliation and deletion behavior
- process integration coverage for two- and three-device private networks

## Why

The underlying KV transport was already strong enough to move encrypted state
between devices, but it was not yet a usable secrets product.

The missing pieces were:

- a trust model that matched the real private-network design
- a user-facing way to store and inspect secrets without exposing raw KV
- correct semantics for new trusted devices, sandbox devices, and pinned
  recipients
- enough test coverage to trust multi-device behavior

This matters because `sky10` needs a practical place to keep API keys and
other operator-owned credentials, not just a low-level encrypted sync
primitive.

## What Shipped

### 1. Secrets became device-scoped, not agent-scoped

Secrets v1 now treats devices as the only real custody boundary.

- `trusted` devices can participate in implicit sharing
- `sandbox` devices are excluded from implicit sharing
- the previous agent policy knobs were left explicitly out of the v1 product
  boundary

That matters because the real blast-radius boundary today is the device, not a
soft per-agent allowlist.

### 2. Sharing semantics became explicit

Secrets now support three stable scopes:

- `current`: current device only
- `trusted`: all trusted devices in the manifest
- `explicit`: exact pinned recipient set

That matters because "all devices" was no longer correct once sandbox devices
existed.

### 3. Device trust classes were wired into identity metadata

Private-network devices now carry role metadata and secrets resolution
respects it.

That matters because new devices joining the same network are not all equally
trusted.

### 4. Trusted-scope reconciliation landed

Trusted-scope secrets now rewrap when private-network membership changes.

- new trusted device joins: trusted secrets expand
- trusted device removed: trusted secrets contract
- trusted device downgraded to sandbox: trusted secrets exclude it
- explicit and current secrets do not change automatically

That matters because trusted scope is dynamic membership, not a one-time
recipient snapshot.

### 5. Secrets got a real UI and normal user surfaces

Shipped:

- CLI under `sky10 secrets`
- RPC under `secrets.*`
- web UI at `/settings/secrets`

The web UI now supports:

- storing a secret from text or file
- choosing `current`, `trusted`, or `explicit`
- choosing recipient devices for explicit scope
- revealing and downloading values on demand
- rewrapping recipients
- deleting secrets

That matters because a secrets layer is not complete if it only exists as
internal storage code.

### 6. Internal storage was hidden from generic KV views

Secrets records now live under reserved internal prefixes such as
`_sys/secrets/...`, and generic KV browsing hides reserved internal keys by
default.

That matters as a UX boundary. It reduces accidental exposure of implementation
details without pretending to be a security boundary.

### 7. P2P secrets namespace bootstrap was fixed

An existing P2P private network could previously fail on first secrets use with
an error like:

`missing cached namespace key for "secrets"`

This was fixed by bootstrapping the `secrets` namespace key through the working
default KV namespace and caching it locally when the namespace is first used.

That matters because older private networks must be able to adopt secrets after
devices have already joined, not only during the original join flow.

### 8. First-class delete landed

Secrets now have a real delete path that matches KV naming:

- `secrets.delete`
- `sky10 secrets delete <id-or-name>`
- delete action in `/settings/secrets`

Delete removes the head and stored version records so the secret stops
appearing in `list` and `get` across peers after sync.

That matters because a secrets product without delete is incomplete and
operationally hostile.

## Test Coverage Added

Coverage now includes:

- store-level coverage for `current`, `trusted`, and `explicit`
- trusted-device join/reconcile behavior
- sandbox-device exclusion
- trusted-device removal and downgrade exclusion
- explicit-scope pinning across membership changes
- two-process and three-process integration coverage
- 8 KB KV payload sync coverage
- legacy private-network secrets namespace bootstrap regression coverage
- three-device trusted secret deletion convergence
- RPC coverage for public v1 boundaries and delete behavior

That matters because this feature is mostly about membership changes, sync,
restart, and recovery edges. If those paths are not exercised in process
tests, the security and reliability claims are weak.

## User-Facing Outcomes

- a user can store an API key or small secret artifact on one device
- trusted devices in the same private network can sync and decrypt it
- sandbox devices do not inherit trusted secrets implicitly
- explicit recipient sets stay pinned
- a new trusted device can inherit trusted secrets after join and
  reconciliation
- a user can manage secrets from the CLI, RPC, or `/settings/secrets`
- a user can delete a secret cleanly instead of manually editing KV internals

## Important Follow-On Work

Secrets v1 is usable, but not finished in every dimension.

Remaining follow-on work includes:

- authenticated agent/broker access instead of deferred soft policy metadata
- durable approval and audit workflows, likely built on mailbox
- optional garbage collection strategy for secret history beyond v1 delete
- better direct UI affordances around sync state and peer-specific failure
- any future split between owner devices and additional device classes beyond
  `trusted` and `sandbox`

The key point is that these are now follow-on product refinements, not blockers
for everyday private-network secret sync.
