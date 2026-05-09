---
created: 2026-05-09
updated: 2026-05-09
---

# Messaging Gateway Architecture

Sky10 messaging platforms are gateway integrations. Telegram, Slack, IMAP/SMTP,
and future providers enter the system through platform adapters behind the host
messaging broker. Agent runtimes do not own platform credentials or provider
SDK sessions.

## Layers

### 1. Platform adapters

Adapters are southbound provider integrations. They translate platform-specific
APIs into the normalized Sky10 messaging protocol:

- connections and identities
- conversations, messages, placements, and containers
- drafts, sends, replies, moves, labels, and read state
- provider search when supported
- polling or webhook ingress
- file/blob references for attachments

Built-in adapters run as `sky10 messaging <adapter>` child processes. External
bundles can be materialized under the messaging adapter bundle directory.

### 2. Messaging broker

The broker is the authority boundary. It owns:

- credential references and temporary credential staging
- connection lifecycle
- policy and exposure evaluation
- send approvals
- normalized storage and replay-safe events
- workflow and audit records
- attachment materialization into sandbox state

The policy unit is an exposure from one connection to one subject, such as an
agent identity or `runtime:hermes` / `runtime:openclaw`.

### 3. Sandbox bridge

Sandbox runtimes use guest-local bridge endpoints. The host daemon opens and
maintains the upstream socket; guests do not dial host callback URLs.

Messaging uses:

```text
/bridge/messengers/ws
```

The bridge exposes only normalized, policy-scoped methods:

- `messengers.list_connections`
- `messengers.list_conversations`
- `messengers.list_events`
- `messengers.get_messages`
- `messengers.search_conversations`
- `messengers.search_messages`
- `messengers.create_draft`
- `messengers.request_send`

File transfer is path-based. The host materializes inbound files under the
sandbox state mount and passes refs like:

```text
/sandbox-state/messengers/inbox/...
```

Runtimes that create outbound attachments must point refs back into
`/sandbox-state/messengers/` so the host can map them safely before sending.

### 4. Runtime presentation

Runtime presentation is intentionally runtime-specific:

- OpenClaw can expose Telegram, Slack, and IMAP/SMTP as native channel apps
  because OpenClaw has a channel plugin API.
- Hermes consumes the same bridge as a gateway runtime. Its bridge polls
  exposed platform events, prompts Hermes with normalized content and mounted
  file paths, then sends final replies through broker drafts.
- Both runtimes register `sky10.messaging` and install a guest-local
  `sky10-messaging` helper so ordinary agent requests can list/search/read
  exposed connections on demand without raw provider SDKs.

The platform adapter remains the reusable integration point. Runtime plugins
should adapt the normalized Sky10 surface to the runtime UX; they should not
reimplement provider clients or credential handling.

## Adding A Messaging Platform

Add a platform by implementing an adapter capability set behind the broker:

1. Define or update the adapter manifest and settings/actions.
2. Normalize provider identities, conversations, messages, and files into
   `pkg/messaging` records.
3. Implement receive, draft, send/reply, search, and management operations
   according to the provider's capabilities.
4. Let the broker enforce exposure policy and approvals.
5. Let runtimes consume the platform through the bridge or shim surface.

That path makes a new platform available to Hermes, OpenClaw, and future
runtimes without granting any runtime raw provider credentials.
