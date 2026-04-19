---
created: 2026-04-18
updated: 2026-04-18
model: gpt-5.4
---

# Messaging Broker — Implementation Plan

This document turns the messaging architecture draft in
[`README.md`](./README.md) into a milestone plan with concrete checklists.

## Scope

This plan covers:

- the core `pkg/messaging` domain
- broker-owned policy and approval flow
- external platform adapter protocol
- external agent shim protocol
- initial storage and runtime supervision
- first-party adapters for a small launch set

This plan does not yet cover:

- every possible messaging platform
- public plugin marketplace/distribution UX
- billing or paid adapter ecosystem mechanics
- open network messaging between strangers
- final end-user UI polish

## Milestone Status Snapshot

| Milestone | Status | Outcome |
| --- | --- | --- |
| 0. Terminology And Architecture | done | Naming and architecture draft exist |
| 1. Core Domain And Storage | not started | `pkg/messaging` types and persistence backbone |
| 2. Adapter Protocol And Runtime Host | not started | External adapter process contract and supervision |
| 3. Broker Core And Event Flows | not started | Normalize inbound/outbound flow through broker |
| 4. Policy And Approval Engine | not started | Broker-enforced permissions and durable approvals |
| 5. Agent Shim Protocol | not started | One thin runtime-facing messaging surface |
| 6. First-Party Adapters | not started | Initial platform coverage |
| 7. UI And Operator Surfaces | not started | Connections, conversations, drafts, approvals |
| 8. Reliability, Security, And Packaging | not started | Cross-platform runtime, recovery, and release story |

## Milestone 0: Terminology And Architecture

### Goal

Lock the core nouns and high-level shape before implementation spreads naming
mistakes through the codebase.

### Checklist

- [x] Define user-facing terminology: Messaging, platform, connection,
  conversation, message, draft, approval.
- [x] Define code-facing terminology: adapter, connection, auth info,
  identity, policy, exposure, broker, shim.
- [x] Decide that the core package should be `pkg/messaging`.
- [x] Decide that adapters and shims should be external executables with stable
  protocols.
- [x] Decide that the broker owns policy, approvals, and storage.
- [x] Record the architecture and terminology in
  [`README.md`](./README.md).

### Exit Criteria

- [x] There is one canonical terminology doc for messaging.
- [x] The package naming direction is clear enough to begin implementation.

## Milestone 1: Core Domain And Storage

### Goal

Create the durable `pkg/messaging` model and persistence layer without yet
committing to any one platform adapter.

### Checklist

- [ ] Create `pkg/messaging` with the first normalized types:
  `Connection`, `AuthInfo`, `Identity`, `Conversation`, `Participant`,
  `Message`, `Draft`, `Policy`, `Exposure`, `Event`, `Capability`.
- [ ] Define stable ID types for connections, identities, conversations,
  messages, drafts, policies, and exposures.
- [ ] Decide which records are durable truth versus derived cache.
- [ ] Add a storage package for messaging state, likely under
  `pkg/messaging/store`.
- [ ] Persist connections and auth metadata references separately from raw
  secret material.
- [ ] Persist identities discovered through adapters.
- [ ] Persist conversation metadata and message indexes.
- [ ] Persist drafts and their lifecycle state.
- [ ] Persist adapter poll checkpoints and webhook verification state.
- [ ] Persist audit history for inbound, draft, approval, and send events.
- [ ] Ensure Windows-safe data paths and filenames from the start.

### Exit Criteria

- [ ] The broker can restart without losing connection, identity,
  conversation, message, draft, or checkpoint state.
- [ ] Secrets remain in `pkg/secrets` or equivalent secret storage, not inside
  messaging records.
- [ ] The domain model is broad enough to support email, Slack-style threads,
  and phone-number-based messaging.

## Milestone 2: Adapter Protocol And Runtime Host

### Goal

Define and host external platform adapters as supervised local processes.

### Checklist

- [ ] Create `pkg/messaging/protocol` for the southbound adapter contract.
- [ ] Define `Describe`, `ValidateConfig`, `Connect`, `Refresh`,
  `ListIdentities`, `ListConversations`, `ListMessages`, `GetMessage`,
  `CreateDraft`, `UpdateDraft`, `DeleteDraft`, `SendMessage`, `ReplyMessage`,
  `HandleWebhook`, `Poll`, and `Health`.
