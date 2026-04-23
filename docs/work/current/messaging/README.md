---
created: 2026-04-18
updated: 2026-04-18
model: gpt-5.4
---

# Messaging Broker Architecture

## Purpose

This document defines the first architecture draft for `sky10` messaging.

The core idea is:

- `sky10` owns the messaging domain and policy boundary
- external messaging platforms connect through adapter executables
- agent runtimes connect through thin shim executables
- agents never hold raw platform credentials
- all inbound, draft, outbound, and approval decisions flow through the
  `sky10` broker

This product area should stand on its own even if `sky10` later supports zero
agents. Agents are one consumer of the messaging broker, not the definition of
the product.

The implementation plan for this architecture lives in
[`implementation-plan.md`](./implementation-plan.md).

## Terminology

### User-Facing

- **Messaging** — the product area
- **Messaging platform** — Slack, Gmail, WhatsApp, Telegram, Discord, and so on
- **Connection** — one connected instance of a platform
- **Conversation** — a thread, DM, room, group chat, or email thread
- **Message** — one inbound or outbound message
- **Draft** — a proposed outbound reply that has not been sent
- **Reply permissions** — user-facing language for what an agent may do
- **Approval** — a required human decision before send or other sensitive action

### Core Domain / Code

- **Adapter** — a platform integration executable or plugin
- **Connection** — one configured adapter instance with auth and policy
- **AuthInfo** — auth/session metadata for a connection
- **Identity** — one local messaging persona exposed by a connection
- **Conversation** — the normalized thread/container
- **Participant** — one member of a conversation
- **Message** — a normalized inbound or outbound message record
- **Draft** — a normalized unsent outbound message
- **Approval** — a durable approval request for sensitive messaging actions
- **Policy** — rules for inbound, outbound, drafts, approvals, recipients,
  attachments, and timing
- **Workflow** — the human-facing logical unit for one messaging action chain
- **ActivityEvent** — the internal atomic audit record attached to a workflow
- **Broker** — the `sky10` middle layer that owns policy, approvals, routing,
  and storage
- **Event** — a normalized inbound change from an adapter
- **Capability** — what an adapter can do
- **Exposure** — a grant that lets an agent/runtime access a connection under a
  policy

### Extension Layers

- **Platform adapter** — southbound integration to Slack, Gmail, WhatsApp, and
  similar systems
- **Agent shim** — northbound integration to OpenClaw, Hermes, Codex, Claude
  Code, and similar runtimes
- **Protocol** — the stable RPC contract adapters and shims speak to `sky10`

### Terms To Avoid As Canonical

- `channel` — overloaded in OpenClaw, Slack, and existing `pkg/link/channel.go`
- `account` — ambiguous between credential, tenant, persona, mailbox, page, or
  bot
- `connector` — acceptable in implementation details, too broad for the core
  noun
- `bridge` — fine for one implementation, not the product-wide domain term
- `messenger` — poor consumer language for this product area

## Why Connection And Identity Are Separate

One login or credentialed binding can expose multiple local messaging
identities.

Examples:

- one Gmail connection may expose `me@company.com`, `support@company.com`, and
  `billing@company.com`
- one Meta business connection may expose multiple pages
- one future business messaging connection may expose multiple send-as
  identities

That means:

- `Connection` is the configured binding
- `AuthInfo` describes how it authenticates
- `Identity` describes who it can actually speak as or receive as

`account` is not sharp enough for this model.

## High-Level Architecture

```text
External Messaging Platforms
Slack / Gmail / IMAP / WhatsApp / Telegram / Discord / ...

        ^
        | webhook / polling / send / draft APIs
        v

Platform Adapters (external executables, any language)
gmail-adapter
slack-adapter
whatsapp-adapter
...

        ^
        | adapter protocol
        v

sky10 Messaging Broker (Go, pkg/messaging)
- connection manager
- auth + secrets lookup
- identity discovery
- conversation/message normalization
- policy engine
- approval engine
- draft/send orchestration
- event fanout
- storage

        ^
        | shim protocol / MCP / local RPC
        v

Agent Shims (thin runtime-specific bridges)
OpenClaw shim
Hermes shim
Codex shim
Claude Code shim
...

        ^
        | normalized messaging tools/events
        v

Agents
```

## Responsibility Split

### Platform Adapters

Platform adapters:

- talk to Slack, Gmail, WhatsApp, Telegram, and similar APIs
- translate platform-native objects into normalized broker events
- execute outbound send and draft operations when the broker tells them to
- report their capabilities and auth state

