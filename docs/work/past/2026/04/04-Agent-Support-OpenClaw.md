---
created: 2026-04-04
model: claude-opus-4-6
---

# Agent Support + OpenClaw Channel Plugin

sky10 becomes a sidecar for AI agents. Agents register with the local
daemon via HTTP RPC, communicate via messages routed through the daemon
(locally via SSE, cross-device via libp2p), and are discoverable across
the device swarm. The web UI provides a chat interface for directing
agents.

This doc covers the full backend implementation (Go) and the OpenClaw
channel plugin (JavaScript) that connects a real agent (Randy) to the
sky10 network with full tool access.

## Architecture

```
Device A (ours)                     Device B (Randy's)
+-----------------+                 +-----------------+
| Web UI          |                 | OpenClaw Gateway|
|   AgentChat.tsx |                 |   sky10 plugin  |
|       |         |                 |       |         |
|       | HTTP    |                 |       | SSE     |
|       v         |                 |       v         |
| sky10 daemon    |<--- libp2p ---->| sky10 daemon    |
|  agent registry |    (skylink)    |  agent registry |
|  SSE events     |                 |  SSE events     |
+-----------------+                 +-----------------+
```

Message flow (outbound to agent):
1. Web UI calls `agent.send` RPC with `to`, `device_id`, `session_id`
2. Daemon's `Router.Send()` checks device_id, routes via libp2p
3. Remote daemon's `agentSendHandler` receives, emits SSE `agent.message`
4. Plugin's EventSource listener picks up event, POSTs to gateway API
5. OpenClaw processes through full agent loop (with tools)
6. Plugin sends response back via `agent.send` RPC
7. Remote daemon routes via libp2p back to our daemon
8. Our daemon emits SSE, web UI's EventSource picks it up

## Go Backend — pkg/agent/

### types.go
- `AgentInfo`: id, name, device_id, device_name, skills, status, connected_at
- `Message`: id, session_id, from, to, device_id, type, content (json.RawMessage), timestamp
- `SendParams`, `RegisterParams`, sentinel errors
- `GenerateAgentID()`: creates `A-` + 16 chars from Ed25519 keypair (bech32m)
- Agent IDs prefixed `A-` to distinguish from device IDs (`D-`)

### registry.go
- Thread-safe in-memory registry, keyed by agent ID with name->ID lookup
- **Idempotent registration**: re-registering same name returns existing ID,
  updates skills. Critical — without this, agent IDs churned on every restart
  and all routing broke.
- Heartbeat tracking via `lastHeartbeat` map
- Methods: Register, Deregister, Get, GetByName, List, Resolve, Heartbeat

### rpc.go
- RPC handler for `agent.*` namespace (implements `rpc.Handler`)
- Methods: register, deregister, list, send, heartbeat, discover, status
- `rpcSend` sets `From: h.registry.DeviceID()` on messages — this was a
  critical fix. Without it, responses couldn't route back because the sender
  device wasn't identified.
- Emits SSE events on connect/disconnect
- `SetPeerNotifier` broadcasts agent events to other devices via libp2p

### router.go
- Routes messages locally (SSE emit) or remotely (libp2p `node.Call`)
- `Send()`: checks if device_id matches self -> local SSE, else -> libp2p
- `List()`: aggregates local registry + queries all connected peers in
  parallel (3s timeout per peer via errgroup)
- **Peer cache** (`device_id -> peer.ID`): populated during `List()`
  aggregation. This is how the router knows which libp2p peer to call
  for a given device.

### link.go
- Registers skylink capability handlers: `agent.send` and `agent.list`
- **Auto-caches sender peer on incoming messages**: `agentSendHandler`
  extracts `msg.From` (device ID) and `req.PeerID` (libp2p peer) and
  calls `router.cachePeer()`. Without this, the daemon receiving a
  message couldn't route the response back — it had never called
  `agent.list` for the sender's device.

### health.go
- Heartbeat checker: runs every 30s, deregisters after 3 misses
- **Currently disabled** — too aggressive during development. Randy kept
  getting deregistered. Left as TODO.

