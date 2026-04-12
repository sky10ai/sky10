---
created: 2026-04-03
updated: 2026-04-11
model: claude-opus-4-6
---

# Agent Support — Implementation Plan

## Overview

sky10 becomes a sidecar for AI agents. Each device in the swarm can host
multiple agent processes. Agents register with their local daemon via HTTP
RPC. Cross-device communication (agent-to-agent, agent-to-human) always
goes through libp2p with Nostr as fallback for both discovery and relay.

This plan covers **Part 1: Own Swarm** — directing your agents across your
devices. Part 2 (open agent economy: strangers, payments, reputation) is
separate future work defined in `agent-protocol.md`.

> Status note: the async mailbox and Nostr relay handoff now exist in the
> codebase. Follow-on transport and convergence hardening lives in
> [`../past/2026/04/12-Private-Network-Robustness.md`](../past/2026/04/12-Private-Network-Robustness.md).

## Architecture

```
Device A (yours)                    Device B (yours)
+-----------------+                +-----------------+
| Agent "coder"   |                | Agent "research"|
| (http :8200)    |                | (http :8300)    |
|   ^   |         |                |   ^   |         |
|   |   | http rpc|                |   |   | http rpc|
|   v   v         |                |   v   v         |
| sky10 daemon    |<-- libp2p  --->| sky10 daemon    |
|  :9101          |  (+ nostr     |  :9101          |
|  agent registry |   fallback)   |  agent registry |
+-----------------+                +-----------------+
       ^
       | HTTP :9101
       |
   Web UI (Agents page)
```

**Local (agent <-> daemon, same machine):** HTTP RPC in both directions.
Agent calls daemon at `POST localhost:9101/rpc`. Daemon calls agent at
the endpoint the agent provided during registration (e.g.
`POST localhost:8200/rpc`). Both sides are JSON-RPC 2.0 servers.

**Cross-device (agent <-> agent, agent <-> human):** Always libp2p via
skylink. Nostr relays as fallback for both discovery and transport when
libp2p can't connect. The daemon is the agent's libp2p presence — routes
P2P messages to/from local agents via their HTTP RPC endpoints.

**Web UI <-> remote agents:** Human types in web UI -> HTTP RPC to local
daemon -> libp2p to remote daemon -> HTTP RPC to agent on that device.
Independent chat sessions with each agent.

## Current State (as of 2026-04-04)

Recent work that affects this plan:

- **S3 is now optional.** Daemon starts with just a keypair. All sync
  (KV, identity, join) works over P2P. S3 is durable storage, not a
  requirement. Agent discovery cannot assume S3.
- **Network mode is default.** DHT, relay, AutoNAT, hole punching all
  enabled. Agents can use DHT for discovery.
- **Three-layer discovery exists:** S3 registry → Nostr relays → DHT
  FindPeer from manifest. Agent discovery should follow the same pattern.
- **KV syncs over P2P** (`/sky10/kv-sync/1.0.0`). Full encrypted
  snapshot push on every write. The async mailbox will ride this for
  free — no S3 needed.
- **Nostr infrastructure is built.** Multiaddr publishing via NIP-78
  d-tag replaceable events. Query relays by d-tag. Default relays:
  Damus, nos.lol. Agent capability records can use the same pattern.
- **P2P join exists** (`pkg/join/`). Invite code carries identity
  address + Nostr relays. Auto-approve on invite possession. Key
  exchange over libp2p stream. Pattern to follow for agent registration.
- **Device IDs are `D-` + 8 chars** (`Key.ShortID()` returns raw 8).
  Agent IDs will be `A-` + 8 chars using the same derivation.
- **Web UI already has Agents placeholder** (`web/src/pages/Agents.tsx`)
  and nav entry (between Devices and Network in sidebar).
- **`system.*` RPC namespace exists** (self-update). Shows pattern for
  adding new RPC namespaces to serve.go.

## Design Decisions

### 1. Agent Identity

Each agent gets its own Ed25519 keypair on registration. The key is used
only for generating a stable agent ID — same bech32m scheme as device IDs:
`Key.ShortID()` returns 8 chars, prefixed with `A-`.
Example: `A-q7m2k9x4`. 8 chars = 40 bits = ~1 trillion unique values.
The `A-` prefix distinguishes agent IDs from device IDs (`D-`) at a glance.

Agents are owned by the identity that registered them. The agent's key is
NOT used for signing or encryption in V1 — it's purely an ID generator.
Future: agent keys enable delegation, independent reputation, scoped
capabilities.

