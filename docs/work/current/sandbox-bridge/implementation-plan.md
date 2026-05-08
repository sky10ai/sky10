---
created: 2026-04-27
updated: 2026-05-07
---

# Sandbox Bridge Implementation Plan

This plan folds the older sandbox-comms work into the sandbox-bridge
track. The existing `pkg/sandbox/bridge` and `pkg/sandbox/bridge/x402`
packages are retained as completed bridge internals for now.

Use [milestones.md](milestones.md) as the active checklist.

## Done

- [x] Shared WebSocket/envelope plumbing exists in `pkg/sandbox/bridge/`.
- [x] Handler discipline exists in
  [handler-discipline.md](handler-discipline.md).
- [x] x402/metered-services envelope handlers exist in
  `pkg/sandbox/bridge/x402/`.
- [x] Host x402 domain backend exists in `pkg/x402/`.
- [x] Daemon wiring exists for host-local testing in `commands/serve_x402.go`.
- [x] OpenClaw helper work exists for listing services, checking budget, and
  calling a metered service over a WebSocket endpoint.
- [x] Hermes bridge work exists for registering a Hermes runtime with sky10
  and reading runtime tools from `bridge.json` or
  `/shared/agent-manifest.json`.
- [x] Direct guest-to-host callback attempts were reverted and covered by
  regression tests.
- [x] Generic host-owned WebSocket request/response transport exists in
  `pkg/sandbox/bridge`.
- [x] Metered-services canonical route is `/bridge/metered-services/ws`,
  with `/comms/metered-services/ws` left as a compatibility shim.
- [x] Host bridge connection management is wired into sandbox ready and
  reconnect flows for OpenClaw and Hermes templates.
- [x] Guest-local metered-services calls forward over the host-opened bridge
  socket and return `host_bridge_disconnected` when no upstream is attached.
- [x] Host metered-services bridge handler calls the existing `pkg/x402`
  backend through the current daemon adapter.
- [x] OpenClaw defaults to the guest-local bridge route.
- [x] Hermes installs a guest-local `sky10-x402` helper and injects approved
  x402 service context into tool-call prompts.

## Remaining

- [ ] Add end-to-end tests for guest-local helper -> bridge -> host x402
  backend inside a live sandbox.
- [ ] Add Hermes live smoke coverage for service listing and one bridged call
  with no direct host callback URL.
- [ ] Keep live x402 spending tests behind explicit env/build guards.
- [ ] Decide whether bridge status should be persisted in sandbox state, not
  just logs.
- [ ] Decide whether the host-opened bridge should get a daemon-issued
  ephemeral handshake before broader capability rollout.

## Package Direction

Current package names:

- `pkg/sandbox/bridge` - bridge envelope plumbing
- `pkg/sandbox/bridge/x402` - metered-services envelope handlers
- `pkg/x402` - x402 business logic and payment engine

Potential later package names:

- `pkg/sandbox/bridge`
- `pkg/sandbox/bridge/meteredservices`

Do not rename the x402 subpackage again until more capabilities exist. The
capability naming boundary is easier to judge after the second endpoint lands.
