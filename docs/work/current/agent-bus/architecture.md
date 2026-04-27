---
created: 2026-04-26
updated: 2026-04-26
model: claude-opus-4-7
---

# Agent Bus Architecture

## Layers

```
┌──────────────────────────────────────────────────────────────────┐
│  Agent runtime (OpenClaw plugin, Hermes, future runtimes)        │
│  Sends/receives envelopes; never touches sky10 RPC directly      │
└──────────────────────────┬───────────────────────────────────────┘
                           │ websocket (host-local)
                           │ skylink stream (cross-device)
                           ▼
┌──────────────────────────────────────────────────────────────────┐
│  Bus transport                                                    │
│  - authenticates the channel (libp2p identity / ws auth token)   │
│  - stamps agent_id, device_id, ts on every envelope it accepts   │
│  - rejects envelopes with payload-supplied identity fields       │
└──────────────────────────┬───────────────────────────────────────┘
                           │
                           ▼
┌──────────────────────────────────────────────────────────────────┐
│  Bus dispatcher                                                   │
│  - dumb switch on envelope.type → handler file                    │
│  - per-type queue + per-agent rate limit + nonce dedup            │
│  - audit-log entry per envelope (one line, structured)            │
└──────────────────────────┬───────────────────────────────────────┘
                           │
        ┌──────────────────┼─────────────────────────┐
        ▼                  ▼                         ▼
┌───────────────┐  ┌────────────────┐  ┌─────────────────────────┐
│ chat handlers │  │ messaging      │  │ wallet / secrets / x402 │
│ chat.send     │  │ messaging.send │  │ wallet.balance_subscribe│
│ chat.receive  │  │ messaging.recv │  │ x402.sign_payment       │
│ ...           │  │ ...            │  │ ...                     │
└───────────────┘  └────────────────┘  └─────────────────────────┘
        Each handler in its own file. No shared dispatch logic
        beyond "switch type → call handler". No reflection,
        no auto-binding, no shared parsers.
```

## Envelope spec

Every envelope on the bus has the same outer shape:

```json
{
  "type": "x402.sign_payment",
  "agent_id": "A-abcdef0123456789",
  "device_id": "D-12345678",
  "request_id": "f4a8...",
  "ts": "2026-04-26T17:30:00Z",
  "nonce": "1d2c3b4a5e6f7080",
  "payload": { "service_id": "perplexity", "challenge": "...", "max_price_usdc": "0.005" }
}
```

| Field | Source | Purpose |
|---|---|---|
| `type` | caller | dispatch key; must be in the registry |
| `agent_id` | **bus-stamped** | which agent identity this came from; bus replaces any caller-supplied value |
| `device_id` | **bus-stamped** | which device the envelope originated on; for cross-device routing |
| `request_id` | caller (optional) | for sync-on-async correlation; response envelopes echo this back |
| `ts` | **bus-stamped on receipt** | for replay window enforcement and audit ordering |
| `nonce` | caller | dedup against replay; must be unique within the configured window for `(agent_id, type)` |
| `payload` | caller | opaque to the bus; handler-specific schema; **untrusted** |

**Identity injection is structural.** `agent_id` and `device_id` are
not in the payload schema and the bus refuses any envelope whose JSON
includes them at the top level (the deserialization code drops them
and re-stamps from the authenticated channel). Handlers receive a
struct in which these fields exist alongside `payload` but were never
inside `payload`. There is no API in the handler interface to read
"original caller-claimed agent_id" because the concept doesn't exist
once the envelope crosses the bus boundary.

## Transport

We reuse what's already in `pkg/agent/`:

- **Host-local agent ↔ daemon**: `chat_websocket.go` generalizes from
  carrying chat-shaped messages to carrying any envelope. Existing
  websocket connection establishment, auth token validation, and
  framing all keep working. We add a thin envelope wrapper.
- **Cross-device daemon ↔ daemon**: `pkg/link` (libp2p) carries
  envelopes as a stream type. The remote daemon authenticates the
  peer via libp2p identity; that identity becomes the trusted
  `device_id` for every envelope received over that stream.
- **Cross-device agent ↔ remote daemon**: agent's local daemon
  forwards the envelope over skylink to the remote daemon, which
  dispatches. `device_id` is preserved through the hop so handlers
  see the originating device.

No new transports. No new ports. No new processes.

## Replay protection

Each handler-type registers a **nonce window** (default: 10 minutes)
and a **ts skew tolerance** (default: ±2 minutes from bus clock).

