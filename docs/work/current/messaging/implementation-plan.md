---
created: 2026-04-18
updated: 2026-04-26
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
| 1. Core Domain And Storage | in progress | `pkg/messaging` types and persistence backbone |
| 2. Adapter Protocol And Runtime Host | in progress | External adapter process contract and supervision |
| 3. Broker Core And Event Flows | in progress | Normalize inbound/outbound flow through broker |
| 4. Policy And Approval Engine | in progress | Broker-enforced permissions and durable approvals |
| 5. Agent Shim Protocol | in progress | One thin runtime-facing messaging surface |
| 6. First-Party Adapters | in progress | Initial platform coverage |
| 7. UI And Operator Surfaces | not started | Connections, conversations, drafts, approvals |
| 8. Reliability, Security, And Packaging | in progress | Cross-platform runtime, recovery, and release story |

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

- [x] Create `pkg/messaging` with the first normalized types:
  `Connection`, `AuthInfo`, `Identity`, `Conversation`, `Participant`,
  `Container`, `Placement`, `Message`, `Draft`, `Approval`, `Policy`,
  `Exposure`, `Workflow`, `ActivityEvent`, `Event`, `Capability`.
- [x] Define stable ID types for connections, identities, conversations,
  messages, drafts, policies, and exposures.
- [ ] Decide which records are durable truth versus derived cache.
- [x] Add a storage package for messaging state, likely under
  `pkg/messaging/store`.
- [x] Persist connections and auth metadata references separately from raw
  secret material.
- [x] Persist identities discovered through adapters.
- [x] Persist conversation metadata and message indexes.
- [x] Persist provider-side containers and message placements.
- [x] Persist drafts and their lifecycle state.
- [x] Persist durable approval request records and status transitions.
- [x] Persist human-facing `Workflow` records for logical action chains.
- [x] Persist internal `ActivityEvent` records for the full audit trail.
- [x] Persist adapter poll checkpoints.
- [ ] Persist webhook verification state.
- [x] Persist audit history for inbound, draft, approval, and send events.
- [ ] Ensure Windows-safe data paths and filenames from the start.

### Exit Criteria

- [x] The broker can restart without losing connection, identity,
  conversation, message, draft, or checkpoint state.
- [ ] Secrets remain in `pkg/secrets` or equivalent secret storage, not inside
  messaging records.
- [ ] The domain model is broad enough to support email, Slack-style threads,
  and phone-number-based messaging.

## Milestone 2: Adapter Protocol And Runtime Host

### Goal

Define and host external platform adapters as supervised local processes.

### Checklist

- [x] Create `pkg/messaging/protocol` for the southbound adapter contract.
- [x] Define `Describe`, `ValidateConfig`, `Connect`, `Refresh`,
  `ListIdentities`, `ListConversations`, `ListMessages`, `GetMessage`,
  `ListContainers`, `CreateDraft`, `UpdateDraft`, `DeleteDraft`,
  `SendMessage`, `ReplyMessage`, `MoveMessages`, `MoveConversation`,
  `ArchiveMessages`, `ArchiveConversation`, `ApplyLabels`, `MarkRead`,
  `HandleWebhook`, `Poll`, and `Health`.
- [x] Define a capability declaration shape that adapters return from
  `Describe`.
- [x] Define a normalized webhook request envelope so the broker can own public
  HTTP ingress.
- [x] Define polling checkpoints and retry semantics.
- [x] Add a broker-owned credential materialization seam so adapters receive
  staged secret files instead of raw secret bytes embedded in messaging
  records.
- [x] Build `pkg/messaging/runtime` or equivalent for adapter process
  supervision.
- [x] Support adapter start, stop, restart, health check, and backoff.
- [x] Support adapter stdout/stderr capture for operator diagnostics.
- [x] Decide that the first protocol transport is JSON-RPC over stdio.
- [x] Add protocol compatibility/version negotiation.
- [x] Add an external adapter manifest resolver that turns bundled adapter
  artifacts into supervised process specs.

### Exit Criteria

- [x] A dummy adapter process can be launched, described, health-checked, and
  called by the broker.
- [x] Broker-owned webhooks can be forwarded to an adapter and converted into
  normalized events.
- [x] Adapter restart does not corrupt broker state.

## Milestone 3: Broker Core And Event Flows

### Goal

Implement the broker as the one authority for inbound normalization,
outbound orchestration, and event fanout.

### Checklist

