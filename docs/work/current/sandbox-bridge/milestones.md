---
created: 2026-05-07
updated: 2026-05-07
---

# Sandbox Bridge Milestones

## Goal

Finish the host-owned WebSocket bridge for sandbox capabilities, starting
with metered services/x402, while reusing the existing `pkg/sandbox/comms`,
`pkg/sandbox/comms/x402`, and `pkg/x402` work.

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
- [x] Mark `pkg/sandbox/comms` as existing bridge internals, not discarded
  work.
- [x] Mark `pkg/sandbox/comms/x402` as existing metered-services envelope
  validation, not discarded work.
- [x] Update remaining x402 docs that still link to `sandbox-comms`.
- [x] Update code comments that point at `docs/work/current/sandbox-comms/`.
- [x] Record Hermes as a required runtime target for the same bridge
  contract.
- [ ] Rename public route references from `/comms/metered-services/ws` to
  `/bridge/metered-services/ws` once code changes begin.
- [ ] Move the OpenClaw helper off the stale `/comms/x402/ws` route.

## Milestone 1: Host-Owned Bridge Connection

Build only the missing host/guest connection management. Do not replace the
existing comms envelope layer.

- [ ] Add a host-side sandbox bridge manager keyed by sandbox slug and
  capability.
- [ ] Host dials the guest forwarded sky10 endpoint for
  `/bridge/metered-services/ws` after guest sky10 is ready.
- [ ] Host keeps the socket alive with reconnect/backoff.
- [ ] Host closes the socket when the sandbox stops or is removed.
- [ ] Guest records the host-opened socket as the active upstream for
  metered services.
- [ ] Guest replaces dead upstream connections without stranding pending
  requests.
- [ ] Surface bridge status in sandbox logs or state.

Acceptance:

- [ ] A test can create a host-owned bridge socket into a guest test server.
- [ ] Closing the socket fails pending requests with a structured error.
- [ ] No guest-to-host URL is configured or exposed.

## Milestone 2: Guest Forwarding Backend

Guest sky10 should expose the same agent-facing metered-services endpoint,
but its backend forwards over the host-owned bridge.

- [ ] Implement a guest-side `pkg/sandbox/comms/x402.Backend`.
- [ ] Forward `ListServices` over the bridge.
- [ ] Forward `BudgetStatus` over the bridge.
- [ ] Forward `Call` over the bridge.
- [ ] Return a clear `host_bridge_disconnected` error when no upstream is
  connected.
- [ ] Preserve request context deadlines.
- [ ] Preserve existing payload validation before forwarding.

Acceptance:

- [ ] Unit test: guest backend forwards list/budget/call to a fake host
  bridge.
- [ ] Unit test: disconnected bridge returns a stable structured error.

## Milestone 3: Host Metered-Services Handler

Host sky10 should receive forwarded metered-services requests and delegate to
the real x402 backend.

- [ ] Add a host bridge handler for metered-services requests.
- [ ] Stamp trusted sandbox/agent identity on host.
- [ ] Ignore any payload-supplied identity.
- [ ] Call the existing host `pkg/x402` backend through the current adapter.
- [ ] Enforce approvals, user-enabled services, budget, max price, signer
  availability, receipt persistence, and upstream payment handling.
- [ ] Return only response body/headers/status and receipt metadata.

Acceptance:

- [ ] Unit test: payload identity cannot override host-stamped identity.
- [ ] Unit test: host handler calls fake x402 backend with the expected
  agent ID.
- [ ] Unit test: x402 backend errors return structured bridge errors.

## Milestone 4: Route And Runtime Wiring

Settle the public endpoint and runtime defaults.

- [ ] Rename endpoint constant to `/bridge/metered-services/ws`.
- [ ] Keep or shim the old `/comms/metered-services/ws` only if needed for a
  short compatibility window.
- [ ] Update OpenClaw helper default to guest-local
  `ws://127.0.0.1:9101/bridge/metered-services/ws`.
- [ ] Update helper tests away from `/comms/x402/ws`.
- [ ] Update Hermes bridge config or `/shared/agent-manifest.json`
  generation so Hermes sees the same approved x402 service descriptors as
  OpenClaw.
- [ ] Give Hermes a guest-local caller for `list`, `budget`, and `call` that
  uses `/bridge/metered-services/ws`.
- [ ] Do not configure Hermes with any host callback URL.
- [ ] Keep helper commands:
  - `list`
  - `budget`
  - `call`
- [ ] Keep routing generic through service descriptions, hints, endpoints,
  and price metadata.
- [ ] Do not hardcode vendor-specific routing choices.
- [ ] Do not configure OpenClaw with any host callback URL.

Acceptance:

- [ ] OpenClaw prompt context can list approved x402 services from the
  guest-local endpoint.
- [ ] Hermes tool/manifest context can list approved x402 services from the
  guest-local endpoint.
- [ ] Runtime adapters never write a host callback URL into config, manifest,
  or prompt text.

## Milestone 5: End-To-End Tests

- [ ] Unit-test existing `pkg/sandbox/comms` still passes.
- [ ] Unit-test existing `pkg/sandbox/comms/x402` still passes.
- [ ] Add integration test: guest-local WebSocket client ->
  `/bridge/metered-services/ws` -> host fake x402 backend.
- [ ] Add sandbox-style smoke: agent can list approved services without any
  direct host URL.
- [ ] Add Hermes smoke: Hermes sees approved x402 services and calls one
  through the guest-local bridge without any direct host URL.
- [ ] Keep live x402 smoke tests behind env/build guards.
- [ ] Regression-test that host gateway/callback origins are rejected.
- [ ] Regression-test that no removed concrete host gateway alias reappears.

Acceptance:

- [ ] Focused Go tests pass for `pkg/sandbox/comms`,
  `pkg/sandbox/comms/x402`, `pkg/x402`, and the new bridge wiring.
- [ ] The sandbox smoke proves host-owned bridge directionality.

## Milestone 6: Cleanup And Possible Rename

Only after metered services works end to end:

- [ ] Decide whether `pkg/sandbox/comms` should be renamed to
  `pkg/sandbox/bridge`.
- [ ] Decide whether `pkg/sandbox/comms/x402` should move to
  `pkg/sandbox/bridge/meteredservices`.
- [ ] Update all docs and comments after that decision.
- [ ] Remove any compatibility route if one was temporarily added.
- [ ] Update `AGENTS.md` and `CLAUDE.md` if the final rule needs sharper
  wording.

Do not do this cleanup before the first capability works. A premature rename
will hide the actual missing bridge work.
