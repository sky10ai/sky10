---
created: 2026-04-22
model: gpt-5.4
---

# WebSocket File Attachments For Hermes And OpenClaw

This entry covers the attachment/media follow-on work that landed in
`f42ea17`
(`feat(agent): support websocket file attachments`),
`4873939`
(`fix(agent): repair hermes websocket integration test`),
`63c09b5`
(`fix(sandbox): prefer fast hermes chat path`),
`5e1837e`
(`fix(sandbox): send image chat as multimodal hermes input`),
`7eec2d3`
(`fix(sandbox): enable hermes vision fallback for images`),
`c952916`
(`feat(openclaw): support sky10 chat attachments`), and
`da806c9`
(`fix(sandbox): stage openclaw media helper`).

The same rollout also included web-chat hardening commits
`f362532`, `d0438c3`, `f29f500`, and `ce16d17` to keep the attach affordance
visible and prevent stale hidden sessions from poisoning the visible thread.

This work built directly on the previous day's websocket follow-on entry
[`20-OpenClaw-WebSocket-Streaming-And-Guest-Upgrade-Gotchas.md`](20-OpenClaw-WebSocket-Streaming-And-Guest-Upgrade-Gotchas.md),
plus the April 19 transport docs
[`19-Host-Guest-WebSocket-Comms-Foundation.md`](19-Host-Guest-WebSocket-Comms-Foundation.md)
and
[`19-Hermes-WebSocket-Streaming-And-Web-Chat.md`](19-Hermes-WebSocket-Streaming-And-Web-Chat.md).
Those earlier entries established the guest chat socket, event vocabulary,
stream merge behavior, and OpenClaw/Hermes streaming parity. This follow-on
kept that websocket transport intact but widened the payload from text-only chat
into structured text-plus-media chat and then fixed the live rollout failures
that only became obvious once real images and files were sent through both guest
runtimes.

## Why This Follow-On Happened

By April 20, the websocket transport was working, but the product surface still
had a major limitation: sandbox chat was effectively text-only.

- the host page could stream text replies, but there was no first-class
  websocket content model for files or images
- Hermes and OpenClaw each needed different guest-side translation to turn
  websocket media parts into something their runtimes could actually consume
- live sandboxes could still drift from the repo because guest binaries and
  guest plugin assets are staged copies, not direct reads from the current
  checkout

That meant "websocket chat works" was true only for text. The next step was to
make photo/file chat real without introducing a separate legacy side path.

## What Shipped

### 1. The websocket chat contract now carries structured media parts

`f42ea17` introduced a real structured content model for guest chat in
[`pkg/agent/content.go`](../../../../../pkg/agent/content.go).

That model added:

- `ChatContent` with top-level `text`, `parts`, and `client_request_id`
- typed parts for `text`, `image`, `file`, `audio`, and `video`
- typed sources for `base64` and `url`
- normalization and validation before payloads enter the live guest chat path

The guest websocket handler in
[`pkg/agent/chat_websocket.go`](../../../../../pkg/agent/chat_websocket.go)
now parses, validates, and re-marshals structured chat content instead of
treating chat as an opaque string blob.

This mattered because the websocket transport did not need a second protocol for
attachments. It needed the same chat method to be able to carry richer content
without breaking stream semantics, `stream_id`, or `client_request_id`.

Main files:

- [`pkg/agent/content.go`](../../../../../pkg/agent/content.go)
- [`pkg/agent/chat_websocket.go`](../../../../../pkg/agent/chat_websocket.go)
- [`pkg/agent/chat_websocket_test.go`](../../../../../pkg/agent/chat_websocket_test.go)
- [`pkg/agent/chat_websocket_integration_test.go`](../../../../../pkg/agent/chat_websocket_integration_test.go)
- [`pkg/agent/chat_websocket_hermes_integration_test.go`](../../../../../pkg/agent/chat_websocket_hermes_integration_test.go)

### 2. The host web chat page can now compose, persist, and render attachments