Key storage: `~/.sky10/keys/agents/{agentID}.json`

### 2. ACP-Inspired Message Semantics

The Agent Client Protocol (ACP, from the Zed editor team) defines how
editors communicate with AI agents: sessions, prompts, streaming updates,
permission gates, stop reasons. sky10's web UI chat is essentially the
same interaction model — human directs agent, agent reads/writes files,
streams responses, asks for permission.

**What we borrow from ACP (application-level concepts):**
- **Sessions** — chat sessions with history replay. Each agent
  conversation has a session ID. Sessions can be loaded/resumed.
- **Structured streaming** — responses are a sequence of typed updates:
  text chunks, tool calls, diffs, plans, file annotations.
- **Permission requests** — agent asks daemon for permission before
  destructive actions. Daemon surfaces in web UI for human approval.
- **Stop reasons** — `end_turn`, `cancelled`, `max_tokens`,
  `max_turn_requests`, `refusal`. Clean signal for why a response ended.
- **MCP passthrough** — agents can access MCP servers the user has
  configured. Session setup includes MCP server configs.

**What we DON'T adopt from ACP:**
- **stdio transport** — sky10 uses HTTP RPC (local) and libp2p (remote).
  ACP assumes a single local subprocess over stdio pipes.
- **Single-client model** — ACP is one editor talking to one agent.
  sky10 is many-to-many across devices.
- **Editor-specific methods** — `fs/read_text_file`, terminal lifecycle,
  etc. sky10 has its own storage (skyfs, skykv) and doesn't manage
  editor state.

**ACP adapter (future):** For agents that already speak ACP natively
(Zed agents, OpenClaw, etc.), a thin translation layer can bridge ACP
stdio to sky10's HTTP RPC. The daemon spawns the agent subprocess,
manages stdio, and translates messages. This is not V1.

### 3. Nostr as Discovery + Fallback Transport

Nostr serves two roles:

1. **Discovery:** Agents publish capability records to Nostr relays
   using the same NIP-78 d-tag pattern already used for device multiaddr
   publishing. Tag: `"sky10:agent:" + agentID`. Other daemons query
   relays to find agents by capability or ID.

2. **Fallback transport:** When libp2p can't connect directly (both
   peers behind NAT, no relay available), messages are sent as encrypted
   Nostr events through relays. The relay is a dumb pipe — payload is
   encrypted end-to-end.

Discovery layers for agents (same pattern as device discovery):
1. S3 device registry (if configured) — `devices/{id}.json` `agents` field
2. Nostr relays — query by d-tag
3. DHT (network mode) — agent records in DHT
4. Direct query to connected peers — `agent.list` over skylink

### 4. Streaming vs Request/Response

**V1: Request/response** for structured agent calls (search, execute,
file ops). Simple, works with existing skylink protocol
(`/skylink/1.0.0` — write request, close-write, read response).

**V2: Streaming protocol** for chat dialogue. New libp2p protocol
`/skylink/stream/1.0.0` — response is a sequence of length-prefixed
frames until a terminal frame. The libp2p stream stays open (no
close-write before reading). Frame types follow ACP-inspired semantics:
- `text` — partial token / text chunk
- `tool_call` — agent is invoking a tool (with status)
- `diff` — file change payload
- `plan` — agent's plan/reasoning
- `permission` — agent requesting approval
- `done` — terminal frame with stop reason

This enables the web UI to render tokens incrementally, show tool
activity, and gate destructive actions with permission prompts.

### 5. Agent Health / Liveness

Daemon pings each registered agent's HTTP endpoint periodically (every
30s). If an agent fails to respond to 3 consecutive pings, it's marked
disconnected and deregistered. The daemon stops advertising it.

On registration, agent declares its endpoint. Daemon immediately pings
to verify reachability before accepting the registration.

### 6. Human <-> Agent Communication via Web UI

The web UI on device A can chat with agents on devices B and C
independently. Flow:

```
Web UI (device A)
  -> HTTP RPC to local daemon (device A)
    -> libp2p to daemon on device B (or C)
      -> HTTP RPC to agent on device B (or C)
        -> agent responds
      <- response travels back the same path
```

Each chat is an independent session. The web UI maintains conversation
state client-side. Messages are routed through the P2P mesh.

### 7. Multi-Daemon Coordination

Multiple devices join via `pkg/join/` (P2P join with auto-approve).
Each daemon maintains its own agent registry. Discovery uses the same
three-layer approach as device discovery:

