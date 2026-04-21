---
created: 2026-04-20
model: gpt-5.4
---

# OpenClaw WebSocket Streaming And Guest Upgrade Gotchas

This entry covers the OpenClaw guest-chat follow-on work that landed in
`ee2bc50`
(`feat(sandbox): stream openclaw guest replies over websocket`) and
`3290a0e`
(`fix(sandbox): stream openclaw partial guest replies`).

This work built directly on
[`19-Host-Guest-WebSocket-Comms-Foundation.md`](19-Host-Guest-WebSocket-Comms-Foundation.md)
and
[`19-Hermes-WebSocket-Streaming-And-Web-Chat.md`](19-Hermes-WebSocket-Streaming-And-Web-Chat.md).
The April 19 transport work created the guest chat socket, event vocabulary,
and host web chat path. Hermes had already been wired onto that path. This
follow-on branch made OpenClaw speak the same guest-chat contract and exposed a
few operational edges in the sandbox model that were easy to miss while the
transport was still changing.

## Why This Follow-On Happened

After the Hermes websocket work landed, OpenClaw still had two separate gaps:

- the OpenClaw guest bridge was not yet emitting the websocket chat stream
  shape that the agent web chat page already understood
- existing OpenClaw sandboxes could look "broken" even after the code landed
  because the guest VM was still running an older guest-local `sky10` binary

There was also a third issue that only became obvious after live UI testing:
the first OpenClaw websocket integration streamed at block boundaries rather
than partial text boundaries, so it could look like "one token, then the whole
answer" even when the guest websocket itself was healthy.

## What Shipped

### 1. OpenClaw now uses the guest websocket chat contract

`ee2bc50` changed the bundled OpenClaw `sky10` channel plugin so guest replies
go through the same websocket-oriented event shape introduced on April 19:

- `delta`
- final `message`
- shared `stream_id`
- preserved `client_request_id`

The bridge did not replace OpenClaw's native direct-DM runtime. It adapted that
runtime onto the `sky10` guest chat wire shape using OpenClaw's channel reply
pipeline helpers.

Main files:

- [`pkg/sandbox/templates/openclaw-sky10-channel/src/index.js`](../../../../../pkg/sandbox/templates/openclaw-sky10-channel/src/index.js)
- [`pkg/sandbox/templates/openclaw-sky10-channel/src/sky10.js`](../../../../../pkg/sandbox/templates/openclaw-sky10-channel/src/sky10.js)
- [`templates/lima/openclaw-sky10-channel/src/index.js`](../../../../../templates/lima/openclaw-sky10-channel/src/index.js)
- [`templates/lima/openclaw-sky10-channel/src/sky10.js`](../../../../../templates/lima/openclaw-sky10-channel/src/sky10.js)

The first pass used OpenClaw's buffered block dispatcher so the plugin could
emit streamed `delta` events without having to reimplement OpenClaw's full
reply runtime.

### 2. Asset parity and OpenClaw-specific websocket coverage were added

That same branch added OpenClaw-specific coverage in two places:

- template/bundled asset parity checks for the OpenClaw bridge files
- a focused guest websocket integration test for OpenClaw event shape

Main files:

- [`commands/agent_lima_test.go`](../../../../../commands/agent_lima_test.go)
- [`pkg/sandbox/manager_test.go`](../../../../../pkg/sandbox/manager_test.go)
- [`pkg/agent/chat_websocket_openclaw_integration_test.go`](../../../../../pkg/agent/chat_websocket_openclaw_integration_test.go)

This mattered because the OpenClaw bridge lives in generated/bundled guest
assets rather than normal Go runtime files. The asset checks make it much less
likely that one template copy drifts from the other or that future edits drop
stream metadata propagation silently.

### 3. Live testing showed that buffered block streaming was not enough

After the first branch landed and the guest daemon was updated, live web chat
testing showed that the transport was working but the reply cadence was still
wrong. OpenClaw often emitted one tiny early delta and then one very large
delta before the final message.

The key finding was that the first bridge only forwarded `onBlockReply`
callbacks. That meant the websocket saw completed reply blocks, not true
partial text updates.

