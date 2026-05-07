---
created: 2026-04-26
updated: 2026-04-26
model: claude-opus-4-7
---

# Sandbox Comms Architecture

## High-level shape

```
┌────────────────────────────────────────────────────────────────┐
│  Agent runtime (OpenClaw plugin, etc.) inside Lima sandbox     │
│  Opens one websocket per capability it actually uses           │
└────────────┬───────────────────────────────────────────────────┘
             │ ws (host-local; libp2p stream cross-device)
             │
   ┌─────────┴───────────┬─────────────────────┬─────────────────┐
   │ /comms/metered-services/ws      │ /comms/wallet/ws    │ /comms/messengers/ws
   │ (this branch)       │ (future)            │ (future)
   ▼                     ▼                     ▼
┌──────────────────┐ ┌──────────────────┐ ┌──────────────────┐
│ x402 endpoint    │ │ wallet endpoint  │ │ messengers       │
│ x402 handlers    │ │ wallet handlers  │ │ handlers         │
│ delegates to     │ │ delegates to     │ │ delegates to     │
│ pkg/x402         │ │ pkg/wallet       │ │ pkg/messaging/   │
│                  │ │                  │ │   broker         │
└────────┬─────────┘ └────────┬─────────┘ └────────┬─────────┘
         │                    │                    │
         └────────────────────┼────────────────────┘
                              │
                              ▼
              ┌──────────────────────────────┐
              │ pkg/sandbox/comms/ plumbing  │
              │  - websocket framing          │
              │  - envelope wrapping          │
              │  - identity injection         │
              │  - replay protection          │
              │  - audit log                  │
              │  - per-(agent,type) quotas    │
              └──────────────────────────────┘
```

The plumbing is shared library code each capability imports. Every
endpoint handler is a separate websocket route on a separate URL
path. There is no central dispatcher — the Go HTTP mux routes by
path and that's the entire dispatch story.

## Package layout

```
pkg/sandbox/comms/                # shared transport plumbing (M1)
├── doc.go
├── conn.go            websocket connection lifecycle, accept/upgrade,
│                      read/write loops, close handling, ping/pong
├── envelope.go        Envelope struct, TypeSpec, Direction (RR/Push/Sub)
├── registry.go        per-endpoint type registry; init-time validation
│                      of TypeSpec required fields
├── identity.go        bus-stamped agent_id and device_id from
│                      authenticated channel; strips any payload-supplied
│                      identity fields before handler sees the envelope
├── replay.go          bounded LRU for nonce dedup; ts skew window
├── audit.go           per-envelope structured log line
├── quota.go           token-bucket per (agent_id, envelope_type)
├── endpoint.go        helper that turns a registered TypeSpec set into
│                      an http.HandlerFunc for one capability's URL
└── *_test.go          unit tests for each piece
```

```
pkg/sandbox/comms/x402/           # x402 capability (M2)
├── doc.go
├── endpoint.go        registers /comms/metered-services/ws via comms.Endpoint
├── list_services.go   handler for x402.list_services
├── service_call.go    handler for x402.service_call (sync)
├── budget_status.go   handler for x402.budget_status (sync)
├── changes.go         handler for x402.changes (push from host)
└── *_test.go          per-handler tests
```

## Envelope spec

Every envelope on every comms endpoint has the same outer shape:

```json
{
  "type": "x402.service_call",
  "agent_id": "A-abcdef0123456789",
  "device_id": "D-12345678",
  "request_id": "f4a8...",
  "ts": "2026-04-26T17:30:00Z",
  "nonce": "1d2c3b4a5e6f7080",
  "payload": { "service_id": "perplexity", "path": "/search", "body": "...", "max_price_usdc": "0.005" }
}
```

| Field | Source | Purpose |
|---|---|---|
| `type` | caller | dispatch within the endpoint; must be in that endpoint's registry |
| `agent_id` | **plumbing-stamped** | which agent identity opened this connection; payload-supplied value is stripped |
| `device_id` | **plumbing-stamped** | originating device; for cross-device routing |
| `request_id` | caller (optional) | sync-on-async correlation |
| `ts` | **plumbing-stamped on receipt** | replay window enforcement and audit ordering |
| `nonce` | caller | replay dedup; unique within configured window for `(agent_id, type)` |
| `payload` | caller | opaque to plumbing; handler-specific; **untrusted** |

Identity injection is structural. The Go struct exposed to
handlers does not contain a "caller-claimed agent_id" field
because the concept doesn't exist after the envelope crosses the
plumbing. Removing the structural protection requires changing the
struct definition in `pkg/sandbox/comms/envelope.go` — a deliberate
code change with review.

## URL is dispatch

The "dispatcher" is the Go HTTP mux. Each capability registers its
own route:

```go
// In each capability's endpoint.go
mux.HandleFunc("/comms/metered-services/ws", x402Endpoint.HandleWS)
```