The host web chat path was updated in
[`web/src/pages/AgentChat.tsx`](../../../../../web/src/pages/AgentChat.tsx),
[`web/src/lib/agentChat.ts`](../../../../../web/src/lib/agentChat.ts), and
[`web/src/lib/rpc.ts`](../../../../../web/src/lib/rpc.ts).

The page now:

- accepts attachments from the composer
- converts them into structured websocket `parts`
- renders inbound and outbound attachment cards inline in the transcript
- keeps `client_request_id` attached to structured chat sends
- redacts embedded base64 payloads from persisted transcript snapshots instead
  of storing the full binary blob in browser persistence

Live rollout also showed that the attach affordance itself mattered. The first
composer rewrite accidentally changed or obscured the upload action, so the
series ended with the explicit inline paperclip control restored.

Main files:

- [`web/src/pages/AgentChat.tsx`](../../../../../web/src/pages/AgentChat.tsx)
- [`web/src/pages/AgentChat.test.tsx`](../../../../../web/src/pages/AgentChat.test.tsx)
- [`web/src/lib/agentChat.ts`](../../../../../web/src/lib/agentChat.ts)
- [`web/src/lib/agentChat.test.ts`](../../../../../web/src/lib/agentChat.test.ts)
- [`web/src/lib/rpc.ts`](../../../../../web/src/lib/rpc.ts)

### 3. Hermes now translates image/file parts into real multimodal input

The first attachment pass made the websocket transport capable of carrying media
parts, but Hermes still needed a bridge-side conversion layer.

The Hermes bridge in
[`pkg/sandbox/templates/hermes-sky10-bridge.py`](../../../../../pkg/sandbox/templates/hermes-sky10-bridge.py)
and
[`templates/lima/hermes-sky10-bridge.py`](../../../../../templates/lima/hermes-sky10-bridge.py)
was extended to:

- stage base64 parts into guest-local files
- turn image parts into real multimodal Hermes input instead of attachment text
- keep file attachments available as fallback prompt context
- convert outbound `MEDIA:` artifacts back into structured websocket chat parts

Live testing exposed two Hermes-specific follow-ons:

- image analysis initially hit a "vision not configured" path, so the sandbox
  bootstrap now sets `auxiliary.vision.provider=main`
- Hermes `/responses` latency in this bridge path was dramatically slower than
  `/chat/completions`, so the sandbox service now prefers the faster chat path
  for this consumer chat surface while preserving bridge compatibility

The important boundary is that this bridge still flattens Hermes output into
text plus media artifacts for `sky10`. It does not expose native Hermes tool
events or full Responses-item fidelity to the host UI.

Main files:

- [`pkg/sandbox/templates/hermes-sky10-bridge.py`](../../../../../pkg/sandbox/templates/hermes-sky10-bridge.py)
- [`templates/lima/hermes-sky10-bridge.py`](../../../../../templates/lima/hermes-sky10-bridge.py)
- [`pkg/sandbox/templates/hermes-sky10.user.sh`](../../../../../pkg/sandbox/templates/hermes-sky10.user.sh)
- [`templates/lima/hermes-sky10.user.sh`](../../../../../templates/lima/hermes-sky10.user.sh)
- [`pkg/agent/chat_websocket_hermes_integration_test.go`](../../../../../pkg/agent/chat_websocket_hermes_integration_test.go)

### 4. OpenClaw now maps websocket attachments into its media runtime

OpenClaw needed a different guest-side translation than Hermes.

`c952916` changed the bundled OpenClaw `sky10` channel so inbound websocket
attachments are no longer flattened to text. Instead, the bridge now stages
media and populates the OpenClaw media context fields that its runtime expects.

The new helper
[`pkg/sandbox/templates/openclaw-sky10-channel/src/media.js`](../../../../../pkg/sandbox/templates/openclaw-sky10-channel/src/media.js)
does the heavy lifting:

- base64 attachments are staged under `~/.openclaw/media/inbound/sky10/...`
- inbound content is translated into `MediaPath`, `MediaPaths`, `MediaUrl`,
  `MediaUrls`, and `MediaTypes`
- outbound `MEDIA:` lines are converted back into structured websocket chat
  attachments