`3290a0e` fixed that by switching the OpenClaw bridge onto the lower-level
`onPartialReply` callback, computing incremental suffix deltas from the running
reply text, and suppressing duplicate block-based deltas once partial streaming
was active.

That change kept the block path as fallback for runs that do not surface
partials, but it let normal streamed OpenClaw runs feel like actual websocket
streaming instead of one buffered flush.

Main files:

- [`pkg/sandbox/templates/openclaw-sky10-channel/src/index.js`](../../../../../pkg/sandbox/templates/openclaw-sky10-channel/src/index.js)
- [`templates/lima/openclaw-sky10-channel/src/index.js`](../../../../../templates/lima/openclaw-sky10-channel/src/index.js)
- [`commands/agent_lima_test.go`](../../../../../commands/agent_lima_test.go)
- [`pkg/sandbox/manager_test.go`](../../../../../pkg/sandbox/manager_test.go)

## What We Learned While Rolling It Out

### Existing sandboxes do not automatically pick up a new guest `sky10` build

The most important operational surprise was not in OpenClaw at all.

The host `make dev-install` flow updated the host daemon, but the running
OpenClaw sandbox still had an older guest-local `sky10` binary. The sandbox
bootstrap only installs guest `sky10` when the binary is missing, so existing
guests can stay on an older version even after the repo branch and host daemon
move forward.

That produced a very confusing symptom:

- source code looked correct
- host web chat was dialing the correct guest websocket route
- OpenClaw logged `reply sent`
- but the guest websocket behavior still matched the older guest daemon build

Once the guest-local `sky10` binary was replaced with the current Linux build,
the guest websocket path behaved like the current source again.

The practical lesson is that guest runtime rollout is a separate problem from
host daemon rollout. For sandbox work, "installed on the host" is not enough.

### OpenClaw self-upgrade expectations do not match the current sandbox install layout

Another surprise from live testing was that OpenClaw could not simply upgrade
itself the way it often can in a user-managed install.

The sandbox currently installs OpenClaw globally with:

```bash
npm install -g "openclaw@${OPENCLAW_VERSION}"
```

from
[`pkg/sandbox/templates/openclaw-sky10.system.sh`](../../../../../pkg/sandbox/templates/openclaw-sky10.system.sh).

That places the runtime under `/usr/lib/node_modules/openclaw`, owned by
`root`, while the gateway itself runs as the unprivileged guest user. So the
service is correctly non-root, but the install location is not writable by the
running process.

That means "upgrade OpenClaw" from the gateway UI is not a reliable workflow in
this sandbox shape even though it may be normal in other OpenClaw deployments.

The practical lesson is that the current provisioning model is good for
reproducible initial installs but a poor fit for OpenClaw's usual self-upgrade
workflow. A user-writable install prefix would preserve the non-root runtime
while making OpenClaw's expected upgrade path work again.

### OpenClaw is faster than Hermes, but its visible streaming cadence is different

After the partial-stream fix, OpenClaw guest chat felt substantially faster than
Hermes in the same sandbox environment. But even with `onPartialReply` wired
through, OpenClaw's visible cadence still reflects how its runtime emits
partial text. It is more bursty than Hermes' direct provider-stream bridge.

That distinction matters when reading user reports:

- "no reply" can be transport
- "first token is slow" can be runtime/model latency
- "streaming feels chunky" can be partial emission cadence even when the
  websocket transport is correct

## Outcome

After these two commits and the live rollout fix, OpenClaw sandbox chat moved
onto the same host-to-guest websocket product surface Hermes already used:

- guest replies stream over `/rpc/agents/{agent}/chat`
- OpenClaw preserves `stream_id` and `client_request_id`
- the host web chat page can merge OpenClaw `delta` events into one assistant
  turn
- partial text now streams incrementally instead of only at block boundaries
- existing sandboxes still need an explicit guest-binary update when guest
  `sky10` changes
- the current root-owned global OpenClaw install is a provisioning mismatch for
  self-upgrade expectations

The key product result is that OpenClaw is now a real participant in the April
19 guest websocket architecture rather than a sandbox runtime that still needs a
special older reply path.
