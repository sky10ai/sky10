---
created: 2026-05-09
updated: 2026-05-09
model: gpt-5
---

# AI Model API Compatibility Plan

## Goal

Expose a guest-local OpenAI-compatible model API that agents can call with
ordinary HTTP clients while sky10 keeps provider credentials, user approval,
and paid-provider plumbing on the host side. The first supported providers are
OpenAI and Anthropic, with Venice using the same provider path through the
existing x402 work.

The compatibility surface should stay small and deliberate:

- `GET /v1/health`
- `GET /v1/models`
- `POST /v1/chat/completions`
- `POST /v1/responses`

The guest-local API is the stable agent-facing contract. Provider adapters are
an implementation detail inside `pkg/ai/llm`.

## Baseline

- OpenAI-compatible chat completions are the core request shape.
- OpenAI and Anthropic should work first because they establish the adapter
  shape without payment complexity.
- Venice should reuse the same request path, with x402 isolated inside the
  Venice provider adapter.
- Model and connection selection must be shared by chat completions and
  responses so the two endpoints cannot drift.

## Non-Goals

- A full OpenAI API clone.
- Public guest-local `/v1/messages` unless an actual runtime requires it.
- Provider-native passthrough endpoints.
- Embeddings, files, batches, stored responses, cancellation, response
  retrieval, or input-token counting.
- Agent-runtime changes before the guest-local HTTP API and streaming behavior
  are reliable.

## Milestone 1: Responses Compatibility

Build `POST /v1/responses` as a thin adapter over the existing chat backend.

### Implementation Checklist

- [x] Add Responses request and response types under `pkg/ai/llm`.
- [x] Convert string `input` into a user chat message.
- [x] Convert message-style `input` into chat messages.
- [x] Convert `instructions` into a system message.
- [x] Map `max_output_tokens` to the chat max-token field.
- [x] Pass through `model`, `temperature`, `top_p`, and `stream`.
- [x] Reject unsupported input shapes with clear OpenAI-compatible errors.
- [x] Return a Responses-style object for non-streaming requests.
- [x] Mount `POST /v1/responses` beside the existing chat-completions route.

### Validation Checklist

- [x] Unit test string input conversion.
- [x] Unit test message-style input conversion.
- [x] Unit test `instructions` ordering before user messages.
- [x] Unit test unsupported input errors.
- [x] Unit test non-streaming response shape.
- [x] Live test Anthropic through `/v1/responses`.
- [ ] Live test OpenAI through `/v1/responses` when an OpenAI key is present.

## Milestone 2: Responses Streaming

Make streaming Responses output reliable and compatible enough for OpenAI-style
clients and agent runtimes.

### Implementation Checklist

- [x] Add a Responses SSE writer that wraps the existing chat streaming path.
- [x] Emit `response.created` before text deltas.
- [x] Convert streamed chat text deltas into `response.output_text.delta`.
- [x] Emit `response.completed` exactly once.
- [x] Emit terminal `data: [DONE]` exactly once.
- [x] Flush every SSE frame when the writer supports flushing.
- [ ] Stop upstream work when the HTTP client disconnects.
- [x] Convert upstream provider errors into a stable error envelope.

### Validation Checklist

- [x] Unit test streamed delta conversion.
- [x] Unit test completion and `[DONE]` emission.
- [x] Unit test upstream error propagation.
- [ ] Unit test client-cancellation behavior where practical.
- [x] Live streaming test with a prompt that produces multiple chunks.
- [x] Verify Anthropic streaming through the Responses endpoint.
- [ ] Verify OpenAI streaming through the Responses endpoint when a key is
      present.

## Milestone 3: Model And Connection Resolution

Replace ad hoc model routing with one resolver used by every model endpoint.

### Implementation Checklist

- [ ] Define one model selector parser in `pkg/ai/llm`.
- [ ] Support raw model names such as `claude-opus-4.7`.
- [ ] Support provider-qualified names such as
      `anthropic/claude-opus-4.7`.
- [ ] Support connection-qualified names such as
      `work-anthropic/claude-opus-4.7`.
- [ ] Keep connection id internal and label user-facing.
- [ ] Avoid implicit default-model behavior unless explicitly configured.
- [ ] Use the same resolver for chat completions, responses, and model lists.
- [ ] Make ambiguous model errors explain which selector forms are accepted.

### Validation Checklist

- [ ] Unit test raw model resolution.
- [ ] Unit test provider-qualified resolution.
- [ ] Unit test connection-qualified resolution.
- [ ] Unit test unknown provider errors.
- [ ] Unit test unknown connection errors.
- [ ] Unit test ambiguous model errors.
- [ ] Unit test `/v1/models` output for multiple configured connections.
- [ ] Confirm chat completions and responses resolve the same model selector
      identically.

## Milestone 4: Provider HTTP Layer

Factor repeated OpenAI, Anthropic, and Venice HTTP behavior into a small shared
provider layer without turning sky10 into a general-purpose AI gateway.

### Implementation Checklist

- [ ] Add a shared JSON request helper for provider adapters.
- [ ] Add shared response/error body parsing.
- [ ] Add shared SSE scanning helpers.
- [ ] Standardize provider timeouts and context cancellation.
- [ ] Keep provider-specific payload translation inside each adapter.
- [ ] Keep Venice x402 payment handling inside the Venice adapter path.
- [ ] Remove duplicated request, error, and SSE code from provider adapters.

### Validation Checklist

- [ ] Unit test provider request headers and JSON bodies.
- [ ] Unit test upstream `400`, `401`, `429`, and `500` error handling.
- [ ] Unit test malformed SSE handling.
- [ ] Confirm OpenAI adapter tests still cover OpenAI-specific payloads.
- [ ] Confirm Anthropic adapter tests still cover Anthropic-specific payloads.
- [ ] Confirm Venice adapter tests cover x402-specific behavior.
- [ ] Run `make check`.
- [ ] Run `go test ./... -count=1`.

## Sequencing

Milestones 1 and 2 make the guest-local endpoint useful. Milestone 3 removes
routing ambiguity before more providers are added. Milestone 4 keeps provider
growth manageable without changing the public agent-facing API.