The bus dispatcher rejects an envelope if:

- `ts` is outside the skew window
- `(agent_id, type, nonce)` has been seen within the nonce window

Nonce dedup state lives in an in-memory bounded LRU keyed by
`(agent_id, type, nonce)` with TTL = nonce window. Crash resets the
window — acceptable because the worst case is one extra accepted
replay across a daemon restart.

For envelope types that carry money (x402.sign_payment,
wallet.transfer), the **handler also enforces an idempotency check**:
if the payment-binding nonce inside the payload has already been
signed, the handler returns the cached signed result instead of
re-signing. Belt and suspenders.

## Sync-on-async pattern

Some envelopes need a synchronous answer. x402.sign_payment is the
canonical case: the agent is mid-HTTP-request to a paid service, gets
a 402, must obtain a signed payment header, and resume the request.

Pattern:

```
agent → bus  : { type:"x402.sign_payment", request_id:"r1", ... }
bus → handler
handler responds via bus
bus → agent  : { type:"x402.payment_signed", request_id:"r1", payload:{...} }
agent       : matches request_id back to the original promise
```

Client side (in the agent's bus library):

- `await bus.request("x402.sign_payment", payload, { timeout: 10s })`
- Library generates `request_id`, stores a promise keyed by it, sends
  envelope, awaits matching response or timeout.
- Timeout returns a typed error; agent decides whether to retry.

Server side (in the handler):

- Handler is a normal function returning `(payload, err)`. The bus
  wraps the return into a response envelope with the same `request_id`
  and `type=<request-type>.<response-suffix>` (e.g.
  `x402.payment_signed`).

Latency budget for x402: the user is already waiting on a paid HTTP
call to a remote service (typically 200ms–3s). Adding 20–50ms for the
bus roundtrip is invisible.

## Audit log

Every accepted envelope writes one line to a structured audit log:

```jsonl
{"ts":"...","agent_id":"A-...","device_id":"D-...","type":"x402.sign_payment","nonce":"...","payload_hash":"sha256:...","decision":"accepted"}
```

Rejections also log, with `decision` ∈ `{rate_limited, replay,
type_unregistered, payload_too_large, scope_denied, parse_error}`.
Payload bodies are **not** logged in full — just a content hash —
because they can carry sensitive data (challenges, message bodies,
secrets-issuance requests).

Audit log lives at `os.UserConfigDir()/sky10/bus.jsonl` (subject to
the open question on log location).

## Where drive sync stays separate

`pkg/fs` already does encrypted, libp2p-replicated drive sync between
sky10 daemons. It is the right tool for:

- bulk file content (attachments, archived messages, large blobs)
- per-agent state directories (the agent's local SQLite, scratch
  files, etc.)
- anything large enough that JSON-framing it on the bus is wasteful

The bus *triggers* drive sync — e.g. an envelope says "your drive has
a new manifest at version V" — but does not *carry* the bytes. This
keeps the bus latency-friendly and the drive-sync code path
specialized.

A common pattern: host fetches a Slack message body, writes it to the
agent's drive at a stable path, sends an envelope
`messaging.message_arrived` with the path. Agent reads the body
locally from its drive.

## Cross-device behavior

When an agent on device A wants to call a host capability that lives
on device B:

1. Agent → local daemon (device A) over websocket as usual.
2. Local daemon recognizes the envelope is targeted at a remote
   device (e.g. via routing fields or by handler hint).
3. Local daemon forwards over skylink to device B's daemon, preserving
   `agent_id` and `device_id` (originating).
4. Device B's daemon receives, dispatches to handler.
5. Response envelope reverses the route.

Transparent to the agent. Latency higher (libp2p hop), bounded by
relay and direct-connection availability.

## Failure modes

| Condition | Behavior |
|---|---|
| Bus queue full for an envelope type | Bus replies with `bus.busy` envelope; older envelopes dropped per drop policy; audit-logged |
| Handler panics | Bus recovers, logs, replies with `bus.handler_error` envelope; agent sees it and can retry |
| Auth token revoked | Connection terminated; agent's local library reconnects with fresh token via existing websocket auth flow |
| Cross-device skylink down | Local daemon queues briefly, then returns `bus.unreachable_device`; agent decides whether to retry |
| Envelope type registry mismatch (agent uses type the host doesn't know) | Bus replies `bus.type_unregistered`; agent treats as unsupported feature |
