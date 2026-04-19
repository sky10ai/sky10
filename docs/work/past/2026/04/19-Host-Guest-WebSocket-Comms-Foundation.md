---
created: 2026-04-19
model: gpt-5.4
---

# Host-Guest WebSocket Comms Foundation

This entry covers the start of the host-guest WebSocket transport work that
landed in `fce979b42497a178818a325d0eece38a5bd0c2dc`
(`feat(agent): restore guest websocket chat stream`) and
`3edb4631e8bce1d995a74c93df39d579f3793ec1`
(`fix(ci): clean up host guest sandbox checks`).

This was not the final managed-VM transport design. It was the first narrow
slice that reintroduced a dedicated host-initiated guest chat path so later
work can stream agent output back into the `sky10` web UI and move toward a
more secure VM model where the host establishes the guest connection instead of
depending on a broader reverse bridge.

## Why This Started

The OpenClaw and Hermes Lima work had already proven that guest-local runtimes
could talk back to host `sky10`, but the transport shape was still too tied to
runtime-specific bridges and too loose for a stronger sandbox story.

The missing middle layer was:

- a dedicated guest-side chat transport
- a stable streaming event vocabulary
- real host/guest integration coverage
- a path that keeps connection initiation on the host side

That last point matters for the long-term VM model. If the host is the side
that opens the connection to the guest, `sky10` can move toward a tighter
managed-VM boundary where the guest exposes a narrow, intentional surface
instead of a broader "guest can call back into host however it wants" design.

## What Shipped

### 1. Guest chat got a dedicated WebSocket endpoint again

`fce979b` added a narrow guest-side endpoint:

```text
GET /rpc/agents/{agent}/chat?session_id=<session-id>
```

The route is intentionally scoped to chat sessions. It is not generic RPC
tunneling and it does not reopen the full host/guest control surface.

Main files:

- [`commands/serve.go`](../../../../../commands/serve.go)
- [`pkg/agent/chat_websocket.go`](../../../../../pkg/agent/chat_websocket.go)
- [`pkg/agent/message_hub.go`](../../../../../pkg/agent/message_hub.go)
- [`pkg/agent/rpc.go`](../../../../../pkg/agent/rpc.go)

### 2. The first protocol slice is text-only but streaming-oriented

The first transport slice deliberately stayed narrow:

- host sends `message.send`
- guest emits `session.ready`
- stream events are shaped as `delta`, `message`, `done`, and `error`

That gave the project a stable wire shape before wiring in full runtime
streaming from managed guest agents.

The important product point here was not "WebSocket exists." It was
"WebSocket now looks like the eventual streaming channel the web UI and managed
guest runtimes can target."

### 3. Real two-daemon integration coverage shipped with it

`fce979b` also added an in-process host/guest integration test that brings up
two distinct HTTP daemons on different ports, keeps the WebSocket route on the
guest only, opens sessions, checks `session.ready`, sends a text message, and
verifies ordered `delta -> message -> done` delivery plus session isolation.

Main test files:

- [`pkg/agent/chat_websocket_test.go`](../../../../../pkg/agent/chat_websocket_test.go)
- [`pkg/agent/chat_websocket_integration_test.go`](../../../../../pkg/agent/chat_websocket_integration_test.go)

This mattered because the transport goal is explicitly about host/guest
topology, not just a single daemon exposing another local helper endpoint.

### 4. A dedicated GitHub Actions bucket followed immediately

`fce979b` introduced a dedicated workflow for the new host/guest transport
coverage, and `3edb463` immediately cleaned up the CI follow-through by fixing
WebSocket test response-body handling and tightening the workflow naming.

Workflow:

- [`.github/workflows/test-host-guest.yml`](../../../../../.github/workflows/test-host-guest.yml)

This made the transport slice a real maintained test bucket instead of a local
experiment that could silently rot.

## Why The Host-Initiated Shape Matters

The long-term intention behind this work was larger than one endpoint.

The project wants:

- streaming from managed guest agents back into the host `sky10` web interface
- a more secure VM environment
- a narrower and easier-to-reason-about trust boundary between host and guest

The host-initiated transport direction supports that because it allows the
guest VM to expose one deliberate chat/session surface rather than relying on a
more open-ended callback path into the host.

That does not solve the full VM security model by itself, but it establishes a
better substrate for it:

- host chooses when to connect
- guest exposes a limited endpoint
- protocol is session-scoped
- transport is easy to test with real daemon separation

## What This Did Not Finish

This was groundwork, not the full guest-streaming system.

It did not yet:

- hook Hermes/OpenClaw runtime streaming directly into the WebSocket protocol
- switch the host web UI fully onto this path
- carry files or media
- impose the final managed-guest network/policy restrictions

Those next steps were intentionally left for follow-on branches.

## Outcome

After these commits, `sky10` had the beginning of a dedicated host-guest chat
transport again:

- narrow guest chat endpoint
- text-only first slice
- explicit streaming event vocabulary
- two-daemon host/guest integration coverage
- dedicated CI coverage

That was the right first step because it created the transport substrate needed
for two later goals at the same time:

- real streaming from guest agents back into the host web interface
- a more secure managed VM design where the host establishes the guest
  connection and the guest surface stays intentionally small
