---
created: 2026-05-09
updated: 2026-05-09
model: gpt-5.5
---

# AI Model API Connections And Venice X402

This archives the completed AI model endpoint and provider-connection work.
The current planning thread was turned into implemented daemon, bridge,
adapter, and Settings behavior.

## Goal

Expose a guest-local OpenAI-compatible model API that agents can call with
ordinary HTTP clients while sky10 keeps provider credentials, wallet signing,
service approval, and paid-provider plumbing on the host side.

The supported compatibility surface is intentionally small:

- `GET /v1/health`
- `GET /v1/models`
- `POST /v1/chat/completions`
- `POST /v1/responses`

The guest-local API is the agent-facing contract. Provider adapters remain an
implementation detail under `pkg/ai/llm`.

## What Landed

- Added named AI connections under `pkg/ai/llm` with Settings UI and
  `inference.*` RPCs for providers and connection CRUD.
- Added initial provider adapters for OpenAI, Anthropic, and Venice.
- Mounted host-local and guest-local OpenAI-compatible HTTP endpoints for
  health, models, chat completions, and Responses.
- Added `POST /v1/responses` as a thin compatibility layer over chat
  completions.
- Added Responses streaming SSE output with `response.created`,
  `response.output_text.delta`, `response.completed`, and terminal
  `data: [DONE]`.
- Added one model selector resolver shared by chat completions, Responses, and
  `/v1/models`.
- Supported raw model names, provider-qualified selectors, and
  connection-qualified selectors such as
  `bovilus-venice/anthropic/opus-4-7`.
- Avoided implicit default-model routing unless a connection explicitly
  configures a default model.
- Kept connection IDs internal and labels user-facing in Settings.
- Added shared provider HTTP helpers for JSON requests, error parsing, SSE
  scanning, provider timeout defaults, and context cancellation.
- Routed Venice through the metered-service x402 backend instead of direct
  provider credentials.
- Added true Venice streaming through x402:
  - `pkg/x402.Transport.Stream`
  - `pkg/x402.Backend.StreamCall`
  - host/guest sandbox bridge stream frames
  - `VeniceAdapter.StreamChatCompletions` using `StreamMeteredService`
- Preserved buffered x402 `Call` behavior by reading from the stream path.
- Kept non-streaming Venice requests as `stream: false` without
  `stream_options`, because Venice rejects `stream_options` when streaming is
  disabled.
- Added Venice-side balance lookup for AI Connections:
  - `inference.veniceBalance`
  - wallet address resolution from the configured connection wallet
  - SIWX-signed request to Venice `/api/v1/x402/balance/{walletAddress}`
  - Settings → AI Connections Venice card shows balance, wallet name, network,
    wallet address, can-consume state, and refresh control

## Host/Guest Boundary

The host owns provider credentials and x402 wallet state. Guest agents call the
guest-local model API with ordinary HTTP clients.

For Venice, the request path is:

```text
guest agent
  -> guest sky10 /v1/chat/completions or /v1/responses
  -> Venice adapter
  -> guest metered-service backend
  -> host-owned sandbox bridge WebSocket
  -> host x402 backend
  -> Venice x402 API
```

The host dials the guest bridge endpoint and keeps the WebSocket open. The
guest does not dial host loopback, host RPC, or host callback addresses.

## Venice Balance Scope

The Venice balance shown in AI Connections is not the raw Base wallet USDC
balance. It is Venice's account balance for the configured wallet address.

The Settings card makes that explicit by showing:

- Venice balance
- connection wallet name
- network
- wallet address
- note that changing the wallet changes the Venice balance

This belongs under AI Connections because it is provider-account state for a
specific Venice connection, not a generic wallet balance.

## Validation

The completed branch passed:

- `go test ./pkg/ai/llm ./commands -count=1`
- `go test ./pkg/sandbox/bridge ./pkg/sandbox/bridge/x402 ./pkg/sandbox ./pkg/x402 ./commands ./pkg/ai/llm -count=1`
- `go test ./... -count=1`
- `make check`
- `make build-web`
- Live Anthropic Responses test
- Live OpenAI Responses test
- Live Venice host/guest streaming test:

  ```text
  PATH="$HOME/.sky10/apps/ows/versions/v1.3.2:$PATH" \
  SKY10_LLM_LIVE_VENICE=1 \
  go test ./pkg/ai/llm -run '^TestHostGuestVeniceLiveBridgeStreamsChat$' -count=1 -v
  ```

- Live Venice balance test:

  ```text
  PATH="$HOME/.sky10/apps/ows/versions/v1.3.2:$PATH" \
  SKY10_LLM_LIVE_VENICE=1 \
  go test ./pkg/ai/llm -run '^TestVeniceBalanceClientLive$' -count=1 -v
  ```

The live Venice balance run confirmed the configured wallet address and
`can_consume=true`.

## Branch Commits

- `d8e78429` `feat(ai): add llm connections and host API`
- `dc4f66c1` `docs(ai): add model api compatibility plan`
- `726c3381` `feat(ai): add responses compatibility endpoint`
- `39ed5fca` `fix(ai): use OpenAI max completion tokens`
- `3ad85fb4` `feat(ai): normalize model selector resolution`
- `2a990641` `refactor(ai): share provider http handling`
- `977a5519` `fix(ai): stop responses stream on cancellation`
- `c8960425` `feat(ai): route Venice through x402 LLM adapter`
- `441e1548` `feat(ai): stream Venice through x402 bridge`
- `3c1e5e04` `feat(ai): show Venice balance on connections`

## Follow-Up

- Add user-facing setup guidance for configuring OpenAI, Anthropic, and Venice
  AI connections once the UX settles.
- Add provider health/test-connection actions in AI Connections.
- Consider `/v1/messages` only if an actual runtime requires an
  Anthropic-native guest-local endpoint.
- Keep agent-runtime changes separate from the provider API surface.
