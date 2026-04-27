---
created: 2026-04-26
updated: 2026-04-26
model: claude-opus-4-7
---

# Envelope Type Registry

Initial set of envelope types covering the workloads we know about
today. Each row is one handler file under `pkg/bus/envelopes/`.

Direction shorthand:

- **RR** — request/response (sync-on-async, agent expects an answer)
- **Push** — host → agent, fire-and-forget
- **Sub** — agent subscribes; host pushes updates over time

`Scope` describes how the handler scopes its work to the calling
identity. Every handler enforces scope — the bus does not.

## chat.*

| Type | Direction | Purpose | Scope |
|---|---|---|---|
| `chat.send` | Push (agent → human) | Agent sends a chat message to the user (or to another agent over the existing chat router) | by agent_id |
| `chat.receive` | Push (human → agent) | User sends a chat message to the agent | by agent_id |
| `chat.tool_call` | Push | Existing OpenClaw tool call message | by agent_id |
| `chat.tool_result` | Push | Existing OpenClaw tool result message | by agent_id |
| `chat.permission` | RR | Existing OpenClaw permission prompt | by agent_id |
| `chat.diff` | Push | Existing OpenClaw diff message | by agent_id |

These are the existing chat_websocket message types relabelled as
envelopes. The migration plan covers backward compatibility.

## messaging.*

| Type | Direction | Purpose | Scope |
|---|---|---|---|
| `messaging.search` | RR | Search messages across the agent's authorized connections | connections owned by agent_id |
| `messaging.send` | RR | Send a message via a host-side broker connection | connection must be owned by agent_id |
| `messaging.list_connections` | RR | List the connections this agent is authorized to use | by agent_id |
| `messaging.message_arrived` | Push | Host pushes a new inbound message reference (body delivered via drive sync) | host → agent_id |
| `messaging.message_sent` | Push | Send confirmation / failure for an outbox attempt | host → agent_id |

Handler-side scope: the messaging broker on host already accepts a
`secrets.Requester` — handlers set `requester = agent_id` and the
broker filters connections to those owned by that requester. Same
pattern for `searchMessages`, `searchConversations`, etc.

Bulk message bodies travel via drive sync, not over the bus. The
`messaging.message_arrived` envelope carries a stable drive path; the
agent reads the body from the drive locally.

## wallet.*

| Type | Direction | Purpose | Scope |
|---|---|---|---|
| `wallet.balance_subscribe` | Sub | Agent subscribes to balance updates | by agent_id |
| `wallet.balance_update` | Push | Host pushes balance change | host → agent_id |
| `wallet.transfer` | RR | Initiate a USDC transfer within agent's per-call cap and daily budget | by agent_id, budget-bounded |
| `wallet.transfer_signed` | RR (response) | Signed transfer result | host → agent_id |
| `wallet.history` | RR | List recent transactions | by agent_id |

`wallet.transfer` is the only envelope that can move money outside
of x402. Subject to per-agent caps configured separately from x402's
caps. Most agents will never need `wallet.transfer` — this exists for
the specific case where an agent legitimately needs to pay another
party (e.g. agent-to-agent commerce on the sky10 network).

## secrets.*

The secrets API on the bus is **deliberately narrow**. Agents
**cannot read raw secrets**. They can only request scoped, time-
limited tokens for specific upstream services that the user has
authorized for that agent.

| Type | Direction | Purpose | Scope |
|---|---|---|---|
| `secrets.issue_scoped_token` | RR | Request a scoped token for an authorized upstream service (e.g. a Slack bot token usable only for read operations on listed channels) | by agent_id, by service, by scope |
| `secrets.token_issued` | RR (response) | Token, expiry, scope description | host → agent_id |
| `secrets.list_authorizations` | RR | What upstream services can this agent request tokens for? | by agent_id |

Notably absent: `secrets.get`. There is no envelope type that returns
a raw secret to an agent. Ever. Adding one is a deliberate code
change with review.

## x402.*

| Type | Direction | Purpose | Scope |
|---|---|---|---|
| `x402.list_services` | RR | List approved services for this agent, with price hints and tier | by agent_id |
| `x402.service_call` | RR | Invoke an approved x402 service: handler builds the request, signs the 402 challenge, posts, returns the response | by agent_id, by service approval, by per-call cap |
| `x402.budget_status` | RR | Caps and current spend | by agent_id |
| `x402.budget_changed` | Push | Host notifies when budget caps change (e.g. user lowered the daily cap) | host → agent_id |
| `x402.changes` | Push | New / risky / removed services in the latest catalog refresh | host → agent_id |

`x402.service_call` is the load-bearing one. Payload includes
`{ service_id, path, body, headers, max_price_usdc }`. Handler
verifies the service is approved for this agent, performs the full
402 round-trip on host (agent never sees the challenge), returns the
upstream response. See [`docs/work/current/x402/`](../x402/) for the
x402-specific catalog, refresh, and budget design that lives behind
this envelope type.

## home.*

The existing `home.*` RPC methods (history, runs, etc.) get exposed
through the bus the same way: one envelope type per method, one
handler file per envelope type. Migration is mechanical and out of
scope for this initial plan.

## How to add a new envelope type

Checklist (also enforced by the registration code and arch-test):

1. Pick a namespaced type name (`<area>.<verb>` or `<area>.<noun>`).
2. Create `pkg/bus/envelopes/<area>_<verb>.go` with the mandatory
   header comment.
3. Define the typed payload struct **inside the handler file** (not
   shared with other handlers).
4. Write the handler. First non-trivial statements must be
   unmarshal + validate; arch-test will fail otherwise.
5. Register the type with full `TypeSpec` metadata. Compile fails
   if any required field is zero.
6. Add a row to this document.
7. If the envelope is `RR`, declare the response envelope type too
   (`<request>.<noun>` → `<request>.<noun>_response` is the convention
   when no specific response name fits, e.g. `x402.budget_status` →
   `x402.budget_status_response`).

If a proposed envelope type would expose a generic capability (e.g.
"call any messaging method"), reject it. Split it into the specific
operations the agent actually needs.
