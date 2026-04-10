---
created: 2026-04-09
updated: 2026-04-09
model: gpt-5-codex
---

# Mailbox

## Overview

`mailbox` is the durable control-plane layer for agent and human messaging.
It sits above direct skylink transport.

Direct skylink stays the fast path for online peers. Mailbox adds:

- durable inbox/outbox state
- retry and reconnect delivery
- claim/lease semantics for queues
- resumable task and payment workflows
- a single async model across the private network and the sky10 network

The key distinction is:

- **transport** answers "can I reach the peer right now?"
- **mailbox** answers "can this work survive failure and complete later?"

## Why Mailbox

Today agent messaging is live-first and brittle:

- direct delivery works when both sides are online and connected
- transient discovery or reconnect failures surface as hard send failures
- late agent registration loses timing-sensitive work
- payment and approval flows have no durable recovery point

Mailbox turns those failure modes into queued, observable state.

Examples:

- an agent asks a human for approval while the user is offline
- one agent asks another agent to do work, but the destination device reconnects later
- `payment_required` is generated, but the caller disappears before acting
- `payment_proof` is produced, but the provider crashes before sending `result`
- a task needs to go "somewhere" rather than to one preselected target

## Goals

- Keep direct skylink as the fast path for online request/response.
- Add durable inbox/outbox semantics for async work.
- Support both addressed delivery and claimable queues.
- Make payment, approval, and task flows resumable after restart or reconnect.
- Reuse existing KV sync for private-network durability.
- Keep one mailbox model while allowing multiple storage/delivery backends.

## Non-Goals

- Replace direct skylink for streaming or low-latency chat.
- Turn base KV into a Redis clone.
- Guarantee exactly-once processing over an eventually connected network.
- Put private mailbox contents into public gossip state.
- Ship full public-network offline delivery before the private-network mailbox is stable.

## Design Principles

1. Persist first, deliver second.
2. At-least-once delivery with idempotency keys.
3. Append-only events, not mutable records.
4. Use leases for queues, not destructive pop.
5. Keep payload envelopes small; spill larger bodies to blob refs.
6. One mailbox model, multiple backends.

## Terminology

- **Principal**: the durable identity that owns mailbox state. A principal may
  be a human identity, a local agent identity, or a public agent identity.
- **Mailbox**: the durable lifecycle system spanning inbox, outbox, sent, and
  failed/dead-letter views.
- **Inbox**: items addressed to a principal.
- **Outbox**: items initiated by a principal and still awaiting terminal state.
- **Queue**: mailbox items that can be claimed by one eligible worker.
- **Item**: the durable envelope for a unit of work or a protocol step.
- **Event**: an immutable transition record for an item.
- **AppendLog**: an append-only durable sequence used as the base primitive
  for mailbox state.
- **AppendLogEntry**: one immutable record in an `AppendLog`.
- **Lease**: a time-bounded claim on a queue item.
- **PayloadRef**: a pointer to payload data stored outside the inline mailbox
  envelope, such as a chunked value or `skyfs` object.

## Delivery Model

### Fast Path

When a recipient is reachable now, the sender should still use direct skylink
first. Mailbox does not replace live delivery.

### Durable Path

Every async send writes durable state before it is considered accepted:

1. write item + initial events to outbox
2. attempt direct delivery
3. if direct delivery succeeds, wait for ack/claim/response events
4. if direct delivery fails or is deferred, route via mailbox backend
5. retry until terminal state or TTL expiry

### Semantics

Mailbox should be explicitly:

- **delivery**: at-least-once
- **processing**: at-most-once only when the worker respects leases and
  idempotency keys
- **correctness**: enforced by protocol state machines, not by transport magic

Required ids:

- `item_id`
- `request_id`
- `reply_to`
- `idempotency_key`
- protocol-specific nonce when needed, such as `payment_nonce`

## Data Model

### Principal

```go
type Principal struct {
    ID          string // durable identity or agent identity
    Kind        string // human, local_agent, network_agent, capability_queue
    Scope       string // private_network or sky10_network
    DeviceHint  string // optional direct-delivery hint
}
```