Platform adapters do not:

- own policy
- decide whether a message should be sent
- hold approval logic
- expose arbitrary direct access to messaging providers

### Messaging Broker

The broker owns:

- the stable messaging domain model
- connection lifecycle
- auth metadata and secret references
- identity discovery and indexing
- conversation and message storage
- draft lifecycle
- workflow aggregation
- policy evaluation
- approval workflows
- audit history
- fanout to UI and agent shims

The broker is the policy and authority boundary.

### Agent Shims

Agent shims:

- translate one runtime's tool/plugin model into a stable local `sky10`
  messaging interface
- expose normalized read/draft/request-send operations to the runtime
- do not embed platform-specific logic

Agents should learn one `sky10 messaging` interface, not Slack or Gmail
directly.

## Core Domain Model

The first normalized model should include:

- `Adapter`
- `Connection`
- `AuthInfo`
- `Identity`
- `Conversation`
- `Participant`
- `Message`
- `Draft`
- `Policy`
- `Exposure`
- `Workflow`
- `ActivityEvent`
- `Event`
- `Capability`

Recommended relationship shape:

- one `Adapter` supports many `Connection`s
- one `Connection` may expose many `Identity` records
- one `Connection` owns many `Conversation`s
- one `Conversation` contains many `Message`s
- one `Message` may lead to one or more `Draft`s or approvals
- one `Connection` has a default `Policy`
- one `Exposure` narrows a `Policy` for one agent/runtime
- one inbound-triggered logical action chain becomes one `Workflow`
- one `Workflow` owns many internal `ActivityEvent` records

## Workflow And Audit Model

The broker should store atomic audit events, but the primary operator-facing
surface should be one logical workflow row per inbound-triggered action chain.

That means:

- `ActivityEvent` is the internal atomic audit record
- `Workflow` is the human-facing summarized unit

Example:

- Latisha sends a Slack DM
- a rule matches
- a draft is created
- an operator is notified on Telegram
- the operator approves
- the broker sends on Slack

Internally, that may be many `ActivityEvent` records.
Human-facing, it should usually appear as one `Workflow`.

Recommended workflow state examples:

- `new`
- `matched`
- `drafted`
- `awaiting_approval`
- `approved`
- `sending`
- `sent`
- `failed`
- `dismissed`

Recommended workflow timestamps:

- `source_created_at`
- `broker_received_at`
- `rule_matched_at`
- `draft_created_at`
- `operator_notified_at`
- `operator_responded_at`
- `approved_at`
- `send_requested_at`
- `source_sent_at`
- `fulfilled_at`
- `last_activity_at`

This split allows:

- precise internal auditability
- one clean human-facing workflow table
- drill-down from workflow to raw event timeline on demand

## Capabilities

Each platform adapter should declare capabilities so the broker can adapt its
behavior without platform-specific branching throughout the core.

Initial capability set:

- `receive_messages`
- `send_messages`
- `create_drafts`
- `update_drafts`
- `delete_drafts`
- `list_conversations`
- `list_messages`
- `threading`
- `attachments`
- `webhooks`
- `polling`
- `mark_read`
- `typing_indicators`
- `delivery_status`
- `reactions`
- `edits`
- `deletes`

Search and lookup should be modeled explicitly rather than hidden inside
discoverability.

Useful additional capability examples:

- `resolve_identity`
- `search_identities`
- `search_conversations`
- `search_messages`
- `search_remote`
- `search_indexed`

## Lookup And Search

`discoverability` and `search` are not the same thing.

- `discoverability` is how someone can find or initiate contact with an identity
- `lookup/search` is how the broker, UI, or an agent finds people,
  destinations, or content inside a connected platform

The broker should treat search as a first-class surface with at least four
shapes:

- `identity lookup`
  Contacts, usernames, email addresses, phone numbers, handles
- `destination lookup`
  Channels, rooms, groups, shared inboxes, bots, mailing lists
- `content search`
  Messages, threads, email bodies, attachment metadata
- `derived search`
  Broker/index-level queries like “questions from the board”, “messages needing
  reply”, or “unanswered asks”

This should become a distinct protocol surface, not one vague `Search()`
method. The likely adapter-facing methods are:

- `ResolveIdentity`
- `SearchIdentities`
- `SearchConversations`
- `SearchMessages`

The architecture split should be:

- adapter-backed search for live platform lookups
  Good for contacts, usernames, channels, rooms
- broker/index-backed search for normalized and cross-platform content
  Good for message search, derived queries, workflow-aware search

