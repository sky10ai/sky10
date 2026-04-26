---
created: 2026-04-26
model: gpt-5.5
---

# Messenger Adapters And Broker

This entry archives the completed messaging/messenger adapter workstream. It
replaces the active `docs/work/current/messaging` planning docs with a
historical summary of what landed, why the architecture looks this way, and
what remains open.

The core decision was to make `Messaging` a product area owned by Sky10, not a
direct "agent has full Slack/Gmail access" feature. Messaging platforms connect
through adapters; agents and runtimes connect through a narrower shim surface;
the broker owns credentials, policy, approvals, normalized storage, and audit.

The reason for that broker layer is blast-radius control. A useful agent may
need to read or draft against a slice of a user's messaging world, such as one
Slack channel, one Gmail label, one board-member thread set, or one customer
support inbox. That does not mean the agent should receive blanket access to
the user's Slack workspace, mailbox, phone-number messaging, or future linked
platforms. The broker lets Sky10 create per-agent exposures and rules so an
agent can process the specific messaging slice it was authorized for while
being unable to read, search, draft, send, move, label, or archive outside that
slice.

That distinction matters for ordinary model mistakes and for worse failures:
rogue agents, compromised runtimes, malicious plugins, and supply-chain
vulnerabilities in adapter or agent glue. If a runtime goes bad, its northbound
shim still only has the exposure granted to it. If adapter code goes bad, it
still does not own policy or user approval. The goal is not just convenience;
it is to make "connect Slack/Gmail/etc." safe enough that users do not feel
like connecting a messaging app means handing an agent the keys to their whole
life.

## Landed Commits

Primary messaging/messenger commits reviewed for this archive:

- `bb9d106b` `feat(web): add messaging adapter settings`
- `e8f29037` `feat(messaging): run generic adapter actions`
- `a62d4a5e` `feat(messaging): discover external adapters`
- `9c83db72` `feat(messaging): add slack adapter bundle`
- `2d1961d9` `feat(messaging): add policy document authoring`
- `f0e41724` `feat(messaging): add external adapter manifests`
- `aff61d11` `docs(messaging): describe external messenger adapters`
- `2a004200` `test(messengers): cover imap-smtp normalization and replies`
- `33a57b2f` `feat(messengers): add imap-smtp message search`
- `a6464319` `feat(messaging): add normalized search surfaces`
- `88e465a2` `feat(messaging): fan out broker events`
- `e460e20e` `feat(messaging): add shim rpc host`
- `727d3878` `feat(messaging): add shim rpc handler`
- `c4d1275c` `feat(messaging): add policy-gated shim service`
- `63850a24` `test(messaging): cover multi-connection isolation`
- `97caaa02` `test(messaging): cover webhook replay idempotency`
- `2931bdc2` `test(messaging): cover broker recovery paths`
- `7b77e22c` `feat(messaging): add connection lifecycle controls`
- `ce8806ee` `feat(messaging): add message placement management`
- `d2092eba` `feat(messaging): wire broker into daemon`
- `3658f046` `feat(messengers): implement imap-smtp adapter`
- `98f507ed` `feat(messaging): add built-in adapter command scaffold`
- `29c4aebd` `feat(messaging): stage adapter credentials in broker runtime`
- `1c6c65e8` `feat(messaging): dedupe broker events and sends`
- `241ecbfe` `feat(messaging): add draft send approval flow`
- `e7b61ac9` `feat(messaging): add policy evaluation hooks`
- `c005c6d2` `feat(messaging): add broker webhook flow`
- `829a586e` `feat(messaging): add broker polling flow`
- `50403cee` `feat(messaging): add messaging store backbone`
- `24f3054a` `feat(messaging): add adapter supervisor`
- `4d24b8fe` `docs(messaging): define platform search semantics`
- `04ce27d5` `feat(messaging): add typed adapter client`
- `066fe3a7` `feat(messaging): add adapter process host`
- `e4194e9d` `feat(messaging): add stdio runtime transport`
- `b3b1ce46` `feat(messaging): add adapter protocol schema`
- `615b7806` `feat(messaging): add workflow audit model`
- `253171fb` `feat(messaging): add core domain types`
- `08dedcf8` `docs(messaging): add implementation milestone plan`
- `e6b0fb5a` `docs(messaging): add broker architecture draft`

Adjacent support work:

- `a5acf78e` `feat(apps): manage bun and zerobox runtimes`

That support commit matters because the JavaScript adapter strategy assumes
Sky10 can manage a Bun runtime and later sandbox adapter execution.

## Terminology That Stuck

