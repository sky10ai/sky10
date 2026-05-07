---
created: 2026-05-07
updated: 2026-05-07
---

# Sandbox Bridge Milestones

## Goal

Build the host-owned WebSocket bridge that lets sandboxed agents use
host-side capabilities without the guest dialing host callback addresses.

The first capability is metered services/x402. The bridge must preserve the
Lima `user-v2` boundary:

- host dials guest
- guest never dials host daemon RPC, host loopback, host gateway aliases, or
  host Unix sockets
- no Skylink for this path
- no generic RPC tunnel
- one endpoint per capability

Canonical first endpoint:

`/bridge/metered-services/ws`

## Working Model

```text
Host sky10
  dials guest sky10 over the existing host-owned forwarded guest endpoint
  and keeps /bridge/metered-services/ws connected

OpenClaw/helper inside the guest
  dials guest-local sky10 at /bridge/metered-services/ws

Guest sky10
  accepts local metered-service requests and forwards them over the
  host-opened bridge connection

Host sky10
  runs the real x402 backend, wallet adapter, budget checks, receipts,
  and upstream API call, then returns the result over the same socket
```

The shared path is the agent-facing capability path. The distinction between
the host-held upstream connection and guest-local agent callers must be made by
connection setup and daemon-owned state, not by teaching agents host URLs.

## Milestone 0: Settle Names And Boundaries

- [ ] Rename current `/comms/metered-services/ws` references to
  `/bridge/metered-services/ws`.
- [ ] Keep `pkg/x402` as the host-side payment/service engine.
- [ ] Keep `pkg/sandbox/comms/x402` as the x402 envelope/validation layer for
  now, or move it under `pkg/sandbox/bridge/meteredservices` if the package
  boundary gets in the way.
- [ ] Document that `pkg/sandbox/bridge` is transport plumbing only, not a
  capability mux and not host RPC.
- [ ] Remove stale `/comms/x402/ws` references from OpenClaw helper tests.

## Milestone 1: Reusable Bridge Transport

Create `pkg/sandbox/bridge` as small reusable WebSocket plumbing.

- [ ] Define a bridge frame format with:
  - `id`
  - `type`
  - `payload`
  - structured `error`
- [ ] Implement request/response correlation for concurrent calls.
- [ ] Implement handler dispatch for inbound requests on a single connection.
- [ ] Add max frame size handling.
- [ ] Add close/error propagation to pending calls.
- [ ] Add context deadline support for calls.
- [ ] Add focused tests for:
  - round trip
  - handler errors
  - context timeout
  - close while requests are pending
  - malformed frame rejection

Non-goal for this milestone:

- [ ] Do not multiplex unrelated capabilities over one WebSocket.
- [ ] Do not add arbitrary method names that map to daemon RPC.

## Milestone 2: Guest Bridge Endpoint

Guest sky10 owns `/bridge/metered-services/ws`.

- [ ] Accept guest-local agent/helper WebSocket connections on the endpoint.
- [ ] Accept or attach the host-opened upstream bridge connection on the same
  capability endpoint, with daemon-owned state deciding which connection is
  the host upstream.
- [ ] Store at most one active host upstream bridge per sandbox capability.
- [ ] Fail agent calls clearly when the host upstream is not connected.
- [ ] Reconnect-safe behavior: replacing a dead host upstream must not strand
  pending local calls forever.
- [ ] Do not expose a guest-to-host URL, token, or socket path to OpenClaw.

Open design item:

- [ ] Decide whether the host upstream is identified by WebSocket
  subprotocol/header, a daemon-issued ephemeral handshake, or lifecycle-only
  attachment. Avoid guest-readable static secrets pretending to authenticate
  the host.

## Milestone 3: Host Bridge Manager

Host sky10 dials the guest bridge endpoint after guest sky10 is ready.

- [ ] Add a sandbox bridge manager keyed by sandbox slug and capability.
- [ ] After `ready.guest.sky10`, dial the sandbox forwarded sky10 endpoint at
  `/bridge/metered-services/ws`.
- [ ] Keep the connection alive with reconnect/backoff.
- [ ] Close bridge connections when a sandbox stops or is removed.
- [ ] Surface bridge status in sandbox state or logs.
- [ ] Ensure this does not use Skylink.
- [ ] Ensure this does not use direct guest-to-host callbacks.

## Milestone 4: Metered Services Backend Over Bridge

Wire the first capability end to end.

- [ ] Guest-side backend implements the existing metered-services interface by
  forwarding:
  - `x402.list_services`
  - `x402.budget_status`
  - `x402.service_call`
- [ ] Host-side bridge handler receives those requests and calls the real
  host `pkg/x402` backend.
- [ ] Host stamps trusted sandbox/agent identity; ignore any payload-supplied
  identity.
- [ ] Host enforces service approval, budget, max price, receipt persistence,
  and wallet signing.
- [ ] Guest returns only upstream response data and receipt metadata to the
  agent.
- [ ] Agents never see x402 challenges, payment headers, wallet paths, or host
  endpoints.

## Milestone 5: OpenClaw Runtime Wiring

Make the existing helper use the guest-local bridge endpoint.

- [ ] Update the helper default endpoint to
  `ws://127.0.0.1:9101/bridge/metered-services/ws`.
- [ ] Keep helper commands:
  - `list`
  - `budget`
  - `call`
- [ ] Generate prompt context from settings-approved service listings.
- [ ] Keep routing generic: use service descriptions, hints, endpoints, and
  prices; do not hardcode vendor choices.
- [ ] Do not configure OpenClaw with any host callback URL.

## Milestone 6: Tests And Smoke

- [ ] Unit-test bridge transport in `pkg/sandbox/bridge`.
- [ ] Unit-test guest metered-services forwarding with a fake host bridge.
- [ ] Unit-test host bridge handler with a fake x402 backend.
- [ ] Integration-test guest local helper -> guest bridge endpoint -> host
  fake x402 backend.
- [ ] Regression-test that host gateway/callback origins are rejected.
- [ ] Regression-test that no repo code or docs introduce the removed concrete
  host gateway alias.
- [ ] Keep live x402 smoke tests behind env/build guards.
- [ ] Add a sandbox smoke path that proves the agent can list approved
  services without a direct host URL.

## Milestone 7: Documentation And Cleanup

- [ ] Update `docs/work/current/sandbox-comms/` once the bridge package owns
  the transport story.
- [ ] Update `docs/work/current/x402/agent-integration.md` with the final
  bridge flow.
- [ ] Decide whether to rename or merge `pkg/sandbox/comms` after one complete
  capability works.
- [ ] Document operational failure modes:
  - host bridge disconnected
  - budget exceeded
  - signer missing
  - service disabled
  - upstream payment rejected
- [ ] Keep `AGENTS.md` and `CLAUDE.md` explicit that guests must not call host
  services directly.
