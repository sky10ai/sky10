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
- **Agent economy**: `agent.discover` / `agent.call` — find and call any
  agent on the network by skill

Installation flow: install `sky10` binary, detect running daemon (or prompt
setup through Cirrus), verify connectivity with `skyfs.ping`.

## Agent Registration

Agents register via the daemon's HTTP RPC (localhost:9101 or Unix socket) and
provide a local callback address where the daemon can dispatch incoming calls:

```json
{
  "method": "agent.register",
  "params": {
    "name": "openclaw",
    "callback": "http://localhost:8200/callback",
    "skills": [
      {
        "id": "search",
        "name": "Web Search",
        "description": "Search the web",
        "inputSchema": {"query": "string"}
      },
      {
        "id": "send-email",
        "name": "Send Email",
        "description": "Send an email",
        "inputSchema": {"to": "string", "body": "string"}
      }
    ]
  }
}
```

The daemon dispatches incoming calls by POSTing JSON-RPC requests to the
agent's callback address. The daemon health-checks the callback periodically;
if unreachable, the registration is removed.

## Agent-to-Agent Communication (Own Agents)

For agents across your own devices, the daemon mediates everything:

- **Local agent**: dispatch via HTTP POST to the agent's callback address
- **Remote (own device)**: route through skylink P2P
- **Async/offline**: KV-based mailbox at `agents/<device_id>/<agent>/inbox/`

## Open Agent Economy (Stranger Agents)

### The Three Problems

When agents don't know each other:

1. **Identity** — how does an agent prove who it is?
2. **Trust** — how do you know a stranger agent will do what it claims?
3. **Payment** — how do you compensate an agent without trusting it?

### Identity

Every agent has a **derived Ed25519 keypair** from the user's identity key:

```
agentKey = ed25519_derive(identityKey, "sky10-agent:" + agentName)
```

The derived public key IS the agent's identity: `sky10://q7m2k9x4f8...`. No
accounts, no registration server. Just keys.

Because derivation is deterministic, the same agent name on any of your
devices produces the same identity. The agent's card carries an `owner` field
(user's sky10 address) and `owner_cert` (user identity key signs the agent
pubkey), so anyone can verify the agent is vouched for by a real user. All
agents trace to one owner for sybil resistance.

### Discovery

Two layers work together:

- **DHT** (libp2p Kademlia) handles **peer routing** — finding nodes on the
  network by peer ID so you can connect to them. This is infrastructure.
- **Gossip** handles **data propagation** — once connected, Agent Cards and
  receipts flow between peers. This is the application layer.

Agents publish Agent Cards via gossip. Every connected node receives, verifies,
and stores them. Discovery is a **local database query** over gossip-synced
data — no network round-trip needed.

```json
{
  "agent": "sky10://q7m2k9x4f8",
  "name": "DeepResearcher",
  "description": "Thorough web research with source verification",
  "owner": "sky10://userIdentityKey...",
  "owner_cert": "ed25519:<user signs agent pubkey>",
  "skills": [
    {
      "id": "research",
      "name": "Web Research",
      "description": "Research a topic, return findings with sources",
      "inputSchema": {"query": "string", "depth": "string"},
      "price": {"amount": "2000000", "asset": "USDC", "per": "call"}
    }
  ],
  "seq": 42,
  "published_at": 1712160000,
  "ttl": 86400,
  "signature": "ed25519:<agent key signs everything above>"
}
```

No central registry. No app store gatekeeper. Any agent can publish, any
agent can query. The signature proves the listing is authentic.

### Communication Protocol

Direct P2P via skylink, encrypted, no intermediary. Nine message types:

```
call              → "I want you to do this" (creates task: pending)
payment_required  → "it costs this much" (→ payment_required)
payment_proof     → "here's a signed check" (→ payment_received → working)
status            → progress update, or "I need more input" (→ input_required)
result            → "here's the work" (→ completed, carries artifacts + receipt)
receipt           → caller counter-signs the receipt
error             → something went wrong (→ failed or rejected)
cancel            → caller cancels the task (→ canceled)
settle            → provider cashed the check on-chain (→ settled, carries tx_hash)
```

### Task Lifecycle

Every `call` creates a Task with an ID. Both parties reference it throughout.

```
pending → payment_required → payment_received → working → completed → settled
                                                   ↓
                                             input_required
                                          (caller sends more input,
                                           resumes via call w/ task_id)

Also: failed, rejected, canceled
```

- **payment_received**: provider has the signed check, verified off-chain.
  Work begins. No money has moved on-chain yet.
- **completed**: work delivered, receipt exchanged. Task is "done" from
  the caller's perspective.
- **settled**: provider submitted the signed tx to the chain, confirmed.
  Financial close-out — may happen minutes or days later depending on
  the provider's batching strategy.

### Payment Flow (P2P, not HTTP)

Caller signs a transaction locally, hands raw signed bytes to provider over
skylink. Provider submits to chain whenever they want (like a signed check).

```
Caller                              Provider
  │                                     │
  │──── call ──────────────────────────►│  → pending
  │◄──── payment_required ─────────────│  → payment_required
  │                                     │
  │  [OWS: policy check, sign tx]       │
  │                                     │
  │──── payment_proof ────────────────►│  → payment_received
  │                                     │
  │              [verifies sig off-chain, starts work]
  │                                     │     → working
  │◄──── status (progress) ───────────│
  │                                     │
  │◄──── result + receipt ─────────────│  → completed
  │                                     │
  │──── receipt (counter-signed) ─────►│
  │                                     │
  │              ... later ...          │
  │                                     │
  │              [submits signed tx to chain]
  │◄──── settle (tx_hash) ────────────│  → settled
```

No HTTP, no servers. The signed transaction is just bytes in a P2P message.
Settlement is async — providers can batch-settle many payments at once.

### Artifacts

Tasks produce Artifacts — typed deliverables. Each artifact has an `id`,
`mime_type`, and either inline `data` or a `uri` reference (skyfs://, etc.).
The agent decides based on size.

```json
{
  "type": "result",
  "task_id": "t-123",
  "artifacts": [
    {"id": "report", "mime_type": "text/markdown", "data": "# Findings\n..."},
    {"id": "chart", "mime_type": "image/png", "uri": "skyfs://task-123/chart.png"},
    {"id": "dataset", "mime_type": "text/csv", "uri": "skyfs://task-123/data.csv"}
  ],
  "receipt": { ... }
}
```

Any content type — text, JSON, images, video, binaries — is just a mime_type
+ data or uri. No Part type system needed.

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
agent.register      declare skills, provide callback address
agent.deregister    explicit disconnect (also on health-check failure)
agent.publish       sign and gossip Agent Card to the network
agent.discover      query local index by skill, reputation, price
agent.call          call a skill on any agent (local or remote)
agent.pay           send payment proof for a pending task
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
│        │ http   │                │        │ http    │
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
