---
created: 2026-04-26
updated: 2026-05-07
model: claude-opus-4-7
---

# x402 Agent Integration

## Two surfaces

Agent runtimes consume x402 services through narrow runtime surfaces.
For Lima sandboxes, that surface must be carried over the existing
host-owned guest connection path. The guest must not dial the host
daemon directly via host gateway aliases or any other host callback
address; `user-v2` sandboxes intentionally expose guest services to the
host, not host services to the guest.

Host UI and CLI continue to use the `x402.*` JSON-RPC methods for
catalog management.

### 1. OpenClaw plugin (ship first)

`external/runtimebundles/openclaw/sky10-openclaw/` already exposes
sky10 to OpenClaw. The first shipped surface is a `sky10-x402`
helper that:

- queries approved x402 services through a host-owned sandbox comms
  bridge
- is written to `~/.openclaw/sky10-x402.mjs`
- supports `list`, `budget`, and `call` commands
- proxies calls through `x402.service_call`, so the daemon handles
  payment, receipts, budget checks, and wallet signing

Do not configure OpenClaw with a direct host gateway URL for
`/comms/x402/ws`. That violates the Lima security boundary. The host
must initiate the guest connection and broker x402 envelopes across that
existing channel.

Durable tool-call prompts include the approved services and the helper
commands. The agent never handles wallet state, x402 payment headers,
or x402 challenges. Native OpenClaw tool registration remains the next
step once the runtime tool API is stable enough to bind each service
directly.

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
  "name": "structured_data.search",
  "description": "Structured records from a paid API.\nCost: ~$0.05/call.\nPrefer browser/search for general queries; use this only when you need records that the service description explicitly advertises.",
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

This is intentional. A paid x402 API and OpenClaw browsing/search may
both be valid; the LLM reads the description, sees the price, sees the
hint, and decides. We do not hardcode vendor-specific routing choices.

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