### Mailbox Item

```go
type Item struct {
    ID             string
    Kind           string // task_request, approval_request, payment_required, ...
    From           Principal
    To             *Principal
    TargetSkill    string // optional capability queue target
    SessionID      string
    RequestID      string
    ReplyTo        string
    IdempotencyKey string
    PayloadRef     string // optional blob/chunk ref
    PayloadInline  []byte // small envelope only
    Priority       string
    ExpiresAt      time.Time
    CreatedAt      time.Time
}
```

### Mailbox Event

```go
type Event struct {
    ItemID     string
    EventID    string
    Type       string // created, delivered, seen, claimed, approved, ...
    Actor      Principal
    LeaseID    string
    Error      string
    Timestamp  time.Time
    Meta       map[string]string
}
```

### Item Kinds

Initial supported kinds:

- `message`
- `task_request`
- `approval_request`
- `payment_required`
- `payment_proof`
- `result`
- `receipt`
- `error`

### Event Types

Initial supported event types:

- `created`
- `delivery_attempted`
- `delivered`
- `delivery_failed`
- `seen`
- `claimed`
- `lease_expired`
- `approved`
- `rejected`
- `completed`
- `cancelled`
- `expired`
- `dead_lettered`

## Storage Model

### Base Primitives

Mailbox should be built on top of KV, not inside the base KV API.

Needed library primitives:

- `appendlog`
  - append-only durable event sequence
- `lease`
  - claim and expiry semantics for queue work
- `payloadref`
  - pointer to chunked value or `skyfs` object for oversized payloads

Potential package layout:

- `pkg/kv/collections/appendlog.go`
- `pkg/kv/collections/lease.go`
- `pkg/kv/collections/payloadref.go`

### Private-Network KV Backend

The first backend should use a dedicated encrypted KV namespace, for example
`mailbox`.

Suggested key layout:

```text
mailbox/items/<principal>/<item_id>
mailbox/events/<principal>/<item_id>/<event_id>
mailbox/leases/<queue>/<item_id>
mailbox/index/outbox/<principal>/<item_id>
mailbox/index/inbox/<principal>/<item_id>
```

The exact key format is less important than the rule that mailbox state is
append-only and reconstructible from immutable records.

### Payload Sizing

Base KV should keep the current inline-value discipline. Mailbox should not
require increasing KV's global value cap.

Rules:

- keep the inline envelope small
- put larger bodies in chunked values or `skyfs`
- store only refs in mailbox items/events

## Views

Mailbox state should materialize into these views:

- `Inbox(principal)`
- `Outbox(principal)`
- `Queue(queue_principal or capability)`
- `Sent(principal)`
- `Failed(principal)`

These are derived views, not separate sources of truth.

## Queue and Claim Model

For "this task needs to go somewhere", mailbox uses a queue view plus leases.

Flow:

1. sender creates a claimable item targeting a queue principal or capability
2. eligible workers discover the item
3. one worker acquires a lease
4. that worker processes and emits progress/completion
5. if the worker disappears, the lease expires and another worker may claim

Mailbox does not use destructive pop. Queue behavior should be modeled as
`AppendLog + Lease`.

## Routing Model

Mailbox routing should be explicit and pluggable.

### Private Network

- direct skylink first
- fallback to private-network KV mailbox
- drain on reconnect or agent registration

### sky10 Network

The long-term model needs a separate backend:

- direct skylink for online addressed delivery
- durable network mailbox/store-and-forward for offline recipients
- capability queue support for unaddressed task routing

The mailbox item format stays the same; only the backend changes.

## Payment and Approval Flows

Mailbox should carry the control-plane state for payment and approval protocols.

It does **not** make those protocols correct on its own. Each protocol still
needs explicit ids and terminal states.

### Payment State Machine

Suggested states:

- `requested`
- `quoted`
- `proof_sent`
- `proof_verified`
- `work_completed`
- `result_delivered`
- `receipt_finalized`
- `cancelled`
- `expired`

