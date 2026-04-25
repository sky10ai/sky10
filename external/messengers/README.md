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
5. run through `zerobox` once sandbox launch support is wired

Sky10 should not run `npm install`, `bun install`, or package lifecycle scripts
as part of normal adapter startup.

## Current First Target

`slack` is the first planned external messenger adapter. It should reuse the
useful Slack primitives from `agent-slack` while presenting Sky10's normalized
messaging adapter protocol to the broker.