That means the same host websocket chat surface can now send a photo to
OpenClaw and receive a reply without inventing an OpenClaw-only transport.

Main files:

- [`pkg/sandbox/templates/openclaw-sky10-channel/src/index.js`](../../../../../pkg/sandbox/templates/openclaw-sky10-channel/src/index.js)
- [`pkg/sandbox/templates/openclaw-sky10-channel/src/media.js`](../../../../../pkg/sandbox/templates/openclaw-sky10-channel/src/media.js)
- [`pkg/sandbox/templates/openclaw-sky10-channel/src/media.test.js`](../../../../../pkg/sandbox/templates/openclaw-sky10-channel/src/media.test.js)
- [`templates/lima/openclaw-sky10-channel/src/index.js`](../../../../../templates/lima/openclaw-sky10-channel/src/index.js)
- [`templates/lima/openclaw-sky10-channel/src/media.js`](../../../../../templates/lima/openclaw-sky10-channel/src/media.js)

### 5. Live rollout exposed two separate guest-staging failure modes

The biggest practical lesson from this branch was that "repo is fixed" and
"guest is fixed" are still different states.

The first OpenClaw failure was a staged plugin-asset bug:

- the guest plugin started importing `./media.js`
- but the sandbox staging list in
  [`pkg/sandbox/manager.go`](../../../../../pkg/sandbox/manager.go)
  still copied only `index.js` and `sky10.js`
- the result was a guest plugin load error even though the repo copy was correct

`da806c9` fixed that by adding `media.js` to the staged OpenClaw asset set and
by extending the manager regression coverage.

The second failure was a guest-runtime mismatch:

- the running OpenClaw VM had guest `sky10 v0.57.0 (fadf949)`
- the host and staged plugin were already sending structured `image` parts
- the old guest daemon rejected them with
  `unsupported content part type "image"`

That happened because the sandbox bootstrap in
[`pkg/sandbox/templates/openclaw-sky10.user.sh`](../../../../../pkg/sandbox/templates/openclaw-sky10.user.sh)
installs the latest published guest `sky10` release when missing. It does not
automatically install the current working-tree build from the host.

So the operational rule is now clearer:

- host `make dev-install` updates the host daemon only
- existing guests need their own binary rollout
- bundled guest assets need exhaustive parity checks or they silently drift

Main files:

- [`pkg/sandbox/manager.go`](../../../../../pkg/sandbox/manager.go)
- [`pkg/sandbox/manager_test.go`](../../../../../pkg/sandbox/manager_test.go)
- [`pkg/sandbox/templates/openclaw-sky10.user.sh`](../../../../../pkg/sandbox/templates/openclaw-sky10.user.sh)
- [`templates/lima/openclaw-sky10.user.sh`](../../../../../templates/lima/openclaw-sky10.user.sh)

### 6. Session and composer polish mattered as much as transport correctness

Real screenshot-driven testing also exposed two UI failures that were easy to
misread as transport bugs:

- the attach action became less obvious while the composer was being rebuilt
- a stale hidden session could be reused even after the visible transcript no
  longer matched, which made the agent answer from context the user could not
  see

Those fixes were not new transport primitives, but they were necessary to make
the attachment product understandable. A correct websocket/media path still
looks broken if the user cannot tell how to attach a file or if the visible chat
thread is no longer the backend session that receives the message.

## Outcome

After this series and the live guest rollouts, the April 19/20 websocket chat
architecture stopped being a text-only stream path and became a real media chat
surface for both sandbox runtimes:

- the host websocket chat contract can carry structured text and media parts
- the web chat UI can send and render attachments
- Hermes can consume image/file parts and return media artifacts on the same
  chat path
- OpenClaw can consume image/file parts through its media runtime on the same
  chat path
- existing guests still require explicit runtime rollout when guest `sky10`
  semantics change
- bundled guest assets remain a distinct class of failure from normal Go/web
  source files

The key product result is that `sky10` sandbox chat is no longer "streamed text
over websocket, plus out-of-band attachment behavior." Attachments now ride the
same websocket chat surface that text already used, and the remaining problems
are mostly guest rollout and product polish rather than missing transport
capability.