That means:

- Slack channel lookup is adapter search
- Telegram username lookup is adapter search
- IMAP mailbox/message search is adapter search
- cross-platform “questions from the board” is broker/index search
- “messages awaiting reply” is broker/workflow search

IMAP deserves explicit treatment here because it is narrower than chat/workspace
platforms:

- `imap-smtp` should support `SearchMessages`
  Sender, recipient, subject, date, flags, and often body/header text via IMAP
  search
- `imap-smtp` may support mailbox/folder lookup
  Useful for folders, labels, shared mailboxes, and similar destinations
- `imap-smtp` should not be treated as rich contact or channel discovery
  It is not a directory platform, and it does not naturally support the same
  people/destination search surface as Slack or Telegram

So the honest capability shape for `imap-smtp` is closer to:

- `search_messages = true`
- `search_conversations = limited`
- `search_identities = false`

Search also needs policy. It is not automatically safe just because it is
read-only.

Important search policy dimensions:

- can search identities
- can search conversation metadata
- can search message bodies
- can search only indexed data
- can search only approved connections
- can search only conversations already visible through the current exposure
- can use remote platform search versus broker/index search

## Southbound Adapter Protocol

Platform adapters should be external executables speaking a stable protocol to
the broker.

The first transport should be JSON-RPC over stdio:

- broker writes requests to adapter stdin
- adapter writes responses to stdout
- adapter logs go to stderr
- large binary payloads stay out of band as broker-owned blob/staging refs

Initial method set:

- `Describe`
- `ValidateConfig`
- `Connect`
- `Refresh`
- `ListIdentities`
- `ListConversations`
- `ListMessages`
- `GetMessage`
- `CreateDraft`
- `UpdateDraft`
- `DeleteDraft`
- `SendMessage`
- `ReplyMessage`
- `HandleWebhook`
- `Poll`
- `Health`

Planned lookup/search additions:

- `ResolveIdentity`
- `SearchIdentities`
- `SearchConversations`
- `SearchMessages`

Design constraints:

- adapters may be written in any language
- adapters should be restartable without corrupting broker state
- adapters should not require public network listeners of their own when
  webhooks are involved
- adapters should not store long-lived policy decisions locally

The current schema lives in `pkg/messaging/protocol` and includes:

- protocol version metadata
- capability declaration through `Describe`
- webhook envelopes for broker-owned HTTP ingress
- polling checkpoints and suggested poll delays
- staged attachment/blob references for binary payloads

## Northbound Shim Protocol

Agent shims should also speak a stable protocol to the broker.

Initial method set:

- `ListConnections`
- `ListIdentities`
- `ListConversations`
- `GetConversation`
- `GetMessages`
- `CreateDraft`
- `UpdateDraft`
- `RequestSend`
- `SendDraft`
- `MarkRead`
- `SubscribeEvents`

Agents should be able to:

- read normalized inbound messages
- inspect conversations
- create and revise drafts
- request sends

Agents should not:

- hold platform credentials
- bypass policy
- call platform APIs directly
- auto-send without broker approval

## Policy Model

Policy lives in the broker.

Representative policy controls:

- can read inbound messages
- can draft replies
- can send replies
- approval required before send
- replies only in existing conversations
- cannot start new conversations
- attachments disallowed
- only these identities may send
- only during business hours
- only for these channels/folders/labels/conversation classes

Policy should apply at two levels:

- `Connection` default policy
- `Exposure` policy for one agent/runtime/user surface

## Inbound Flow

1. An adapter receives a webhook, or the broker schedules a poll.
2. The adapter returns normalized `Event` values.
3. The broker upserts `Conversation`, `Message`, and `Participant` records.
4. The broker evaluates inbound policy.
5. The broker stores event and audit history.
6. The broker fans out updates to UI and eligible agent shims.
7. Agents may create drafts or request actions.

## Outbound Flow

1. An agent shim or UI creates a `Draft` or `RequestSend`.
2. The broker validates the caller's `Exposure` and effective `Policy`.
3. If approval is required, the broker creates an approval request.
4. A human approves or rejects.
5. The broker calls the platform adapter's send/reply operation.
6. The adapter returns delivery state and remote identifiers.
7. The broker records the outbound message and emits updates.

## Approvals

`sky10` already has durable mailbox and approval workflow primitives in the
agent/messaging-adjacent parts of the repo. Messaging approvals should reuse
that direction rather than creating an unrelated approval system.

Messaging approvals should cover operations such as:

