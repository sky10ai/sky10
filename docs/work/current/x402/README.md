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
- 2026-04-27 — M1 (protocol core) landed in `pkg/x402/`. Wire
  types, registry + persisted store, manifest pinning, per-agent
  budget caps, http transport that performs the 402 round-trip
  with a Signer abstraction, and a Backend that ties the pieces
  into one delegate the comms handlers call. End-to-end test
  exercises the full path against a httptest x402 fake. Real
  OWS-backed signing follows; the M1 production wiring uses
  StubSigner so misconfiguration fails with a typed error rather
  than a panic.
- 2026-04-27 — Daemon wiring landed in `commands/serve_x402.go`.
  The x402 endpoint is mounted at `/comms/x402/ws` when the
  daemon starts; identity is resolved from the `agent` query
  parameter against the existing agent registry; budget defaults
  apply per-agent on first sight. Adapter translates between
  pkg/x402 native types and the comms wire shape. With this,
  sandbox-comms M2 and x402 plan M5 are both complete.
- 2026-04-27 — M2 (discovery and refresh) landed in
  `pkg/x402/discovery/`. Source interface, StaticSource with the
  builtin primitive set, Refresh orchestrator with diff
  classification, and an embedded overlay providing tier/hint
  metadata. Daemon seeds the registry on startup with the curated
  primitives (Deepgram, fal.ai, E2B, Browserbase) so agents have
  something concrete to approve. Live agentic.market HTTP source
  and periodic refresh ticker follow.
- 2026-04-27 — Settings → Services page landed
  (`web/src/pages/SettingsServices.tsx`). Renders the catalog from
  `x402.listServices` host RPC, with each service showing blurb,
  category, chain, tier, price, and an enable toggle backed by
  `x402.setEnabled`. The toggle drives a new user-level enable
  state on the Registry; Backend.Call falls back to user-level
  approval when no per-agent record exists. M3 (host RPC) and M6
  (Web UI) are partially done; remaining surface (approve/revoke
  per agent, budget panel, receipts log, CLI) follows.
- 2026-04-27 — Services page now shows a wallet-status banner that
  surfaces "OWS not installed" / "no wallet yet" so the toggle
  isn't misleading when calls would actually fail. Discovery now
  pulls live data from `https://api.agentic.market/v1/services` via
  `AgenticMarketSource`, with the StaticSource as a fallback for
  offline installs. A daemon goroutine runs `Refresh` hourly with
  ±10min jitter so the local catalog stays current.
- 2026-04-28 — OWS sign integration. `pkg/x402/protocol.go` now
  speaks the real x402 wire shape (`accepts`/PaymentRequirements,
  base64-JSON `X-PAYMENT` payloads, EIP-3009
  TransferWithAuthorization for USDC). New `OWSSigner` shells out
  to `ows sign message --typed-data --json` to produce the
  authorization signature; daemon picks OWSSigner when OWS is
  installed and StubSigner otherwise. Each successful charge logs
  `x402 charge agent_id=A service_id=S amount_usdc=X tx=H` so
  payment attribution is visible in the daemon log. Solana signing
  remains a follow-up — the OWSSigner refuses Solana requirements
  cleanly so agent routing can fall back rather than silently
  fail. There is no per-service "top up" model in x402: the user
  funds one wallet and the protocol pays per request.

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
