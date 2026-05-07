---
created: 2026-04-26
updated: 2026-05-06
model: claude-opus-4-7
---

# x402 Implementation Plan

The protocol core, discovery, host RPC, wallet preflight, sandbox
comms endpoint, Web UI, and SIWX integration all landed on the
`integrate-market-services` branch. See
[`docs/work/past/2026/05/06-X402-Market-Services-Integration.md`](../../../work/past/2026/05/06-X402-Market-Services-Integration.md)
for the archived slice — it documents the wire shape, OWS-backed
EVM + Solana signing paths, retry-header compatibility, live
verification matrix, and SIWX flow. The milestones below are what
remains.

## M7 — telemetry and overlay tuning — *deferred*

Receipt records would carry `was_browser_attempted_first` from the
agent's tool-call log; an aggregate dashboard would surface "paid
services that beat browse-it-yourself" so we can iterate
`overlay.json` defaults from real signal. Blocked on M10 — without
agent traffic flowing through the comms endpoint, there is no signal
to aggregate.

## M8 — quality and reputation — *deferred*

See [`threat-model.md`](threat-model.md). Outcome scoring,
auto-quarantine, volume anomaly detection. Stays out of scope until
we have enough live agent traffic to learn from. Sequenced after M7
because the same telemetry feeds both.

## M10 — OpenClaw plugin — *deferred*

Agents in the VM call paid services through the comms endpoint at
`/comms/metered-services/ws`. Closes the loop from "user funds wallet" to
"agent uses paid service" with the safety story (per-agent caps,
audit trail) the sandbox-comms architecture provides. Not started.

## Out of scope

These were considered and explicitly skipped; they are not deferred
work, just non-goals for this product cut:

- `sky10 market` CLI. The host RPC covers every consumer we have.
- Per-(agent, service) approval UI. Today every approved agent
  shares the user-level enable; tightening this is a known safety
  hole tracked separately.
- Auto top-up. Deposit-style services require an explicit user
  action to fund credits — Backend doesn't quietly drain on
  insufficient-balance 402s.
- Per-service typed Go clients (`pkg/x402/wrappers/...`). Driven
  later by usage signal if at all.
- Non-x402 payment protocols (MPP, etc.).
- Replacing existing API-key flows where users already have
  credentials.
- A standalone MCP server for x402. Agents go through comms.
