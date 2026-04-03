---
created: 2026-04-03
model: claude-opus-4-6
---

# Agent Protocol Design

## Overview

Design for an open agent economy built on sky10. Agents discover each other,
communicate peer-to-peer, pay for services, and build reputation — with no
central intermediary, no platform, no gatekeeper.

## OpenClaw Integration (starting point)

OpenClaw (or any agent runtime) interfaces with sky10 through the daemon's
existing HTTP RPC at `localhost:9101` or Unix socket. The daemon becomes the
local sidecar for any agent:

- **Storage**: `skyfs.put` / `skyfs.get` / `skyfs.list` — encrypted,
  cross-device file storage
- **Agent memory**: `skykv.set` / `skykv.get` — encrypted, cross-device KV
  (namespaced per agent: `openclaw/memory/...`)
- **Device awareness**: `identity.show` / `identity.devices` — agent knows
  which device it's on and what other devices exist
- **Cross-device calls**: `skylink.call` — agent on device A can trigger
  actions on device B over the P2P mesh

Installation flow: install `sky10` binary, detect running daemon (or prompt
setup through Cirrus), verify connectivity with `skyfs.ping`.

## Agent Registration

Agents connect via WebSocket and declare themselves to the local daemon:

```json
{
  "method": "agent.register",
  "params": {
    "name": "openclaw",
    "capabilities": ["web-search", "email", "calendar"],
    "methods": {
      "search": {
        "description": "Search the web",
        "params": {"query": "string"}
      },
      "sendEmail": {
        "description": "Send an email",
        "params": {"to": "string", "body": "string"}
      }
    }
  }
}
```

The connection becomes bidirectional — the daemon dispatches incoming calls to
the agent as JSON-RPC requests. When the agent disconnects, its registration
is removed (ephemeral, in-memory).

## Agent-to-Agent Communication (Own Agents)

For agents across your own devices, the daemon mediates everything:

- **Local agent**: dispatch directly over its WebSocket connection
- **Remote (own device)**: route through skylink P2P
- **Async/offline**: KV-based mailbox at `agents/<device_id>/<agent>/inbox/`

## Open Agent Economy (Stranger Agents)

### The Three Problems

When agents don't know each other:

1. **Identity** — how does an agent prove who it is?
2. **Trust** — how do you know a stranger agent will do what it claims?
3. **Payment** — how do you compensate an agent without trusting it?

### Identity

Every sky10 device has an Ed25519 keypair and a deterministic address. An
agent inherits its device's identity or generates its own keypair. No
accounts, no registration server. Just keys.

```
sky10://q7m2k9x4f8...   ← this IS the agent's identity
```

### Discovery

skylink already runs libp2p with a DHT. Agents publish capability records:

```json
{
  "agent": "sky10://q7m2k9x4f8",
  "name": "DeepResearcher",
  "description": "Thorough web research with source verification",
  "capabilities": ["web-research", "summarization", "fact-checking"],
  "methods": {
    "research": {
      "description": "Research a topic, return findings with sources",
      "params": {"query": "string", "depth": "string"},
      "price": {"amount": 500, "unit": "sats"}
    }
  },
  "signature": "ed25519:<sig>"
}
```

Discovery is a query: "find all agents advertising `web-research`." No central
registry. No app store gatekeeper. Any agent can publish, any agent can query.
The signature proves the listing is authentic.

### Communication Protocol

Direct P2P via skylink, encrypted, no intermediary. Six message types:

```
call              → "I want you to do this"
payment_required  → "it costs this much"
payment_proof     → "here's a signed transaction you can submit"
result            → "here's the work"
receipt           → co-signed proof of completed transaction
error             → something went wrong
```

### Payment Flow (P2P, not HTTP)

Caller signs a transaction locally, hands raw signed bytes to provider over
skylink. Provider submits to chain whenever they want (like a signed check).

```
Caller                              Provider
  │                                     │
  │──── call ──────────────────────────►│
  │◄──── payment_required ─────────────│
  │                                     │
  │  [wallet signs locally]             │
  │                                     │
  │──── payment_proof ────────────────►│
  │                                     │
  │              [provider verifies sig, does work]
  │                                     │
  │◄──── result + receipt ─────────────│
  │                                     │
  │  [counter-sign receipt]             │
  │                                     │
  │              ... later ...          │
  │                                     │
  │              [provider submits tx to chain, gets paid]
```

No HTTP, no servers. The signed transaction is just bytes in a P2P message.
Settlement is async — providers can batch-settle many payments at once.

### Reputation

After each completed transaction, both parties co-sign a receipt:

```json
{
  "tx_hash": "0xabc...",
  "caller": "sky10://abc...",
  "provider": "sky10://q7m...",
  "method": "research",
  "amount": "1000000",
  "caller_rating": 5,
  "provider_rating": 4,
  "caller_signature": "...",
  "provider_signature": "..."
}
```

Both signatures make it unforgeable. Receipts are published for reputation
tracking. An agent with 10,000 signed successful completions is trustworthy.
A brand new agent with zero history — your agent might require smaller amounts
or refuse entirely.

## The `agent.*` RPC Namespace

```
agent.register      declare capabilities, hold connection open
agent.deregister    explicit disconnect (also on connection drop)
agent.publish       advertise to the network
agent.discover      query by capability, name, or device
agent.call          call a method on any agent (local or remote)
agent.pay           send payment proof for a pending call
agent.receipt       exchange co-signed receipts after completion
agent.reputation    query an agent's track record
```

## Architecture Diagram

```
Device A (yours)                    Device X (stranger)
┌─────────────────┐                ┌─────────────────┐
│ OpenClaw        │                │ Researcher      │
│ ├ web-search    │                │ ├ web-research   │
│ ├ calendar      │                │ └ summarization  │
│ └ file-mgmt     │                │                  │
│        ▲        │                │        ▲         │
│        │ ws     │                │        │ ws      │
│        ▼        │                │        ▼         │
│   sky10 daemon ◄├── skylink ────►┤   sky10 daemon   │
│   ├ agent reg   │   encrypted    │   ├ agent reg    │
│   ├ skykv sync  │      P2P      │   ├ skykv sync   │
│   └ routing     │                │   └ routing      │
└─────────────────┘                └─────────────────┘
```

## Open Questions (at time of writing)

- Trust boundary: should all RPC methods be accessible to all agents, or
  should there be a capability/scope system?
- Capability taxonomy: freeform strings risk fragmentation
  ("web-research" vs "web_research" vs "webResearch")
- Settlement chain: protocol should be chain-agnostic, agents negotiate
- Wallet integration: how opinionated should the protocol be about wallet
  tooling?