- [x] Create `pkg/messaging/broker`.
- [x] Add connection lifecycle management: create, refresh, disable, delete.
- [x] Add identity refresh/discovery flow from adapters.
- [x] Add conversation upsert logic.
- [x] Add message upsert logic for inbound records.
- [x] Add message upsert logic for outbound records.
- [x] Add draft lifecycle management.
- [x] Add normalized message management surfaces for list containers, move,
  archive, label mutation, and read-state changes.
- [x] Add normalized lookup/search surfaces split between adapter-backed live
  search and broker/index-backed content search.
- [x] Add normalized inbound event ingestion from polling sources.
- [x] Add normalized inbound event ingestion from webhook sources.
- [x] Add outbound operations that always flow through the broker.
- [x] Stage adapter credentials into broker-owned runtime paths instead of
  leaking secret material into persisted messaging records.
- [x] Instantiate the messaging broker in the daemon with the real
  `pkg/secrets` resolver and mailbox-backed approval plumbing.
- [x] Restore persisted built-in messaging connections on daemon startup and
  reattach them to supervised adapter processes.
- [x] Expose a minimal daemon RPC surface for built-in connection lifecycle,
  registration, connect, list, manual poll, container listing, and message
  management flows.
- [x] Add a background poll loop for connected polling-based adapters.
- [x] Add event fanout to UI and northbound shims.
- [x] Aggregate raw activity into human-facing workflow state for draft,
  approval, and send flows.
- [x] Add deduplication/idempotency for inbound events and outbound send
  results.
- [ ] Add delivery status and edit/delete state updates when supported.

### Exit Criteria

- [x] Inbound events from an adapter become durable conversation/message state.
- [x] Drafts and sends can be created without platform-specific code leaking
  into the broker.
- [x] The broker is the only place that decides whether a send proceeds.

## Milestone 4: Policy And Approval Engine

### Goal

Make policy and approval the real control boundary before any runtime gets
meaningful messaging power.

### Checklist

- [x] Create `pkg/messaging/policy`.
- [x] Define broker-enforced policy rules for:
  read inbound, draft replies, send replies, start new conversations,
  attachment handling, allowed identities, message management, allowed
  containers, and search permissions.
- [x] Add a policy document authoring shape with conversational intent,
  compiled broker rules, generator provenance, and human review state.
- [ ] Apply validated policy document bindings to connection defaults and
  exposure overrides.
- [ ] Add an AI-assisted policy authoring flow that proposes file diffs instead
  of mutating broker state directly.
- [ ] Add allowed connection scopes and allowed time windows.
- [x] Support connection-level default policy.
- [x] Support exposure-level narrowed policy for a specific agent/runtime.
- [x] Define durable approval request objects for sends and other sensitive
  operations.
- [x] Reuse or integrate the repo's existing mailbox/approval primitives where
  practical instead of creating an unrelated second approval engine.
- [x] Add approval statuses and audit timeline.
- [x] Add explicit refusal reasons when an operation is denied by policy.
- [x] Add tests for approval-required, reply-only, no-new-recipient, and
  attachment-blocked paths.

### Exit Criteria

- [x] A draft can be blocked, approved, or sent based on policy.
- [ ] Agents cannot bypass broker approval by calling adapters directly.
- [x] Operators can inspect why an action was denied or held for approval.

## Milestone 5: Agent Shim Protocol

### Goal

Expose one thin runtime-facing messaging interface so agents learn `sky10`
messaging once instead of learning each platform separately.

### Checklist

- [x] Create an initial northbound shim method catalog and service surface with
  operations such as
  `ListConnections`, `ListIdentities`, `ListConversations`,
  `GetConversation`, `GetMessages`, `SearchIdentities`,
  `SearchConversations`, `SearchMessages`, `CreateDraft`, `UpdateDraft`,
  `RequestSend`, `ListContainers`, `MoveMessages`, `ArchiveConversation`,
  `ApplyLabels`, `MarkRead`, and reserved `SubscribeEvents`.
- [x] Decide that the default northbound path starts as local JSON-RPC, with
  MCP available later as a wrapper over the same service surface.
- [x] Add a local JSON-RPC handler for the northbound shim method catalog.
- [x] Add a local JSON-RPC shim host that exposes only `messaging.shim.*`
  methods for one exposure-bound service.
- [x] Create a shim host or broker-facing surface for runtime-specific shims.
- [x] Make sure shims never receive raw platform credentials.
- [x] Ensure runtime-facing operations always respect exposures and policy.
- [x] Add one reference in-process shim implementation.
- [x] Add tests proving that shims can read, draft, and request send without
  bypassing policy.