Mailbox benefits:

- recover after disconnect
- recover after agent crash
- retry result/receipt delivery
- preserve a durable audit trail

### Approval Flow

Suggested states:

- `pending_approval`
- `approved`
- `rejected`
- `expired`
- `completed`

Mailbox benefits:

- human can approve on any device in the private network
- agent can wait on durable state instead of transient SSE
- approvals survive daemon restarts

## RPC and UI Surface

Mailbox should eventually expose explicit RPCs, not overload `agent.send`.

Suggested RPC namespace:

- `agent.mailbox.send`
- `agent.mailbox.listInbox`
- `agent.mailbox.listOutbox`
- `agent.mailbox.get`
- `agent.mailbox.claim`
- `agent.mailbox.release`
- `agent.mailbox.ack`
- `agent.mailbox.approve`
- `agent.mailbox.reject`
- `agent.mailbox.complete`
- `agent.mailbox.retry`

Suggested SSE events:

- `agent.mailbox.updated`
- `agent.mailbox.claimed`
- `agent.mailbox.completed`

Suggested web surfaces:

- inbox view
- outbox view
- approvals center
- task queue monitor
- per-item timeline

## Package and File Targets

### KV Collections

- `pkg/kv/collections/appendlog.go`
- `pkg/kv/collections/lease.go`
- `pkg/kv/collections/payloadref.go`

### Mailbox Core

- `pkg/agent/mailbox/types.go`
- `pkg/agent/mailbox/store.go`
- `pkg/agent/mailbox/backend.go`
- `pkg/agent/mailbox/private_kv.go`
- `pkg/agent/mailbox/index.go`

### Agent Integration

- `pkg/agent/router.go`
- `pkg/agent/rpc.go`
- `pkg/agent/registry.go`
- `pkg/agent/link.go`
- `commands/serve.go`

### UI and RPC

- `web/src/lib/rpc.ts`
- `web/src/lib/events.ts`
- `web/src/pages/`

## Milestones

### M0: Semantics and Design

Define the canonical mailbox model.

Scope:

- glossary and terminology
- item and event schema
- delivery semantics
- private-network vs sky10-network routing boundary
- sizing and payload rules

Exit criteria:

- one mailbox glossary
- one item/event model
- one delivery semantic: at-least-once + idempotency
- one routing split: direct transport vs durable backend

Checklist:

- [ ] Confirm mailbox naming and glossary.
- [ ] Confirm item and event schema.
- [ ] Confirm delivery semantics: at-least-once + idempotency.
- [ ] Confirm queue semantics: claim + lease, not destructive pop.
- [ ] Confirm payload sizing and `PayloadRef` rules.
- [ ] Confirm private-network-first scope for initial implementation.

### M1: KV Collections

Implement the reusable replicated primitives on top of KV.

Primary files:

- `pkg/kv/collections/appendlog.go`
- `pkg/kv/collections/lease.go`
- `pkg/kv/collections/payloadref.go`

Exit criteria:

- append-only `AppendLog` abstraction
- lease abstraction with expiry
- `PayloadRef` helper for oversized payloads
- tests for replay, rebuild, and concurrency edges

Checklist:

- [x] Create `pkg/kv/collections`.
- [x] Implement `AppendLog`.
- [x] Define `AppendLogEntry`.
- [x] Implement `Lease`.
- [x] Implement `PayloadRef`.
- [x] Add unit tests for replay and rebuild.
- [x] Add unit tests for lease expiry and re-claim.
- [x] Add unit tests for oversized payload indirection.

### M2: Private-Network Mailbox Backend

Build `pkg/agent/mailbox` on top of a dedicated KV namespace.

Primary files:

- `pkg/agent/mailbox/types.go`
- `pkg/agent/mailbox/store.go`
- `pkg/agent/mailbox/backend.go`
- `pkg/agent/mailbox/private_kv.go`
- `pkg/agent/mailbox/index.go`

Exit criteria:

- durable inbox/outbox views
- item timeline reconstruction from events
- queue claim/lease support
- restart-safe state reload