User-facing language:

- `Messaging` is the product area.
- `Messaging platform` means Slack, Email, Telegram, WhatsApp, Discord, and
  similar apps.
- `Connection` is one linked instance of a platform, such as work Slack,
  personal Gmail, or a second Gmail account.
- `Conversation`, `Message`, `Draft`, `Approval`, and `Reply permissions` are
  the user-facing nouns for normalized activity and control.

Code-facing language:

- `Adapter` is a platform integration executable or built-in child process.
- `Connection` is the configured binding with auth metadata and policy.
- `AuthInfo` is auth/session metadata; raw secrets live elsewhere.
- `Identity` is a local send/receive persona exposed by a connection.
- `Policy`, `Exposure`, `Broker`, `Workflow`, and `ActivityEvent` are the
  authority, access, and audit nouns.

The work intentionally avoided `channel` as the top-level noun. It is
overloaded by Slack, OpenClaw, and existing networking code, and it does not
describe email or identity-bearing connections cleanly.

## Architecture

The architecture is split into three layers:

- Platform adapters are southbound. They talk to Slack, IMAP/SMTP, Gmail,
  Telegram, and future platforms.
- The Sky10 messaging broker is the authority boundary. It owns connection
  lifecycle, secrets references, policy, approvals, normalized storage,
  workflows, and event fanout.
- Agent shims are northbound. They expose a narrow normalized interface to
  runtimes without giving those runtimes raw provider credentials or direct
  adapter access.

This is the key safety property: an agent should learn one Sky10 messaging
surface, not direct provider APIs. Sending, drafting, search, message
management, and approvals all flow through the broker.

The policy unit is intentionally `Exposure`: a grant from one connection to one
agent/runtime/user surface under a narrowed policy. A connection can exist
without any agent access, and multiple agents can receive different exposures
over the same connection. For example, one agent might be allowed to summarize
only board-related email, another might draft replies in a support Slack
channel, and neither can search personal messages or send without approval.
This is the mechanism that keeps a compromised or poorly behaving agent from
turning one useful integration into full-account compromise.

Primary code anchors:

- [`pkg/messaging`](../../../../../pkg/messaging) contains the normalized
  domain model.
- [`pkg/messaging/store`](../../../../../pkg/messaging/store) persists
  connections, identities, conversations, messages, placements, drafts,
  workflows, approvals, events, exposures, policies, and poll checkpoints.
- [`pkg/messaging/broker`](../../../../../pkg/messaging/broker) owns inbound,
  outbound, policy, approval, search, management, and runtime orchestration.
- [`pkg/messaging/protocol`](../../../../../pkg/messaging/protocol) defines
  the southbound adapter protocol.
- [`pkg/messaging/runtime`](../../../../../pkg/messaging/runtime) supervises
  adapter processes and speaks JSON-RPC over stdio.
- [`pkg/messaging/shim`](../../../../../pkg/messaging/shim) exposes the
  northbound runtime-facing service surface.
- [`pkg/messaging/rpc`](../../../../../pkg/messaging/rpc) exposes daemon RPC
  methods for UI/operator control.

## Adapter Protocol And Runtime

The southbound adapter protocol landed as JSON-RPC over stdio. The broker
writes requests to adapter stdin, adapters write protocol responses to stdout,
and logs go to stderr. Large binary payloads stay out of JSON-RPC and are
represented as broker-owned blob/staging references.

The protocol includes:

- `Describe`, `ValidateConfig`, `Connect`, `Refresh`, and `Health`
- identity, conversation, message, container, and search methods
- draft, send, reply, move, archive, label, and read-state methods
- broker-owned webhook envelopes
- polling requests and checkpoints
- runtime paths for state, cache, logs, blobs, staging, and staged secrets

The runtime layer added:

- stdio framing and client calls
- adapter process host
- typed adapter client
- process supervision with start/stop/restart/health/backoff
- stdout/stderr capture for diagnostics
- compatibility/version negotiation

Credential materialization is broker-owned. Messaging records keep
`credential_ref`; when an adapter needs credentials, the broker resolves the
secret through `pkg/secrets` and stages temporary material into broker-owned
runtime paths under the adapter's execution environment.

## Broker, Store, Policy, And Audit

The broker now has enough core infrastructure to make adapters useful without
leaking platform semantics throughout the app:

- durable normalized domain types for adapters, connections, auth info,
  identities, conversations, participants, containers, placements, messages,
  drafts, policies, exposures, approvals, workflows, events, and capabilities
