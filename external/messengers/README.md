---
created: 2026-04-25
updated: 2026-04-25
model: gpt-5.4
---

# External Messenger Adapters

This directory incubates out-of-process messenger adapters before they are
split into standalone repos.

Adapters here are intentionally not Go packages compiled into `sky10`. They
must behave like external adapter executables that speak the stable messaging
adapter protocol over JSON-RPC on stdio.

## Goals

- Prove the plugin and packaging shape while keeping protocol iteration fast.
- Keep first-party external adapters near the broker code during early design.
- Make the future repo split mostly mechanical.
- Avoid runtime package installation on user machines.

## Expected Layout

Use one directory per messenger:

```text
external/messengers/<adapter-id>/
  adapter.json
  package.json
  bun.lock
  src/
  dist/
```

For JavaScript or TypeScript adapters, `dist/` should contain a bundled entry
file that can run on Sky10-managed Bun without `node_modules`.

## Adapter Manifest

Each adapter directory should declare an `adapter.json` manifest:

```json
{
  "id": "slack",
  "display_name": "Slack",
  "version": "0.1.0",
  "auth_methods": ["bot_token"],
  "capabilities": {
    "receive_messages": true,
    "send_messages": true,
    "list_conversations": true,
    "search_conversations": true
  },
  "runtime": {
    "type": "bun",
    "version": "^1.3"
  },
  "entry": "dist/adapter.js",
  "sandbox": {
    "mode": "none"
  }
}
```

The manifest is resolved by `pkg/messaging/external` into the same supervised
process runtime used by built-in adapters. `entry` must be a slash-separated
relative path inside the adapter bundle. `sandbox.mode` is intentionally
explicit; use `none` only for development while the Zerobox launch path is
being wired.

Sky10 passes these stable environment variables to the adapter process:

- `SKY10_MESSAGING_ADAPTER_ID`
- `SKY10_MESSAGING_ADAPTER_BUNDLE_DIR`

## Runtime Contract

External messenger adapters should:

- speak JSON-RPC over stdio using `pkg/messaging/protocol`
- receive staged credential material from the broker
- store adapter-local state under broker-provided runtime paths
- avoid direct access to `pkg/secrets`
- avoid broad filesystem assumptions
- declare capabilities honestly in `Describe`
- keep policy decisions in the broker, not the adapter

## Packaging Direction

The default JavaScript adapter model is:

1. develop with Bun and normal package dependencies
2. build a bundled `dist/adapter.js`
3. install the bundle as an adapter artifact
4. launch with Sky10-managed Bun
5. run through Sky10-managed `zerobox` once sandbox launch support is wired

Sky10 should not run `npm install`, `bun install`, or package lifecycle scripts
as part of normal adapter startup.

`bun` and `zerobox` are managed helper apps in Sky10, so adapter artifacts can
depend on those runtime tools without bundling a separate copy per adapter.

## Current Slack Adapter

`slack` is the first external messenger adapter bundle in this directory. It
currently speaks the Sky10 adapter protocol over stdio, runs on Sky10-managed
Bun, and calls Slack Web API methods for auth validation, identity discovery,
conversation discovery/search, message listing/search, and send/reply.

The remaining Slack product work is daemon-side external adapter discovery,
OAuth/install UX, webhook/event ingestion, and deciding whether to replace or
augment the direct Web API calls with reusable pieces from `agent-slack`.