### Wiring (commands/serve.go)
```go
agentRegistry := skyagent.NewRegistry(bundle.DeviceID(), skyfs.GetDeviceName(), nil)
agentRouter := skyagent.NewRouter(agentRegistry, linkNode, server.Emit, bundle.DeviceID(), nil)
agentRPC := skyagent.NewRPCHandler(agentRegistry, server.Emit)
agentRPC.SetRouter(agentRouter)
server.RegisterHandler(agentRPC)
skyagent.RegisterLinkHandlers(linkNode, agentRegistry, server.Emit, agentRouter)
```

## OpenClaw Channel Plugin

Separate repo: `github.com/sky10ai/openclaw-sky10-channel`

### What OpenClaw Is
OpenClaw is an AI agent framework that supports multiple "channels"
(Telegram, Discord, Slack, etc.). Each channel is a plugin that handles
inbound messages from an external platform, routes them through the
agent loop (LLM + tools), and sends responses back.

### Plugin Architecture
- **Entry point**: `export default async function register(api)` in `src/index.js`
- **Plugin API** (`api` object) exposes: `registerTool`, `registerHook`,
  `registerHttpRoute`, `registerChannel`, `registerGatewayMethod`,
  `registerCli`, `registerService`, `registerCliBackend`,
  `registerProvider`, `registerSpeechProvider`
- **Plugin manifest**: `openclaw.plugin.json` with id, channels array,
  configSchema
- **No `api.messaging.dispatch`** — this was our initial assumption about
  how to push messages into the agent loop. It doesn't exist.

### The Inbound Dispatch Problem

This was the hardest part. OpenClaw's plugin API has no obvious method
for "push this message into the agent loop." The intended pattern is:

1. Use `createChatChannelPlugin()` from `openclaw/plugin-sdk/core`
2. Define a `gateway` adapter with `startAccount()`
3. OpenClaw calls `startAccount` with a runtime context containing dispatch functions
4. Use `dispatchInboundDirectDmWithRuntime()` from the SDK