Checklist:

- [x] Create `pkg/agent/mailbox`.
- [x] Define mailbox item types.
- [x] Define mailbox event types.
- [x] Implement store and materialized views.
- [x] Implement dedicated private-network KV backend.
- [x] Implement rebuild-on-start from persisted state.
- [x] Implement inbox projection.
- [x] Implement outbox projection.
- [x] Implement queue projection.
- [x] Add mailbox unit tests.

### M3: Agent Routing Integration

Integrate mailbox into agent messaging without replacing direct skylink.

Primary files:

- `pkg/agent/router.go`
- `pkg/agent/rpc.go`
- `pkg/agent/registry.go`
- `pkg/agent/link.go`
- `commands/serve.go`

Exit criteria:

- direct send remains first attempt
- fallback to mailbox on temporary failure or deferred delivery
- drain on agent registration
- drain on reconnect

Checklist:

- [ ] Add mailbox wiring in `commands/serve.go`.
- [ ] Keep direct skylink send as the first attempt.
- [ ] Integrate mailbox fallback into `pkg/agent/router.go`.
- [ ] Add drain-on-registration behavior.
- [ ] Add reconnect-triggered drain behavior.
- [ ] Emit mailbox-related SSE events.
- [ ] Add integration tests for offline device delivery.
- [ ] Add integration tests for late agent registration.

### M4: Task, Approval, and Payment Workflows

Add protocol-specific state machines above mailbox.

Primary areas:

- mailbox item kinds
- approval flow
- payment flow
- result and receipt delivery

Exit criteria:

- approval requests persist and resolve cleanly
- payment protocol messages survive crash/reconnect
- retry and dedupe rules are explicit

Checklist:

- [ ] Define approval item flow.
- [ ] Define payment item flow.
- [ ] Define result and receipt flow.
- [ ] Add idempotency checks and nonce rules.
- [ ] Add replay/retry tests for interrupted flows.
- [ ] Add duplicate-delivery tests for idempotent processing.

### M5: RPC and Web UX

Make mailbox observable and operable.

Primary files:

- `web/src/lib/rpc.ts`
- `web/src/lib/events.ts`
- `web/src/pages/`

Exit criteria:

- mailbox RPCs are available
- web inbox/outbox views exist
- approval actions work from UI
- item timelines are visible

Checklist:

- [ ] Add `agent.mailbox.*` RPC methods.
- [ ] Add mailbox SSE events.
- [ ] Add inbox view.
- [ ] Add outbox view.
- [ ] Add approvals center.
- [ ] Add task queue monitor.
- [ ] Add item timeline UI.

### M6: sky10-Network Backend

Add the public-network mailbox backend after private-network behavior is stable.

Primary areas:

- network mailbox backend
- addressed delivery fallback
- claimable public-network tasks
- indirect-storage encryption rules

Exit criteria:

- addressed sky10-network delivery can fall back to durable mailbox storage
- claimable public-network tasks have lease semantics
- encrypted mailbox payloads are preserved when indirect storage is used

Checklist:

- [ ] Define network mailbox backend interface.
- [ ] Define addressed sky10-network delivery fallback.
- [ ] Define claimable public-network queue behavior.
- [ ] Define encryption requirements for indirect storage.
- [ ] Add public-network backend implementation.
- [ ] Add integration tests for online direct delivery plus durable fallback.

## Open Questions

- Should human principals and agent principals use one shared mailbox view or
  separate views over the same store?
- Should mailbox acks be explicit events or implicit on claim/complete?
- How much protocol state belongs in mailbox versus per-workflow reducers?
- What is the right TTL default for payment/approval items?
- Which public-network backend comes first: relay/dropbox, sealed channel, or
  direct-only with later durable fallback?

## Current Recommendation

Ship mailbox in this order:

1. semantics and collections
2. private-network KV mailbox
3. agent routing fallback
4. approval/payment flows
5. web and RPC surfaces
6. sky10-network backend

That order keeps the first version narrow, useful, and aligned with the
reliability gaps we already have today.
