---
created: 2026-04-26
updated: 2026-04-26
model: claude-opus-4-7
---

# x402 Agent Integration

## Two surfaces

Agent runtimes consume x402 services through two parallel paths. Both
ultimately call `x402.serviceCall` on the daemon over loopback RPC.

### 1. OpenClaw plugin (ship first)

`external/runtimebundles/openclaw/sky10-openclaw/` already exposes
sky10 to OpenClaw. We add an `x402-tools.js` module that:

- queries `x402.serviceList` for approved services
- registers each as an OpenClaw tool with name, description, JSON schema
- handler proxies to `x402.serviceCall` and maps typed errors

OpenClaw sees x402 services as ordinary tools. No payment knowledge
in the agent's prompt or runtime.

### 2. Standalone MCP server (universal hookup)

`cmd/sky10-x402-mcp` exposes the same registry as MCP tools so any
MCP-aware runtime — Hermes, Codex, future ones — picks them up with no
per-runtime code. OpenClaw eventually adopts MCP too; the plugin path
remains for sky10-specific integrations beyond x402.

## Tool description format

Each service is rendered into a tool description that includes price,
capabilities, and a routing hint. The LLM does the routing because the
description tells it how.

```json
{
  "name": "tripadvisor.search",
  "description": "Travel content, hotel/restaurant reviews, and ratings.\nCost: ~$0.05/call.\nPrefer the browser tool for general queries; use this only when you need structured ratings or verified IDs.",
  "input_schema": { "...": "..." },
  "metadata": {
    "tier": "convenience",
    "category": "data",
    "price_usdc_per_call": "0.05",
    "approval_required": true
  }
}
```

The `description` field carries human-readable price and a "prefer X
first" hint when applicable. `metadata` carries machine-readable
fields the runtime can use to filter at registration time.

## When to use a paid service vs do it locally

Default routing rubric, baked into the system prompt of any
sky10-aware agent runtime:

```
ROUTING POLICY FOR x402 SERVICES

1. Free local tools first. Try browser, web-search, file-ops, shell.
2. Escalate to a paid x402 service only when local tools cannot do
   the job:
   - audio I/O (Deepgram for STT)
   - GPU inference (Hyperbolic for custom models)
   - structured market data (Messari, Allium)
   - residential proxies / anti-bot (Browserbase, Anchor Browser)
   - captcha solving (2Captcha)
   - sandboxed code exec (E2B)
3. For information retrieval, prefer the browser unless the tool
   description explicitly says it beats browse-and-summarize.
4. If a call would exceed the per-call cap, ask the user; do not
   silently fail and do not silently spend.
```

The point is structural: the LLM picks. The daemon shapes that pick
through:

- which services it exposes (only approved ones)
- what their descriptions say (price + hint)
- what budget pressure it applies (`price_quote_too_high` errors when
  budget is tight)

This is intentional. Tripadvisor on x402 vs OpenClaw browsing
tripadvisor.com directly may both be valid; the LLM reads the
description, sees the price, sees the hint, and decides. We do not
hardcode the choice in Go.

## Tier defaults

| Tier | Default state | Rationale |
|---|---|---|
| `primitive` | ON for opted-in starter set | OpenClaw genuinely cannot DIY: audio, GPU, market data, residential IPs, premium LLMs |
| `convenience` | OFF | Browser+search likely matches or beats them; user opts in if they have a specific reason |

Even primitives with `default_on=true` in the overlay still require
the user to fund the x402 subwallet before any call lands.

## Telemetry feedback

Every service call records `was_browser_attempted_first` (best-effort
from the agent's recent tool-call log). Aggregating this over time
tells us which paid services genuinely beat local tools, so the
overlay defaults can move from intuition to data.

## Error handling for callers

Agents and CLI users see a small typed error vocabulary. Routing
should treat each one the same way regardless of caller:

| Error | Caller should |
|---|---|
| `service_not_approved` | surface to user as an approval prompt, do not retry |
| `service_removed` | drop the tool from this turn's tool list, retry without it |
| `price_quote_too_high` | fall back to a free local tool, or prompt the user to raise the cap |
| `budget_exceeded` | fall back to a free local tool; surface "out of budget today" |
| `insufficient_funds` | prompt user to refill the x402 subwallet |
| `manifest_diverged` | prompt user to re-approve; treat service as unavailable until then |
| `payment_failed` | retry once with backoff; on second failure, fall back |