But this requires:
- The channel to be configured in `~/.openclaw/openclaw.json` under `channels.sky10`
- The `openclaw` package to be resolvable (needed symlink on Randy's machine)
- Assembling a complex `DirectDmRuntime` object with ~8 internal functions

**What we tried (and why it failed):**

| Approach | Result |
|----------|--------|
| `api.messaging.dispatch()` | Doesn't exist on the API object |
| Import `openclaw/plugin-sdk/*` | Package not resolvable without symlink to global install |
| `registerChannel` return value | Returns `undefined` |
| `gateway.startAccount` callback | Never called — channel not configured in openclaw.json |
| Assemble `DirectDmRuntime` manually | Too many internal dependencies (8+ functions from deep internals) |

**What actually worked: Gateway HTTP API**

OpenClaw's gateway exposes an OpenAI-compatible REST API:
```
POST /v1/responses
Authorization: Bearer <token>
{
  "model": "openclaw",
  "input": "message text",
  "user": "sky10:<sender>:<session>"
}
```

The plugin POSTs to this endpoint from within the gateway process. The
`user` field provides a stable session key for multi-turn conversations.
The gateway processes through the full agent pipeline (including tools)
and returns the response. The plugin then sends it back via sky10.

This is technically the gateway calling itself over HTTP, but it works
reliably and avoids all the SDK complexity.

### Key Configuration (Randy's `~/.openclaw/openclaw.json`)

Plugin section:
```json
"plugins": {
  "entries": {
    "sky10": {
      "enabled": true,
      "config": {
        "agentName": "randy",
        "skills": ["code", "shell", "web-search", "file-ops"],
        "gatewayUrl": "http://localhost:18789",
        "gatewayToken": "<gateway auth token>"
      }
    }
  },
  "load": { "paths": ["/tmp/openclaw-sky10-channel"] }
}
```

Gateway HTTP API must be enabled:
```json
"gateway": {
  "http": {
    "endpoints": {
      "responses": { "enabled": true }
    }
  }
}
```

### Node.js Gotchas

- **No native EventSource in Node**: Required `npm i eventsource` package.
  The polyfill has different export patterns between v1 (`module.exports`)
  and v2 (`export { EventSource }`). Plugin handles both.
- **TypeScript not stripped by default in Node 22**: Converted plugin to
  plain JavaScript. OpenClaw loads plugins via jiti which handles TS, but
  external deps may not.
- **`openclaw` package resolution**: Plugin runs from its install path
  (e.g., `/tmp/openclaw-sky10-channel`). The `openclaw` package isn't in
  its `node_modules`. Needed `ln -s` from global install. Not required
  for the HTTP API approach, but needed if using SDK functions directly.

### Plugin File Structure
```
openclaw-sky10-channel/
  src/
    index.js     — plugin entry, SSE listener, dispatch logic
    sky10.js     — Sky10Client (RPC: register, send, heartbeat, sseUrl)
  openclaw.plugin.json — manifest with channels, configSchema
  package.json
```

## Web UI

### AgentChat.tsx
- Chat interface with message bubbles, markdown rendering (react-markdown)
- Direct EventSource for receiving `agent.message` SSE events
  (not the `subscribe()` wrapper — it didn't pass events through)
- Session ID per mount via `crypto.randomUUID()`
- Typing indicator with 30s timeout, clears on response
- Sends via `agent.send` RPC with `device_id` for cross-device routing

### Agents.tsx
- Agent list page with card grid, navigates to chat on click
- Uses `useRPC` with `AGENT_EVENT_TYPES` for live updates + 5s polling
- Shows agent count, name, device, skills, status

## Bugs Fixed During Integration

1. **SSE subscriber count wrong**: `SubscriberCount()` only counted Unix
   socket subscribers, not HTTP SSE. We couldn't tell if Randy was
   connected. Fixed to include `httpSubs`.

2. **Agent ID churn**: Every re-registration generated a new ID. Randy
   had 5+ IDs in one session. Fixed with idempotent registration by name.

3. **Empty `from` field**: Messages didn't carry sender device ID.
   Responses couldn't route back. Fixed by setting `From` in `rpcSend`.

4. **Peer cache not populated**: Randy's daemon couldn't route responses
   back because it never called `agent.list` to populate the cache.
   Fixed by auto-caching sender peer in `agentSendHandler`.

5. **systemd startup on Linux**: `/tmp/sky10` didn't exist, daemon couldn't
   start. Fixed with `ExecStartPre=/bin/mkdir -p /tmp/sky10`.

6. **Model name for API**: OpenClaw requires `model: "openclaw"`, not
   `model: "default"`. Got 400 until fixed.

7. **Wrong API endpoint**: Plugin hit `/v1/chat/completions` which wasn't
   enabled. Randy's gateway had `/v1/responses` enabled. Fixed to try
   responses first, fall back to chat/completions.

8. **Triple responses from gateway reloads**: Every inbound message
   dispatched to `/v1/responses` caused OpenClaw to re-evaluate config,
   triggering a gateway reload. The reload spawned a new plugin instance
   (with a new SSE connection) without closing the old one. After the
   first message: 2 SSE listeners. After the second: 3. Each fired for
   the same incoming message, causing duplicate responses. Fixed with a
   `Map<messageId, timestamp>` dedup with 30s TTL — first listener to
   see a message ID processes it, the rest skip.

## Decisions

- **Skills, not capabilities or methods**: Standardized vocabulary across
  the codebase. Skills are what agents advertise.
- **HTTP RPC for local, libp2p for remote**: No WebSockets. Agents connect
  to the local daemon via HTTP, cross-device goes through skylink.
- **Message-based, not callback**: Agents don't expose endpoints. The
  daemon is the message bus. Agents listen via SSE.
- **Gateway HTTP API over SDK internals**: The OpenClaw SDK's internal
  dispatch pipeline is complex and fragile. The HTTP API gives the same
  result with 10 lines of code.
- **Heartbeat checker disabled**: Too aggressive for development. Will
  re-enable with longer timeouts later.
- **Nostr and mailbox deferred**: Part 1 only covers own swarm over
  libp2p. Nostr discovery and async mailbox are future work.
