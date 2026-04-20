---
created: 2026-04-19
model: gpt-5.4
---

# Hermes WebSocket Streaming And Web Chat

This entry covers the Hermes follow-on work that landed in `3d9575d`
(`feat(sandbox): stream hermes guest replies over websocket`), `fbca895`
(`fix(web): merge streamed hermes chat events`), `495d7f6`
(`fix(web): keep pending indicator during streamed replies`), `dad8a77`
(`feat(web): use websocket transport for agent chat`), `557b188`
(`fix(web): require guest websocket for sandbox chat`), `7844d27`
(`fix(hermes): stop queueing same-session replies`), `7beb7ce`
(`fix(web): move progress into streaming bubble`), `7467be2`
(`feat(chat): show reply timing metrics`), and `8305381`
(`fix(hermes): warm bridge before first chat`).

This work built directly on
[`19-Host-Guest-WebSocket-Comms-Foundation.md`](19-Host-Guest-WebSocket-Comms-Foundation.md)
and the earlier Hermes integration entry
[`16-Hermes-Lima-And-Host-Chat.md`](16-Hermes-Lima-And-Host-Chat.md).
The earlier April 19 websocket work created the guest chat transport and the
streaming event vocabulary. This follow-on branch was the part that actually
made Hermes use that transport end to end from the host web UI.

## Why This Follow-On Happened

After the transport foundation landed, Hermes still had four practical gaps:

- the guest bridge was still returning host-visible replies over the older
  `agent.send` path instead of the new chat websocket
- the host chat page was not yet speaking directly to the guest websocket
- streamed `delta`, final `message`, and `done` events were not being rendered
  as one coherent assistant turn
- Hermes sessions could silently serialize replies, which made the websocket
  path look broken even when transport was healthy

There was also a fifth issue that only became obvious during live testing:
first-token latency and Hermes cold-start behavior were now measurable because
the transport itself was no longer hiding them.

## What Shipped

### 1. Hermes bridge replies now stream onto the guest chat websocket

`3d9575d` changed the Hermes guest bridge so it emits chat-stream events in the
same protocol shape introduced by the websocket foundation:

- `delta`
- final `message`
- `done`
- `error`

The bridge now streams Hermes `/v1/responses` output when available, falls back
to streamed `/v1/chat/completions`, preserves a stable `stream_id`, and mirrors
guest-targeted `agent.message` traffic into the guest chat message hub so the
chat websocket can observe Hermes replies in real time.

Main files:

- [`commands/serve.go`](../../../../../commands/serve.go)
- [`pkg/agent/chat_websocket_hermes_integration_test.go`](../../../../../pkg/agent/chat_websocket_hermes_integration_test.go)
- [`pkg/sandbox/templates/hermes-sky10-bridge.py`](../../../../../pkg/sandbox/templates/hermes-sky10-bridge.py)
- [`templates/lima/hermes-sky10-bridge.py`](../../../../../templates/lima/hermes-sky10-bridge.py)

This was the point where the generic host-guest websocket substrate stopped
being just groundwork and became a real Hermes transport path.

### 2. The host web chat page switched to the guest websocket path

`dad8a77` moved the host web chat page onto `/rpc/agents/{agent}/chat`, and
`557b188` tightened that further so sandbox agents use the guest websocket
directly instead of falling back to queued host-side delivery.

That cutover mattered because the foundation work had restored the guest chat
socket, but the host web UI still needed to actually dial it and treat sandbox
agents differently from host-local agents.

Main files:

- [`pkg/agent/chat_websocket.go`](../../../../../pkg/agent/chat_websocket.go)
- [`pkg/agent/chat_websocket_test.go`](../../../../../pkg/agent/chat_websocket_test.go)
- [`web/src/lib/agentChat.ts`](../../../../../web/src/lib/agentChat.ts)
- [`web/src/lib/rpc.ts`](../../../../../web/src/lib/rpc.ts)
- [`web/src/pages/AgentChat.tsx`](../../../../../web/src/pages/AgentChat.tsx)

### 3. Stream rendering and waiting-state UX had to catch up

Once Hermes deltas were really flowing, the web page needed to stop treating
transport events like independent chat messages.

`fbca895`, `495d7f6`, and `7beb7ce` fixed the visible UX:

- `delta` chunks accumulate into one streaming assistant bubble
- the final assistant `message` replaces that draft bubble instead of creating
  a duplicate
- `done` markers stay internal instead of rendering raw protocol text
- the pre-first-token waiting state remains visible until something real arrives
- once text is streaming, progress moves into the active assistant bubble
  instead of leaving the global waiting banner stuck on screen

This mattered because "the transport is streaming correctly" and "the user can
tell that it is streaming correctly" turned out to be separate problems.

### 4. Same-session reply queueing was removed

`7844d27` fixed a subtle Hermes bridge problem: work was effectively serialized
per `session_id`, so a second prompt could be acknowledged immediately while
silently waiting behind a still-running earlier turn in the same chat session.

That bug made the websocket path look non-streaming even when transport was
fine. The fix removed the per-session queueing behavior and tightened the
fallback chat-history commit logic so overlapping same-session requests do not
clobber one another when they finish out of order.

### 5. Timing instrumentation made the remaining latency visible

`7467be2` added per-turn timing surfaces to the chat UI by threading a
`client_request_id` through the websocket request, Hermes bridge stream, and
final rendered assistant bubble.

The chat page can now show:

- `First token <duration>`
- `Complete <duration>`

That made it possible to separate:

- websocket transport latency
- guest bridge latency
- Hermes/runtime latency

from the same visible chat bubble.

### 6. Hermes cold-start needed explicit warm-up

Live probing after the transport cleanup showed that the worst latency spike
was not the websocket at all. A fresh Hermes gateway could take roughly a
minute to emit the first token for a trivial prompt even though direct provider
calls were sub-second on the same VM.

`8305381` added a bridge-side warm-up pass before the bridge registers for
chat. That way the obvious cold-start penalty is paid during bridge startup
instead of on the first real user message.

This did not make Hermes generally "fast." It addressed one specific cold-path
failure mode that had been wrongly blamed on the websocket transport.

## What We Learned

This branch clarified a few things that were easy to blur together before the
instrumentation existed:

- the websocket transport was working well before the remaining Hermes latency
  problems were solved
- stream rendering bugs in the host page could make correct websocket traffic
  look duplicated or stalled
- same-session serialization in the bridge could mimic transport failure
- Hermes cold-start and context/runtime overhead remained a separate problem
  even after the websocket path was correct

The key product lesson was that restoring the guest websocket transport was
necessary but not sufficient. The branch had to finish the rest of the stack:

- runtime stream production
- host-web transport cutover
- UI stream rendering
- request correlation and timing
- session overlap behavior

## Outcome

After this series, Hermes chat in `sky10` changed from "guest bridge that
eventually answers through host-side message delivery" into a real streamed
guest-websocket path:

- Hermes bridge emits websocket-style stream events directly
- the host web chat page dials the guest websocket for sandbox agents
- stream events render as one coherent assistant reply
- timing is visible in the UI
- overlapping same-session turns no longer silently queue behind one another
- cold-start behavior is at least warmed before the first visible chat turn

The remaining first-token complaints after this point were no longer transport
ambiguity. They were evidence about Hermes itself.