- [ ] Define a capability declaration shape that adapters return from
  `Describe`.
- [ ] Define a normalized webhook request envelope so the broker can own public
  HTTP ingress.
- [ ] Define polling checkpoints and retry semantics.
- [ ] Build `pkg/messaging/runtime` or equivalent for adapter process
  supervision.
- [ ] Support adapter start, stop, restart, health check, and backoff.
- [ ] Support adapter stdout/stderr capture for operator diagnostics.
- [ ] Decide whether the first protocol transport is stdio, local HTTP, or
  local JSON-RPC.
- [ ] Add protocol compatibility/version negotiation.

### Exit Criteria

- [ ] A dummy adapter process can be launched, described, health-checked, and
  called by the broker.
- [ ] Broker-owned webhooks can be forwarded to an adapter and converted into
  normalized events.
- [ ] Adapter restart does not corrupt broker state.

## Milestone 3: Broker Core And Event Flows

### Goal

Implement the broker as the one authority for inbound normalization,
outbound orchestration, and event fanout.

### Checklist

- [ ] Create `pkg/messaging/broker`.
- [ ] Add connection lifecycle management: create, refresh, disable, delete.
- [ ] Add identity refresh/discovery flow from adapters.
- [ ] Add conversation upsert logic.
- [ ] Add message upsert logic for inbound and outbound records.
- [ ] Add draft lifecycle management.
- [ ] Add normalized inbound event ingestion from both webhook and polling
  sources.
- [ ] Add outbound operations that always flow through the broker.
- [ ] Add event fanout to UI and northbound shims.
- [ ] Add deduplication/idempotency for inbound events and outbound send
  results.
- [ ] Add delivery status and edit/delete state updates when supported.

### Exit Criteria

- [ ] Inbound events from an adapter become durable conversation/message state.
- [ ] Drafts and sends can be created without platform-specific code leaking
  into the broker.
- [ ] The broker is the only place that decides whether a send proceeds.

## Milestone 4: Policy And Approval Engine

### Goal

Make policy and approval the real control boundary before any runtime gets
meaningful messaging power.

### Checklist

- [ ] Create `pkg/messaging/policy`.
- [ ] Define broker-enforced policy rules for:
  read inbound, draft replies, send replies, start new conversations,
  attachment handling, allowed identities, allowed connection scopes,
  allowed time windows.
- [ ] Support connection-level default policy.
- [ ] Support exposure-level narrowed policy for a specific agent/runtime.
- [ ] Define durable approval request objects for sends and other sensitive
  operations.
- [ ] Reuse or integrate the repo's existing mailbox/approval primitives where
  practical instead of creating an unrelated second approval engine.
- [ ] Add approval statuses and audit timeline.
- [ ] Add explicit refusal reasons when an operation is denied by policy.
- [ ] Add tests for approval-required, reply-only, no-new-recipient, and
  attachment-blocked paths.

### Exit Criteria

- [ ] A draft can be blocked, approved, or sent based on policy.
- [ ] Agents cannot bypass broker approval by calling adapters directly.
- [ ] Operators can inspect why an action was denied or held for approval.

## Milestone 5: Agent Shim Protocol

### Goal

Expose one thin runtime-facing messaging interface so agents learn `sky10`
messaging once instead of learning each platform separately.

### Checklist

- [ ] Create a northbound shim protocol with operations such as
  `ListConnections`, `ListIdentities`, `ListConversations`,
  `GetConversation`, `GetMessages`, `CreateDraft`, `UpdateDraft`,
  `RequestSend`, `SendDraft`, `MarkRead`, `SubscribeEvents`.
- [ ] Decide whether the default northbound path is MCP, local JSON-RPC, or
  both.
- [ ] Create a shim host or broker-facing surface for runtime-specific shims.
- [ ] Make sure shims never receive raw platform credentials.
- [ ] Ensure runtime-facing operations always respect exposures and policy.
- [ ] Add one reference shim implementation.
- [ ] Add tests proving that shims can read, draft, and request send without
  bypassing policy.

### Exit Criteria

- [ ] One runtime can consume normalized messaging without platform-specific
  code.
- [ ] The broker can expose one connection to one agent under a narrowed
  policy.
- [ ] A runtime can draft a reply and request send through the broker.

## Milestone 6: First-Party Adapters

### Goal