Inside one endpoint's handler, after envelope unwrapping, there is
a small switch on `envelope.type` — but that switch only knows
about that capability's envelope types. It physically cannot serve
a `wallet.transfer` envelope; the registered set doesn't include
it. A capability bug stays inside its capability.

## Connection auth

Initial design: an existing websocket auth token (whatever
`pkg/agent/chat_websocket.go` uses today) authenticates the
connection at upgrade time. The token's identity becomes the
trusted `agent_id` for every envelope received over that socket.
Cross-device skylink streams use libp2p peer identity directly.

This is intentionally the same primitive existing chat uses, so
we don't introduce a new auth surface to harden. Open question
in the README about whether we need anything stronger for
sandbox-originating connections.

## Replay protection

Each envelope type registers a **nonce window** (default
10 minutes) and a **ts skew tolerance** (default ±2 minutes).

The plumbing rejects an envelope before any handler sees it if:

- `ts` is outside the skew window
- `(agent_id, type, nonce)` has been seen within the nonce window

State lives in an in-memory bounded LRU keyed by
`(agent_id, type, nonce)` with TTL = nonce window. Daemon restart
resets the window — acceptable; worst case is one extra accepted
replay.

For envelope types that move money (`x402.service_call`,
`wallet.transfer`), the **handler** also enforces an idempotency
check on its own payment-binding nonce inside the payload. Belt
and suspenders.

## Sync-on-async

Some envelopes need a synchronous answer.
`x402.service_call` is the canonical case: agent is mid-HTTP-
request to a paid service, gets 402, must obtain a signed
payment header, resume.

Pattern:

```
agent → comms : { type:"x402.service_call", request_id:"r1", ... }
plumbing → handler
handler returns (responsePayload, err)
plumbing → agent : { type:"x402.service_call_response", request_id:"r1", payload:{...} }
agent matches request_id back to the original promise
```

Library on the agent side: `await conn.request("x402.service_call",
payload, { timeout: 30s })`. The library generates `request_id`,
parks a promise keyed by it, sends the envelope, awaits the
matching response. Timeout returns a typed error.

Library on the host side (in `pkg/sandbox/comms/`): handler
signature is `func(ctx, env) (json.RawMessage, error)`. The
plumbing wraps the return into a response envelope with the same
`request_id`. Handlers don't have to think about correlation.

Latency budget: x402 service calls already involve a remote HTTP
roundtrip (200ms–3s). Adding 20–50ms for the comms hop is
invisible.

## Audit log

Every accepted envelope writes one line to a structured audit
log:

```jsonl
{"ts":"...","agent_id":"A-...","device_id":"D-...","endpoint":"x402","type":"x402.service_call","nonce":"...","payload_hash":"sha256:...","decision":"accepted"}
```

Rejections also log, with `decision` ∈ `{rate_limited, replay,
type_unregistered, payload_too_large, parse_error}`. Payload
bodies are **not** logged in full — just a content hash — because
they can carry sensitive data (API responses, message contents).

Audit log location is an open question (see README). Default
proposal: `os.UserConfigDir()/sky10/comms.jsonl` with rotation.

## Backpressure

Per-endpoint, per-(agent, envelope-type) token bucket. Each
`TypeSpec` declares its rate limit at registration. Per-endpoint
queue is bounded; on overflow the plumbing replies with
`comms.busy` and drops oldest envelopes (default policy; subject
to open question).

Per-endpoint quotas are independent. An x402-flooding agent does
not affect its messengers connection (when that ships) or its
chat connection.

## Cross-device behavior

When an agent on device A calls a host capability whose handler
runs on device B:

1. Agent → local daemon (device A) over the appropriate `/comms/...`
   websocket as usual.
2. Local daemon recognizes the envelope is targeted at a remote
   device (via routing fields or handler hint).
3. Local daemon forwards over skylink to device B's daemon,
   preserving `agent_id` and `device_id` (originating).
4. Device B's daemon dispatches to the correct capability handler.
5. Response envelope reverses the route.

Transparent to the agent. Latency higher (libp2p hop), bounded by
relay availability.

## Where drive sync stays separate

`pkg/fs` already does encrypted, libp2p-replicated drive sync
between sky10 daemons. It remains the right path for:

- bulk content (large API response bodies the agent might cache)
- per-agent state directories
- anything large enough that JSON-framing it on a comms socket
  would be wasteful

A comms envelope can *trigger* drive sync — e.g. a hypothetical
future `messengers.message_arrived` envelope carries a stable
drive path; the agent reads the body locally from its drive. Comms
itself does not carry the bytes.

## x402 capability shape

The handlers in `pkg/sandbox/comms/x402/` are thin: each one
unmarshals the payload, validates, calls into `pkg/x402` with
`requester = env.AgentID` so the catalog/budget/policy code
applies the agent's scope, and returns. No business logic lives
in the handlers. See [`docs/work/current/x402/`](../x402/) for
the host-side x402 design behind these handlers.