### Exit Criteria

- [x] One runtime can consume normalized messaging without platform-specific
  code.
- [x] The broker can expose one connection to one agent under a narrowed
  policy.
- [x] A runtime can draft a reply and request send through the broker.

## Milestone 6: First-Party Adapters

### Goal

Ship a small first-party adapter set that proves the architecture across
different messaging shapes.

### Recommended Order

1. `email/imap-smtp`
2. `slack`
3. `gmail`
4. `telegram`
5. `microsoft365`
6. `whatsapp`

### Checklist

- [x] Pick the first two adapters for MVP and keep the rest behind them.
- [x] Keep official built-in adapter code under `pkg/messengers/adapters/*`
  and dispatch it through `sky10 messaging <adapter>` so `sky10` can self-exec
  a child adapter process without shipping many binaries.
- [x] Implement a thread-oriented adapter (`gmail` or `slack`).
- [x] Add an initial external Slack adapter bundle with auth validation,
  identity lookup, conversation search, message search, list messages, and
  send/reply over the adapter protocol.
- [x] Register and discover external adapter bundles from daemon connection
  lifecycle flows.
- [ ] Add Slack OAuth/install UX instead of requiring manually staged bot token
  credentials.
- [x] Implement a mailbox-oriented adapter (`gmail` or `imap-smtp`).
- [ ] Implement one webhook-driven adapter.
- [x] Implement one polling-driven adapter.
- [ ] Implement identity discovery for each adapter.
- [x] Implement honest platform-specific lookup/search support declarations.
- [x] Ensure `imap-smtp` is treated as mailbox/message search, not rich
  contact/channel discovery.
- [x] Implement IMAP/SMTP container listing and placement reporting for cached
  messages.
- [x] Implement IMAP/SMTP inbound normalization.
- [x] Implement IMAP/SMTP draft/send and reply flow.
- [ ] Implement inbound normalization for each remaining first-party adapter.
- [ ] Implement draft/send or reply flow for each remaining first-party adapter.
- [ ] Implement auth refresh and expired-credential handling.
- [x] Implement adapter-specific config validation.
- [x] Add deterministic IMAP/SMTP adapter tests for normalization, search,
  send, and reply behavior.
- [ ] Add end-to-end tests for each remaining first-party adapter path using
  mocks or deterministic fixtures.

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
- [ ] Add a workflow-first messaging activity table instead of a raw event log
  as the default view.
- [ ] Add connection list and connection detail views.
- [ ] Show connection health, auth status, and exposed identities.
- [ ] Show conversations and message timelines.
- [ ] Show drafts and approval-needed queues.
- [ ] Show one logical workflow row per inbound-triggered action chain.
- [ ] Add drill-down from workflow rows into the raw event timeline.
- [ ] Show reply permissions and effective policy in plain language.
- [ ] Show adapter capability differences where they matter to users.
- [x] Define a generic adapter settings/action schema so adapter settings
  screens can render text inputs, secret fields, validation buttons, connect
  buttons, and external setup links from manifest data.
- [x] Add a generic daemon action RPC that maps adapter settings into
  connection metadata, auth metadata, secrets, validation, and connect calls.
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

- [x] Decide how first-party adapters are installed and discovered.
- [ ] Decide how third-party adapters are installed and discovered.
- [ ] Add signature or provenance expectations for official adapter binaries.
- [ ] Add process isolation and least-privilege guidance for adapters.
- [x] Add Sky10-managed Bun and Zerobox app entries for JavaScript adapter
  runtime and sandbox launch.
- [x] Add an incubating `external/messengers/*` adapter layout with explicit
  `adapter.json` manifests.
- [x] Resolve bundled Bun adapter manifests into broker-supervised process
  specs without requiring `node_modules` at runtime.
- [x] Materialize embedded first-party external adapters under
  `~/.sky10/messaging/adapters/<adapter-id>/_bundle` before launch.
- [ ] Wire Zerobox sandbox launch for external adapter manifests.
- [x] Add broker restart recovery tests.
- [x] Add adapter crash and reconnection tests.
- [x] Add webhook replay/idempotency tests.
- [x] Add poll checkpoint recovery tests.
- [x] Add multi-connection tests for the same adapter type.
- [ ] Add Windows-specific packaging checks for adapter sidecars.
- [x] Decide that JS adapters default to bundled JavaScript artifacts running on
  Sky10-managed Bun, with self-contained binaries reserved for special cases.
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