1. **S3 device registry** (if configured): `devices/{id}.json` includes
   `agents` field
2. **Nostr relays:** Primary discovery. Agent capability records published
   as NIP-78 events. Works without S3.
3. **libp2p direct:** Query connected peers via `agent.list` skylink call.
   DHT FindPeer if peer ID is known.

S3 is durable storage that can aid discovery — it's not a requirement.

### 8. Auth / Scoping (Deferred)

**Problem:** Any process on the machine can call any daemon RPC — full
access to fs, kv, link, identity. Agents should have limited access:
- No fs access by default
- Only access to a specific KV namespace (e.g. `agents/{agentID}/...`)
- No identity/device management

**Current state:** The RPC dispatch has no per-caller permission model.
Adding scoping requires either:
- Per-connection auth tokens checked in dispatch
- A separate agent RPC endpoint with filtered methods
- Middleware that inspects caller identity before dispatch

**Decision:** Punt to a later phase. Document as a known gap. For V1,
local machine = trusted. Scoping is required before agents run untrusted
code or before the open economy (Part 2).

## Implementation Phases

### Phase 1: Agent Registry + HTTP RPC Registration

**Goal:** Agents register via HTTP RPC, daemon can call them back, all
local. Foundation for everything else.

New package `pkg/agent/`:
- `types.go` — AgentInfo, MethodSpec, RegisterParams, CallParams, errors
- `registry.go` — in-memory map of registered agents, thread-safe
- `rpc.go` — `agent.*` RPC handler (register, deregister, list, call, status)
- `health.go` — periodic ping to agent endpoints, deregister on failure

Agent ID generation: `skykey.Generate()` → `key.ShortID()` → `"A-" + id`

Registration flow:
1. Agent calls `POST localhost:9101/rpc` with `agent.register`
2. Params: `{name, endpoint, capabilities, methods}`
3. Daemon generates agent keypair, derives 8-char agent ID
4. Daemon pings agent endpoint to verify reachability
5. Daemon stores agent in registry, emits `agent.connected` SSE event
6. Returns: `{agent_id, status: "registered"}`

Calling an agent:
1. Caller sends `agent.call` with `{agent, method, params}`
2. Daemon looks up agent endpoint in registry
3. Daemon POSTs JSON-RPC request to agent's endpoint
4. Returns agent's response

Modify: `commands/serve.go` (wire registry + handler — follows the
`system.*` pattern from self-update)

Tests: registry CRUD, health ping, concurrent access, call dispatch.

### Phase 2: Cross-Device Agent Routing via libp2p

**Goal:** Call agents on other devices. Aggregate agent lists across swarm.

New files in `pkg/agent/`:
- `router.go` — decides local vs remote, routes through link node
- `link.go` — registers skylink capability handlers (agent.call, agent.list)

Flow:
1. `agent.call` with `device_id` targeting remote device
2. Router resolves device peer ID via resolver (3-layer: S3 → Nostr → DHT)
3. `linkNode.Call(ctx, peerID, "agent.call", params)`
4. Remote daemon's skylink handler dispatches to local agent via HTTP RPC

`agent.list` aggregates: local registry + query each connected peer via
skylink (parallel with errgroup, 3s timeout per peer).

Optionally extend `pkg/fs/device.go`: add `agents` field to DeviceInfo
for S3-aided discovery (only when S3 configured).

Modify: `commands/serve.go` (wire router, register link handlers)

Tests: two-node routing, list aggregation, unreachable device, discover by
capability.

### Phase 3: Nostr Agent Discovery

**Goal:** Agents discoverable via Nostr relays. Uses existing Nostr
infrastructure (NIP-78 d-tag, relay publishing/querying).

New files in `pkg/agent/`:
- `nostr.go` — publish agent capability records to Nostr relays, query
  by capability. Follows same pattern as `pkg/link/nostr.go` for device
  multiaddr publishing.

Agent record on Nostr (kind 30078, d-tag `"sky10:agent:{agentID}"`):
```json
{
  "agent_id": "A-q7m2k9x4",
  "name": "coder",
  "device_id": "D-abc12345",
  "capabilities": ["code", "test"],
  "methods": {...},
  "address": "sky10q..."
}
```

Extend router: include Nostr-discovered agents in `agent.list`.

### Phase 4: Nostr Relay Fallback Transport

**Goal:** When libp2p can't connect, relay agent calls through Nostr.

