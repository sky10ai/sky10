---
created: 2026-05-07
updated: 2026-05-07
---

# Sandbox Bridge Milestones

## Goal

Finish the host-owned WebSocket bridge for sandbox capabilities, starting
with metered services/x402, while reusing the existing `pkg/sandbox/bridge`,
`pkg/sandbox/bridge/x402`, and `pkg/x402` work.

The bridge must preserve the Lima `user-v2` boundary:

- host dials guest
- guest does not dial host RPC, host loopback, host gateway aliases, or host
  Unix sockets
- no Skylink in this local host/guest path
- no generic RPC tunnel
- one endpoint per capability

Canonical first endpoint:

`/bridge/metered-services/ws`

## Milestone 0: Consolidate Docs And Names

- [x] Move the old `docs/work/current/sandbox-comms/` material under
  `docs/work/current/sandbox-bridge/`.
- [x] Make `sandbox-bridge` the canonical current work directory.
- [x] Preserve the existing handler discipline documentation.
- [x] Mark `pkg/sandbox/bridge` as existing bridge internals, not discarded
  work.
- [x] Mark `pkg/sandbox/bridge/x402` as existing metered-services envelope
  validation, not discarded work.
- [x] Update remaining x402 docs that still link to `sandbox-comms`.
- [x] Update code comments that point at `docs/work/current/sandbox-comms/`.
- [x] Record Hermes as a required runtime target for the same bridge
  contract.
- [x] Rename public route references from `/comms/metered-services/ws` to
  `/bridge/metered-services/ws` once code changes begin.
- [x] Move the OpenClaw helper off the stale `/comms/x402/ws` route.

## Milestone 1: Host-Owned Bridge Connection

Build only the missing host/guest connection management. Do not replace the
existing bridge envelope layer.

- [x] Add a host-side sandbox bridge manager keyed by sandbox slug and
  capability.
- [x] Host dials the guest forwarded sky10 endpoint for
  `/bridge/metered-services/ws` after guest sky10 is ready.
- [x] Host keeps the socket alive with reconnect/backoff.
- [x] Host closes the socket when the sandbox stops or is removed.
- [x] Guest records the host-opened socket as the active upstream for
  metered services.
- [x] Guest replaces dead upstream connections without stranding pending
  requests.
- [x] Surface bridge status in sandbox logs or state.

Acceptance:

- [x] A test can create a host-owned bridge socket into a guest test server.
- [x] Closing the socket fails pending requests with a structured error.
- [x] No guest-to-host URL is configured or exposed.

## Milestone 2: Guest Forwarding Backend

Guest sky10 should expose the same agent-facing metered-services endpoint,
but its backend forwards over the host-owned bridge.

- [x] Implement a guest-side `pkg/sandbox/bridge/x402.Backend`.
- [x] Forward `ListServices` over the bridge.
- [x] Forward `BudgetStatus` over the bridge.
- [x] Forward `Call` over the bridge.
- [x] Return a clear `host_bridge_disconnected` error when no upstream is
  connected.
- [x] Preserve request context deadlines.
- [x] Preserve existing payload validation before forwarding.

Acceptance:

- [x] Unit test: guest backend forwards list/budget/call to a fake host
  bridge.
- [x] Unit test: disconnected bridge returns a stable structured error.

## Milestone 3: Host Metered-Services Handler

Host sky10 should receive forwarded metered-services requests and delegate to
the real x402 backend.

- [x] Add a host bridge handler for metered-services requests.
- [x] Stamp trusted sandbox/agent identity on host.
- [x] Ignore any payload-supplied identity.
- [x] Call the existing host `pkg/x402` backend through the current adapter.
- [x] Enforce approvals, user-enabled services, budget, max price, signer
  availability, receipt persistence, and upstream payment handling.
- [x] Return only response body/headers/status and receipt metadata.

Acceptance:

- [x] Unit test: payload identity cannot override host-stamped identity.
- [x] Unit test: host handler calls fake x402 backend with the expected
  agent ID.
- [x] Unit test: x402 backend errors return structured bridge errors.

## Milestone 4: Route And Runtime Wiring

Settle the public endpoint and runtime defaults.

- [x] Rename endpoint constant to `/bridge/metered-services/ws`.
- [x] Keep or shim the old `/comms/metered-services/ws` only if needed for a
  short compatibility window.
- [x] Update OpenClaw helper default to guest-local
  `ws://127.0.0.1:9101/bridge/metered-services/ws`.
- [x] Update helper tests away from `/comms/x402/ws`.
- [x] Update Hermes bridge prompt/tool context so Hermes sees the same
  approved x402 service descriptors as OpenClaw.
- [x] Give Hermes a guest-local caller for `list`, `budget`, and `call` that
  uses `/bridge/metered-services/ws`.
- [x] Do not configure Hermes with any host callback URL.
- [x] Keep helper commands:
  - `list`
  - `budget`
  - `call`
- [x] Keep routing generic through service descriptions, hints, endpoints,
  and price metadata.
- [x] Do not hardcode vendor-specific routing choices.
- [x] Do not configure OpenClaw with any host callback URL.

Acceptance:

- [x] OpenClaw prompt context can list approved x402 services from the
  guest-local endpoint.
- [x] Hermes prompt/tool context can list approved x402 services from the
  guest-local endpoint.
- [x] Runtime adapters never write a host callback URL into config, manifest,
  or prompt text.

## Milestone 5: End-To-End Tests

- [x] Unit-test existing `pkg/sandbox/bridge` still passes.
- [x] Unit-test existing `pkg/sandbox/bridge/x402` still passes.
- [x] Add integration test: guest-local WebSocket client ->
  `/bridge/metered-services/ws` -> host fake x402 backend.
- [ ] Add sandbox-style smoke: agent can list approved services without any
  direct host URL.
- [ ] Add Hermes smoke: Hermes sees approved x402 services and calls one
  through the guest-local bridge without any direct host URL.
- [ ] Keep live x402 smoke tests behind env/build guards.
- [x] Regression-test that host gateway/callback origins are rejected.
- [ ] Regression-test that no removed concrete host gateway alias reappears.

Acceptance:

- [x] Focused Go tests pass for `pkg/sandbox/bridge`,
  `pkg/sandbox/bridge/x402`, `pkg/x402`, and the new bridge wiring.
- [ ] The sandbox smoke proves host-owned bridge directionality.

## Milestone 6: Cleanup And Package Rename

- [x] Move the old `pkg/sandbox/comms/*` package tree under
  `pkg/sandbox/bridge/*`.
- [x] Keep frame-level `bridge.Option`/`bridge.Handler` names and rename the
  moved endpoint constructor option and envelope handler types to avoid API
  collisions.
- [ ] Decide whether `pkg/sandbox/bridge/x402` should move to
  `pkg/sandbox/bridge/meteredservices`.
- [x] Update active docs and comments after the package move.
- [ ] Remove the legacy `/comms/metered-services/ws` compatibility route when
  guest helpers no longer need it.
- [x] Update `AGENTS.md` and `CLAUDE.md` if the final rule needs sharper
  wording.
