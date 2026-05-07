---
created: 2026-04-27
updated: 2026-05-07
---

# Sandbox Bridge Implementation Plan

This plan folds the older sandbox-comms work into the sandbox-bridge
track. The existing `pkg/sandbox/comms` and `pkg/sandbox/comms/x402`
packages are retained as completed bridge internals for now.

Use [milestones.md](milestones.md) as the active checklist.

## Done

- [x] Shared WebSocket/envelope plumbing exists in `pkg/sandbox/comms/`.
- [x] Handler discipline exists in
  [handler-discipline.md](handler-discipline.md).
- [x] x402/metered-services envelope handlers exist in
  `pkg/sandbox/comms/x402/`.
- [x] Host x402 domain backend exists in `pkg/x402/`.
- [x] Daemon wiring exists for host-local testing in `commands/serve_x402.go`.
- [x] OpenClaw helper work exists for listing services, checking budget, and
  calling a metered service over a WebSocket endpoint.
- [x] Hermes bridge work exists for registering a Hermes runtime with sky10
  and reading runtime tools from `bridge.json` or
  `/shared/agent-manifest.json`.
- [x] Direct guest-to-host callback attempts were reverted and covered by
  regression tests.

## Remaining

- [ ] Rename the public capability route from `/comms/metered-services/ws`
  to `/bridge/metered-services/ws`.
- [ ] Add host-owned bridge connection management for sandbox records.
- [ ] Add guest-side forwarding backend that uses the host-opened socket.
- [ ] Wire guest-local metered-services calls through that forwarding backend.
- [ ] Point OpenClaw helper defaults at the guest-local bridge route.
- [ ] Feed the same approved x402 service descriptors into Hermes via
  `bridge.json`, `/shared/agent-manifest.json`, or another guest-local Hermes
  adapter surface.
- [ ] Add end-to-end tests for guest-local helper -> bridge -> host x402
  backend.
- [ ] Update x402 docs and code comments once the route and package names are
  settled.

## Package Direction

Current package names:

- `pkg/sandbox/comms` - bridge envelope plumbing
- `pkg/sandbox/comms/x402` - metered-services envelope handlers
- `pkg/x402` - x402 business logic and payment engine

Potential later package names:

- `pkg/sandbox/bridge`
- `pkg/sandbox/bridge/meteredservices`

Do not rename packages before the host/guest bridge works. The package
boundary is easier to judge after one complete capability is live.
