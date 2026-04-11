---
created: 2026-04-09
updated: 2026-04-11
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
    RouteHint   string // optional sky10 address for public-network routing
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
- `handed_off`
- `delivered`
- `delivery_failed`
- `seen`
- `claimed`
- `assigned`
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

The current implementation uses:

- a separate `mailbox/sky10` scope in KV-backed durable state
- direct skylink delivery for addressed items when `Principal.RouteHint`
  resolves to a live sky10 address
- durable sender-side retry when direct delivery is unavailable
- sealed Nostr relay/dropbox handoff when direct sky10 delivery is unavailable
- recipient relay polling plus sender delivery receipts for store-and-forward
- lease-backed queue semantics for sky10-network queue items once they land on
  a mailbox host

Routing rule:

- `Scope == sky10_network` means the item belongs in the sky10-network mailbox
  backend
- `To.RouteHint` is the public sky10 address used to reach the remote mailbox
  host
- if `RouteHint` is absent and `To.ID` is itself a sky10 address, `To.ID` is
  used as the route target

Indirect payload rule:

- if a sky10-network payload must spill out of the inline envelope, store it as
  a sealed opaque object for the recipient
- the ref that remains in mailbox state may be public, but the payload bytes
  must stay sealed to the intended recipient

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

- [x] Add mailbox wiring in `commands/serve.go`.
- [x] Keep direct skylink send as the first attempt.
- [x] Integrate mailbox fallback into `pkg/agent/router.go`.
- [x] Add drain-on-registration behavior.
- [x] Add reconnect-triggered drain behavior.
- [x] Emit mailbox-related SSE events.
- [x] Add integration tests for offline device delivery.
- [x] Add integration tests for late agent registration.

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

- [x] Define approval item flow.
- [x] Define payment item flow.
- [x] Define result and receipt flow.
- [x] Add idempotency checks and nonce rules.
- [x] Add replay/retry tests for interrupted flows.
- [x] Add duplicate-delivery tests for idempotent processing.

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

- [x] Add `agent.mailbox.*` RPC methods.
- [x] Add mailbox SSE events.
- [x] Add inbox view.
- [x] Add outbox view.
- [x] Add approvals center.
- [x] Add task queue monitor.
- [x] Add item timeline UI.

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

- [x] Define network mailbox backend interface.
- [x] Define addressed sky10-network delivery fallback.
- [x] Define claimable public-network queue behavior.
- [x] Define encryption requirements for indirect storage.
- [x] Add public-network backend implementation.
- [x] Add integration tests for online direct delivery plus durable fallback.

### M7: Public-Network Store-and-Forward Backend

Add a real offline delivery backend for addressed sky10-network recipients.

Primary areas:

- relay/dropbox mailbox backend
- sealed envelope format
- sender outbox handoff and retry
- recipient poll or subscription ingestion

Exit criteria:

- addressed sky10-network items can be handed off while the recipient daemon is
  fully offline
- relays or dropboxes only see sealed payloads plus minimal routing metadata
- recipient can ingest handed-off items and materialize them into mailbox state
- sender can observe handoff, delivery, and terminal resolution separately

Checklist:

- [x] Define relay/dropbox object model for addressed mailbox items.
- [x] Define sealed envelope metadata versus ciphertext boundary.
- [x] Add a network store-and-forward backend interface.
- [x] Implement sender-side handoff from outbox to relay/dropbox.
- [x] Implement recipient-side poll or subscribe ingestion path.
- [x] Add replay protection and duplicate-handoff idempotency rules.
- [x] Add tests for sender offline-to-online retry plus recipient late pickup.
- [x] Add tests proving relays cannot read sealed payload contents.

### M8: Public Capability Queue Discovery and Claim Routing

Extend mailbox queue semantics so work can go to any suitable public-network
agent, not just an addressed recipient.

Primary areas:

- public capability advertisement
- queue discovery and filtering
- sealed claim propagation
- result and receipt return routing

Exit criteria:

- a sender can publish a claimable public-network task without naming a single
  recipient
- eligible public agents can discover queue items by capability or queue name
- one claimant wins at a time with sender-authoritative lease arbitration
- results and receipts route back to the original sender mailbox cleanly

Checklist:

- [x] Define public capability queue record format.
- [x] Define discovery path for queue offers and claimable tasks.
- [x] Define sealed queue-claim propagation back to the sender mailbox.
- [x] Implement claimant-side acquisition through sealed queue claims.
- [x] Implement sender-side assignment and result routing for claimed public tasks.
- [x] Add conflict tests for concurrent public claims.
- [x] Add end-to-end tests for public task offer, claim, result, and receipt.

### M9: Mailbox Lifecycle Policy and Cleanup

Make mailbox behavior predictable over time instead of relying on open-ended
queue retention.

Primary areas:

- TTL defaults by item kind
- expiry and dead-letter transitions
- ack semantics
- retention and garbage collection

Exit criteria:

- each mailbox item kind has documented TTL and retry defaults
- expired items transition deterministically into terminal states
- ack behavior is explicit and consistent across human, agent, and queue flows
- old terminal state can be compacted or garbage-collected safely

Checklist:

- [ ] Define default TTLs for message, approval, payment, result, and receipt items.
- [ ] Define retry budgets and backoff rules by item kind.
- [ ] Decide whether `ack` stays explicit or becomes implicit on later transitions.
- [ ] Implement expiry scanning and terminal-state transitions.
- [ ] Implement dead-letter handling for permanently undeliverable items.
- [ ] Define retention and compaction policy for items, events, and claims.
- [ ] Add tests for expiry, dead-letter, and cleanup behavior.

### M10: Principal Views and Product Model

Decide how humans, local agents, and public agents should see and act on the
same underlying mailbox state.

Primary areas:

- principal-scoped mailbox projections
- permissions and action boundaries
- UI grouping and filtering
- workflow ownership model

Exit criteria:

- the product model clearly defines whether humans and agents share a mailbox
  view or use separate projections
- RPC methods can query mailbox state by principal and role cleanly
- the web UI presents human and agent work without ambiguous ownership
- approval and payment actions are available to the right principals only

Checklist:

- [ ] Define the product rule for shared versus separate principal mailbox views.
- [ ] Define mailbox permissions for human, local-agent, and public-agent actors.
- [ ] Add principal- and role-scoped list/query APIs where needed.
- [ ] Update the UI information architecture around principal views.
- [ ] Add tests for authorization and projection boundaries.

### M11: Debug and Operations Tooling

Make mailbox failures and stuck workflows operable without dropping into raw KV
inspection.

Primary areas:

- raw mailbox debug views
- delivery-attempt visibility
- request and reply correlation
- repair and retry tooling

Exit criteria:

- an operator can inspect one mailbox item end-to-end from item creation
  through delivery attempts and terminal state
- delivery failures, retry reasons, and claim state are visible without
  inspecting KV directly
- request and reply chains can be traced by request id or reply target
- repair actions exist for common stuck states

Checklist:

- [ ] Add request-id, reply-to, queue, and principal filters to mailbox views.
- [ ] Add a debug panel or route for raw item, event, claim, and payload-ref inspection.
- [ ] Surface delivery attempts, retry reasons, and last-error details in the UI.
- [ ] Add repair actions for retry, dead-letter replay, and claim release.
- [ ] Add operational tests or fixtures for common failure scenarios.

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
7. real public-network store-and-forward
8. public capability queue routing
9. lifecycle and cleanup policy
10. principal views and permissions
11. debug and operations tooling

That order keeps the first version narrow, useful, and aligned with the
reliability gaps we already have today.
