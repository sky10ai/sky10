# Messaging Platforms

Sky10 connects messaging apps through host-side platform adapters. Agents do
not get raw Slack, email, or Telegram credentials. They get narrowed,
broker-authorized access to the conversations and actions the user exposed.

## Connect A Platform

Open `Settings -> Messaging` and connect a platform adapter:

- Telegram uses a bot token and long polling, so it works behind NAT.
- Slack uses the Slack adapter bundle.
- IMAP/SMTP uses mailbox/server credentials for email-style accounts.

Each connected account becomes a Sky10 messaging connection. The daemon stores
credential references in secrets, keeps normalized messages in the broker
store, and emits durable messaging events.

## Expose A Connection To An Agent

After connecting a platform, expose it to a managed agent or runtime subject.
For Hermes sandboxes, use the agent identity or `runtime:hermes`. For
OpenClaw sandboxes, use the agent identity or `runtime:openclaw`.

The exposure controls what the agent can read, draft, reply to, search, and
send. The broker still owns approvals and can hold outbound sends before a
platform adapter receives them.

## Runtime Behavior

OpenClaw presents bridge-backed messaging apps as native channels when their
OpenClaw channel plugins are enabled. Telegram, Slack, and IMAP/SMTP messages
appear as native channel conversations inside OpenClaw, and replies go back
through broker drafts/send requests.

Hermes has no native channel-app registry. Instead,
`hermes-sky10-bridge.py` consumes the same Sky10 messenger bridge as a gateway
runtime: it reads exposed platform events, prompts Hermes with normalized
message content, and sends the final reply through broker drafts.

## Files And Media

Messaging files are mounted, not embedded. Inbound attachments are copied into:

```text
/sandbox-state/messengers/
```

The bridge passes file paths to the runtime. This is the expected path for
Telegram voice notes, images, videos, email attachments, and future platform
media. Large files should not be base64-encoded into the protocol.

## Architecture Boundary

The host owns:

- platform adapter processes
- platform credentials
- messaging policy and approvals
- normalized storage and audit
- sandbox bridge authorization

The guest runtime owns:

- model prompting and tool execution
- reading mounted attachment paths
- returning draftable reply text

That split is deliberate. Adding a new messaging app should mean adding or
updating a platform adapter behind the broker, not giving each agent runtime a
direct platform SDK and credentials.