Ship a small first-party adapter set that proves the architecture across
different messaging shapes.

### Recommended Order

1. `gmail`
2. `email/imap-smtp`
3. `slack`
4. `telegram`
5. `microsoft365`
6. `whatsapp`

### Checklist

- [ ] Pick the first two adapters for MVP and keep the rest behind them.
- [ ] Implement a thread-oriented adapter (`gmail` or `slack`).
- [ ] Implement a mailbox-oriented adapter (`gmail` or `imap-smtp`).
- [ ] Implement one webhook-driven adapter.
- [ ] Implement one polling-driven adapter.
- [ ] Implement identity discovery for each adapter.
- [ ] Implement inbound normalization for each adapter.
- [ ] Implement draft/send or reply flow for each adapter.
- [ ] Implement auth refresh and expired-credential handling.
- [ ] Implement adapter-specific config validation.
- [ ] Add end-to-end tests for each first-party adapter path using mocks or
  deterministic fixtures.

### Exit Criteria

- [ ] At least two materially different messaging platforms work through the
  same broker model.
- [ ] The broker can handle multiple connections of the same adapter type,
  such as personal Gmail plus work Gmail or multiple Slack workspaces.
- [ ] Policy and approval behavior remain broker-owned across adapters.

## Milestone 7: UI And Operator Surfaces

### Goal

Make messaging usable and inspectable from the product, not only through
internal RPCs.

### Checklist

- [ ] Add a `Messaging` section in the UI.
- [ ] Add connection list and connection detail views.
- [ ] Show connection health, auth status, and exposed identities.
- [ ] Show conversations and message timelines.
- [ ] Show drafts and approval-needed queues.
- [ ] Show reply permissions and effective policy in plain language.
- [ ] Show adapter capability differences where they matter to users.
- [ ] Add operator diagnostics for webhook failures, poll failures, and auth
  expiry.
- [ ] Add retry and reconnect affordances.
- [ ] Add audit visibility for who drafted, approved, rejected, and sent.

### Exit Criteria

- [ ] An operator can connect a platform, inspect inbound messages, review a
  draft, and approve or reject a send.
- [ ] An operator can tell whether a connection is degraded because of auth,
  webhook failure, adapter crash, or policy denial.

## Milestone 8: Reliability, Security, And Packaging

### Goal

Harden the broker and sidecar model for cross-platform use and realistic
deployment.

### Checklist

- [ ] Decide how first-party adapters are installed and discovered.
- [ ] Decide how third-party adapters are installed and discovered.
- [ ] Add signature or provenance expectations for official adapter binaries.
- [ ] Add process isolation and least-privilege guidance for adapters.
- [ ] Add broker restart recovery tests.
- [ ] Add adapter crash and reconnection tests.
- [ ] Add webhook replay/idempotency tests.
- [ ] Add poll checkpoint recovery tests.
- [ ] Add multi-connection tests for the same adapter type.
- [ ] Add Windows-specific packaging checks for adapter sidecars.
- [ ] Decide whether Bun-compiled executables, static binaries, or another
  packaging model is the default for JS adapters.
- [ ] Add release/build documentation for official adapters and shims.

### Exit Criteria

- [ ] The messaging broker survives broker restart, adapter restart, and
  temporary platform outages without ambiguous state.
- [ ] Official adapters have a clear cross-platform packaging story.
- [ ] The architecture supports a first-party set and third-party extensions
  without changing the core model.

## Suggested First Execution Slice

The smallest useful implementation sequence is:

1. Milestone 1: core domain and storage
2. Milestone 2: adapter protocol and runtime host
3. Milestone 3: broker core
4. Milestone 4: policy and approvals
5. Milestone 6: two first-party adapters
6. Milestone 5: one reference agent shim

That ordering keeps the policy boundary in place before an agent runtime gets
real messaging authority, while still getting to a demonstrable product.

## Success Criteria For The First Real Milestone Set

The first meaningful external demo should satisfy all of these:

- [ ] Connect two separate messaging connections of the same platform type.
- [ ] Ingest inbound messages into normalized conversations.
- [ ] Expose those conversations to one agent through an exposure policy.
- [ ] Let the agent draft a reply.
- [ ] Hold or send that reply according to policy.
- [ ] Persist state across restart.
- [ ] Show the resulting state in the UI and audit timeline.