- KV-backed messaging store with clone/index layers and restart-safe load
- connection create/refresh/disable/delete lifecycle
- broker-owned webhook forwarding
- broker-scheduled polling and checkpoint persistence
- inbound event ingestion into conversations/messages/placements
- outbound draft, send, reply, approval, and policy handling
- event and send-result dedupe
- workflow and activity-event audit records
- fanout under the `messaging:event` UI/shim event name

Policy landed as deterministic broker enforcement plus a human-editable policy
document shape. The policy document can carry conversational intent,
compiled rules, generator provenance, and review state. The intended AI flow is
file-diff-first: AI proposes a policy document change, the user reviews it, and
only then does Sky10 apply the compiled policy.

Implemented policy coverage includes read, draft, send, reply-only,
new-conversation blocking, attachment blocking, allowed identities, message
management, allowed containers, search permissions, approvals, and refusal
reasons.

This policy work is not decorative. It is the reason the broker exists. The
broker can enforce rules before reads, searches, drafts, sends, and management
actions reach an adapter. It also records why something was denied or held for
approval. That gives Sky10 a defensible security story: agents operate under
least-privilege messaging grants, and the durable audit trail can explain what
the agent saw, what it attempted, what policy allowed, and what a human
approved.

## Search And Discoverability

Search was made explicit instead of burying it inside a vague
`discoverability` API. The adapter-facing shapes are:

- `ResolveIdentity`
- `SearchIdentities`
- `SearchConversations`
- `SearchMessages`

The distinction matters by platform:

- Slack channel/user lookup is adapter-backed identity or conversation search.
- Telegram username lookup would be adapter-backed identity search.
- IMAP/SMTP supports message search and limited mailbox/container lookup, not
  rich contact or channel discovery.
- Cross-platform derived queries like "questions from the board" belong to the
  broker/index/workflow layer.

Search is policy-gated because read-only does not automatically mean safe. The
shim and broker surfaces enforce exposure policy around identity,
conversation, message, and remote-platform search.

## Message Management Model

The generic archive/move/label model uses:

- `Container`, a provider-side grouping target such as mailbox, label, channel,
  archive, trash, or sent.
- `Placement`, the mutable provider locator showing that a normalized message
  exists in a container with provider metadata such as an IMAP UID.

This avoids making every platform pretend to be IMAP. Plain IMAP usually has
one mailbox placement per message. Gmail-like labels can produce multiple
placements for one message. Slack channels are containers, but not mailboxes.

The broker and adapter protocol include generic operations for list containers,
move messages/conversations, archive messages/conversations, apply labels, and
mark read.

## First-Party Adapter Strategy

Two adapter styles landed.

Built-in Go adapters live under
[`pkg/messengers/adapters`](../../../../../pkg/messengers/adapters). They run
as child processes through `sky10 messaging <adapter>` so Sky10 can ship one
binary but still preserve the adapter-process boundary.

External JavaScript adapter bundles live under
[`external/messengers`](../../../../../external/messengers). They include an
`adapter.json` manifest and a bundled JavaScript artifact so a user does not
need to install npm packages. The daemon can embed and materialize these
bundles under:

```text
~/.sky10/messaging/adapters/<adapter-id>/_bundle
```

Manifests declare:

- adapter id, display name, version, description, auth methods, capabilities
- runtime type and entrypoint
- sandbox mode
- settings with targets: `metadata`, `auth`, or `credential`
- actions such as `open_url`, `validate_config`, and `connect`

The Sky10-managed Bun runtime support was added so bundled JavaScript adapters
can run without requiring users to manage Node/npm themselves. Zerobox is
tracked as the intended sandbox option, but launch wiring is still open.

## IMAP/SMTP Adapter

The IMAP/SMTP adapter is the first mailbox-oriented adapter. It is implemented
in Go under
[`pkg/messengers/adapters/imapsmtp`](../../../../../pkg/messengers/adapters/imapsmtp).

It supports:

- config parsing and validation for IMAP and SMTP settings
- IMAP connection and mailbox listing
- polling inbound messages
- RFC822 normalization into conversations, messages, parts, and placements
- poll checkpoints
- SMTP send and reply
- standard threading headers such as `Message-ID`, `In-Reply-To`, and
  `References`
- Sky10 workflow/draft headers such as `X-Sky10-Workflow-ID` and
  `X-Sky10-Draft-ID`
- Gmail thread metadata when available, including `X-GM-THRID`
- IMAP message search through remote `UID SEARCH`

The important design point: IMAP/SMTP is honest about its shape. It is a
mailbox/message adapter, not a rich people/channel directory. It supports
message search and container/mailbox concepts; it should not pretend to offer
Slack-style discovery.

