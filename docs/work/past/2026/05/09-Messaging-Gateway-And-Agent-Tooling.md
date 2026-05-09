---
created: 2026-05-09
updated: 2026-05-09
model: gpt-5.5
---

# Messaging Gateway And Agent Tooling

This archives the completed Telegram, Slack, IMAP/SMTP, sandbox bridge, and
agent-runtime messaging integration work. The messaging gateway architecture
notes that lived in `docs/work/current/messaging-gateway-architecture.md` have
been moved here because this branch turned that plan into implemented bridge
and runtime behavior.

User-facing setup guidance now lives in
[Messaging Platforms](../../../../guides/messaging-platforms.md). The broader
AI-first architecture note remains current because model-facing tool exposure is
still an active product layer:
[AI-First App Architecture](../../../current/ai-first-app/architecture.md).

## Why

Messaging needed to feel native to agents without giving sandbox runtimes raw
Telegram, Slack, email, or provider SDK credentials. The durable boundary is:

- platform adapters run southbound of the host messaging broker
- the broker owns credentials, exposures, approvals, normalized storage, audit,
  and file materialization
- guest runtimes use narrow bridge/shim methods scoped to their agent or
  runtime subject
- large media and files are passed as mounted paths, not base64 protocol blobs

This also keeps Telegram practical behind NAT. Telegram can run through bot
long polling on the host side, while the guest still uses only the local sky10
bridge.

## What Landed

- Added the Telegram bot adapter path and Settings support for Telegram
  connection configuration.
- Removed the UI "Telegram coming soon" placeholder and wired Settings
  Messaging to real platform connection flows.
- Kept the messaging platform boundary in the gateway architecture: Telegram,
  Slack, and IMAP/SMTP are platform adapters behind the broker, not runtime
  SDKs embedded in OpenClaw or Hermes.
- Added the messenger sandbox bridge endpoint at:

  ```text
  /bridge/messengers/ws
  ```

- Wired host-owned sandbox bridge forwarding for messenger operations so the
  guest calls its local sky10 endpoint and the host maintains the upstream
  socket.
- Added policy-scoped bridge methods:

  ```text
  messengers.list_connections
  messengers.list_conversations
  messengers.list_events
  messengers.get_messages
  messengers.search_conversations
  messengers.search_messages
  messengers.create_draft
  messengers.request_send
  ```

- Added bridge search validation and trusted identity stamping. Payload
  `agent_id` is ignored; the host/bridge plumbing stamps the agent identity.
- Materialized message attachments for read and search-message results into the
  sandbox state mount before returning them to the runtime.
- Made OpenClaw expose Telegram, Slack, and IMAP/SMTP as native channel apps.
- Scoped OpenClaw messaging sessions by connection and conversation to prevent
  cross-account/thread collisions.
- Added Slack and IMAP/SMTP channel definitions to OpenClaw so multiple
  platform connections can be represented as native channel apps.
- Wired Hermes as a gateway runtime over the same messenger bridge: it polls
  exposed platform events, prompts Hermes with normalized content and mounted
  file paths, then replies through broker drafts/send requests.
- Added `sky10.messaging` tool metadata and guest-local helpers for OpenClaw
  and Hermes so ordinary agent requests like "check my email" can list,
  search, and read exposed messaging connections on demand.
- Added helper commands for:

  ```text
  connections
  conversations
  events
  messages
  search-conversations
  search-messages
  draft
  send
  ```

## Architecture Boundary

Messaging platforms are gateway integrations:

1. Platform adapters translate provider APIs into normalized `pkg/messaging`
   records and protocol results.
2. The broker enforces exposure policy, approval policy, and normalized
   storage.
3. The sandbox bridge exposes only narrowed, policy-scoped operations to guest
   runtimes.
4. Runtime plugins adapt the normalized Sky10 surface into native runtime UX.

OpenClaw has a channel plugin API, so it can present Telegram, Slack, and
IMAP/SMTP as native channel apps. Hermes does not have that same channel-app
registry, so `hermes-sky10-bridge.py` acts as the gateway adapter for Hermes.

The adapter remains the reusable integration point. Adding another messaging
platform should mean implementing an adapter capability set behind the broker,
then letting OpenClaw, Hermes, and future runtimes consume the normalized bridge
or shim surface.

## Files And Media

File transfer remains path-based. The host materializes inbound files under the
sandbox state mount and passes refs like:

```text
/sandbox-state/messengers/inbox/...
```

This is the expected path for Telegram voice notes, images, videos, email
attachments, Slack files, and future provider media. Runtimes should use these
paths directly. The bridge should not base64-encode large files into envelopes.

## Validation

The completed branch passed:

- `go test ./pkg/sandbox/bridge/messengers ./commands -count=1`
- `go test ./pkg/sandbox ./pkg/agent -count=1`
- `go test ./... -count=1`
- `make check`
- `python3 -m py_compile external/runtimebundles/hermes/bridge/hermes-sky10-bridge.py`
- `bun test external/runtimebundles/openclaw/sky10-openclaw/src/messaging.test.js`
- `bun build --target=node '--external=/usr/lib/node_modules/openclaw/*' external/runtimebundles/openclaw/sky10-openclaw/src/index.js --outfile=/tmp/sky10-openclaw-index-check.js`

## Branch Commits

- `9bfebc92` `feat(messaging): add telegram bot adapter`
- `e51e1747` `feat(messaging): bridge messengers into sandboxes`
- `14bf9ea9` `fix(web): remove telegram coming soon placeholder`
- `8c14c14d` `feat(openclaw): add native telegram channel`
- `8e88320c` `feat(hermes): bridge messaging platforms`
- `aef347d7` `fix(openclaw): scope telegram sessions by connection`
- `dc54445e` `feat(openclaw): add slack and imap channels`
- `fd07ad73` `feat(messaging): expose messenger search to agents`

## Follow-Up

- Real provider smoke remains valuable for Telegram bot polling, Slack
  workspace access, and IMAP/SMTP account behavior.
- Future messaging platforms should start at the adapter/broker layer, then
  inherit the same bridge/runtime presentation.
- The root AI product layer still needs curated model-facing tool policy around
  when a model should search/read messages and when reply/send requires
  approval.