Extend router: on libp2p call failure, wrap the call as an encrypted
Nostr event and publish to relays. Target daemon subscribes and picks up.
Response travels back the same way.

This is more complex than Phase 3 and may need its own design doc.

### Phase 5: Async Mailbox (KV-based)

**Goal:** Calls to offline agents queued, delivered on reconnect.

New file: `mailbox.go`
- KV key: `agent/inbox/{deviceID}/{agentID}/{timestamp}-{rand}`
- Enqueue on failed remote call (agent offline)
- Drain on agent registration (deliver pending messages)
- TTL-based cleanup (24h default)

Uses existing KV CRDT — syncs across devices via P2P push automatically.
No S3 needed.

### Phase 6: Web UI — Agents Page

**Goal:** See and direct all agents across all devices from the browser.

The Agents page placeholder already exists (`web/src/pages/Agents.tsx`)
and nav entry is already in the sidebar. Build it out:

- `pages/Agents.tsx` — card grid of all agents, status, capabilities
- `pages/AgentDetail.tsx` — full info, methods, call form

Modified web files:
- `App.tsx` — add detail route
- `lib/rpc.ts` — add `agent` namespace + TypeScript types
- `lib/events.ts` — add agent event types

### Phase 7: Streaming Chat Protocol

**Goal:** Real-time token-by-token chat with LLM agents.

New libp2p protocol `/skylink/stream/1.0.0`:
- Request: single length-prefixed JSON frame
- Response: sequence of frames until terminal frame
- No close-write before reading (stream stays open)

Web UI chat component that renders tokens as they arrive.

### Phase 8: Swarm Broadcast

**Goal:** Multi-select agents, send one call to all, see per-agent results.

- `agent.broadcast` RPC — parallel calls via errgroup
- Web UI multi-select + broadcast panel

## Phase Dependency Graph

```
Phase 1 (registry + HTTP RPC)
    |
    v
Phase 2 (cross-device libp2p)
    |
    +-------+-------+-------+-------+
    |       |       |       |       |
    v       v       v       v       v
  Ph 3    Ph 4    Ph 5    Ph 6    Ph 7
 (nostr  (nostr  (mail)  (web)  (stream)
  disc)  relay)
    |       |       |       |       |
    +-------+-------+-------+-------+
                    |
                    v
              Phase 8 (broadcast)
```

Phases 3-7 are largely independent after Phase 2.

## Key Files

| Area | File | Role |
|------|------|------|
| Agent types | `pkg/agent/types.go` | AgentInfo, MethodSpec, params, errors |
| Agent registry | `pkg/agent/registry.go` | In-memory agent tracking |
| Agent RPC | `pkg/agent/rpc.go` | agent.* handler dispatch |
| Agent health | `pkg/agent/health.go` | Periodic ping, deregister stale |
| Agent routing | `pkg/agent/router.go` | Local vs remote dispatch |
| Skylink handlers | `pkg/agent/link.go` | P2P capability registration |
| Nostr discovery | `pkg/agent/nostr.go` | Publish/query agent records |
| Async mailbox | `pkg/agent/mailbox.go` | KV-based offline queue |
| Daemon wiring | `commands/serve.go` | Initialization, plumbing |
| Device registry | `pkg/fs/device.go` | S3 agent advertising (optional) |
| Web UI page | `web/src/pages/Agents.tsx` | Already exists (placeholder) |
| Web UI detail | `web/src/pages/AgentDetail.tsx` | Agent info + call form |
| Web RPC client | `web/src/lib/rpc.ts` | agent.* TypeScript bindings |

## Existing Infrastructure to Reuse

| What | Where | How agents use it |
|------|-------|-------------------|
| Key generation | `skykey.Generate()` | Agent keypair |
| Short ID | `Key.ShortID()` | 8-char agent ID derivation |
| RPC handler pattern | `rpc.Handler` interface | `agent.*` namespace handler |
| Skylink call | `link.Node.Call()` | Cross-device agent calls |
| Capability registration | `link.Node.RegisterCapability()` | agent.call, agent.list handlers |
| 3-layer resolver | `link.Resolver.Resolve()` | Find remote device for agent |
| Nostr publishing | `pkg/link/nostr.go` | Agent capability records |
| KV store | `pkg/kv/Store` | Async mailbox storage |
| KV P2P sync | `pkg/kv/p2p.go` | Mailbox syncs across devices |
| SSE events | `rpc.Server.Emit()` | agent.connected/disconnected |
| serve.go wiring | `commands/serve.go` | Follow system.* pattern |