Coverage added around IMAP/SMTP includes normalization, search, send, reply,
container/placement behavior, and regression tests for the adapter surfaces.

## Slack External Adapter Bundle

Slack became the first thread/workspace-style external adapter bundle under
[`external/messengers/slack`](../../../../../external/messengers/slack).

The bundle uses a prebuilt JavaScript artifact and declares its setup shape in
`adapter.json`. It covers:

- token-based auth validation
- bot identity lookup
- channel/conversation lookup
- message search/listing
- send/reply over the adapter protocol
- manifest-declared settings and actions for the generic settings UI

Current limitation: Slack still needs OAuth/install UX. The initial bundle is
workable glue for the adapter protocol and broker model, but mainstream setup
should not require manually staged bot tokens long term.

## Generic Adapter Actions And UI

Generic adapter setup actions landed so the UI does not need a bespoke Slack or
IMAP settings screen.

The daemon RPC method `messaging.runAdapterAction` maps manifest settings into:

- connection metadata
- auth metadata
- secret writes through `pkg/secrets`
- process registration/supervision
- adapter validation calls
- adapter connect calls

The web UI added `/settings/messaging`, reachable from Settings, the command
palette, and pinnable sidebar pages. The screen:

- calls `messaging.adapters`
- calls `messaging.connections`
- renders manifest-declared inputs generically
- handles text, URL, number, boolean, select, password, and secret fields
- supports secret scope selection
- runs manifest-declared actions
- shows connection status
- shows field-level and action-level validation issues inline

The UI checkpoint was validated with `make build-web`; it passed with the
existing Vite large-chunk warning.

## Agent Shim Surface

The northbound shim layer is the runtime-facing side of the policy boundary.
It is exposure-bound and strips credential-like metadata before giving data to
a runtime.

Implemented shim pieces:

- in-process `pkg/messaging/shim.Service`
- local JSON-RPC handler under `pkg/messaging/shim/rpc`
- local shim host under `pkg/messaging/shim/host`
- read operations for connections, identities, conversations, messages, and
  containers
- search operations for identities, conversations, and messages
- draft creation/update
- request-send/send-draft flow through broker policy
- message management operations such as move/archive/label/read-state
- tests proving shims can read, draft, and request send without raw credential
  access or direct adapter access

MCP is intentionally deferred as a wrapper over this same service contract,
not as a separate authority surface.

## Reliability And Tests

The workstream added coverage for:

- store persistence and restart recovery
- broker recovery paths
- adapter process host/client behavior
- adapter crash/restart paths
- polling checkpoints
- webhook replay/idempotency
- event fanout
- multi-connection isolation for the same adapter type
- connection lifecycle RPCs
- policy decisions and approval flows
- shim policy enforcement
- external adapter manifest parsing/resolution
- Slack adapter fixture behavior
- IMAP/SMTP normalization, search, send, reply, and placement behavior

This is enough to validate the architecture mechanically. Live platform smoke
testing with real accounts remains separate from deterministic repo tests.

## What Remains

Open work after this archive:

- live smoke test real IMAP/SMTP and Slack configurations
- add Slack OAuth/install UX instead of manual bot-token setup
- wire Zerobox launch for external adapters
- finalize third-party adapter install/discovery and provenance expectations
- add webhook verification state persistence
- add delivery status, edit, delete, typing, reactions, and richer platform
  state updates when supported
- implement auth refresh and expired-credential handling
- expand identity discovery across remaining adapters
- add remaining first-party adapters such as Gmail, Telegram, Microsoft 365,
  WhatsApp, Discord, Signal, and social messaging surfaces if product scope
  still wants them
- add workflow-first activity table, connection detail pages, conversation
  timelines, draft/approval queues, diagnostics, retry/reconnect affordances,
  and audit visibility
- add Windows-specific packaging checks for adapter sidecars
- prove the first external demo end-to-end: two connections of one adapter
  type, inbound ingestion, agent exposure, draft, approval/send, restart
  persistence, and UI audit visibility

## Design Takeaway

The adapter work moved Sky10 from "agents might get broad app access" toward a
brokered messaging model:

- adapters know platforms
- shims know runtimes
- the broker owns authority
- per-agent exposures limit what each runtime can see and do
- secrets stay in secrets
- policy and approval are enforced before outbound action
- rogue agents and supply-chain failures hit a bounded messaging slice instead
  of the whole connected account
- UI and agents consume the same normalized model

That is the part worth preserving as the implementation continues.
