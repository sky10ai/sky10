---
created: 2026-04-27
updated: 2026-04-27
model: claude-opus-4-7
---

# Sandbox Comms Implementation Plan

Milestones in dependency order for this branch. M1 lands the
shared plumbing; M2 lands x402 as the first (and, for this branch,
only) capability on top. Future capabilities (wallet, messengers)
are listed below M2 but explicitly out of scope here.

## Pre-flight

- [ ] Confirm answers to open questions in the
  [README](README.md#open-questions): connection auth, audit log
  location, default per-(agent,type) quotas, cross-device identity
  hop behavior, drop policy.
- [ ] Skim `pkg/agent/chat_websocket.go` for the websocket library
  and auth pattern we'll mirror in `pkg/sandbox/comms/conn.go`.
- [ ] Confirm package path: `pkg/sandbox/comms/` for shared
  plumbing; `pkg/sandbox/comms/<capability>/` per capability.

## M1 — shared transport plumbing — **done 2026-04-27**

`pkg/sandbox/comms/`. No endpoints registered yet. The plumbing
is library code that future capability packages import.

Landed in commit `202e91b0` (post-rebase). Files match the list
below; tests pass; `go vet` and `gofmt` clean. The `arch_test.go`
described under M1 below moves to M2 because it has nothing to
scan until the first capability subpackage exists.

Files:

- `pkg/sandbox/comms/doc.go`
- `pkg/sandbox/comms/envelope.go` — `Envelope` struct, `TypeSpec`,
  `Direction` (`RequestResponse | Push | Subscribe`)
- `pkg/sandbox/comms/conn.go` — websocket lifecycle, accept and
  upgrade, read/write loops, ping/pong, graceful close
- `pkg/sandbox/comms/identity.go` — extract `agent_id` and
  `device_id` from authenticated channel; strip and ignore any
  payload-supplied identity fields
- `pkg/sandbox/comms/registry.go` — per-endpoint type registry;
  init-time validation that every required `TypeSpec` field is
  set; panic on misuse
- `pkg/sandbox/comms/audit.go` — structured per-envelope log line
  with payload hash, never raw payload
- `pkg/sandbox/comms/replay.go` — bounded LRU keyed by
  `(agent_id, type, nonce)` with TTL = nonce window; ts skew
  check
- `pkg/sandbox/comms/quota.go` — token-bucket per
  `(agent_id, envelope_type)`; per-endpoint queue with bounded
  size and configurable drop policy
- `pkg/sandbox/comms/endpoint.go` — `Endpoint` value combining
  registry + auth + identity + replay + audit + quota into an
  `http.HandlerFunc` that capability packages register on the
  daemon's mux
- `pkg/sandbox/comms/arch_test.go` — enforces rule 5 (validation
  precedes business logic) by walking handler ASTs in subpackages
- Per-piece unit tests:
  - dispatcher routing within an endpoint
  - identity injection (payload `agent_id` ignored)
  - replay rejection (within window)
  - audit log shape and payload-hashing
  - quota: token-bucket accept/reject, queue overflow

Exit criteria: a Go test that exercises a tiny in-memory test
endpoint (registered with two synthetic envelope types) end-to-
end through the plumbing — including identity injection, replay
rejection, quota enforcement, and audit logging. No live daemon
integration yet, no x402 yet.

## M2 — x402 capability — **done 2026-04-27**

`pkg/sandbox/comms/x402/`. Registers `/comms/x402/ws` on the
daemon HTTP server. Handlers delegate to `pkg/x402` (host-side
catalog/budget logic from the [x402 plan](../x402/)).

The handlers, the Backend interface, full test coverage, the
real Backend implementation in `pkg/x402`, and the daemon wiring
in `commands/serve_x402.go` are all in place. The endpoint is
registered when the daemon starts; agents reach it at
`/comms/x402/ws?agent=<name-or-id>` (identity resolved against
the existing agent registry). M1 wiring uses pkg/x402's
StubSigner — calls fail with ErrSignerNotConfigured rather than
charging an unconfigured wallet; OWS-backed signing follows.

Includes the arch_test in pkg/sandbox/comms/arch_test.go that was
deferred from M1 — now scans the x402 subpackage's handlers and
asserts validation-first. A meta-test verifies the classifier
correctly rejects non-validation shapes.

This depends on x402 plan M1 (protocol core in `pkg/x402/`) and
benefits from M2 (discovery) being far enough along that the
catalog has services to list. The two plans run in parallel; the
comms M2 milestone here can land its handler shells against
mocked `pkg/x402` interfaces while the host-side x402 work
matures.

Files:

- `pkg/sandbox/comms/x402/doc.go`
- `pkg/sandbox/comms/x402/endpoint.go` — defines the endpoint,
  registers the four envelope types, exposes
  `RegisterOnMux(mux *http.ServeMux)` for daemon wiring
- `pkg/sandbox/comms/x402/list_services.go` — `x402.list_services`
  (RR). Scope: agent's approved services only.
- `pkg/sandbox/comms/x402/service_call.go` —
  `x402.service_call` (RR, sync). Scope: approved-and-funded;
  enforces per-call cap and budget; idempotency on payment-binding
  nonce in payload.
- `pkg/sandbox/comms/x402/budget_status.go` —
  `x402.budget_status` (RR). Scope: this agent's view.
- `pkg/sandbox/comms/x402/changes.go` — `x402.changes` (Push).
  Host-initiated; emitted when refresh detects new / risky /
  removed services for this agent.
- Daemon wiring in `commands/serve.go`: register endpoint **only
  on host** (skip on guest sky10 to enforce no-local-handler);
  feature-flag-gated until M2 is stable.
- Per-handler tests with mocked `pkg/x402` backend:
  - scope: agent only sees services it has approved
  - budget: per-call max enforced
  - idempotency: replayed `service_call` with same payment-binding
    nonce returns cached signed result, not double-charge
  - sync correlation: response carries matching `request_id`
- Integration test (skippable behind env flag): an in-test
  websocket client opens `/comms/x402/ws`, calls `list_services`
  and `service_call` against an `httptest`-based fake x402 endpoint.

Exit criteria: a Go test where an in-test client successfully
runs an end-to-end `x402.service_call` against an httptest fake
service via the comms plumbing, with a real `pkg/x402` transport
performing the 402 round-trip and signing on a Base testnet (or
fake-signing in test mode). The wallet stays on host. The client
never sees the 402 challenge.

## Future (out of scope this branch)

These capabilities use the same M1 plumbing once we get there.
Listed for context; not implemented in this branch.

- **`pkg/sandbox/comms/wallet/`** — `wallet.balance_subscribe`,
  `wallet.transfer`, `wallet.history`. Future branch when
  agent-to-agent commerce or non-x402 wallet flows are needed.
- **`pkg/sandbox/comms/messengers/`** — `messengers.search`,
  `messengers.send`, `messengers.message_arrived`. Future branch.
  Hooks into the existing `pkg/messaging/broker` with
  `requester = agent_id` for scope filtering. Inbound message
  bodies arrive via drive sync, not over comms.

Secrets are explicitly not a future comms capability. They come
into the VM via env-var mount at sandbox creation
(`pkg/sandbox/openclaw_env.go`), pre-staged at boot, never
fetched at runtime.

## Out of scope for this plan entirely

- Replacing host-side unix-socket RPC for CLI / Web UI / host
  tools. Those are inside the trust boundary and don't need the
  comms discipline.
- Migrating `/rpc/agents/{agent}/chat` onto comms. Existing chat
  stays as-is. If we ever consolidate, that's a separate
  decision.
- A third-party SDK for the comms protocol. We control all
  current consumers.
- A graphical inspector for comms traffic. Audit log + log
  analysis cover M1–M2 needs.
