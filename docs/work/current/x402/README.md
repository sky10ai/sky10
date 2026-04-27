---
created: 2026-04-26
updated: 2026-04-26
model: claude-opus-4-7
---

# x402 Service Integration

## Goal

Make every service listed at <https://agentic.market> usable from sky10
agents and human users out of the box, without per-service adapters and
without exposing the wallet to agent processes. This is achieved by
implementing the x402 payment protocol once at the daemon's HTTP
transport layer and exposing a single registry to agent runtimes
(OpenClaw today, MCP-compatible runtimes in general).

## Why now

- agentic.market lists 573+ x402-enabled services across LLM inference,
  data, search, media generation, comms, and trading. They share one
  protocol (x402), one auth model (SIWE for EVM, SIWS for Solana), and
  one currency (USDC on Base/Solana primary).
- sky10 already ships wallet infrastructure (`pkg/wallet`) with Solana
  USDC transfers via OWS. The marginal cost to support all 573
  services is one transport implementation, not 573 adapters.
- OpenClaw and other agent runtimes already plug into the daemon; they
  need a tool surface that lets the LLM choose paid services
  intelligently versus doing the work locally with browser/web-search
  tools.

## Non-goals

- Per-service typed Go clients. Out of scope for first cut; add later
  driven by usage signal.
- Replacing existing API-key flows where users already have one.
- Becoming a payment processor for non-x402 protocols.

## Scope

| Area | In scope |
|---|---|
| Protocol | x402 over HTTPS; SIWE + SIWS signing; Base + Solana USDC |
| Discovery | agentic.market `/v1/services` + per-service `/.well-known/x402.json` + user-added URLs |
| Catalog freshness | Hourly refresh, change classification, manifest pinning, repo-curated overlay |
| Agent surface | x402 envelope types on the [sandbox comms](../sandbox-comms/) |
| Human surface | Web UI services browser + `sky10 market` CLI |
| Safety | Dedicated x402 subwallet, daily/per-call/per-service budget caps, receipt log |

## Status

- 2026-04-26 — plan drafted; awaiting answers to the open questions
  below before code lands.
- 2026-04-26 — refresh cadence locked: hourly default with ±10min
  jitter, configurable up to 4h.
- 2026-04-26 — added [threat model](threat-model.md). First cut bounds
  damage via budget caps; quality / reputation detection deferred to
  M9.
- 2026-04-26 — default-on primitives narrowed to capabilities that
  cannot be substituted locally: Deepgram, fal.ai, E2B, Browserbase.
  LLM and search primitives default OFF (most users have direct API
  keys).
- 2026-04-26 — agent surface rebased onto per-intent sandbox comms
  endpoints under `pkg/sandbox/comms/`. x402 ships as a single
  websocket endpoint `/comms/x402/ws` with its own narrow handler
  package, not as envelopes on a unified bus. OpenClaw plugin and
  MCP server milestones removed from this plan. See
  [sandbox-comms](../sandbox-comms/) for the comms architecture and
  [sandbox-comms/implementation-plan.md](../sandbox-comms/implementation-plan.md)
  for the M2 x402 capability milestone where the handlers land.
- 2026-04-27 — narrowed branch scope: only x402 ships under
  `pkg/sandbox/comms/` in this branch. Wallet and messengers comms
  are future work; secrets stay out of comms entirely (VM env-var
  mount via `pkg/sandbox/openclaw_env.go`).

## Documents

- [Architecture](architecture.md) — packages, components, trust model
- [Auto-update](auto-update.md) — catalog refresh and change handling
- [Agent integration](agent-integration.md) — OpenClaw, MCP, when-to-use routing
- [Wallet and budget](wallet-and-budget.md) — subwallet, caps, receipts
- [Threat model](threat-model.md) — malicious-service threats, what is mitigated, what is deferred
- [Implementation plan](implementation-plan.md) — milestones in dependency order

## Open questions

1. **Refresh cadence** — hourly OK, or slower (4h / daily) / manual-only?
2. **Default-on primitives** — pick a starter set (Anthropic, OpenAI,
   Perplexity, Exa, Deepgram, fal.ai, E2B, Browserbase) or default
   everything OFF and force opt-in?
3. **Subwallet UX** — auto-derive on first approval, or require an
   explicit "fund x402 wallet" action by the user?
4. **MCP server** — same milestone or follow-up?
5. **Web/CLI scope** — full UI in the first PR, or land daemon + plugin
   first and add Web/CLI in a second?
6. **Networks** — Base + Solana day one, or Base-only first?