- send this draft
- send as this identity
- allow attachment delivery
- allow starting a new conversation

The current broker implementation keeps a normalized messaging approval record
in `pkg/messaging` and can mirror that request into the repo's durable mailbox
approval store when a mailbox target is configured.

## Webhook And Poll Ownership

The broker should own public HTTP ingress for webhook-based platforms.

Reasoning:

- one public network surface
- one verification and audit boundary
- easier packaging and deployment
- adapters stay local workers
- simpler cross-platform support, including Windows

Recommended pattern:

- broker receives webhook
- broker forwards a normalized request envelope to the adapter
- adapter verifies/parses platform-specific content
- adapter returns normalized events

Polling adapters should follow the same authority model:

- broker schedules polls
- adapter returns events plus an updated checkpoint

## Storage

The broker should persist:

- connections
- auth metadata references
- discovered identities
- conversation index
- message index
- drafts
- workflows
- activity events
- policy bindings
- exposures
- checkpoints/cursors for polling
- webhook verification state
- approval state
- audit log

Secrets should not live directly in the messaging store:

- `AuthInfo` references secret material
- actual tokens/passwords should live in `pkg/secrets`
- when an adapter needs credentials, the broker should resolve the
  `credential_ref` and stage the raw payload into a broker-owned runtime
  secret file under `~/.sky10/messaging/...`; adapters should receive only the
  staged file/blob reference, not embedded secret bytes in messaging records

## Packaging Direction

The recommended packaging split is:

- broker built into `sky10`
- official built-in adapters implemented in Go under
  `pkg/messengers/adapters/*` and launched by self-exec of the main binary via
  `sky10 messaging <adapter>`
- optional third-party or extra official adapters can still ship as separate
  binaries
- agent shims shipped as external binaries or MCP-compatible local servers

This gives:

- a clean language boundary
- process isolation
- restartability
- support for first-party and third-party adapter ecosystems

If JavaScript is attractive for adapters or shims, a self-contained sidecar
runtime such as Bun-compiled executables is a plausible packaging option.

## Current Daemon Integration

The current `sky10 serve` path now:

- instantiates a messaging broker over a KV-backed messaging store
- resolves adapter credentials through `pkg/secrets`
- mirrors approvals through the durable mailbox store when configured
- restores persisted built-in connections on startup
- re-launches built-in adapters through `sky10 messaging <adapter>`
- runs a basic background poll loop for connected polling-based adapters
- exposes a minimal RPC surface:
  - `messaging.adapters`
  - `messaging.connections`
  - `messaging.connectBuiltin`
  - `messaging.connectConnection`
  - `messaging.pollConnection`

This is not yet the full operator UX, but it means messaging now exists in the
running daemon, not only in isolated package tests.

## Package Layout

The first package layout should center on:

- `pkg/messaging`
- `pkg/messaging/broker`
- `pkg/messaging/policy`
- `pkg/messaging/protocol`
- `pkg/messaging/store`
- `pkg/messaging/webhook`
- `pkg/messaging/runtime`
- `pkg/messaging/approval`
- `pkg/messengers/adapters`
- `pkg/messengers/adapters/imapsmtp`

This keeps the messaging domain independent from the current `pkg/agent`
layout, which is important because messaging should still make sense if the
product later has zero agents.

## Initial Platform Priority

The architecture should be broad enough to support many platforms even if
`sky10` only ships a smaller first-party set.

Reasonable early first-party priorities:

- `email/imap-smtp`
- `gmail`
- `microsoft365`
- `slack`
- `telegram`
- `whatsapp`

Search expectations should differ by platform:

- `email/imap-smtp` is a mailbox/message adapter with limited lookup
- `gmail` and `microsoft365` should eventually support richer mailbox and
  provider-native search than plain IMAP
- `slack` should support identity, destination, and content search
- `telegram` should support username and bot-oriented lookup, not workspace-style
  destination search

Reasonable later first-party candidates:

- `instagram`
- `messenger`
- `discord`
- `x`

The model should still accommodate third-party adapters for platforms that are
out of scope, expensive, brittle, or policy-constrained.

## First Implementation Checkpoint

The first buildable checkpoint should include:

1. `pkg/messaging` core types
2. a stable adapter protocol
3. a minimal broker with one polling path and one webhook path
4. persistence for `Connection`, `Identity`, `Conversation`, `Message`, and
   `Draft`
5. one approval path
6. one shim path
7. one or two first-party adapters

That is enough to validate the architecture without committing to every
platform upfront.
