---
created: 2026-05-08
updated: 2026-05-08
---

# Sandbox Bridge Follow-Up

The completed sandbox bridge architecture is archived in
[`docs/work/past/2026/05/08-Sandbox-Bridge-Host-Owned-Metered-Services.md`](../past/2026/05/08-Sandbox-Bridge-Host-Owned-Metered-Services.md).

Keep this file for remaining bridge work that is not active branch planning.

## Smoke And Regression Coverage

- [ ] Add live sandbox smoke coverage proving an agent can list approved x402
  services through the guest-local `/bridge/metered-services/ws` route without
  any direct host URL.
- [ ] Add Hermes smoke coverage proving Hermes sees approved x402 services and
  completes one bridged service call through the guest-local route with no host
  callback URL.
- [ ] Keep live x402 spending tests behind explicit environment or build-tag
  guards before any real-USDC smoke lands in routine validation.
- [ ] Decide what live-sandbox smoke should gate the first real-USDC x402 run.
- [ ] Add a regression sweep that removed concrete host gateway aliases,
  loopback aliases, and callback-origin paths do not reappear in sandbox
  runtime config, manifests, prompts, or generated helper scripts.
- [ ] Prove host-owned bridge directionality in the sandbox smoke, not just in
  package-level or two-process integration tests.

## Hardening Decisions

- [ ] Decide whether bridge status should be persisted in sandbox state or
  remain lifecycle/log-only for now.
- [ ] Decide whether the host-opened bridge should use a daemon-issued
  ephemeral handshake before broader capability rollout.
- [ ] Decide whether Hermes x402 descriptors should move from prompt/tool
  context into generated `bridge.json`, `/shared/agent-manifest.json`, or both.
- [ ] Decide whether `pkg/sandbox/bridge/x402` should move to
  `pkg/sandbox/bridge/meteredservices` after another bridge capability lands.

## Guardrails To Preserve

- [ ] Keep sandbox bridge endpoints per capability; do not introduce a generic
  everything-over-one-WebSocket RPC tunnel.
- [ ] Keep Skylink out of local host/guest sandbox bridge calls.
- [ ] Keep agent chat on `/rpc/agents/{agent}/chat`; do not route chat through
  the metered-services bridge.
